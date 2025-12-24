// internal/strategy/strategy.go
package strategy

import (
    "context"
    "fmt"
    "log"
    "math/big"
    "sort"
    "sync"
    "time"

    "github.com/ethereum/go-ethereum/common"
    "your-project/internal/database"
    "your-project/pkg/cache"
    "your-project/pkg/web3"
)

// StrategyEngine 策略引擎
type StrategyEngine struct {
    config        *StrategyConfig
    web3Client    *web3.Client
    db            *database.Database
    cache         *cache.RedisCache
    pathFinder    *PathFinder
    profitCalc    *ProfitCalculator
    optimizer     *AmountOptimizer
    gasEstimator  *GasEstimator
    
    // 池子信息缓存
    poolCache     map[string]*PoolInfo
    poolCacheMu   sync.RWMutex
    
    // 运行状态
    running       bool
    stopCh        chan struct{}
}

// NewStrategyEngine 创建策略引擎
func NewStrategyEngine(
    config *StrategyConfig,
    web3Client *web3.Client,
    db *database.Database,
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
    
    return profitable, nil
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
                log.Printf("Evaluate path failed: %v", err)
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
        return nil, err
    }
    
    // 2. 估算Gas成本
    gasEstimate, gasPrice, err := e.gasEstimator.EstimateGas(ctx, path, optimalAmount)
    if err != nil {
        return nil, err
    }
    
    gasCost := new(big.Int).Mul(
        new(big.Int).SetUint64(gasEstimate),
        gasPrice,
    )
    
    // 3. 计算minProfit = 2 * gasCost
    minProfit := new(big.Int).Mul(gasCost, big.NewInt(2))
    
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