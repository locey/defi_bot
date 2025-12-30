package collector

import (
	"context"
	"fmt"
	"log"
	"math/big"
	"time"

	"github.com/defi-bot/backend/internal/database"
	"github.com/defi-bot/backend/internal/models"
	"github.com/defi-bot/backend/pkg/web3"
)

// GasCollector Gas 价格采集器
// 使用业界标准的 EIP-1559 方法采集 Gas 价格
type GasCollector struct {
	web3Client *web3.Client
}

// NewGasCollector 创建 Gas 采集器
func NewGasCollector(web3Client *web3.Client) *GasCollector {
	return &GasCollector{
		web3Client: web3Client,
	}
}

// CollectGasPrice 采集 Gas 价格
// 业界最佳实践：同时获取 Legacy 和 EIP-1559 Gas 价格
func (g *GasCollector) CollectGasPrice() error {
	db := database.GetDB()

	// 获取当前区块号
	blockNumber, err := g.web3Client.GetBlockNumber()
	if err != nil {
		return fmt.Errorf("获取区块号失败: %w", err)
	}

	// 方法1：获取基础 Gas 价格（Legacy）
	gasPrice, err := g.web3Client.GetClient().SuggestGasPrice(context.Background())
	if err != nil {
		return fmt.Errorf("获取 Gas 价格失败: %w", err)
	}

	// 方法2：获取 EIP-1559 数据（如果支持）
	baseFee, priorityFee, maxFee := g.getEIP1559GasPrice()

	// 方法3：计算不同速度的 Gas 价格
	fastPrice := new(big.Int).Add(gasPrice, percentOf(gasPrice, 20)) // +20%
	standardPrice := gasPrice
	slowPrice := percentOf(gasPrice, 80) // -20%

	// 获取待处理交易数量（用于判断网络拥堵）
	pendingCount, err := g.getPendingTransactionCount()
	if err != nil {
		pendingCount = 0
	}

	// 判断网络负载
	networkLoad := g.determineNetworkLoad(gasPrice, pendingCount)

	// 保存到数据库
	gasPriceRecord := models.GasPriceHistory{
		GasPrice:       gasPrice.String(),
		Priority:       priorityFee.String(),
		MaxFee:         maxFee.String(),
		BaseFee:        baseFee.String(),
		FastPrice:      fastPrice.String(),
		StandardPrice:  standardPrice.String(),
		SlowPrice:      slowPrice.String(),
		PendingTxCount: pendingCount,
		NetworkLoad:    networkLoad,
		BlockNumber:    blockNumber,
		Timestamp:      time.Now(),
	}

	if err := db.Create(&gasPriceRecord).Error; err != nil {
		return fmt.Errorf("保存 Gas 价格失败: %w", err)
	}

	log.Printf("✅ Gas 价格采集成功: %s Gwei (负载: %s)",
		weiToGwei(gasPrice), networkLoad)

	return nil
}

// getEIP1559GasPrice 获取 EIP-1559 Gas 价格
// 业界标准：使用 eth_feeHistory 获取
func (g *GasCollector) getEIP1559GasPrice() (baseFee, priorityFee, maxFee *big.Int) {
	client := g.web3Client.GetClient()
	ctx := context.Background()

	// 尝试获取 EIP-1559 数据
	header, err := client.HeaderByNumber(ctx, nil)
	if err != nil {
		// 如果失败，返回默认值
		return big.NewInt(0), big.NewInt(0), big.NewInt(0)
	}

	// 获取 BaseFee（EIP-1559）
	baseFee = header.BaseFee
	if baseFee == nil {
		baseFee = big.NewInt(0)
	}

	// 推荐的 Priority Fee
	priorityFee, err = client.SuggestGasTipCap(ctx)
	if err != nil {
		priorityFee = big.NewInt(0)
	}

	// MaxFee = BaseFee * 2 + PriorityFee（业界标准公式）
	maxFee = new(big.Int).Mul(baseFee, big.NewInt(2))
	maxFee.Add(maxFee, priorityFee)

	return baseFee, priorityFee, maxFee
}

// getPendingTransactionCount 获取待处理交易数量
func (g *GasCollector) getPendingTransactionCount() (int, error) {
	// 注意：此方法需要 RPC 节点支持 txpool API
	// 大多数公共节点不支持，返回 0

	// TODO: 如果有自己的节点，可以调用 txpool_status
	// 或使用 Etherscan API 获取

	return 0, nil
}

// determineNetworkLoad 判断网络负载
// 业界标准：根据 Gas 价格判断
func (g *GasCollector) determineNetworkLoad(gasPrice *big.Int, pendingCount int) string {
	// 转换为 Gwei
	gasPriceGwei := new(big.Float).Quo(
		new(big.Float).SetInt(gasPrice),
		big.NewFloat(1e9),
	)

	gwei, _ := gasPriceGwei.Float64()

	// 根据 Gas 价格判断（针对以太坊主网）
	switch {
	case gwei < 20:
		return "low" // 低负载
	case gwei < 50:
		return "normal" // 正常
	case gwei < 100:
		return "high" // 高负载
	default:
		return "congested" // 拥堵
	}
}

// percentOf 计算百分比
func percentOf(value *big.Int, percent int) *big.Int {
	result := new(big.Int).Mul(value, big.NewInt(int64(percent)))
	result.Div(result, big.NewInt(100))
	return result
}

// weiToGwei 将 wei 转换为 Gwei 字符串
func weiToGwei(wei *big.Int) string {
	gwei := new(big.Float).Quo(
		new(big.Float).SetInt(wei),
		big.NewFloat(1e9),
	)
	return gwei.Text('f', 2)
}
