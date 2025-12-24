package collector

import (
	"fmt"
	"log"
	"math/big"
	"time"

	"github.com/defi-bot/backend/internal/database"
	"github.com/defi-bot/backend/internal/models"
)

// CollectV3Depths 采集 V3 流动性深度数据
// 这是业界标准的深度采集方法：使用 QuoterV2 模拟不同金额的交换
func (c *Collector) CollectV3Depths() error {
	db := database.GetDB()

	// 获取所有 V3 交易对
	var pairs []models.TradingPair
	err := db.Preload("Token0").
		Preload("Token1").
		Preload("Dex").
		Joins("JOIN dexes ON dexes.id = trading_pairs.dex_id").
		Where("dexes.support_v3_ticks = ? AND dexes.quoter_address != ? AND trading_pairs.is_active = ?",
			true, "", true).
		Find(&pairs).Error

	if err != nil {
		return fmt.Errorf("查询V3交易对失败: %w", err)
	}

	if len(pairs) == 0 {
		log.Println("没有V3交易对需要采集深度")
		return nil
	}

	log.Printf("开始采集 %d 个 V3 池的流动性深度...", len(pairs))

	// 定义测试金额（业界标准）
	testAmounts := []*big.Int{
		parseEther("0.1"), // 0.1 ETH - 小额交易
		parseEther("1"),   // 1 ETH - 中等交易
		parseEther("10"),  // 10 ETH - 大额交易
		parseEther("100"), // 100 ETH - 巨额交易
	}

	blockNumber, _ := c.web3Client.GetBlockNumber()
	timestamp := time.Now()

	totalDepths := 0

	// 逐个采集
	for _, pair := range pairs {
		if pair.Dex.QuoterAddress == "" {
			continue
		}

		depths, err := c.collectPairDepth(pair, testAmounts, blockNumber, timestamp)
		if err != nil {
			log.Printf("⚠️  采集深度失败 %s/%s @ %s: %v",
				pair.Token0.Symbol, pair.Token1.Symbol, pair.Dex.Name, err)
			continue
		}

		// 批量插入
		if len(depths) > 0 {
			if err := db.CreateInBatches(depths, 100).Error; err != nil {
				log.Printf("⚠️  写入深度数据失败: %v", err)
			} else {
				totalDepths += len(depths)
				log.Printf("✅ 采集深度: %s/%s @ %s - %d 个测试点",
					pair.Token0.Symbol, pair.Token1.Symbol, pair.Dex.Name, len(depths))
			}
		}
	}

	log.Printf("✅ 深度采集完成: 共 %d 条记录", totalDepths)
	return nil
}

// collectPairDepth 采集单个交易对的深度数据
func (c *Collector) collectPairDepth(
	pair models.TradingPair,
	testAmounts []*big.Int,
	blockNumber uint64,
	timestamp time.Time,
) ([]models.LiquidityDepth, error) {
	depths := make([]models.LiquidityDepth, 0, len(testAmounts)*2)

	// 获取当前价格（用于计算价格影响）
	priceInfo, err := c.protocolFactory.CreateProtocol(pair.Dex.Protocol)
	if err != nil {
		return nil, err
	}

	currentPriceInfo, err := priceInfo.GetPrice(pair.PairAddress)
	if err != nil {
		return nil, err
	}

	// 对每个测试金额，查询两个方向的深度
	for _, amount := range testAmounts {
		// ===  方向1: token0 → token1 ===
		result0to1, err := c.web3Client.QuoteExactInputSingle(
			pair.Dex.QuoterAddress,
			pair.Token0.Address,
			pair.Token1.Address,
			amount,
			pair.Dex.FeeTier,
		)

		if err == nil && result0to1.AmountOut.Sign() > 0 {
			priceImpact := c.web3Client.CalculatePriceImpact(
				currentPriceInfo.SqrtPriceX96,
				result0to1.SqrtPriceX96After,
			)

			slippageBps := uint32(priceImpact * 100) // 转换为基点

			executionPrice := calculateExecutionPrice(amount, result0to1.AmountOut, pair.Token0.Decimals, pair.Token1.Decimals)

			depths = append(depths, models.LiquidityDepth{
				PairID:         pair.ID,
				AmountIn:       amount.String(),
				AmountOut:      result0to1.AmountOut.String(),
				PriceImpact:    priceImpact,
				SlippageBps:    slippageBps,
				Direction:      "token0_to_token1",
				ExecutionPrice: executionPrice,
				BlockNumber:    blockNumber,
				Timestamp:      timestamp,
			})
		}

		// === 方向2: token1 → token0 ===
		result1to0, err := c.web3Client.QuoteExactInputSingle(
			pair.Dex.QuoterAddress,
			pair.Token1.Address,
			pair.Token0.Address,
			amount,
			pair.Dex.FeeTier,
		)

		if err == nil && result1to0.AmountOut.Sign() > 0 {
			priceImpact := c.web3Client.CalculatePriceImpact(
				currentPriceInfo.SqrtPriceX96,
				result1to0.SqrtPriceX96After,
			)

			slippageBps := uint32(priceImpact * 100)

			executionPrice := calculateExecutionPrice(amount, result1to0.AmountOut, pair.Token1.Decimals, pair.Token0.Decimals)

			depths = append(depths, models.LiquidityDepth{
				PairID:         pair.ID,
				AmountIn:       amount.String(),
				AmountOut:      result1to0.AmountOut.String(),
				PriceImpact:    priceImpact,
				SlippageBps:    slippageBps,
				Direction:      "token1_to_token0",
				ExecutionPrice: executionPrice,
				BlockNumber:    blockNumber,
				Timestamp:      timestamp,
			})
		}
	}

	return depths, nil
}

// parseEther 将 ETH 数量转换为 wei
func parseEther(eth string) *big.Int {
	// 1 ETH = 1e18 wei
	ethFloat, _ := new(big.Float).SetString(eth)
	weiFloat := new(big.Float).Mul(ethFloat, big.NewFloat(1e18))
	wei, _ := weiFloat.Int(nil)
	return wei
}

// calculateExecutionPrice 计算实际成交价格
func calculateExecutionPrice(amountIn, amountOut *big.Int, decimalsIn, decimalsOut int) string {
	if amountIn.Sign() == 0 {
		return "0"
	}

	// 调整精度
	adjustedIn := new(big.Float).SetInt(amountIn)
	adjustedOut := new(big.Float).SetInt(amountOut)

	// 除以 10^decimals
	adjustedIn.Quo(adjustedIn, big.NewFloat(pow10(decimalsIn)))
	adjustedOut.Quo(adjustedOut, big.NewFloat(pow10(decimalsOut)))

	// price = amountOut / amountIn
	price := new(big.Float).Quo(adjustedOut, adjustedIn)

	return price.String()
}

// pow10 计算 10^n
func pow10(n int) float64 {
	result := 1.0
	for i := 0; i < n; i++ {
		result *= 10
	}
	return result
}
