// internal/strategy/fast_detector.go
// 增量套利检测器，监听价格变化事件，实时计算套利机会
package strategy

import (
	"context"
	"math/big"
	"sort"
	"strings"
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

	// DEX 名称 → Router 地址映射（构建 Dexes 数组给合约用）
	dexRouters map[string]common.Address

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
		dexRouters:    make(map[string]common.Address),
		paths:         make([]*ArbitragePath, 0),
		pathsByPool:   make(map[string][]*ArbitragePath),
		config:        config,
		opportunities: make(chan *ArbitrageOpportunity, config.OpportunityBufferSize),
		stopCh:        make(chan struct{}),
	}
}

// SetDexRouters 设置 DEX 名称到 Router 地址的映射
func (d *ArbitrageDetector) SetDexRouters(routers map[string]common.Address) {
	d.dexRouters = routers
}

// defaultDetectorConfig 默认配置
func defaultDetectorConfig() *DetectorConfig {
	return &DetectorConfig{
		MinPathLength: 3,
		MaxPathLength: 4,
		ProfitThresholds: map[int]float64{
			3: 0.005, // 0.5%（含 Flash Loan 0.05% 费用 + Gas + 安全边际）
			4: 0.008, // 0.8%（4-hop 更高门槛）
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
		if !ok {
			return nil
		}
		// V3 池子检查 SqrtPriceX96 + Liquidity，V2 池子检查 Reserve0 + Reserve1
		if price.IsV3 {
			if price.SqrtPriceX96 == nil || price.SqrtPriceX96.Sign() <= 0 ||
				price.Liquidity == nil || price.Liquidity.Sign() <= 0 {
				return nil
			}
		} else {
			if price.Reserve0 == nil || price.Reserve1 == nil {
				return nil
			}
		}
		prices[i] = price
	}

	// 计算最优投入金额（先算，用于利润率评估）
	// 业界标准：用实际执行金额（而非 1 token）计算利润率，AMM 是非线性曲线，
	// 大额交易的价格冲击可能让 1 token 时的利润率变为负值
	optimalAmountForEval := calculateOptimalAmount(prices, path.Tokens)
	amountIn := new(big.Float).SetInt(optimalAmountForEval)
	currentAmount := new(big.Float).Set(amountIn)

	for i := 0; i < len(path.Pools); i++ {
		price := prices[i]
		tokenIn := path.Tokens[i]
		tokenOut := path.Tokens[i+1]

		var amountOut *big.Float

		// V3 池子：使用 sqrtPriceX96 + liquidity 精确计算
		if price.IsV3 && price.SqrtPriceX96 != nil && price.SqrtPriceX96.Sign() > 0 &&
			price.Liquidity != nil && price.Liquidity.Sign() > 0 {
			currentAmountInt, _ := currentAmount.Int(nil)
			if currentAmountInt == nil || currentAmountInt.Sign() <= 0 {
				return nil
			}

			// V3 合约中 token0 < token1（按地址排序），但 PriceCache 中 Token0 是数据库顺序
			// sqrtPriceX96 始终是合约级别的 token1/token0
			// 需要用合约排序来确定 zeroForOne
			tokenInHex := strings.ToLower(tokenIn.Hex())
			dbToken0Hex := strings.ToLower(price.Token0.Hex())
			dbToken1Hex := strings.ToLower(price.Token1.Hex())

			dbOrderMatchesContract := dbToken0Hex < dbToken1Hex
			var zeroForOne bool
			if dbOrderMatchesContract {
				zeroForOne = tokenInHex == dbToken0Hex // db_token0 == contract_token0
			} else {
				zeroForOne = tokenInHex == dbToken1Hex // db_token1 == contract_token0（因为 db 顺序和合约相反）
			}

			amountOutInt, err := CalculateV3SwapOutput(
				price.SqrtPriceX96,
				price.Liquidity,
				currentAmountInt,
				price.Fee,
				zeroForOne,
			)
			if err != nil || amountOutInt == nil || amountOutInt.Sign() <= 0 {
				return nil
			}
			// V3 诊断日志：追踪每一跳的输入输出，排查 8000x 高估
			log.Strategy().Debug().
				Str("path", path.ID).
				Int("hop", i).
				Str("tokenIn", tokenIn.Hex()[:10]).
				Str("tokenOut", tokenOut.Hex()[:10]).
				Str("amountIn", currentAmountInt.String()).
				Str("amountOut", amountOutInt.String()).
				Str("sqrtPriceX96", price.SqrtPriceX96.String()).
				Str("liquidity", price.Liquidity.String()).
				Uint64("fee", price.Fee).
				Bool("zeroForOne", zeroForOne).
				Str("pool", path.Pools[i][:10]).
				Msg("V3 hop calc")
			amountOut = new(big.Float).SetInt(amountOutInt)
		} else {
			// V2 池子或 V3 fallback（缺少 sqrtPriceX96/Liquidity 数据）
			if price.IsV3 {
				// V3 池子但缺少 V3 数据，用 V2 公式 = 不可靠（token balances ≠ active reserves）
				log.Strategy().Warn().
					Str("path", path.ID).
					Int("hop", i).
					Str("pool", path.Pools[i][:10]).
					Bool("hasSqrt", price.SqrtPriceX96 != nil && price.SqrtPriceX96.Sign() > 0).
					Bool("hasLiq", price.Liquidity != nil && price.Liquidity.Sign() > 0).
					Msg("⚠️ V3 pool using V2 fallback — unreliable")
				return nil // 直接跳过：V3 池子用 V2 公式计算不可靠
			}
			if price.Reserve0 == nil || price.Reserve1 == nil ||
				price.Reserve0.Sign() <= 0 || price.Reserve1.Sign() <= 0 {
				return nil
			}
			// 流动性过滤：跳过 dust 池（amountIn > 10% of reserveIn = 过大价格影响）
			var reserveIn *big.Int
			if tokenIn == price.Token0 {
				reserveIn = price.Reserve0
			} else {
				reserveIn = price.Reserve1
			}
			curAmtInt, _ := currentAmount.Int(nil)
			if curAmtInt != nil {
				// amountIn > reserveIn → 池子完全无法承接此交易量
				if curAmtInt.Cmp(reserveIn) > 0 {
					return nil // amountIn 超过池子全部储备，不可行
				}
			}
			if tokenIn == price.Token0 {
				amountOut = calculateSwapOutput(
					currentAmount,
					new(big.Float).SetInt(price.Reserve0),
					new(big.Float).SetInt(price.Reserve1),
					price.Fee,
				)
			} else {
				amountOut = calculateSwapOutput(
					currentAmount,
					new(big.Float).SetInt(price.Reserve1),
					new(big.Float).SetInt(price.Reserve0),
					price.Fee,
				)
			}
			// V2 诊断日志
			amtOutStr := "nil"
			if amountOut != nil {
				amtOutStr = amountOut.Text('f', 0)
			}
			log.Strategy().Debug().
				Str("path", path.ID).
				Int("hop", i).
				Str("tokenIn", tokenIn.Hex()[:10]).
				Str("tokenOut", tokenOut.Hex()[:10]).
				Str("amountIn", currentAmount.Text('f', 0)).
				Str("amountOut", amtOutStr).
				Str("reserve0", price.Reserve0.String()).
				Str("reserve1", price.Reserve1.String()).
				Uint64("fee", price.Fee).
				Uint8("dec0", price.Decimals0).
				Uint8("dec1", price.Decimals1).
				Str("pool", path.Pools[i][:10]).
				Msg("V2 hop calc")
		}
		_ = tokenOut

		if amountOut == nil || amountOut.Sign() <= 0 {
			return nil
		}
		currentAmount = amountOut
	}

	// 计算毛利润率（不含 gas）
	grossProfit := new(big.Float).Sub(currentAmount, amountIn)
	grossProfitRate := new(big.Float).Quo(grossProfit, amountIn)
	grossProfitRateFloat, _ := grossProfitRate.Float64()

	// 异常利润诊断：>5% 一定是计算误差（V3 dampening 后仍高估）
	if grossProfitRateFloat > 0.05 {
		tokenAddrs := make([]string, len(path.Tokens))
		for ti, t := range path.Tokens {
			tokenAddrs[ti] = t.Hex()[:10]
		}
		poolAddrs := make([]string, len(path.Pools))
		for pi, p := range path.Pools {
			poolAddrs[pi] = p[:10]
		}
		log.Strategy().Warn().
			Str("path", path.ID).
			Str("amountIn", amountIn.Text('f', 0)).
			Str("amountOut", currentAmount.Text('f', 0)).
			Float64("gross_pct", grossProfitRateFloat*100).
			Strs("tokens", tokenAddrs).
			Strs("pools", poolAddrs).
			Strs("dexes", path.DexNames).
			Int("hops", len(path.Pools)).
			Msg("🚨 Abnormal profit — likely calculation bug")
	}

	// 估算 Gas 成本并从利润中扣除，得到净利润率
	// Arbitrum: L2 gas ~450K/hop × 0.1 gwei + L1 calldata ~800 bytes × 16 gas × 20 gwei
	numSwaps := len(path.Pools)
	l2Gas := uint64(50_000) + uint64(numSwaps)*450_000
	l2Gas = l2Gas * 120 / 100 // 20% margin
	l2CostWei := l2Gas * 100_000_000 // × 0.1 gwei
	calldataSize := 800 + numSwaps*64
	l1CostWei := uint64(calldataSize) * 16 * 20_000_000_000 // × 20 gwei L1
	totalGasCostWei := l2CostWei + l1CostWei

	gasCostFloat := new(big.Float).SetUint64(totalGasCostWei)
	netProfit := new(big.Float).Sub(grossProfit, gasCostFloat)
	netProfitRate := new(big.Float).Quo(netProfit, amountIn)
	profitRateFloat, _ := netProfitRate.Float64()

	// 更新路径统计（用净利润率）
	path.LastProfit = profitRateFloat
	path.LastCalculated = time.Now()

	// 检查是否达到阈值（净利润率必须为正）
	threshold := d.config.ProfitThresholds[path.PathLength]
	if threshold == 0 {
		threshold = 0.003 // 默认0.3%
	}

	if profitRateFloat < threshold {
		return nil
	}

	// 过滤荒谬利润率（V3 单 tick 模型已加 50% dampening，>1.5% 仍不可信）
	// QuoterV2 on-chain 验证是最终裁判，本地计算只做粗筛
	if grossProfitRateFloat > 0.015 {
		return nil
	}

	// 使用前面计算的最优金额（与利润率评估用的相同金额）
	optimalAmountIn := optimalAmountForEval

	// 构建 DEX Router 地址列表（合约需要 dexes.length == swapPath.length - 1）
	dexAddresses := make([]common.Address, 0, len(path.DexNames))
	for _, dexName := range path.DexNames {
		if addr, ok := d.dexRouters[dexName]; ok {
			dexAddresses = append(dexAddresses, addr)
		} else {
			return nil // 无法找到 DEX Router 地址
		}
	}

	// 构建 FeeTiers: 从 PriceCache 获取每个池子的 fee
	// V3 池子: PriceCache.Fee 是 bps (5=0.05%), 合约需要 raw fee (500)
	// V2 池子: feeTier 必须传 0，合约才会用 swapExactTokensForTokens
	// 关键: feeTier>0 会让合约调用 V3 exactInputSingle，V2 router 没有这个函数!
	feeTiers := make([]uint32, 0, len(path.Pools))
	for _, poolAddr := range path.Pools {
		if pp, ok := d.priceCache.Get(poolAddr); ok {
			if pp.IsV3 {
				fee := uint32(pp.Fee) * 100 // bps→raw: 5→500, 30→3000
				if fee == 0 {
					fee = 3000 // V3 池子 fallback 默认 0.3%
				}
				feeTiers = append(feeTiers, fee)
			} else {
				feeTiers = append(feeTiers, 0) // V2 池子: 必须传 0
			}
		} else {
			feeTiers = append(feeTiers, 0) // 未知池子默认 V2 (fee=0)
		}
	}

	// 构建机会对象（使用净利润率，已扣除 gas）
	opp := &ArbitrageOpportunity{
		ID:           path.ID + "_" + time.Now().Format("20060102150405"),
		SwapPath:     path.Tokens,
		Dexes:        dexAddresses,
		DexNames:     path.DexNames,
		AmountIn:     optimalAmountIn,
		ProfitRate:   profitRateFloat, // 净利润率（已扣 gas）
		PathLength:   path.PathLength,
		Timestamp:    time.Now(),
		ValidUntil:   time.Now().Add(5 * time.Second), // 缩短有效期 15s→5s（链上机会存活 <3s）
		Confidence:   calculatePathConfidence(path, profitRateFloat),
		IsCex:        false,
		FeeTiers:     feeTiers,
	}

	// 计算预期净利润（wei 单位）
	if optimalAmountIn != nil && optimalAmountIn.Sign() > 0 {
		// ExpectProfit = amountIn × netProfitRate
		expectedProfit := new(big.Float).SetInt(optimalAmountIn)
		expectedProfit.Mul(expectedProfit, big.NewFloat(profitRateFloat))
		opp.ExpectProfit, _ = expectedProfit.Int(nil)
		if opp.ExpectProfit != nil && opp.ExpectProfit.Sign() < 0 {
			return nil // 扣完 gas 后亏损
		}

		// MinProfit = 实际 gas 成本（已在净利润中扣除，这里设较低值作为合约安全网）
		gasCostWei := new(big.Int).SetUint64(totalGasCostWei)
		opp.MinProfit = gasCostWei // 合约层面至少要覆盖 gas
		opp.GasEstimate = l2Gas
	}

	calcTime := time.Since(startTime)
	log.Strategy().Info().
		Str("path", path.ID).
		Float64("gross_pct", grossProfitRateFloat*100).
		Float64("net_pct", profitRateFloat*100).
		Uint64("gas_cost_wei", totalGasCostWei).
		Str("expect_profit_wei", func() string { if opp.ExpectProfit != nil { return opp.ExpectProfit.String() } ; return "0" }()).
		Float64("confidence", opp.Confidence).
		Int("hops", path.PathLength).
		Dur("calc_time", calcTime).
		Msg("ArbitrageDetector: Found opportunity (net of gas)")

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

// calculateSwapOutput 计算AMM交换输出（恒定乘积公式，纯整数运算）
// amountOut = reserveOut * amountIn * (10000 - feeBps) / (reserveIn * 10000 + amountIn * (10000 - feeBps))
// 使用 big.Int 避免 float64 精度丢失（之前 float64 版本导致 5-44% 利润高估）
func calculateSwapOutput(amountIn, reserveIn, reserveOut *big.Float, feeBps uint64) *big.Float {
	if reserveIn.Sign() <= 0 || reserveOut.Sign() <= 0 {
		return big.NewFloat(0)
	}

	// 转换为 big.Int 做纯整数运算
	amtIn, _ := amountIn.Int(nil)
	resIn, _ := reserveIn.Int(nil)
	resOut, _ := reserveOut.Int(nil)
	if amtIn == nil || resIn == nil || resOut == nil {
		return big.NewFloat(0)
	}

	feeNumerator := big.NewInt(int64(10000 - feeBps)) // e.g. 9970 for 30bps
	base := big.NewInt(10000)

	// amountInWithFee = amountIn * (10000 - feeBps)
	amountInWithFee := new(big.Int).Mul(amtIn, feeNumerator)

	// numerator = reserveOut * amountInWithFee
	numerator := new(big.Int).Mul(resOut, amountInWithFee)

	// denominator = reserveIn * 10000 + amountInWithFee
	denominator := new(big.Int).Add(
		new(big.Int).Mul(resIn, base),
		amountInWithFee,
	)

	if denominator.Sign() <= 0 {
		return big.NewFloat(0)
	}

	// amountOut = numerator / denominator
	amountOutInt := new(big.Int).Div(numerator, denominator)
	return new(big.Float).SetInt(amountOutInt)
}

// calculateOptimalAmount 计算最优投入金额（简化版）
// 业界标准：基于代币精度动态计算
func calculateOptimalAmount(prices []*cache.PoolPrice, tokens []common.Address) *big.Int {
	if len(prices) == 0 || len(tokens) == 0 {
		return big.NewInt(1e18)
	}

	firstPool := prices[0]
	var tokenDecimals uint8

	if tokens[0] == firstPool.Token0 {
		tokenDecimals = firstPool.Decimals0
	} else {
		tokenDecimals = firstPool.Decimals1
	}

	// V3 池子没有 Reserve，用基于精度的固定测试金额
	// V2 池子用储备量的 1%
	var optimalAmount *big.Int

	if firstPool.IsV3 || firstPool.Reserve0 == nil || firstPool.Reserve1 == nil {
		// V3 或缺少 Reserve 数据：使用基于精度的固定金额（如 0.1 个代币）
		optimalAmount = calculateTestAmountForDecimals(tokenDecimals)
	} else {
		var relevantReserve *big.Int
		if tokens[0] == firstPool.Token0 {
			relevantReserve = firstPool.Reserve0
		} else {
			relevantReserve = firstPool.Reserve1
		}
		if relevantReserve != nil && relevantReserve.Sign() > 0 {
			optimalAmount = new(big.Int).Div(relevantReserve, big.NewInt(100))
		} else {
			optimalAmount = calculateTestAmountForDecimals(tokenDecimals)
		}
	}

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
// 改进版：更保守的评估，低利润率给更低置信度（扣完 Gas+Flash Loan 费后可能亏损）
// 跨 DEX 路径加权：使用不同 DEX 的路径比同 DEX 不同 fee tier 更有利润空间
func calculatePathConfidence(path *ArbitragePath, profitRate float64) float64 {
	// 基础置信度
	confidence := 0.7

	// 路径长度影响（3-hop 最佳）
	switch path.PathLength {
	case 3:
		confidence *= 1.0 // 3-hop 最常见最可靠
	case 4:
		confidence *= 0.85 // 4-hop 显著降低
	default:
		confidence *= 0.6 // 5+ hop 很低
	}

	// 利润率合理性检查
	if profitRate > 0.5 { // >50% 极不可能
		confidence *= 0.05
	} else if profitRate > 0.1 { // >10% 可疑
		confidence *= 0.2
	} else if profitRate > 0.05 { // >5% 需要验证
		confidence *= 0.5
	} else if profitRate > 0.01 { // 1%-5% 合理
		confidence *= 0.9
	} else if profitRate > 0.005 { // 0.5%-1% 边缘，扣 Gas 后可能亏
		confidence *= 0.7
	} else { // <0.5% 扣完 Gas + Flash Loan 0.05% 后大概率亏
		confidence *= 0.4
	}

	// 跨 DEX 路径加权：路径中包含 >1 个不同 DEX 时，利润空间更大
	if isCrossDEXPath(path.DexNames) {
		confidence += 0.1
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

// RecordResult 记录 eth_call 模拟结果，反馈给路径置信度
// pathID 格式: "path_YYYYMMDD_XX"（去掉时间戳后缀）
func (d *ArbitrageDetector) RecordResult(pathID string, passed bool) {
	d.mu.Lock()
	defer d.mu.Unlock()

	// 从 opp.ID (如 "path_20260310_D6_20260310000143") 提取路径前缀
	// 匹配所有 paths 中 ID 前缀相同的
	for _, p := range d.paths {
		if p.ID == pathID || strings.HasPrefix(pathID, p.ID) {
			if passed {
				p.SuccessCount++
			} else {
				p.FailCount++
			}
			return
		}
	}
}

// isCrossDEXPath 检查路径是否跨越多个不同 DEX
func isCrossDEXPath(dexNames []string) bool {
	if len(dexNames) < 2 {
		return false
	}
	first := dexNames[0]
	for _, name := range dexNames[1:] {
		if name != first {
			return true
		}
	}
	return false
}
