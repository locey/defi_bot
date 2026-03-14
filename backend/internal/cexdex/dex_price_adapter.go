// Package cexdex 提供 CEX-DEX 套利功能
package cexdex

import (
	"strings"
	"sync"

	"github.com/defi-bot/backend/pkg/cache"
	"github.com/defi-bot/backend/pkg/log"
	"github.com/ethereum/go-ethereum/common"
)

// TokenMapping CEX 符号到 DEX token 地址的映射
type TokenMapping struct {
	Symbol       string         // CEX 符号 (如 "ETHUSDT")
	BaseToken    common.Address // 基础 token (如 ETH)
	QuoteToken   common.Address // 计价 token (如 USDT)
	RouterAddr   common.Address // DEX 路由地址
	PreferredDEX string         // 优先 DEX
}

// DEXPriceAdapter 将 PriceCache 适配到 DEXPriceProvider 接口
type DEXPriceAdapter struct {
	priceCache *cache.PriceCache
	mappings   map[string]*TokenMapping // CEX symbol -> TokenMapping
	mappingsMu sync.RWMutex

	// Arbitrum 主网 token 地址
	tokenAddrs map[string]common.Address
}

// TokenConfig 链特定的代币配置
type TokenConfig struct {
	Symbol    string
	Address   string
	CEXSymbol string // 对应的 CEX 交易对（如 "ETHUSDT"）
}

// NewDEXPriceAdapter 创建 DEX 价格适配器
// tokenConfigs 可选：如果提供则使用配置中的代币地址（多链支持），否则使用 Arbitrum 默认值
func NewDEXPriceAdapter(priceCache *cache.PriceCache, tokenConfigs ...[]TokenConfig) *DEXPriceAdapter {
	adapter := &DEXPriceAdapter{
		priceCache: priceCache,
		mappings:   make(map[string]*TokenMapping),
		tokenAddrs: make(map[string]common.Address),
	}

	if len(tokenConfigs) > 0 && len(tokenConfigs[0]) > 0 {
		// 使用配置传入的代币地址（支持任意链）
		adapter.initTokensFromConfig(tokenConfigs[0])
	} else {
		// 默认 Arbitrum 主网
		adapter.initArbitrumTokens()
	}
	adapter.initDefaultMappings()

	// 诊断：打印 PriceCache 中的池子信息
	adapter.diagnosePriceCache()

	return adapter
}

// initTokensFromConfig 从配置初始化代币地址（多链支持）
func (a *DEXPriceAdapter) initTokensFromConfig(configs []TokenConfig) {
	for _, tc := range configs {
		if tc.Address != "" && tc.Symbol != "" {
			a.tokenAddrs[tc.Symbol] = common.HexToAddress(tc.Address)
			// WETH/ETH 互为别名
			if tc.Symbol == "WETH" {
				a.tokenAddrs["ETH"] = common.HexToAddress(tc.Address)
			}
		}
	}
}

// diagnosePriceCache 诊断 PriceCache 中的数据
func (a *DEXPriceAdapter) diagnosePriceCache() {
	allPools := a.priceCache.GetAll()
	log.Debug("DEX 适配器诊断: PriceCache 包含 %d 个池子", len(allPools))

	if len(allPools) == 0 {
		log.Warn("DEX 适配器诊断: PriceCache 为空！")
		return
	}

	// 打印前 3 个池子的详细信息
	count := 0
	wethAddr := strings.ToLower(a.tokenAddrs["WETH"].Hex())
	usdtAddr := strings.ToLower(a.tokenAddrs["USDT"].Hex())

	for _, pool := range allPools {
		t0 := strings.ToLower(pool.Token0.Hex())
		t1 := strings.ToLower(pool.Token1.Hex())

		// 检查是否包含 WETH 或 USDT
		if strings.Contains(t0, "82af") || strings.Contains(t1, "82af") ||
			strings.Contains(t0, "fd08") || strings.Contains(t1, "fd08") {
			log.Debug("DEX 适配器诊断: 找到相关池子 pool=%s token0=%s token1=%s price=%.6f",
				pool.PoolAddress, pool.Token0.Hex(), pool.Token1.Hex(), pool.Price)
			count++
			if count >= 5 {
				break
			}
		}
	}

	log.Debug("DEX 适配器诊断: 期望的 WETH=%s USDT=%s", wethAddr, usdtAddr)
}

// initArbitrumTokens 初始化 Arbitrum 主网 token 地址
func (a *DEXPriceAdapter) initArbitrumTokens() {
	// Arbitrum 主网主要 token
	a.tokenAddrs = map[string]common.Address{
		"WETH":   common.HexToAddress("0x82aF49447D8a07e3bd95BD0d56f35241523fBab1"),
		"ETH":    common.HexToAddress("0x82aF49447D8a07e3bd95BD0d56f35241523fBab1"), // WETH
		"USDT":   common.HexToAddress("0xFd086bC7CD5C481DCC9C85ebE478A1C0b69FCbb9"),
		"USDC":   common.HexToAddress("0xaf88d065e77c8cC2239327C5EDb3A432268e5831"), // Native USDC
		"USDC.e": common.HexToAddress("0xFF970A61A04b1cA14834A43f5dE4533eBDDB5CC8"), // Bridged USDC
		"WBTC":   common.HexToAddress("0x2f2a2543B76A4166549F7aaB2e75Bef0aefC5B0f"),
		"BTC":    common.HexToAddress("0x2f2a2543B76A4166549F7aaB2e75Bef0aefC5B0f"), // WBTC
		"ARB":    common.HexToAddress("0x912CE59144191C1204E64559FE8253a0e49E6548"),
		"DAI":    common.HexToAddress("0xDA10009cBd5D07dd0CeCc66161FC93D7c9000da1"),
		"GMX":    common.HexToAddress("0xfc5A1A6EB076a2C7aD06eD22C90d7E710E35ad0a"),
		"LINK":   common.HexToAddress("0xf97f4df75117a78c1A5a0DBb814Af92458539FB4"),
		"UNI":    common.HexToAddress("0xFa7F8980b0f1E64A2062791cc3b0871572f1F7f0"),
		"AAVE":   common.HexToAddress("0xba5DdD1f9d7F570dc94a51479a000E3BCE967196"),
		"CRV":    common.HexToAddress("0x11cDb42B0EB46D95f990BeDD4695A6e3fA034978"),
		"PENDLE": common.HexToAddress("0x0c880f6761F1af8d9Aa9C466984b80DAb9a8c9e8"),
	}

	// Uniswap V3 Router (Arbitrum)
	a.tokenAddrs["UNISWAP_V3_ROUTER"] = common.HexToAddress("0xE592427A0AEce92De3Edee1F18E0157C05861564")
	// SushiSwap Router (Arbitrum)
	a.tokenAddrs["SUSHISWAP_ROUTER"] = common.HexToAddress("0x1b02dA8Cb0d097eB8D57A175b88c7D8b47997506")
}

// initDefaultMappings 初始化默认的 CEX 符号映射
func (a *DEXPriceAdapter) initDefaultMappings() {
	defaultRouter := a.tokenAddrs["UNISWAP_V3_ROUTER"]

	// Binance 交易对映射
	// 注意：Arbitrum 上大多数流动性池使用 USDC（非 USDT）作为计价代币
	// 因此同时注册 USDT 和 USDC 作为备选 quote token
	mappings := []struct {
		symbol     string
		baseToken  string
		quoteToken string
	}{
		{"ETHUSDT", "WETH", "USDC"},   // Arbitrum: WETH/USDC 流动性最高
		{"BTCUSDT", "WBTC", "WETH"},    // Arbitrum: WBTC/WETH 流动性最高
		{"ARBUSDT", "ARB", "USDC"},
		{"USDCUSDT", "USDC", "USDT"},  // 稳定币对仍用 USDT
		{"DAIUSDT", "DAI", "USDC"},
		{"GMXUSDT", "GMX", "WETH"},    // GMX 主要与 WETH 配对
		{"LINKUSDT", "LINK", "WETH"},
		{"UNIUSDT", "UNI", "WETH"},
		{"AAVEUSDT", "AAVE", "WETH"},
		{"CRVUSDT", "CRV", "WETH"},
		{"PENDLEUSDT", "PENDLE", "WETH"},
	}

	for _, m := range mappings {
		baseAddr, ok1 := a.tokenAddrs[m.baseToken]
		quoteAddr, ok2 := a.tokenAddrs[m.quoteToken]
		if ok1 && ok2 {
			a.mappings[m.symbol] = &TokenMapping{
				Symbol:       m.symbol,
				BaseToken:    baseAddr,
				QuoteToken:   quoteAddr,
				RouterAddr:   defaultRouter,
				PreferredDEX: "Uniswap V3",
			}
		}
	}
}

// GetPrice 获取 DEX 价格（实现 DEXPriceProvider 接口）
// 返回的价格是 1 baseToken = ? quoteToken（如 BTCUSDT 返回 68000，表示 1 BTC = 68000 USDT）
func (a *DEXPriceAdapter) GetPrice(symbol string) (float64, error) {
	// 标准化符号
	symbol = strings.ToUpper(symbol)

	a.mappingsMu.RLock()
	mapping, ok := a.mappings[symbol]
	a.mappingsMu.RUnlock()

	if !ok {
		return 0, nil // 没有映射，返回 0
	}

	// 从 PriceCache 获取池子价格
	// 统一使用小写地址，与 makeTokenPairKey 保持一致
	baseHex := strings.ToLower(mapping.BaseToken.Hex())
	quoteHex := strings.ToLower(mapping.QuoteToken.Hex())

	pools := a.priceCache.GetByTokenPair(baseHex, quoteHex)
	if len(pools) == 0 {
		pools = a.priceCache.GetByTokenPair(quoteHex, baseHex)
	}

	// 备用：直接遍历所有池子（应对索引未建立的时序问题）
	if len(pools) == 0 {
		all := a.priceCache.GetAll()
		for _, p := range all {
			t0 := strings.ToLower(p.Token0.Hex())
			t1 := strings.ToLower(p.Token1.Hex())
			if (t0 == baseHex && t1 == quoteHex) || (t0 == quoteHex && t1 == baseHex) {
				pools = append(pools, p)
			}
		}
		if len(pools) > 0 {
			log.Debug("CEX-DEX: 通过全量扫描找到 %s 的 %d 个池子（索引可能未建立）", symbol, len(pools))
		} else {
			log.Debug("CEX-DEX: DEX 价格为 0 (未找到池子) symbol=%s base=%s quote=%s allPools=%d",
				symbol, baseHex[:10], quoteHex[:10], len(all))
			return 0, nil
		}
	}

	// 选择最佳池子策略：
	// 1. 优先选择 V3 池子（通常流动性更好）
	// 2. 在同类型池子中选择流动性最高的
	var bestPool *cache.PoolPrice
	var bestScore float64 = 0


	for _, pool := range pools {
		// 过滤掉价格为 0 的池子
		if pool.Price <= 0 {
			continue
		}

		// 计算池子的评分
		var score float64
		
		// V3 池子基础分 = 1000，V2 池子基础分 = 1
		if pool.IsV3 {
			score = 1000
			if pool.Liquidity != nil {
				liq, _ := pool.Liquidity.Float64()
				// 对流动性取对数，避免数值过大
				if liq > 0 {
					score += liq / 1e15 // 归一化
				}
			}
		} else if pool.Reserve0 != nil && pool.Reserve1 != nil {
			score = 1
			r0, _ := pool.Reserve0.Float64()
			r1, _ := pool.Reserve1.Float64()
			if r0 > 0 && r1 > 0 {
				// 使用储备量乘积归一化
				reserveProduct := r0 * r1
				if reserveProduct > 0 {
					score += reserveProduct / 1e30 // 归一化
				}
			}
		} else {
			score = 0.1 // 无流动性数据的池子给最低分
		}

		if score > bestScore {
			bestPool = pool
			bestScore = score
		}
	}

	if bestPool == nil || bestPool.Price <= 0 {
		return 0, nil
	}

	// 诊断：打印最佳池子信息
	if symbol == "BTCUSDT" || symbol == "ETHUSDT" {
		log.Debug("DEX GetPrice: symbol=%s pool=%s t0=%s t1=%s price=%.10g isV3=%v score=%.4f dec0=%d dec1=%d",
			symbol, bestPool.PoolAddress[:10], bestPool.Token0.Hex()[:10], bestPool.Token1.Hex()[:10],
			bestPool.Price, bestPool.IsV3, bestScore, bestPool.Decimals0, bestPool.Decimals1)
	}

	// PriceCache 存储的 Price 是 token1/token0（已调整 decimals）
	// CEX 价格 "ETHUSDT = 1978" 表示 1 ETH = 1978 USDT，即 quoteToken/baseToken
	// 我们需要返回 quoteToken/baseToken
	poolPrice := bestPool.Price
	baseIsToken0 := strings.EqualFold(bestPool.Token0.Hex(), baseHex)

	var finalPrice float64
	if baseIsToken0 {
		// base 是 token0, quote 是 token1
		// poolPrice = token1/token0 = quote/base -> 正是我们需要的
		finalPrice = poolPrice
	} else {
		// base 是 token1, quote 是 token0
		// poolPrice = token1/token0 = base/quote -> 需要取倒数
		if poolPrice > 0 {
			finalPrice = 1.0 / poolPrice
		}
	}

	// 如果 quote token 不是 USDT/USDC，需要二次转换到 USD 价格
	// 例如 LINK/WETH → LINK 的 WETH 价格 × WETH 的 USD 价格 = LINK 的 USD 价格
	quoteIsStable := strings.EqualFold(mapping.QuoteToken.Hex(), a.tokenAddrs["USDT"].Hex()) ||
		strings.EqualFold(mapping.QuoteToken.Hex(), a.tokenAddrs["USDC"].Hex())

	if !quoteIsStable && finalPrice > 0 {
		// quote token 是 WETH 等非稳定币，需要获取其 USD 价格
		wethUSDPrice := a.getWETHUSDPrice()
		log.Debug("DEX Price debug: symbol=%s poolPrice=%.10g baseIsToken0=%v finalPriceBeforeConvert=%.10g wethUSD=%.4f result=%.10g",
			symbol, poolPrice, baseIsToken0, finalPrice, wethUSDPrice, finalPrice*wethUSDPrice)
		if wethUSDPrice > 0 {
			finalPrice = finalPrice * wethUSDPrice
		}
	}

	return finalPrice, nil
}

// getWETHUSDPrice 获取 WETH 的 USD 价格（从 PriceCache 中的 WETH/USDC 池获取）
func (a *DEXPriceAdapter) getWETHUSDPrice() float64 {
	wethHex := strings.ToLower(a.tokenAddrs["WETH"].Hex())
	usdcHex := strings.ToLower(a.tokenAddrs["USDC"].Hex())

	pools := a.priceCache.GetByTokenPair(wethHex, usdcHex)
	if len(pools) == 0 {
		pools = a.priceCache.GetByTokenPair(usdcHex, wethHex)
	}

	if len(pools) == 0 {
		return 0
	}

	// 选择流动性最高的池子
	var bestPool *cache.PoolPrice
	var bestScore float64
	for _, p := range pools {
		if p.Price <= 0 {
			continue
		}
		score := float64(1)
		if p.IsV3 && p.Liquidity != nil {
			liq, _ := p.Liquidity.Float64()
			score = liq
		}
		if score > bestScore {
			bestPool = p
			bestScore = score
		}
	}
	if bestPool == nil {
		return 0
	}

	// 确定 WETH/USDC 方向
	if strings.EqualFold(bestPool.Token0.Hex(), wethHex) {
		// token0=WETH, price = USDC/WETH → 正是我们要的
		return bestPool.Price
	}
	// token0=USDC, price = WETH/USDC → 取倒数
	if bestPool.Price > 0 {
		return 1.0 / bestPool.Price
	}
	return 0
}

// GetTokenPairForSymbol 根据 CEX 符号和方向返回 (tokenIn, tokenOut) 地址
// direction="dex_to_cex": 在 DEX 低价买入 baseToken，卖出 quoteToken
// direction="cex_to_dex": 在 DEX 高价卖出 baseToken，买入 quoteToken
func (a *DEXPriceAdapter) GetTokenPairForSymbol(symbol, direction string) (tokenIn, tokenOut common.Address) {
	symbol = strings.ToUpper(symbol)
	a.mappingsMu.RLock()
	mapping, ok := a.mappings[symbol]
	a.mappingsMu.RUnlock()

	if !ok {
		return
	}

	if direction == "dex_to_cex" {
		// DEX 低价：用 quoteToken（如 USDT）买入 baseToken（如 WETH）
		tokenIn = mapping.QuoteToken
		tokenOut = mapping.BaseToken
	} else {
		// CEX 低价：卖出 baseToken（如 WETH），换回 quoteToken（如 USDT）
		tokenIn = mapping.BaseToken
		tokenOut = mapping.QuoteToken
	}
	return
}

// GetPoolAddress 获取 DEX 池子地址（实现 DEXPriceProvider 接口）
func (a *DEXPriceAdapter) GetPoolAddress(symbol string) common.Address {
	symbol = strings.ToUpper(symbol)

	a.mappingsMu.RLock()
	mapping, ok := a.mappings[symbol]
	a.mappingsMu.RUnlock()

	if !ok {
		return common.Address{}
	}

	// 从 PriceCache 获取池子（使用带 checksum 的地址）
	baseHex := mapping.BaseToken.Hex()
	quoteHex := mapping.QuoteToken.Hex()

	pools := a.priceCache.GetByTokenPair(baseHex, quoteHex)

	if len(pools) == 0 {
		pools = a.priceCache.GetByTokenPair(quoteHex, baseHex)
	}

	if len(pools) > 0 {
		return common.HexToAddress(pools[0].PoolAddress)
	}

	return common.Address{}
}

// GetRouterAddress 获取 DEX 路由地址（实现 DEXPriceProvider 接口）
func (a *DEXPriceAdapter) GetRouterAddress(symbol string) common.Address {
	symbol = strings.ToUpper(symbol)

	a.mappingsMu.RLock()
	mapping, ok := a.mappings[symbol]
	a.mappingsMu.RUnlock()

	if ok {
		return mapping.RouterAddr
	}

	// 默认返回 Uniswap V3 Router
	return a.tokenAddrs["UNISWAP_V3_ROUTER"]
}

// GetPoolFeeTier 获取 DEX 池子的 fee tier（V3: 500/3000/10000, V2: 0）
func (a *DEXPriceAdapter) GetPoolFeeTier(symbol string) uint32 {
	symbol = strings.ToUpper(symbol)

	a.mappingsMu.RLock()
	mapping, ok := a.mappings[symbol]
	a.mappingsMu.RUnlock()

	if !ok {
		return 0
	}

	baseHex := strings.ToLower(mapping.BaseToken.Hex())
	quoteHex := strings.ToLower(mapping.QuoteToken.Hex())

	pools := a.priceCache.GetByTokenPair(baseHex, quoteHex)
	if len(pools) == 0 {
		pools = a.priceCache.GetByTokenPair(quoteHex, baseHex)
	}
	if len(pools) == 0 {
		return 0
	}

	// 选最佳池子的 fee
	var bestPool *cache.PoolPrice
	var bestScore float64
	for _, p := range pools {
		if p.Price <= 0 {
			continue
		}
		score := float64(0.1)
		if p.IsV3 && p.Liquidity != nil {
			liq, _ := p.Liquidity.Float64()
			score = 1000 + liq/1e15
		}
		if score > bestScore {
			bestPool = p
			bestScore = score
		}
	}
	if bestPool == nil {
		return 0
	}

	// V3: PriceCache.Fee 是 bps (5=0.05%), 合约需要 raw fee (500)
	// V2: 必须返回 0，合约才用 V2 路由
	if !bestPool.IsV3 {
		return 0
	}
	fee := uint32(bestPool.Fee) * 100
	if fee == 0 {
		fee = 3000 // V3 fallback
	}
	return fee
}

// AddMapping 添加自定义符号映射
func (a *DEXPriceAdapter) AddMapping(symbol string, baseToken, quoteToken, router common.Address) {
	a.mappingsMu.Lock()
	defer a.mappingsMu.Unlock()

	a.mappings[strings.ToUpper(symbol)] = &TokenMapping{
		Symbol:     strings.ToUpper(symbol),
		BaseToken:  baseToken,
		QuoteToken: quoteToken,
		RouterAddr: router,
	}
}
