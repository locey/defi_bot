// internal/strategy/cex_dex_strategy.go
package strategy

import (
	"context"
	"fmt"
	"log"
	"math/big"
	"strings"
	"time"

	"github.com/defi-bot/backend/internal/models"
	"github.com/ethereum/go-ethereum/common"
)

// CexDexDirection CEX-DEX 套利方向
type CexDexDirection string

const (
	// CexBuyDexSell CEX 价格低，DEX 价格高：CEX 买入 -> DEX 卖出
	CexBuyDexSell CexDexDirection = "cex_buy_dex_sell"
	// DexBuyCexSell DEX 价格低，CEX 价格高：DEX 买入 -> CEX 卖出
	DexBuyCexSell CexDexDirection = "dex_buy_cex_sell"
)

// CexDexOpportunity CEX-DEX 套利机会
type CexDexOpportunity struct {
	ID             string          `json:"id"`
	CexName        string          `json:"cex_name"`        // "binance", "okx"
	DexName        string          `json:"dex_name"`        // "uniswap_v2", "sushiswap"
	TokenPair      string          `json:"token_pair"`      // "ETH/USDT"
	BaseToken      common.Address  `json:"base_token"`      // 基础代币地址
	QuoteToken     common.Address  `json:"quote_token"`     // 报价代币地址
	CexPrice       *big.Float      `json:"cex_price"`       // CEX 价格
	DexPrice       *big.Float      `json:"dex_price"`       // DEX 价格
	PriceSpread    float64         `json:"price_spread"`    // 价差百分比
	Direction      CexDexDirection `json:"direction"`       // 交易方向
	AmountIn       *big.Int        `json:"amount_in"`       // 建议投入金额
	ExpectedProfit *big.Int        `json:"expected_profit"` // 预期利润
	MinProfit      *big.Int        `json:"min_profit"`      // 最小利润
	GasEstimate    uint64          `json:"gas_estimate"`    // Gas 估算
	Timestamp      time.Time       `json:"timestamp"`       // 发现时间
	ValidUntil     time.Time       `json:"valid_until"`     // 有效期
	Confidence     float64         `json:"confidence"`      // 置信度
}

// CexDexConfig CEX-DEX 策略配置
type CexDexConfig struct {
	MinSpread        float64       // 最小价差阈值 (如 1.5 = 1.5%)
	MaxSpread        float64       // 最大价差阈值 (如 10.0 = 10%，超过可能是异常)
	MinAmount        *big.Int      // 最小投入金额
	MaxAmount        *big.Int      // 最大投入金额
	CexFeeRate       float64       // CEX 手续费率 (如 0.001 = 0.1%)
	DexSlippage      float64       // DEX 预期滑点
	WithdrawFee      *big.Int      // 提币费用（CEX -> 链上）
	ValidityDuration time.Duration // 机会有效期
}

// DefaultCexDexConfig 默认配置
func DefaultCexDexConfig() *CexDexConfig {
	minAmount := new(big.Int)
	minAmount.SetString("100000000000000000", 10) // 0.1 ETH

	maxAmount := new(big.Int)
	maxAmount.SetString("10000000000000000000", 10) // 10 ETH

	withdrawFee := new(big.Int)
	withdrawFee.SetString("1000000000000000", 10) // 0.001 ETH

	return &CexDexConfig{
		MinSpread:        1.5,  // 1.5% 最小价差
		MaxSpread:        10.0, // 10% 最大价差（超过可能是异常数据）
		MinAmount:        minAmount,
		MaxAmount:        maxAmount,
		CexFeeRate:       0.001, // 0.1% CEX 手续费
		DexSlippage:      0.005, // 0.5% DEX 滑点
		WithdrawFee:      withdrawFee,
		ValidityDuration: 30 * time.Second,
	}
}

// FindCexDexOpportunities 查找 CEX-DEX 套利机会
func (e *StrategyEngine) FindCexDexOpportunities(ctx context.Context) ([]*CexDexOpportunity, error) {
	config := DefaultCexDexConfig()

	// 1. 获取 CEX 价格（从 Redis 缓存或数据库）
	cexPrices, err := e.getCexPrices(ctx)
	if err != nil {
		return nil, fmt.Errorf("get cex prices failed: %w", err)
	}

	if len(cexPrices) == 0 {
		log.Printf("No CEX prices available")
		return nil, nil
	}

	// 2. 获取 DEX 价格（从池子数据计算）
	dexPrices, err := e.getDexPrices(ctx)
	if err != nil {
		return nil, fmt.Errorf("get dex prices failed: %w", err)
	}

	if len(dexPrices) == 0 {
		log.Printf("No DEX prices available")
		return nil, nil
	}

	var opportunities []*CexDexOpportunity

	// 3. 比较价差
	for pair, cexPrice := range cexPrices {
		dexPrice, exists := dexPrices[pair]
		if !exists {
			continue
		}

		// 计算价差百分比
		spread := calculateSpread(cexPrice.Price, dexPrice.Price)
		absSpread := spread
		if absSpread < 0 {
			absSpread = -absSpread
		}

		// 检查价差是否在有效范围内
		if absSpread < config.MinSpread {
			continue // 价差太小，不值得套利
		}
		if absSpread > config.MaxSpread {
			log.Printf("⚠️ Abnormal spread for %s: %.2f%% (skipped)", pair, absSpread)
			continue // 价差太大，可能是异常数据
		}

		opp := &CexDexOpportunity{
			ID:          fmt.Sprintf("cexdex_%d_%s", time.Now().UnixNano(), pair),
			TokenPair:   pair,
			CexPrice:    cexPrice.Price,
			DexPrice:    dexPrice.Price,
			PriceSpread: spread,
			BaseToken:   dexPrice.BaseToken,
			QuoteToken:  dexPrice.QuoteToken,
			Timestamp:   time.Now(),
			ValidUntil:  time.Now().Add(config.ValidityDuration),
		}

		if spread > 0 {
			// CEX 价格 > DEX 价格：DEX 买入，CEX 卖出
			opp.Direction = DexBuyCexSell
			opp.CexName = cexPrice.Exchange
			opp.DexName = dexPrice.DexName
		} else {
			// DEX 价格 > CEX 价格：CEX 买入，DEX 卖出
			opp.Direction = CexBuyDexSell
			opp.CexName = cexPrice.Exchange
			opp.DexName = dexPrice.DexName
		}

		// 评估可行性和计算利润
		if e.evaluateCexDexFeasibility(opp, config) {
			opportunities = append(opportunities, opp)
		}
	}

	log.Printf("Found %d CEX-DEX opportunities", len(opportunities))

	// 保存到数据库
	if len(opportunities) > 0 {
		e.saveCexDexOpportunitiesToDB(opportunities)
	}

	return opportunities, nil
}

// CexPriceInfo CEX 价格信息
type CexPriceInfo struct {
	Exchange  string
	Symbol    string
	Price     *big.Float
	BidPrice  *big.Float // 买一价
	AskPrice  *big.Float // 卖一价
	Timestamp time.Time
}

// DexPriceInfo DEX 价格信息
type DexPriceInfo struct {
	DexName    string
	BaseToken  common.Address
	QuoteToken common.Address
	Price      *big.Float
	Liquidity  *big.Int
	Timestamp  time.Time
}

// getCexPrices 从数据库/缓存获取 CEX 价格
func (e *StrategyEngine) getCexPrices(ctx context.Context) (map[string]*CexPriceInfo, error) {
	prices := make(map[string]*CexPriceInfo)

	if e.db == nil {
		return prices, nil
	}

	// 从 cex_tickers 表获取最新价格，并关联交易对信息
	var tickers []models.CexTicker
	if err := e.db.WithContext(ctx).
		Preload("Pair").
		Where("timestamp > ?", time.Now().Add(-5*time.Minute)). // 只取5分钟内的数据
		Order("timestamp DESC").
		Find(&tickers).Error; err != nil {
		return nil, err
	}

	// 去重，保留每个交易对的最新价格
	seen := make(map[uint]bool)
	for _, ticker := range tickers {
		if seen[ticker.PairID] {
			continue
		}
		seen[ticker.PairID] = true

		// 获取交易对符号
		symbol := ticker.Pair.Symbol
		if symbol == "" {
			symbol = fmt.Sprintf("%s%s", ticker.Pair.BaseAsset, ticker.Pair.QuoteAsset)
		}

		// 从 decimal.Decimal 转换为 big.Float
		price := new(big.Float)
		price.SetString(ticker.LastPrice.String())

		bidPrice := new(big.Float)
		if ticker.BidPrice != nil {
			bidPrice.SetString(ticker.BidPrice.String())
		}

		askPrice := new(big.Float)
		if ticker.AskPrice != nil {
			askPrice.SetString(ticker.AskPrice.String())
		}

		// 标准化交易对名称
		normalizedPair := normalizeSymbol(symbol)

		prices[normalizedPair] = &CexPriceInfo{
			Exchange:  ticker.ExchangeName,
			Symbol:    symbol,
			Price:     price,
			BidPrice:  bidPrice,
			AskPrice:  askPrice,
			Timestamp: ticker.Timestamp,
		}
	}

	return prices, nil
}

// getDexPrices 从池子数据获取 DEX 价格
func (e *StrategyEngine) getDexPrices(ctx context.Context) (map[string]*DexPriceInfo, error) {
	prices := make(map[string]*DexPriceInfo)

	if e.db == nil {
		return prices, nil
	}

	// 从数据库加载池子数据
	pools, err := e.loadPoolsFromDB(ctx)
	if err != nil {
		return nil, err
	}

	// 计算每个池子的价格
	for _, pool := range pools {
		if pool.Reserve0 == nil || pool.Reserve1 == nil {
			continue
		}
		if pool.Reserve0.Sign() <= 0 || pool.Reserve1.Sign() <= 0 {
			continue
		}

		// 查找代币符号
		var token0, token1 models.Token
		e.db.Where("LOWER(address) = LOWER(?)", pool.Token0.Hex()).First(&token0)
		e.db.Where("LOWER(address) = LOWER(?)", pool.Token1.Hex()).First(&token1)

		if token0.Symbol == "" || token1.Symbol == "" {
			continue
		}

		// 计算价格 (Token0/Token1)
		price := new(big.Float).Quo(
			new(big.Float).SetInt(pool.Reserve1),
			new(big.Float).SetInt(pool.Reserve0),
		)

		// 考虑精度差异
		decimalDiff := token1.Decimals - token0.Decimals
		if decimalDiff != 0 {
			multiplier := new(big.Float).SetFloat64(1.0)
			if decimalDiff > 0 {
				for i := 0; i < decimalDiff; i++ {
					multiplier.Mul(multiplier, big.NewFloat(10))
				}
				price.Quo(price, multiplier)
			} else {
				for i := 0; i < -decimalDiff; i++ {
					multiplier.Mul(multiplier, big.NewFloat(10))
				}
				price.Mul(price, multiplier)
			}
		}

		// 标准化交易对名称
		pair := normalizeSymbol(token0.Symbol + token1.Symbol)

		prices[pair] = &DexPriceInfo{
			DexName:    pool.DexName,
			BaseToken:  pool.Token0,
			QuoteToken: pool.Token1,
			Price:      price,
			Liquidity:  pool.Reserve0,
			Timestamp:  pool.LastUpdate,
		}
	}

	return prices, nil
}

// calculateSpread 计算价差百分比
func calculateSpread(cexPrice, dexPrice *big.Float) float64 {
	if dexPrice.Sign() == 0 {
		return 0
	}

	diff := new(big.Float).Sub(cexPrice, dexPrice)
	ratio := new(big.Float).Quo(diff, dexPrice)
	spread, _ := ratio.Float64()
	return spread * 100 // 转换为百分比
}

// evaluateCexDexFeasibility 评估 CEX-DEX 套利可行性
func (e *StrategyEngine) evaluateCexDexFeasibility(opp *CexDexOpportunity, config *CexDexConfig) bool {
	// 1. 计算总成本
	// - CEX 手续费
	// - DEX Gas 费用
	// - DEX 滑点
	// - 提币费用（如果需要）

	// 简化计算：使用默认投入金额
	opp.AmountIn = config.MinAmount

	// 估算 Gas
	opp.GasEstimate = 150000 // 假设 150k gas

	// 估算利润
	// 简化：价差 * 投入金额 - 手续费 - Gas
	spreadFloat := opp.PriceSpread
	if spreadFloat < 0 {
		spreadFloat = -spreadFloat
	}

	// 毛利润 = 投入金额 * 价差%
	grossProfitFloat := new(big.Float).SetInt(opp.AmountIn)
	grossProfitFloat.Mul(grossProfitFloat, big.NewFloat(spreadFloat/100))
	grossProfit, _ := grossProfitFloat.Int(nil)

	// 扣除 CEX 手续费 (0.1%)
	cexFee := new(big.Int).Div(opp.AmountIn, big.NewInt(1000))

	// 扣除 DEX 滑点预估 (0.5%)
	slippageCost := new(big.Int).Div(opp.AmountIn, big.NewInt(200))

	// 估算 Gas 成本（假设 50 gwei）
	gasPrice := big.NewInt(50e9) // 50 gwei
	gasCost := new(big.Int).Mul(big.NewInt(int64(opp.GasEstimate)), gasPrice)

	// 净利润
	netProfit := new(big.Int).Sub(grossProfit, cexFee)
	netProfit.Sub(netProfit, slippageCost)
	netProfit.Sub(netProfit, gasCost)

	// 最小利润 = 2 * Gas 成本
	opp.MinProfit = new(big.Int).Mul(gasCost, big.NewInt(2))
	opp.ExpectedProfit = netProfit

	// 检查是否有利可图
	if netProfit.Cmp(opp.MinProfit) <= 0 {
		return false
	}

	// 计算置信度
	opp.Confidence = 0.7 // 基础置信度
	if spreadFloat > 3.0 {
		opp.Confidence = 0.5 // 价差较大时降低置信度
	}

	return true
}

// saveCexDexOpportunitiesToDB 保存 CEX-DEX 套利机会到数据库
func (e *StrategyEngine) saveCexDexOpportunitiesToDB(opps []*CexDexOpportunity) {
	if e.db == nil {
		return
	}

	for _, opp := range opps {
		// 查找代币 ID
		var baseToken, quoteToken models.Token
		e.db.Where("LOWER(address) = LOWER(?)", opp.BaseToken.Hex()).First(&baseToken)
		e.db.Where("LOWER(address) = LOWER(?)", opp.QuoteToken.Hex()).First(&quoteToken)

		// 准备可空的 Token ID 指针
		var tokenInID, tokenOutID *uint
		if baseToken.ID > 0 {
			tokenInID = &baseToken.ID
		}
		if quoteToken.ID > 0 {
			tokenOutID = &quoteToken.ID
		}

		dbOpp := &models.ArbitrageOpportunity{
			TokenInID:      tokenInID,
			TokenOutID:     tokenOutID,
			ArbitrageType:  "cex_dex",
			AmountIn:       opp.AmountIn.String(),
			ExpectedProfit: opp.ExpectedProfit.String(),
			MinProfit:      opp.MinProfit.String(),
			ProfitRate:     opp.PriceSpread,
			SwapPath:       fmt.Sprintf(`["%s", "%s"]`, opp.BaseToken.Hex(), opp.QuoteToken.Hex()),
			DexPath:        fmt.Sprintf(`["%s", "%s"]`, opp.CexName, opp.DexName),
			DexRouters:     "[]",
			PoolAddresses:  "[]",
			FeeTiers:       "[]",
			GasEstimate:    opp.GasEstimate,
			Status:         "pending",
			Priority:       calculatePriority(opp.PriceSpread / 100),
			ExpiresAt:      opp.ValidUntil,
		}

		if err := e.db.Create(dbOpp).Error; err != nil {
			log.Printf("Save CEX-DEX opportunity failed: %v", err)
			continue
		}
		log.Printf("✅ Saved CEX-DEX opportunity: %s (spread=%.2f%%)", opp.TokenPair, opp.PriceSpread)
	}
}

// normalizeSymbol 标准化交易对符号
func normalizeSymbol(symbol string) string {
	// 移除分隔符，统一为大写
	symbol = strings.ToUpper(symbol)
	symbol = strings.ReplaceAll(symbol, "/", "")
	symbol = strings.ReplaceAll(symbol, "-", "")
	symbol = strings.ReplaceAll(symbol, "_", "")

	// WETH -> ETH 映射
	symbol = strings.ReplaceAll(symbol, "WETH", "ETH")
	symbol = strings.ReplaceAll(symbol, "WBTC", "BTC")

	return symbol
}
