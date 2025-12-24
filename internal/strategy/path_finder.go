// internal/strategy/path_finder.go
package strategy

import (
    "context"
    "fmt"
    "log"
    "sync"

    "github.com/ethereum/go-ethereum/common"
)

// PathFinder 路径发现器
type PathFinder struct {
    config *StrategyConfig
    engine *StrategyEngine
    
    // 代币图：token => []connectedPools
    tokenGraph   map[common.Address][]*PoolInfo
    tokenGraphMu sync.RWMutex
}

// NewPathFinder 创建路径发现器
func NewPathFinder(config *StrategyConfig, engine *StrategyEngine) *PathFinder {
    return &PathFinder{
        config:     config,
        engine:     engine,
        tokenGraph: make(map[common.Address][]*PoolInfo),
    }
}

// BuildTokenGraph 构建代币关系图
func (pf *PathFinder) BuildTokenGraph(ctx context.Context, pools []*PoolInfo) {
    pf.tokenGraphMu.Lock()
    defer pf.tokenGraphMu.Unlock()
    
    // 清空旧图
    pf.tokenGraph = make(map[common.Address][]*PoolInfo)
    
    for _, pool := range pools {
        // Token0 可以换到 Token1
        pf.tokenGraph[pool.Token0] = append(pf.tokenGraph[pool.Token0], pool)
        // Token1 可以换到 Token0
        pf.tokenGraph[pool.Token1] = append(pf.tokenGraph[pool.Token1], pool)
    }
    
    log.Printf("Built token graph with %d tokens", len(pf.tokenGraph))
}

// FindAllPaths 查找所有可行的套利路径
func (pf *PathFinder) FindAllPaths(ctx context.Context) ([][]PathNode, error) {
    var allPaths [][]PathNode
    
    // 对每个基础代币，查找回环路径
    for _, baseToken := range pf.config.BaseTokens {
        // 查找不同长度的路径
        for pathLen := pf.config.MinPathLength; pathLen <= pf.config.MaxPathLength; pathLen++ {
            paths := pf.findPathsFromToken(ctx, baseToken, pathLen)
            allPaths = append(allPaths, paths...)
        }
    }
    
    return allPaths, nil
}

// findPathsFromToken 从指定代币开始查找路径
func (pf *PathFinder) findPathsFromToken(
    ctx context.Context,
    startToken common.Address,
    targetLength int,
) [][]PathNode {
    
    var results [][]PathNode
    
    // 初始路径
    initialPath := []PathNode{{Token: startToken}}
    
    // DFS搜索
    pf.dfs(ctx, startToken, startToken, initialPath, targetLength, &results)
    
    return results
}

// dfs 深度优先搜索
func (pf *PathFinder) dfs(
    ctx context.Context,
    currentToken common.Address,
    startToken common.Address,
    currentPath []PathNode,
    targetLength int,
    results *[][]PathNode,
) {
    // 检查是否已达到目标长度
    if len(currentPath) == targetLength {
        // 最后一个token必须是起始token（形成环）
        if currentToken == startToken && len(currentPath) > 1 {
            // 复制路径
            pathCopy := make([]PathNode, len(currentPath))
            copy(pathCopy, currentPath)
            *results = append(*results, pathCopy)
        }
        return
    }
    
    // 如果已经超过目标长度，返回
    if len(currentPath) >= targetLength {
        return
    }
    
    pf.tokenGraphMu.RLock()
    pools := pf.tokenGraph[currentToken]
    pf.tokenGraphMu.RUnlock()
    
    // 遍历所有可能的下一跳
    for _, pool := range pools {
        var nextToken common.Address
        if pool.Token0 == currentToken {
            nextToken = pool.Token1
        } else {
            nextToken = pool.Token0
        }
        
        // 检查是否形成无效循环（中间重复）
        if pf.hasIntermediateCycle(currentPath, nextToken, startToken) {
            continue
        }
        
        // 对每个支持的DEX尝试
        for _, dexConfig := range pf.config.SupportedDexes {
            // 检查该池子是否属于这个DEX
            if !pf.isPoolBelongsToDex(pool, dexConfig) {
                continue
            }
            
            // 构建新节点
            newNode := PathNode{
                Token:   nextToken,
                Pool:    pool,
                Dex:     dexConfig.RouterAddress,
                DexName: dexConfig.Name,
            }
            
            // 更新当前路径的DEX信息
            if len(currentPath) > 0 {
                currentPath[len(currentPath)-1].Pool = pool
                currentPath[len(currentPath)-1].Dex = dexConfig.RouterAddress
                currentPath[len(currentPath)-1].DexName = dexConfig.Name
            }
            
            // 继续搜索
            newPath := append(currentPath, newNode)
            pf.dfs(ctx, nextToken, startToken, newPath, targetLength, results)
        }
    }
}

// hasIntermediateCycle 检查中间是否有重复
func (pf *PathFinder) hasIntermediateCycle(
    path []PathNode,
    nextToken common.Address,
    startToken common.Address,
) bool {
    // 允许回到起始代币（这是套利的要求）
    // 但不允许在中间重复
    for i := 1; i < len(path); i++ {
        if path[i].Token == nextToken && nextToken != startToken {
            return true
        }
    }
    return false
}

// isPoolBelongsToDex 检查池子是否属于指定DEX
func (pf *PathFinder) isPoolBelongsToDex(pool *PoolInfo, dex DexConfig) bool {
    // 根据池子地址或Factory判断
    return pool.DexName == dex.Name
}

// FindPathsWithConstraints 带约束的路径查找
func (pf *PathFinder) FindPathsWithConstraints(
    ctx context.Context,
    startToken common.Address,
    minLength int,
    maxLength int,
    requiredDexes []string,
    excludedTokens []common.Address,
) ([][]PathNode, error) {
    
    excludeMap := make(map[common.Address]bool)
    for _, token := range excludedTokens {
        excludeMap[token] = true
    }
    
    var allPaths [][]PathNode
    
    for pathLen := minLength; pathLen <= maxLength; pathLen++ {
        paths := pf.findPathsWithFilters(ctx, startToken, pathLen, requiredDexes, excludeMap)
        allPaths = append(allPaths, paths...)
    }
    
    return allPaths, nil
}

// findPathsWithFilters 带过滤条件的路径查找
func (pf *PathFinder) findPathsWithFilters(
    ctx context.Context,
    startToken common.Address,
    targetLength int,
    requiredDexes []string,
    excludedTokens map[common.Address]bool,
) [][]PathNode {
    
    var results [][]PathNode
    initialPath := []PathNode{{Token: startToken}}
    
    pf.dfsWithFilters(ctx, startToken, startToken, initialPath, targetLength, 
        requiredDexes, excludedTokens, &results)
    
    return results
}

func (pf *PathFinder) dfsWithFilters(
    ctx context.Context,
    currentToken common.Address,
    startToken common.Address,
    currentPath []PathNode,
    targetLength int,
    requiredDexes []string,
    excludedTokens map[common.Address]bool,
    results *[][]PathNode,
) {
    if len(currentPath) == targetLength {
        if currentToken == startToken && len(currentPath) > 1 {
            // 验证是否包含所有必需的DEX
            if pf.containsAllDexes(currentPath, requiredDexes) {
                pathCopy := make([]PathNode, len(currentPath))
                copy(pathCopy, currentPath)
                *results = append(*results, pathCopy)
            }
        }
        return
    }
    
    if len(currentPath) >= targetLength {
        return
    }
    
    pf.tokenGraphMu.RLock()
    pools := pf.tokenGraph[currentToken]
    pf.tokenGraphMu.RUnlock()
    
    for _, pool := range pools {
        var nextToken common.Address
        if pool.Token0 == currentToken {
            nextToken = pool.Token1
        } else {
            nextToken = pool.Token0
        }
        
        // 排除特定代币
        if excludedTokens[nextToken] && nextToken != startToken {
            continue
        }
        
        if pf.hasIntermediateCycle(currentPath, nextToken, startToken) {
            continue
        }
        
        for _, dexConfig := range pf.config.SupportedDexes {
            if !pf.isPoolBelongsToDex(pool, dexConfig) {
                continue
            }
            
            newNode := PathNode{
                Token:   nextToken,
                Pool:    pool,
                Dex:     dexConfig.RouterAddress,
                DexName: dexConfig.Name,
            }
            
            if len(currentPath) > 0 {
                currentPath[len(currentPath)-1].Pool = pool
                currentPath[len(currentPath)-1].Dex = dexConfig.RouterAddress
                currentPath[len(currentPath)-1].DexName = dexConfig.Name
            }
            
            newPath := append(currentPath, newNode)
            pf.dfsWithFilters(ctx, nextToken, startToken, newPath, targetLength,
                requiredDexes, excludedTokens, results)
        }
    }
}

func (pf *PathFinder) containsAllDexes(path []PathNode, requiredDexes []string) bool {
    if len(requiredDexes) == 0 {
        return true
    }
    
    dexSet := make(map[string]bool)
    for i := 0; i < len(path)-1; i++ {
        dexSet[path[i].DexName] = true
    }
    
    for _, dex := range requiredDexes {
        if !dexSet[dex] {
            return false
        }
    }
    
    return true
}