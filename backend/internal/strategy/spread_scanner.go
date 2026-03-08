// internal/strategy/spread_scanner.go
// 动态价差扫描器 —— 自动发现跨 DEX 套利机会
//
// 核心理念：不依赖预设代币列表，直接从 PriceCache 发现所有代币对的跨 DEX 价差。
// 任何新币种只要进入 PriceCache（含新池子事件），就会自动被发现和评估。
package strategy

import (
	"context"
	"math"
	"math/big"
	"strings"
	"sync"
	"time"

	"github.com/defi-bot/backend/pkg/cache"
	"github.com/defi-bot/backend/pkg/log"
	"github.com/ethereum/go-ethereum/common"
)

// SpreadScanner 跨 DEX 价差扫描器
// 消费 PriceCache 中的所有池子数据，自动发现同一代币对在不同 DEX 上的价差
type SpreadScanner struct {
	priceCache *cache.PriceCache

	// 扫描配置
	minSpreadBps    int           // 最小触发价差（basis points，如 30 = 0.3%）
	scanInterval    time.Duration // 全量扫描间隔（WebSocket 触发之外的保底扫描）
	maxOppsPerScan  int           // 每次扫描最多输出的机会数

	// DEX 名称 → Router 地址映射（来自配置，替代硬编码）
	dexRouters map[string]common.Address

	// 输出
	opportunityCh chan *SpreadOpportunity

	// 统计
	totalScans  uint64
	totalFound  uint64
	lastScanAt  time.Time
	mu          sync.RWMutex

	stopCh chan struct{}
}

// SpreadOpportunity 跨 DEX 套利机会（两跳：V2买→V3卖 或 V3买→V2卖）
type SpreadOpportunity struct {
	ID string

	// 代币
	Token0 common.Address // 套利起点/终点代币
	Token1 common.Address // 中间代币

	// 池子信息
	BuyPool  *cache.PoolPrice // 低价 DEX（在这里买入 Token1）
	SellPool *cache.PoolPrice // 高价 DEX（在这里卖出 Token1）

	// 价差
	BuyPrice    float64 // 买入价（Token0 per Token1）
	SellPrice   float64 // 卖出价（Token0 per Token1）
	SpreadBps   int     // 价差（basis points）
	SpreadFloat float64 // 价差（小数，如 0.005 = 0.5%）

	// 构建好的套利路径（直接可用于合约调用）
	SwapPath  []common.Address // [Token0, Token1, Token0]
	DexPath   []common.Address // [BuyPool.RouterAddr, SellPool.RouterAddr]
	DexNames  []string

	IsV2ToV3 bool // true=V2买V3卖, false=V3买V2卖

	DiscoveredAt time.Time
}

// NewSpreadScanner 创建扫描器
func NewSpreadScanner(priceCache *cache.PriceCache, minSpreadBps int) *SpreadScanner {
	if minSpreadBps <= 0 {
		minSpreadBps = 30 // 默认 0.3%
	}
	return &SpreadScanner{
		priceCache:     priceCache,
		minSpreadBps:   minSpreadBps,
		scanInterval:   3 * time.Second, // 每 3 秒全量扫描一次（Arbitrum 0.25s 出块）
		maxOppsPerScan: 20,
		dexRouters:     make(map[string]common.Address),
		opportunityCh:  make(chan *SpreadOpportunity, 200),
		stopCh:         make(chan struct{}),
	}
}

// SetDexRouters 设置 DEX 名称 → Router 地址映射
func (s *SpreadScanner) SetDexRouters(routers map[string]common.Address) {
	s.dexRouters = routers
}

// Start 启动扫描器
func (s *SpreadScanner) Start(ctx context.Context) {
	// 1. 订阅 PriceCache 的价格变化事件（实时触发）
	eventCh := s.priceCache.Subscribe()

	// 2. 首次全量扫描
	go s.fullScan()

	go func() {
		ticker := time.NewTicker(s.scanInterval)
		defer ticker.Stop()
		defer s.priceCache.Unsubscribe(eventCh)

		for {
			select {
			case <-ctx.Done():
				return
			case <-s.stopCh:
				return
			case event := <-eventCh:
				// 价格变化时，只扫描受影响的代币对（增量扫描，更快）
				go s.incrementalScan(event.PoolAddress)
			case <-ticker.C:
				// 定时全量扫描（保底，防止遗漏）
				go s.fullScan()
			}
		}
	}()

	log.Strategy().Info().
		Int("min_spread_bps", s.minSpreadBps).
		Dur("scan_interval", s.scanInterval).
		Msg("SpreadScanner: Started (auto-discovery mode)")
}

// Stop 停止扫描器
func (s *SpreadScanner) Stop() {
	close(s.stopCh)
}

// ScanNow 立即触发一次全量扫描（用于鲸鱼交易等外部事件驱动）
func (s *SpreadScanner) ScanNow() {
	go s.fullScan()
}

// Opportunities 获取机会通道
func (s *SpreadScanner) Opportunities() <-chan *SpreadOpportunity {
	return s.opportunityCh
}

// fullScan 全量扫描：遍历所有池子，按代币对分组，发现跨 DEX 价差
func (s *SpreadScanner) fullScan() {
	startTime := time.Now()
	all := s.priceCache.GetAll()
	if len(all) == 0 {
		return
	}

	// 按代币对分组（token0-token1 规范化排序）
	pairGroups := make(map[string][]*cache.PoolPrice)
	for _, pool := range all {
		if pool.Price <= 0 {
			continue
		}
		key := tokenPairKey(pool.Token0.Hex(), pool.Token1.Hex())
		pairGroups[key] = append(pairGroups[key], pool)
	}

	found := 0
	for _, pools := range pairGroups {
		if len(pools) < 2 {
			continue // 只有一个 DEX 的代币对，无跨 DEX 价差
		}

		opps := s.evaluatePair(pools)
		for _, opp := range opps {
			select {
			case s.opportunityCh <- opp:
				found++
			default:
				// channel 满了，丢弃（不阻塞）
			}
		}

		if found >= s.maxOppsPerScan {
			break
		}
	}

	s.mu.Lock()
	s.totalScans++
	s.totalFound += uint64(found)
	s.lastScanAt = time.Now()
	s.mu.Unlock()

	if found > 0 {
		log.Strategy().Info().
			Int("pairs_scanned", len(pairGroups)).
			Int("opportunities", found).
			Dur("elapsed", time.Since(startTime)).
			Msg("SpreadScanner: Full scan complete")
	}
}

// incrementalScan 增量扫描：只重新评估含特定池子的代币对
func (s *SpreadScanner) incrementalScan(poolAddr string) {
	pool, ok := s.priceCache.Get(poolAddr)
	if !ok || pool.Price <= 0 {
		return
	}

	// 找到同一代币对的所有其他池子
	siblings := s.priceCache.GetByTokenPair(
		strings.ToLower(pool.Token0.Hex()),
		strings.ToLower(pool.Token1.Hex()),
	)
	if len(siblings) < 2 {
		return
	}

	opps := s.evaluatePair(siblings)
	for _, opp := range opps {
		select {
		case s.opportunityCh <- opp:
		default:
		}
	}
}

// evaluatePair 评估同一代币对在多个 DEX 上的价差
// 输入: 同一代币对的所有池子
// 输出: 有利可图的跨 DEX 套利机会
func (s *SpreadScanner) evaluatePair(pools []*cache.PoolPrice) []*SpreadOpportunity {
	if len(pools) < 2 {
		return nil
	}

	// 分为 V2 和 V3 两组（过滤低流动性和过期池子）
	var v2Pools, v3Pools []*cache.PoolPrice
	for _, p := range pools {
		if p.Price <= 0 || !hasMinLiquidity(p) || isStale(p) {
			continue
		}
		if isV3Protocol(p.Protocol) {
			v3Pools = append(v3Pools, p)
		} else {
			v2Pools = append(v2Pools, p)
		}
	}

	// 需要 V2 和 V3 都存在才有跨协议套利机会
	if len(v2Pools) == 0 || len(v3Pools) == 0 {
		// 也检查同协议不同 DEX（如 Camelot vs SushiSwap，两者都是 V2 兼容）
		return s.evaluateSameProtocolSpread(pools)
	}

	var opps []*SpreadOpportunity

	// 选最佳 V2 和 V3 池子（流动性最高）
	bestV2 := bestPool(v2Pools)
	bestV3 := bestPool(v3Pools)

	// 代币对统一化：找到公共的 Token0/Token1
	// Price 的定义: token1/token0（已调整 decimals）
	v2Price := bestV2.Price  // token1/token0 in V2
	v3Price := bestV3.Price  // token1/token0 in V3（同一代币对，同一方向）

	if v2Price <= 0 || v3Price <= 0 {
		return nil
	}

	// 计算价差（防御除零和溢出）
	minPrice := math.Min(v2Price, v3Price)
	if minPrice <= 0 {
		return nil
	}
	rawSpread := math.Abs(v2Price-v3Price) / minPrice
	// 防止数值异常（如某个价格为 0 但没被过滤到）
	if math.IsInf(rawSpread, 0) || math.IsNaN(rawSpread) || rawSpread > 100 {
		return nil // 忽略超过 10000% 价差的异常值
	}

	// 扣除两端 swap fee 后的净价差
	// V2 fee: 0.3% (30 bps), V3 fee: 从 PriceCache.Fee 获取 (bps)
	buyFeeBps := float64(bestV2.Fee) // V2 买入端 fee (bps，如 30=0.3%)
	if buyFeeBps == 0 { buyFeeBps = 30 } // V2 默认 0.3%
	sellFeeBps := float64(bestV3.Fee) // V3 卖出端 fee (bps，如 5=0.05%)
	if sellFeeBps == 0 { sellFeeBps = 30 } // fallback
	if v2Price >= v3Price {
		// V3 买入 V2 卖出: 反向
		buyFeeBps = float64(bestV3.Fee)
		if buyFeeBps == 0 { buyFeeBps = 30 }
		sellFeeBps = float64(bestV2.Fee)
		if sellFeeBps == 0 { sellFeeBps = 30 }
	}
	totalFeePct := (buyFeeBps + sellFeeBps) / 10000.0 // 转为小数
	spread := rawSpread - totalFeePct
	if spread <= 0 {
		return nil // 扣完 fee 后无利润
	}
	spreadBps := int(spread * 10000)

	if spreadBps < s.minSpreadBps {
		return nil
	}

	// 确定套利方向
	var opp *SpreadOpportunity
	if v2Price < v3Price {
		// V2 价格低 → 在 V2 用 Token0 买 Token1，在 V3 用 Token1 卖回 Token0
		opp = s.buildOpportunity(bestV2, bestV3, v2Price, v3Price, spread, spreadBps, true)
	} else {
		// V3 价格低 → 在 V3 用 Token0 买 Token1，在 V2 用 Token1 卖回 Token0
		opp = s.buildOpportunity(bestV3, bestV2, v3Price, v2Price, spread, spreadBps, false)
	}

	if opp != nil {
		opps = append(opps, opp)
		log.Strategy().Info().
			Str("token0", bestV2.Token0.Hex()[:10]).
			Str("token1", bestV2.Token1.Hex()[:10]).
			Str("buy_dex", opp.BuyPool.DexName).
			Str("sell_dex", opp.SellPool.DexName).
			Float64("spread_pct", spread*100).
			Int("spread_bps", spreadBps).
			Bool("v2_to_v3", opp.IsV2ToV3).
			Msg("SpreadScanner: Found cross-DEX spread!")
	}

	return opps
}

// evaluateSameProtocolSpread 评估同协议不同 DEX 之间的价差（如 SushiSwap vs Camelot）
func (s *SpreadScanner) evaluateSameProtocolSpread(pools []*cache.PoolPrice) []*SpreadOpportunity {
	if len(pools) < 2 {
		return nil
	}

	// 过滤低流动性和过期池子
	var validPools []*cache.PoolPrice
	for _, p := range pools {
		if p.Price > 0 && hasMinLiquidity(p) && !isStale(p) {
			validPools = append(validPools, p)
		}
	}
	if len(validPools) < 2 {
		return nil
	}

	var best, second *cache.PoolPrice
	best = validPools[0]
	for _, p := range validPools[1:] {
		if p.DexName != best.DexName {
			second = p
			break
		}
	}

	if second == nil || best.Price <= 0 || second.Price <= 0 {
		return nil
	}

	minP := math.Min(best.Price, second.Price)
	if minP <= 0 {
		return nil
	}
	rawSpread := math.Abs(best.Price-second.Price) / minP
	if math.IsInf(rawSpread, 0) || math.IsNaN(rawSpread) || rawSpread > 100 {
		return nil
	}

	// 扣除两端 swap fee
	bestFeeBps := float64(best.Fee)
	if bestFeeBps == 0 {
		bestFeeBps = 30 // V2 默认 0.3%
	}
	secondFeeBps := float64(second.Fee)
	if secondFeeBps == 0 {
		secondFeeBps = 30
	}
	totalFeePct := (bestFeeBps + secondFeeBps) / 10000.0
	spread := rawSpread - totalFeePct
	if spread <= 0 {
		return nil
	}
	spreadBps := int(spread * 10000)

	if spreadBps < s.minSpreadBps {
		return nil
	}

	// 构建机会
	var buyPool, sellPool *cache.PoolPrice
	if best.Price < second.Price {
		buyPool, sellPool = best, second
	} else {
		buyPool, sellPool = second, best
	}

	opp := s.buildOpportunity(buyPool, sellPool, buyPool.Price, sellPool.Price, spread, spreadBps, false)
	if opp != nil {
		return []*SpreadOpportunity{opp}
	}
	return nil
}

// buildOpportunity 构建套利机会对象
func (s *SpreadScanner) buildOpportunity(buyPool, sellPool *cache.PoolPrice, buyPrice, sellPrice float64, spread float64, spreadBps int, isV2ToV3 bool) *SpreadOpportunity {
	if buyPool == nil || sellPool == nil {
		return nil
	}

	// 确保两个池子是同一代币对
	if !sameTokenPair(buyPool, sellPool) {
		return nil
	}

	// 确定套利的 Token0（起点/终点）和 Token1（中间代币）
	// 我们用 Token0 作为套利的起点，Token1 作为中间资产
	token0 := buyPool.Token0
	token1 := buyPool.Token1

	// 路径: Token0 → Token1 (买入) → Token0 (卖出)
	swapPath := []common.Address{token0, token1, token0}
	buyRouter := s.getDexRouter(buyPool)
	sellRouter := s.getDexRouter(sellPool)

	// 同一个 Router 不同 fee tier 不是真正的跨 DEX 套利
	// 这种机会已被 MEV 压缩到无利可图
	if buyRouter == sellRouter {
		return nil
	}

	dexPath := []common.Address{buyRouter, sellRouter}
	dexNames := []string{buyPool.DexName, sellPool.DexName}

	return &SpreadOpportunity{
		ID:           generateSpreadID(token0, token1, buyPool.DexName, sellPool.DexName),
		Token0:       token0,
		Token1:       token1,
		BuyPool:      buyPool,
		SellPool:     sellPool,
		BuyPrice:     buyPrice,
		SellPrice:    sellPrice,
		SpreadBps:    spreadBps,
		SpreadFloat:  spread,
		SwapPath:     swapPath,
		DexPath:      dexPath,
		DexNames:     dexNames,
		IsV2ToV3:     isV2ToV3,
		DiscoveredAt: time.Now(),
	}
}

// GetStats 获取扫描统计
func (s *SpreadScanner) GetStats() (totalScans, totalFound uint64, lastScan time.Time) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.totalScans, s.totalFound, s.lastScanAt
}

// ============ 辅助函数 ============

func tokenPairKey(t0, t1 string) string {
	t0, t1 = strings.ToLower(t0), strings.ToLower(t1)
	if t0 < t1 {
		return t0 + "|" + t1
	}
	return t1 + "|" + t0
}

func isV3Protocol(protocol string) bool {
	return strings.Contains(strings.ToLower(protocol), "v3")
}

func bestPool(pools []*cache.PoolPrice) *cache.PoolPrice {
	if len(pools) == 0 {
		return nil
	}
	best := pools[0]
	for _, p := range pools[1:] {
		if poolScore(p) > poolScore(best) {
			best = p
		}
	}
	return best
}

func poolScore(p *cache.PoolPrice) float64 {
	if p == nil || p.Price <= 0 {
		return 0
	}
	if p.IsV3 && p.Liquidity != nil {
		f, _ := p.Liquidity.Float64()
		return 1000 + f/1e15
	}
	if p.Reserve0 != nil && p.Reserve1 != nil {
		r0, _ := p.Reserve0.Float64()
		r1, _ := p.Reserve1.Float64()
		return r0 * r1 / 1e30
	}
	return 0.1
}

// hasMinLiquidity 检查池子是否有足够流动性
// V3: Liquidity >= 1e12 (约 0.001 ETH 级别的活跃流动性)
// V2: Reserve0 * Reserve1 > 0 (有实际储备)
func hasMinLiquidity(p *cache.PoolPrice) bool {
	if p.IsV3 {
		if p.Liquidity == nil || p.Liquidity.Sign() <= 0 {
			return false
		}
		// V3 Liquidity 是 uint128，活跃池子通常 > 1e15
		// 设最低阈值为 1e12，过滤几乎无流动性的池子
		minLiq := new(big.Int).SetUint64(1_000_000_000_000) // 1e12
		return p.Liquidity.Cmp(minLiq) >= 0
	}
	// V2: 检查 reserves 有足够流动性
	// 至少需要 Reserve0 和 Reserve1 各 > 1e15 (对 18 位精度代币约 0.001 个)
	if p.Reserve0 == nil || p.Reserve1 == nil {
		return false
	}
	minReserve := new(big.Int).SetUint64(1_000_000_000_000_000) // 1e15
	return p.Reserve0.Cmp(minReserve) > 0 && p.Reserve1.Cmp(minReserve) > 0
}

// isStale 检查池子数据是否过期（超过 5 分钟未更新）
func isStale(p *cache.PoolPrice) bool {
	if p.UpdatedAt.IsZero() {
		return false // 没有时间戳，不过滤
	}
	return time.Since(p.UpdatedAt) > 5*time.Minute
}

func sameTokenPair(a, b *cache.PoolPrice) bool {
	a0 := strings.ToLower(a.Token0.Hex())
	a1 := strings.ToLower(a.Token1.Hex())
	b0 := strings.ToLower(b.Token0.Hex())
	b1 := strings.ToLower(b.Token1.Hex())
	return (a0 == b0 && a1 == b1) || (a0 == b1 && a1 == b0)
}

// getDexRouter 根据池子的 DEX 名称查找 Router 地址
// 优先使用配置的 dexRouters 映射，fallback 到已知的 Arbitrum 地址
func (s *SpreadScanner) getDexRouter(p *cache.PoolPrice) common.Address {
	name := strings.ToLower(p.DexName)

	// 优先从配置的 dexRouters map 查找（精确匹配 key）
	if len(s.dexRouters) > 0 {
		// 尝试精确匹配原始名称
		if addr, ok := s.dexRouters[p.DexName]; ok {
			return addr
		}
		// 模糊匹配：遍历 map key
		for key, addr := range s.dexRouters {
			if strings.Contains(name, strings.ToLower(key)) {
				return addr
			}
		}
	}

	// Fallback: 已知 Arbitrum Router 地址
	switch {
	case strings.Contains(name, "sushi"):
		return common.HexToAddress("0x1b02dA8Cb0d097eB8D57A175b88c7D8b47997506")
	case strings.Contains(name, "uniswap") && strings.Contains(name, "v3"):
		return common.HexToAddress("0xE592427A0AEce92De3Edee1F18E0157C05861564")
	case strings.Contains(name, "camelot"):
		return common.HexToAddress("0xc873fEcbd354f5A56E00E710B90EF4201db2448d")
	default:
		return common.HexToAddress("0x1b02dA8Cb0d097eB8D57A175b88c7D8b47997506") // SushiSwap 作为默认
	}
}

func generateSpreadID(t0, t1 common.Address, buyDex, sellDex string) string {
	return strings.ToLower(t0.Hex()[:8]) + "_" +
		strings.ToLower(t1.Hex()[:8]) + "_" +
		buyDex + "_" + sellDex + "_" +
		time.Now().Format("150405")
}
