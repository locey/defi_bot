// internal/strategy/fast_detector.go
// 增量套利检测器，监听价格变化事件，实时计算套利机会
package strategy

import (
	"context"
	"math/big"
	"sort"
	"sync"
	"time"

	"github.com/defi-bot/backend/pkg/cache"
	"github.com/defi-bot/backend/pkg/log"
	"github.com/ethereum/go-ethereum/common"
)

// ArbitragePath 预计算的套利路径
type ArbitragePath struct {
	ID         string           // 路径唯一ID
	Pools      []string         // 池子地址序列
	Tokens     []common.Address // 代币地址序列（第一个和最后一个相同）
	DexNames   []string         // DEX名称序列
	PathLength int              // 跳数

	// 统计信息
	LastProfit     float64   // 最近一次计算的利润率
	LastCalculated time.Time // 最近计算时间
	SuccessCount   int       // 历史成功次数
	FailCount      int       // 历史失败次数
}

// DetectorConfig 检测器配置
type DetectorConfig struct {
	// 路径配置
	MinPathLength int // 最小路径长度（默认3）
	MaxPathLength int // 最大路径长度（默认4）

	// 利润阈值（按路径长度）
	ProfitThresholds map[int]float64 // pathLength -> minProfitRate

	// 并发配置
	MaxConcurrentCalc int // 最大并发计算数

	// 缓冲区配置
	OpportunityBufferSize int // 机会通道缓冲区大小
}

// ArbitrageDetector 增量套利检测器
type ArbitrageDetector struct {
	// 依赖
	priceCache *cache.PriceCache
	profitCalc *ProfitCalculator

	// 预计算的路径
	mu          sync.RWMutex
	paths       []*ArbitragePath
	pathsByPool map[string][]*ArbitragePath // poolAddr -> affected paths (索引)

	// 配置
	config *DetectorConfig

	// 输出通道
	opportunities chan *ArbitrageOpportunity

	// 状态
	running bool
	stopCh  chan struct{}

	// 统计
	stats     DetectorStats
	statsMu   sync.RWMutex
}

// DetectorStats 检测器统计
type DetectorStats struct {
	PathsComputed       int64         // 预计算路径数
	EventsReceived      int64         // 收到的价格事件数
	PathsAffected       int64         // 受影响的路径数
	OpportunitiesFound  int64         // 发现的机会数
	AvgCalculationTime  time.Duration // 平均计算时间
	LastEventTime       time.Time     // 最近事件时间
}

// NewArbitrageDetector 创建增量检测器
func NewArbitrageDetector(
	priceCache *cache.PriceCache,
	profitCalc *ProfitCalculator,
	config *DetectorConfig,
) *ArbitrageDetector {
	if config == nil {
		config = defaultDetectorConfig()
	}

	return &ArbitrageDetector{
		priceCache:    priceCache,
		profitCalc:    profitCalc,
		paths:         make([]*ArbitragePath, 0),
		pathsByPool:   make(map[string][]*ArbitragePath),
		config:        config,
		opportunities: make(chan *ArbitrageOpportunity, config.OpportunityBufferSize),
		stopCh:        make(chan struct{}),
	}
}

// defaultDetectorConfig 默认配置
func defaultDetectorConfig() *DetectorConfig {
	return &DetectorConfig{
		MinPathLength: 3,
		MaxPathLength: 4,
		ProfitThresholds: map[int]float64{
			3: 0.003, // 0.3%
			4: 0.005, // 0.5%
		},
		MaxConcurrentCalc:     100,
		OpportunityBufferSize: 1000,
	}
}

// PrecomputePaths 预计算所有可能的套利路径
// 这个方法在启动时调用一次，构建路径图和索引
func (d *ArbitrageDetector) PrecomputePaths(pools []*cache.PoolPrice, baseTokens []common.Address) {
	d.mu.Lock()
	defer d.mu.Unlock()

	startTime := time.Now()
	log.Strategy().Info().Msg("ArbitrageDetector: Starting path precomputation...")

	// 构建代币图
	tokenGraph := d.buildTokenGraph(pools)

	// 找所有环路径
	d.paths = make([]*ArbitragePath, 0)
	pathID := 0

	for _, baseToken := range baseTokens {
		for pathLen := d.config.MinPathLength; pathLen <= d.config.MaxPathLength; pathLen++ {
			cycles := d.findCycles(tokenGraph, baseToken, pathLen)
			for _, cycle := range cycles {
				d.paths = append(d.paths, &ArbitragePath{
					ID:         generatePathID(pathID),
					Pools:      cycle.Pools,
					Tokens:     cycle.Tokens,
					DexNames:   cycle.DexNames,
					PathLength: pathLen,
				})
				pathID++
			}
		}
	}

	// 构建索引：poolAddr -> affected paths
	d.pathsByPool = make(map[string][]*ArbitragePath)
	for _, path := range d.paths {
		for _, poolAddr := range path.Pools {
			d.pathsByPool[poolAddr] = append(d.pathsByPool[poolAddr], path)
		}
	}

	d.statsMu.Lock()
	d.stats.PathsComputed = int64(len(d.paths))
	d.statsMu.Unlock()

	duration := time.Since(startTime)
	log.Strategy().Info().Int("paths", len(d.paths)).Dur("duration", duration).Int("pools", len(d.pathsByPool)).Msg("ArbitrageDetector: Path precomputation complete")
}

// CycleResult 环路径结果
type CycleResult struct {
	Pools    []string
	Tokens   []common.Address
	DexNames []string
}

// buildTokenGraph 构建代币图
func (d *ArbitrageDetector) buildTokenGraph(pools []*cache.PoolPrice) map[common.Address][]poolEdge {
	graph := make(map[common.Address][]poolEdge)

	for _, pool := range pools {
		// Token0 -> Token1
		graph[pool.Token0] = append(graph[pool.Token0], poolEdge{
			PoolAddr: pool.PoolAddress,
			ToToken:  pool.Token1,
			DexName:  pool.DexName,
		})
		// Token1 -> Token0
		graph[pool.Token1] = append(graph[pool.Token1], poolEdge{
			PoolAddr: pool.PoolAddress,
			ToToken:  pool.Token0,
			DexName:  pool.DexName,
		})
	}

	return graph
}

// poolEdge 图的边
type poolEdge struct {
	PoolAddr string
	ToToken  common.Address
	DexName  string
}

// findCycles DFS找环路径
func (d *ArbitrageDetector) findCycles(
	graph map[common.Address][]poolEdge,
	startToken common.Address,
	targetLength int,
) []CycleResult {
	var results []CycleResult
	visited := make(map[string]bool) // 防止重复使用同一个池子

	var dfs func(current common.Address, path []string, tokens []common.Address, dexes []string, depth int)
	dfs = func(current common.Address, path []string, tokens []common.Address, dexes []string, depth int) {
		// 达到目标长度，检查是否回到起点
		if depth == targetLength {
			if current == startToken {
				// 找到环
				result := CycleResult{
					Pools:    make([]string, len(path)),
					Tokens:   make([]common.Address, len(tokens)),
					DexNames: make([]string, len(dexes)),
				}
				copy(result.Pools, path)
				copy(result.Tokens, tokens)
				copy(result.DexNames, dexes)
				results = append(results, result)
			}
			return
		}

		// 继续DFS
		for _, edge := range graph[current] {
			// 不重复使用同一个池子
			if visited[edge.PoolAddr] {
				continue
			}

			// 中间不能回到起点（最后一步才回）
			if edge.ToToken == startToken && depth < targetLength-1 {
				continue
			}

			// 避免中间重复访问代币（除了最后回到起点）
			if depth < targetLength-1 {
				skip := false
				for i := 1; i < len(tokens); i++ {
					if tokens[i] == edge.ToToken {
						skip = true
						break
					}
				}
				if skip {
					continue
				}
			}

			visited[edge.PoolAddr] = true
			dfs(edge.ToToken,
				append(path, edge.PoolAddr),
				append(tokens, edge.ToToken),
				append(dexes, edge.DexName),
				depth+1)
			visited[edge.PoolAddr] = false
		}
	}

	// 从起始代币开始DFS
	dfs(startToken, []string{}, []common.Address{startToken}, []string{}, 0)

	return results
}

// Start 启动检测器，监听价格变化
func (d *ArbitrageDetector) Start(ctx context.Context) error {
	if d.running {
		return nil
	}
	d.running = true

	// 订阅价格变化事件
	events := d.priceCache.Subscribe()

	go d.eventLoop(ctx, events)

	log.Strategy().Info().Msg("ArbitrageDetector: Started listening for price changes")
	return nil
}

// Stop 停止检测器
func (d *ArbitrageDetector) Stop() {
	if !d.running {
		return
	}
	d.running = false
	close(d.stopCh)
	log.Strategy().Info().Msg("ArbitrageDetector: Stopped")
}

// eventLoop 事件循环 + 定时全量扫描
func (d *ArbitrageDetector) eventLoop(ctx context.Context, events <-chan cache.PriceChangeEvent) {
	// 并发控制
	semaphore := make(chan struct{}, d.config.MaxConcurrentCalc)

	// 定时全量扫描：每30秒扫描所有路径（弥补事件驱动的盲区）
	scanTicker := time.NewTicker(30 * time.Second)
	defer scanTicker.Stop()

	// 启动后立即做一次全量扫描
	go d.fullScan(semaphore)

	for {
		select {
		case <-ctx.Done():
			return
		case <-d.stopCh:
			return
		case event, ok := <-events:
			if !ok {
				return
			}
			d.onPriceChange(event, semaphore)
		case <-scanTicker.C:
			// 定时全量扫描所有路径
			go d.fullScan(semaphore)
		}
	}
}

// fullScan 全量扫描所有预计算路径，寻找套利机会
// 弥补事件驱动的不足：即使价格变化 < 阈值，跨 DEX 的累积价差可能已经很大
func (d *ArbitrageDetector) fullScan(semaphore chan struct{}) {
	d.mu.RLock()
	allPaths := make([]*ArbitragePath, 0, len(d.paths))
	for _, p := range d.paths {
		allPaths = append(allPaths, p)
	}
	d.mu.RUnlock()

	scanned := 0
	found := 0

	for _, path := range allPaths {
		select {
		case semaphore <- struct{}{}:
		default:
			continue // 并发已满，跳过
		}

		opp := d.calculatePath(path)
		<-semaphore

		if opp != nil {
			found++
			select {
			case d.opportunities <- opp:
			default:
				log.Strategy().Warn().Str("path", path.ID).Msg("ArbitrageDetector: opportunity buffer full")
			}
		}
		scanned++
	}

	d.statsMu.Lock()
	d.stats.EventsReceived += int64(scanned)
	d.statsMu.Unlock()

	if found > 0 {
		log.Strategy().Info().Int("scanned", scanned).Int("found", found).Msg("ArbitrageDetector: Full scan complete - opportunities found!")
	} else {
		log.Strategy().Debug().Int("scanned", scanned).Msg("ArbitrageDetector: Full scan complete")
	}
}

// onPriceChange 处理价格变化事件
func (d *ArbitrageDetector) onPriceChange(event cache.PriceChangeEvent, semaphore chan struct{}) {
	d.statsMu.Lock()
	d.stats.EventsReceived++
	d.stats.LastEventTime = time.Now()
	d.statsMu.Unlock()

	// 查找受影响的路径
	d.mu.RLock()
	affectedPaths := d.pathsByPool[event.PoolAddress]
	d.mu.RUnlock()

	if len(affectedPaths) == 0 {
		return
	}

	d.statsMu.Lock()
	d.stats.PathsAffected += int64(len(affectedPaths))
	d.statsMu.Unlock()

	// 并发计算受影响的路径
	for _, path := range affectedPaths {
		semaphore <- struct{}{} // 获取信号量

		go func(p *ArbitragePath) {
			defer func() { <-semaphore }() // 释放信号量

			opp := d.calculatePath(p)
			if opp != nil {
				d.statsMu.Lock()
				d.stats.OpportunitiesFound++
				d.statsMu.Unlock()

				select {
				case d.opportunities <- opp:
				default:
					log.Strategy().Warn().Msg("ArbitrageDetector: Opportunity channel full, dropping opportunity")
				}
			}
		}(path)
	}
}

// calculatePath 计算单条路径的利润
func (d *ArbitrageDetector) calculatePath(path *ArbitragePath) *ArbitrageOpportunity {
	startTime := time.Now()

	// 获取所有池子的价格
	prices := make([]*cache.PoolPrice, len(path.Pools))
	for i, poolAddr := range path.Pools {
		price, ok := d.priceCache.Get(poolAddr)
		if !ok || price.Reserve0 == nil || price.Reserve1 == nil {
			return nil // 缺少价格数据
		}
		prices[i] = price
	}

	// 模拟交换，计算利润
	// 业界标准：根据起始代币的实际精度计算测试金额
	startTokenDecimals := d.getTokenDecimals(path.Tokens[0], prices[0])
	testAmount := calculateTestAmountForDecimals(startTokenDecimals)
	amountIn := new(big.Float).SetInt(testAmount)
	currentAmount := new(big.Float).Set(amountIn)

	for i := 0; i < len(path.Pools); i++ {
		price := prices[i]
		tokenIn := path.Tokens[i]
		tokenOut := path.Tokens[i+1]

		// 计算换出数量
		var amountOut *big.Float
		if tokenIn == price.Token0 {
			// Token0 -> Token1
			amountOut = calculateSwapOutput(
				currentAmount,
				new(big.Float).SetInt(price.Reserve0),
				new(big.Float).SetInt(price.Reserve1),
				price.Fee,
			)
		} else {
			// Token1 -> Token0
			amountOut = calculateSwapOutput(
				currentAmount,
				new(big.Float).SetInt(price.Reserve1),
				new(big.Float).SetInt(price.Reserve0),
				price.Fee,
			)
		}
		_ = tokenOut // 消除未使用警告

		if amountOut.Sign() <= 0 {
			return nil
		}
		currentAmount = amountOut
	}

	// 计算利润率
	profitRate := new(big.Float).Sub(currentAmount, amountIn)
	profitRate.Quo(profitRate, amountIn)
	profitRateFloat, _ := profitRate.Float64()

	// 更新路径统计
	path.LastProfit = profitRateFloat
	path.LastCalculated = time.Now()

	// 检查是否达到阈值
	threshold := d.config.ProfitThresholds[path.PathLength]
	if threshold == 0 {
		threshold = 0.003 // 默认0.3%
	}

	if profitRateFloat < threshold {
		return nil
	}

	// 计算最优投入金额（简化版，后续可优化）
	optimalAmountIn := calculateOptimalAmount(prices, path.Tokens)

	// 构建机会对象
	opp := &ArbitrageOpportunity{
		ID:           path.ID + "_" + time.Now().Format("20060102150405"),
		SwapPath:     path.Tokens,
		DexNames:     path.DexNames,
		AmountIn:     optimalAmountIn,
		ProfitRate:   profitRateFloat,
		PathLength:   path.PathLength,
		Timestamp:    time.Now(),
		ValidUntil:   time.Now().Add(5 * time.Second), // 5秒有效期
		Confidence:   calculatePathConfidence(path, profitRateFloat),
		IsCex:        false, // DEX-DEX 套利路径
	}

	// 计算预期利润（wei 单位）
	if optimalAmountIn != nil && optimalAmountIn.Sign() > 0 {
		expectedProfit := new(big.Float).SetInt(optimalAmountIn)
		expectedProfit.Mul(expectedProfit, big.NewFloat(profitRateFloat))
		opp.ExpectProfit, _ = expectedProfit.Int(nil)

		// 计算 MinProfit = Gas 成本的 2 倍（保守估计）
		// Arbitrum Gas ~800K × 0.1 gwei = 0.00008 ETH
		gasEstimateWei := new(big.Int).Mul(big.NewInt(800000), big.NewInt(100_000_000)) // 800K gas × 0.1 gwei
		opp.MinProfit = new(big.Int).Mul(gasEstimateWei, big.NewInt(2))
		opp.GasEstimate = 800000
	}

	calcTime := time.Since(startTime)
	log.Strategy().Info().
		Str("path", path.ID).
		Float64("profit_pct", profitRateFloat*100).
		Str("expect_profit_wei", func() string { if opp.ExpectProfit != nil { return opp.ExpectProfit.String() } ; return "0" }()).
		Float64("confidence", opp.Confidence).
		Int("hops", path.PathLength).
		Dur("calc_time", calcTime).
		Msg("ArbitrageDetector: Found opportunity")

	return opp
}

// Opportunities 获取机会通道
func (d *ArbitrageDetector) Opportunities() <-chan *ArbitrageOpportunity {
	return d.opportunities
}

// GetStats 获取统计信息
func (d *ArbitrageDetector) GetStats() DetectorStats {
	d.statsMu.RLock()
	defer d.statsMu.RUnlock()
	return d.stats
}

// GetPathCount 获取路径数量
func (d *ArbitrageDetector) GetPathCount() int {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return len(d.paths)
}

// GetTopPaths 获取利润率最高的N条路径
func (d *ArbitrageDetector) GetTopPaths(n int) []*ArbitragePath {
	d.mu.RLock()
	paths := make([]*ArbitragePath, len(d.paths))
	copy(paths, d.paths)
	d.mu.RUnlock()

	// 按最近利润率排序
	sort.Slice(paths, func(i, j int) bool {
		return paths[i].LastProfit > paths[j].LastProfit
	})

	if n > len(paths) {
		n = len(paths)
	}
	return paths[:n]
}

// ============================================================
// 辅助函数
// ============================================================

// generatePathID 生成路径ID
func generatePathID(index int) string {
	return "path_" + time.Now().Format("20060102") + "_" + string(rune('A'+index%26)) + string(rune('0'+index/26%10))
}

// calculateSwapOutput 计算AMM交换输出（恒定乘积公式）
// amountOut = reserveOut * amountIn * (1 - fee) / (reserveIn + amountIn * (1 - fee))
func calculateSwapOutput(amountIn, reserveIn, reserveOut *big.Float, feeBps uint64) *big.Float {
	if reserveIn.Sign() <= 0 || reserveOut.Sign() <= 0 {
		return big.NewFloat(0)
	}

	// 手续费：feeBps 是基点（30 = 0.3%）
	feeMultiplier := big.NewFloat(1 - float64(feeBps)/10000)

	// amountInWithFee = amountIn * (1 - fee)
	amountInWithFee := new(big.Float).Mul(amountIn, feeMultiplier)

	// numerator = reserveOut * amountInWithFee
	numerator := new(big.Float).Mul(reserveOut, amountInWithFee)

	// denominator = reserveIn + amountInWithFee
	denominator := new(big.Float).Add(reserveIn, amountInWithFee)

	// amountOut = numerator / denominator
	amountOut := new(big.Float).Quo(numerator, denominator)

	return amountOut
}

// calculateOptimalAmount 计算最优投入金额（简化版）
// 业界标准：基于代币精度动态计算
func calculateOptimalAmount(prices []*cache.PoolPrice, tokens []common.Address) *big.Int {
	// 简化实现：基于第一个池子的流动性
	if len(prices) == 0 || len(tokens) == 0 {
		return big.NewInt(1e18) // 默认1个代币（18位精度）
	}

	firstPool := prices[0]
	var relevantReserve *big.Int
	var tokenDecimals uint8
	
	if tokens[0] == firstPool.Token0 {
		relevantReserve = firstPool.Reserve0
		tokenDecimals = firstPool.Decimals0
	} else {
		relevantReserve = firstPool.Reserve1
		tokenDecimals = firstPool.Decimals1
	}

	// 最优金额 = 储备量 * 1%（简化假设）
	optimalAmount := new(big.Int).Div(relevantReserve, big.NewInt(100))

	// 动态计算最大/最小值（基于代币精度）
	maxAmount := calculateMaxAmountForDecimals(tokenDecimals)
	minAmount := calculateMinAmountForDecimals(tokenDecimals)
	
	if optimalAmount.Cmp(maxAmount) > 0 {
		optimalAmount = maxAmount
	}
	if optimalAmount.Cmp(minAmount) < 0 {
		optimalAmount = minAmount
	}

	return optimalAmount
}

// getTokenDecimals 获取代币精度（从池子信息中）
func (d *ArbitrageDetector) getTokenDecimals(token common.Address, pool *cache.PoolPrice) uint8 {
	if token == pool.Token0 {
		return pool.Decimals0
	}
	return pool.Decimals1
}

// calculateTestAmountForDecimals 计算测试金额（1.0 token）
// 业界标准：1 token = 10^decimals wei
func calculateTestAmountForDecimals(decimals uint8) *big.Int {
	return new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil)
}

// calculateMinAmountForDecimals 计算最小金额（0.01 token）
func calculateMinAmountForDecimals(decimals uint8) *big.Int {
	if decimals >= 2 {
		return new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals-2)), nil)
	}
	return big.NewInt(1)
}

// calculateMaxAmountForDecimals 计算最大金额（100 token）
func calculateMaxAmountForDecimals(decimals uint8) *big.Int {
	oneToken := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil)
	return new(big.Int).Mul(oneToken, big.NewInt(100))
}

// calculatePathConfidence 计算路径置信度
// 改进版：更合理的置信度评估
func calculatePathConfidence(path *ArbitragePath, profitRate float64) float64 {
	// 基础置信度
	confidence := 0.8

	// 路径长度影响（3-hop 最佳）
	switch path.PathLength {
	case 3:
		confidence *= 1.0 // 3-hop 最常见最可靠
	case 4:
		confidence *= 0.9 // 4-hop 稍低
	default:
		confidence *= 0.7 // 5+ hop 较低
	}

	// 利润率合理性检查（过高的利润率通常不真实）
	if profitRate > 0.5 { // >50% 极不可能
		confidence *= 0.1
	} else if profitRate > 0.1 { // >10% 可疑
		confidence *= 0.3
	} else if profitRate > 0.05 { // >5% 需要验证
		confidence *= 0.6
	} else if profitRate > 0.003 { // 0.3%-5% 合理范围
		confidence *= 1.0 // 不惩罚
	}

	// 历史成功率（有历史数据时加权）
	if path.SuccessCount+path.FailCount > 0 {
		successRate := float64(path.SuccessCount) / float64(path.SuccessCount+path.FailCount)
		confidence *= (0.5 + 0.5*successRate)
	}

	// 确保置信度在 0-1 范围内
	if confidence > 1.0 {
		confidence = 1.0
	}
	if confidence < 0.01 {
		confidence = 0.01
	}

	return confidence
}
