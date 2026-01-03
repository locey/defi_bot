// internal/strategy/strategy.go
package strategy

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"math/big"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/defi-bot/backend/internal/models"
	"github.com/defi-bot/backend/pkg/cache"
	"github.com/defi-bot/backend/pkg/web3"
	"github.com/ethereum/go-ethereum/common"
	"gorm.io/gorm"
)

// StrategyEngine 策略引擎
type StrategyEngine struct {
	config       *StrategyConfig
	web3Client   *web3.Client
	db           *gorm.DB
	cache        *cache.RedisCache
	pathFinder   *PathFinder
	profitCalc   *ProfitCalculator
	optimizer    *AmountOptimizer
	gasEstimator *GasEstimator

	// 池子信息缓存
	poolCache   map[string]*PoolInfo
	poolCacheMu sync.RWMutex

	// 运行状态
	running bool
	stopCh  chan struct{}
}

// NewStrategyEngine 创建策略引擎
func NewStrategyEngine(
	config *StrategyConfig,
	web3Client *web3.Client,
	db *gorm.DB,
	cache *cache.RedisCache,
) *StrategyEngine {

	engine := &StrategyEngine{
		config:     config,
		web3Client: web3Client,
		db:         db,
		cache:      cache,
		poolCache:  make(map[string]*PoolInfo),
		stopCh:     make(chan struct{}),
	}

	// 初始化子模块
	engine.pathFinder = NewPathFinder(config, engine)
	engine.profitCalc = NewProfitCalculator(config, engine)
	engine.optimizer = NewAmountOptimizer(config, engine)
	engine.gasEstimator = NewGasEstimator(web3Client)

	return engine
}

// Start 启动策略引擎
func (e *StrategyEngine) Start(ctx context.Context) error {
	e.running = true
	log.Println("Strategy engine started")

	// 启动池子信息更新
	go e.poolUpdateLoop(ctx)

	return nil
}

// Stop 停止策略引擎
func (e *StrategyEngine) Stop() {
	e.running = false
	close(e.stopCh)
	log.Println("Strategy engine stopped")
}

// FindOpportunities 查找套利机会
func (e *StrategyEngine) FindOpportunities(ctx context.Context) ([]*ArbitrageOpportunity, error) {
	startTime := time.Now()

	// 0. 从 DB 读取最新池子数据并构建 token graph（否则 FindAllPaths 永远找不到路径）
	if len(e.config.BaseTokens) == 0 || len(e.config.SupportedDexes) == 0 {
		log.Printf("⚠️  StrategyConfig missing BaseTokens or SupportedDexes (baseTokens=%d, dexes=%d) - likely no paths will be found",
			len(e.config.BaseTokens), len(e.config.SupportedDexes))
	}
	pools, err := e.loadPoolsFromDB(ctx)
	if err != nil {
		return nil, fmt.Errorf("load pools from db failed: %w", err)
	}
	if len(pools) == 0 {
		log.Printf("No pools found in DB (need trading_pairs + pair_reserves). Skip analysis.")
		return nil, nil
	}
	e.pathFinder.BuildTokenGraph(ctx, pools)

	// 1. 获取所有可能的路径
	paths, err := e.pathFinder.FindAllPaths(ctx)
	if err != nil {
		return nil, fmt.Errorf("find paths failed: %w", err)
	}

	log.Printf("Found %d potential paths in %v", len(paths), time.Since(startTime))

	// 2. 并发计算每条路径的利润
	opportunities := e.evaluatePathsConcurrently(ctx, paths)

	// 3. 过滤无利可图的机会
	profitable := e.filterProfitableOpportunities(opportunities)

	// 4. 按利润率排序
	sort.Slice(profitable, func(i, j int) bool {
		return profitable[i].ProfitRate > profitable[j].ProfitRate
	})

	log.Printf("Found %d profitable opportunities in %v",
		len(profitable), time.Since(startTime))

	// ✅ 新增：保存套利机会到数据库
	if len(profitable) > 0 {
		if err := e.SaveOpportunitiesToDB(ctx, profitable); err != nil {
			log.Printf("Save opportunities to DB failed: %v", err)
		}
	}

	return profitable, nil
}

// loadPoolsFromDB 从数据库读取 DEX 交易对及其最新储备量，构建策略引擎需要的 PoolInfo 列表
func (e *StrategyEngine) loadPoolsFromDB(ctx context.Context) ([]*PoolInfo, error) {
	if e.db == nil {
		return nil, fmt.Errorf("db is nil")
	}

	type TradingPairRow struct {
		ID             uint   `gorm:"column:id"`
		PairAddress    string `gorm:"column:pair_address"`
		Token0Address  string `gorm:"column:token0_address"`
		Token1Address  string `gorm:"column:token1_address"`
		ExchangeName   string `gorm:"column:exchange_name"`
		ExchangeProto  string `gorm:"column:exchange_proto"`
		ExchangeRouter string `gorm:"column:exchange_router"`
		ExchangeFee    int    `gorm:"column:exchange_fee"`
	}

	var pairs []TradingPairRow
	if err := e.db.WithContext(ctx).
		Table("trading_pairs tp").
		Select(`
			tp.id as id,
			tp.pair_address as pair_address,
			t0.address as token0_address,
			t1.address as token1_address,
			ex.name as exchange_name,
			ex.protocol as exchange_proto,
			ex.router_address as exchange_router,
			ex.fee as exchange_fee
		`).
		Joins("JOIN exchanges ex ON ex.id = tp.exchange_id").
		Joins("JOIN tokens t0 ON t0.id = tp.token0_id").
		Joins("JOIN tokens t1 ON t1.id = tp.token1_id").
		Where("tp.is_active = true").
		Where("ex.exchange_type = ?", "dex").
		Where("tp.pair_address <> ''").
		Scan(&pairs).Error; err != nil {
		return nil, err
	}

	if len(pairs) == 0 {
		return nil, nil
	}

	type ReserveRow struct {
		Reserve0  string    `gorm:"column:reserve0"`
		Reserve1  string    `gorm:"column:reserve1"`
		Timestamp time.Time `gorm:"column:timestamp"`
	}

	pools := make([]*PoolInfo, 0, len(pairs))
	for _, p := range pairs {
		var r ReserveRow
		if err := e.db.WithContext(ctx).
			Table("pair_reserves").
			Select("reserve0, reserve1, timestamp").
			Where("pair_id = ?", p.ID).
			Order("timestamp DESC").
			Limit(1).
			Scan(&r).Error; err != nil {
			log.Printf("Load reserves failed for pair_id=%d: %v", p.ID, err)
			continue
		}

		if r.Reserve0 == "" || r.Reserve1 == "" {
			continue
		}

		r0, ok := new(big.Int).SetString(r.Reserve0, 10)
		if !ok {
			log.Printf("Invalid reserve0 for pair_id=%d: %s", p.ID, r.Reserve0)
			continue
		}
		r1, ok := new(big.Int).SetString(r.Reserve1, 10)
		if !ok {
			log.Printf("Invalid reserve1 for pair_id=%d: %s", p.ID, r.Reserve1)
			continue
		}

		fee := uint64(p.ExchangeFee)
		if fee == 0 {
			fee = 30
		}

		pools = append(pools, &PoolInfo{
			Address:    common.HexToAddress(p.PairAddress),
			Token0:     common.HexToAddress(p.Token0Address),
			Token1:     common.HexToAddress(p.Token1Address),
			Reserve0:   r0,
			Reserve1:   r1,
			Fee:        fee,
			DexName:    p.ExchangeName,
			Protocol:   p.ExchangeProto,
			DexAddress: common.HexToAddress(p.ExchangeRouter),
			LastUpdate: r.Timestamp,
		})
	}

	return pools, nil
}

// evaluatePathsConcurrently 并发评估路径
func (e *StrategyEngine) evaluatePathsConcurrently(
	ctx context.Context,
	paths [][]PathNode,
) []*ArbitrageOpportunity {

	var wg sync.WaitGroup
	resultCh := make(chan *ArbitrageOpportunity, len(paths))

	// 限制并发数
	semaphore := make(chan struct{}, e.config.MaxConcurrentPaths)

	for _, path := range paths {
		wg.Add(1)
		go func(p []PathNode) {
			defer wg.Done()

			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			opp, err := e.evaluatePath(ctx, p)
			if err != nil {
				// 这类错误在大规模路径枚举时非常常见（例如流动性不足/无正利润），属于正常过滤，不要刷屏。
				if !isExpectedPathEvalError(err) {
					log.Printf("Evaluate path failed: %v", err)
				}
				return
			}

			if opp != nil {
				resultCh <- opp
			}
		}(path)
	}

	// 等待所有计算完成
	go func() {
		wg.Wait()
		close(resultCh)
	}()

	// 收集结果
	var opportunities []*ArbitrageOpportunity
	for opp := range resultCh {
		opportunities = append(opportunities, opp)
	}

	return opportunities
}

// evaluatePath 评估单条路径
func (e *StrategyEngine) evaluatePath(
	ctx context.Context,
	path []PathNode,
) (*ArbitrageOpportunity, error) {

	if len(path) < e.config.MinPathLength {
		return nil, nil
	}

	// 1. 计算最优投入金额
	optimalAmount, expectedOut, err := e.optimizer.FindOptimalAmount(ctx, path)
	if err != nil {
		if isExpectedPathEvalError(err) {
			return nil, nil
		}
		return nil, err
	}

	// 2. 估算Gas成本
	gasEstimate, gasPrice, err := e.gasEstimator.EstimateGas(ctx, path, optimalAmount)
	if err != nil {
		if isExpectedPathEvalError(err) {
			return nil, nil
		}
		return nil, err
	}

	gasCost := new(big.Int).Mul(
		new(big.Int).SetUint64(gasEstimate),
		gasPrice,
	)

	// 3. 计算 minProfit = gasCost * GasMultiplier
	// 之前写死 2x，会导致在测试网几乎永远过滤掉（gas 成本相对更高）。
	multiplier := e.config.GasMultiplier
	if multiplier <= 0 {
		multiplier = 2.0
	}
	minProfitFloat := new(big.Float).SetPrec(256).SetInt(gasCost)
	minProfitFloat.Mul(minProfitFloat, new(big.Float).SetPrec(256).SetFloat64(multiplier))
	minProfit, _ := minProfitFloat.Int(nil)

	// 4. 计算预期利润
	expectProfit := new(big.Int).Sub(expectedOut, optimalAmount)

	// 5. 检查是否满足最小利润要求
	if expectProfit.Cmp(minProfit) < 0 {
		return nil, nil // 利润不足
	}

	// 6. 计算利润率
	profitRate := new(big.Float).Quo(
		new(big.Float).SetInt(expectProfit),
		new(big.Float).SetInt(optimalAmount),
	)
	profitRateFloat, _ := profitRate.Float64()

	// 7. 检查最小利润率
	if profitRateFloat < e.config.MinProfitRate {
		return nil, nil
	}

	// 8. 构建机会对象
	opp := &ArbitrageOpportunity{
		ID:           generateOpportunityID(path),
		SwapPath:     extractTokenPath(path),
		Dexes:        extractDexPath(path),
		DexNames:     extractDexNames(path),
		AmountIn:     optimalAmount,
		ExpectedOut:  expectedOut,
		ExpectProfit: expectProfit,
		MinProfit:    minProfit,
		ProfitRate:   profitRateFloat,
		GasEstimate:  gasEstimate,
		GasPrice:     gasPrice,
		GasCost:      gasCost,
		Timestamp:    time.Now(),
		ValidUntil:   time.Now().Add(e.config.ValidityDuration),
		Confidence:   calculateConfidence(path, profitRateFloat),
		PathLength:   len(path),
	}

	return opp, nil
}

// isExpectedPathEvalError 判断“路径被淘汰”的常见原因（属于正常现象，不应刷 error 日志）。
func isExpectedPathEvalError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	// AmountOptimizer
	if strings.Contains(msg, "insufficient liquidity") {
		return true
	}
	if strings.Contains(msg, "no profitable amount found") {
		return true
	}
	// ProfitCalculator
	if strings.Contains(msg, "invalid reserves") || strings.Contains(msg, "invalid amountIn") {
		return true
	}
	return false
}

// filterProfitableOpportunities 过滤有利可图的机会
func (e *StrategyEngine) filterProfitableOpportunities(
	opportunities []*ArbitrageOpportunity,
) []*ArbitrageOpportunity {

	var profitable []*ArbitrageOpportunity

	for _, opp := range opportunities {
		// 利润必须大于minProfit
		if opp.ExpectProfit.Cmp(opp.MinProfit) <= 0 {
			continue
		}

		// 利润率必须大于最小要求
		if opp.ProfitRate < e.config.MinProfitRate {
			continue
		}

		// 必须在有效期内
		if time.Now().After(opp.ValidUntil) {
			continue
		}

		profitable = append(profitable, opp)
	}

	return profitable
}

// GetPool 获取池子信息
func (e *StrategyEngine) GetPool(address common.Address) (*PoolInfo, error) {
	e.poolCacheMu.RLock()
	pool, exists := e.poolCache[address.Hex()]
	e.poolCacheMu.RUnlock()

	if exists && time.Since(pool.LastUpdate) < 10*time.Second {
		return pool, nil
	}

	// 从链上获取最新数据
	pool, err := e.fetchPoolFromChain(address)
	if err != nil {
		return nil, err
	}

	e.poolCacheMu.Lock()
	e.poolCache[address.Hex()] = pool
	e.poolCacheMu.Unlock()

	return pool, nil
}

// poolUpdateLoop 池子信息更新循环
func (e *StrategyEngine) poolUpdateLoop(ctx context.Context) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-e.stopCh:
			return
		case <-ticker.C:
			e.updateAllPools(ctx)
		}
	}
}

// updateAllPools 更新所有池子
func (e *StrategyEngine) updateAllPools(ctx context.Context) {
	e.poolCacheMu.RLock()
	addresses := make([]common.Address, 0, len(e.poolCache))
	for addr := range e.poolCache {
		addresses = append(addresses, common.HexToAddress(addr))
	}
	e.poolCacheMu.RUnlock()

	for _, addr := range addresses {
		pool, err := e.fetchPoolFromChain(addr)
		if err != nil {
			log.Printf("Update pool %s failed: %v", addr.Hex(), err)
			continue
		}

		e.poolCacheMu.Lock()
		e.poolCache[addr.Hex()] = pool
		e.poolCacheMu.Unlock()
	}
}

// fetchPoolFromChain 从链上获取池子信息
func (e *StrategyEngine) fetchPoolFromChain(address common.Address) (*PoolInfo, error) {
	// 这里调用web3Client获取池子信息
	// 需要根据你的实际实现来完成
	return nil, fmt.Errorf("not implemented")
}

// 辅助函数
func generateOpportunityID(path []PathNode) string {
	return fmt.Sprintf("opp_%d_%s", time.Now().UnixNano(), path[0].Token.Hex()[:8])
}

func extractTokenPath(path []PathNode) []common.Address {
	tokens := make([]common.Address, len(path))
	for i, node := range path {
		tokens[i] = node.Token
	}
	return tokens
}

func extractDexPath(path []PathNode) []common.Address {
	// DEX数量 = 路径长度 - 1
	dexes := make([]common.Address, len(path)-1)
	for i := 0; i < len(path)-1; i++ {
		dexes[i] = path[i].Dex
	}
	return dexes
}

func extractDexNames(path []PathNode) []string {
	names := make([]string, len(path)-1)
	for i := 0; i < len(path)-1; i++ {
		names[i] = path[i].DexName
	}
	return names
}

func calculateConfidence(path []PathNode, profitRate float64) float64 {
	// 置信度计算：考虑路径长度、利润率等因素
	// 路径越短越可靠
	pathConfidence := 1.0 - float64(len(path)-3)*0.1
	if pathConfidence < 0.5 {
		pathConfidence = 0.5
	}

	// 利润率适中更可靠（太高可能是假数据）
	profitConfidence := 1.0
	if profitRate > 0.05 { // 5%以上可能有问题
		profitConfidence = 0.8
	}
	if profitRate > 0.1 { // 10%以上很可疑
		profitConfidence = 0.5
	}

	return pathConfidence * profitConfidence
}

// ============================================================
// P0: 套利机会数据库存储
// ============================================================

// SaveOpportunitiesToDB 将套利机会保存到数据库
func (e *StrategyEngine) SaveOpportunitiesToDB(ctx context.Context, opps []*ArbitrageOpportunity) error {
	if e.db == nil {
		return fmt.Errorf("db is nil")
	}

	for _, opp := range opps {
		dbOpp, err := e.convertToDBModel(opp)
		if err != nil {
			log.Printf("Convert opportunity failed: %v", err)
			continue
		}

		// 检查是否已存在相同路径且未过期的机会
		var existing models.ArbitrageOpportunity
		swapPathJSON := dbOpp.SwapPath
		if err := e.db.Where("swap_path = ? AND status = ? AND expires_at > ?",
			swapPathJSON, "pending", time.Now()).First(&existing).Error; err == nil {
			// 已存在且未过期，更新利润信息
			e.db.Model(&existing).Updates(map[string]interface{}{
				"profit_rate":     dbOpp.ProfitRate,
				"expected_profit": dbOpp.ExpectedProfit,
				"min_profit":      dbOpp.MinProfit,
				"gas_estimate":    dbOpp.GasEstimate,
				"max_gas_price":   dbOpp.MaxGasPrice,
				"updated_at":      time.Now(),
			})
			continue
		}

		// 创建新记录
		if err := e.db.Create(dbOpp).Error; err != nil {
			log.Printf("Save opportunity failed: %v", err)
			continue
		}
		log.Printf("✅ Saved opportunity: %s (profit_rate=%.4f%%)", dbOpp.SwapPath, dbOpp.ProfitRate)
	}

	return nil
}

// convertToDBModel 将策略层对象转换为数据库模型
func (e *StrategyEngine) convertToDBModel(opp *ArbitrageOpportunity) (*models.ArbitrageOpportunity, error) {
	// 序列化路径为 JSON
	swapPathJSON, _ := json.Marshal(addressesToStrings(opp.SwapPath))
	dexPathJSON, _ := json.Marshal(opp.DexNames)
	dexRoutersJSON, _ := json.Marshal(addressesToStrings(opp.Dexes))

	// 查找 token ID（根据地址查询）
	var tokenIn, tokenOut models.Token
	tokenInAddr := opp.SwapPath[0].Hex()
	tokenOutAddr := opp.SwapPath[len(opp.SwapPath)-1].Hex()

	if err := e.db.Where("LOWER(address) = LOWER(?)", tokenInAddr).First(&tokenIn).Error; err != nil {
		log.Printf("Token not found for address: %s", tokenInAddr)
		// 创建临时记录，避免外键约束失败
		tokenIn.ID = 0
	}
	if err := e.db.Where("LOWER(address) = LOWER(?)", tokenOutAddr).First(&tokenOut).Error; err != nil {
		log.Printf("Token not found for address: %s", tokenOutAddr)
		tokenOut.ID = 0
	}

	// 确定套利类型
	arbType := "cross_dex"
	if len(opp.SwapPath) > 3 {
		arbType = "triangular"
	}

	// 构建数据库模型
	dbOpp := &models.ArbitrageOpportunity{
		ArbitrageType:  arbType,
		AmountIn:       opp.AmountIn.String(),
		ExpectedProfit: opp.ExpectProfit.String(),
		MinProfit:      opp.MinProfit.String(),
		ProfitRate:     opp.ProfitRate * 100, // 转换为百分比
		SwapPath:       string(swapPathJSON),
		DexPath:        string(dexPathJSON),
		DexRouters:     string(dexRoutersJSON),
		PoolAddresses:  "[]", // 空 JSON 数组
		FeeTiers:       "[]", // 空 JSON 数组
		GasEstimate:    opp.GasEstimate,
		MaxGasPrice:    opp.GasPrice.String(),
		Status:         "pending",
		Priority:       calculatePriority(opp.ProfitRate),
		ExpiresAt:      opp.ValidUntil,
	}

	// 只有找到 Token 时才设置外键（使用指针）
	if tokenIn.ID > 0 {
		dbOpp.TokenInID = &tokenIn.ID
	}
	if tokenOut.ID > 0 {
		dbOpp.TokenOutID = &tokenOut.ID
	}

	return dbOpp, nil
}

func addressesToStrings(addrs []common.Address) []string {
	result := make([]string, len(addrs))
	for i, addr := range addrs {
		result[i] = addr.Hex()
	}
	return result
}

func calculatePriority(profitRate float64) int {
	if profitRate > 0.05 {
		return 100
	}
	if profitRate > 0.03 {
		return 80
	}
	if profitRate > 0.01 {
		return 50
	}
	return 20
}
