package dex

import (
	"fmt"

	"github.com/defi-bot/backend/pkg/web3"
)

// ProtocolFactory 协议工厂
// 根据协议名称创建对应的协议适配器
type ProtocolFactory struct {
	web3Client *web3.Client
}

// NewProtocolFactory 创建协议工厂
func NewProtocolFactory(web3Client *web3.Client) *ProtocolFactory {
	return &ProtocolFactory{
		web3Client: web3Client,
	}
}

// CreateProtocol 创建协议适配器
func (f *ProtocolFactory) CreateProtocol(protocolName string) (Protocol, error) {
	switch protocolName {
	// === V2 兼容协议（AMM） ===
	case "uniswap_v2", "sushiswap", "pancakeswap_v2", "shibaswap", "biswap", "":
		// 空字符串默认为 V2（向后兼容）
		return NewUniswapV2Protocol(f.web3Client), nil

	// === V3 协议（集中流动性 AMM） ===
	case "uniswap_v3", "pancakeswap_v3":
		return NewUniswapV3Protocol(f.web3Client), nil

	// === StableSwap 协议（稳定币交换） ===
	case "curve", "ellipsis":
		// TODO: 实现 Curve 适配器
		return nil, fmt.Errorf("Curve 协议适配器开发中")

	// === 聚合器协议 ===
	case "1inch", "0x", "paraswap", "matcha":
		// TODO: 实现聚合器适配器
		return nil, fmt.Errorf("聚合器协议适配器开发中")

	// === 订单簿协议 ===
	case "dydx", "serum":
		// TODO: 实现订单簿适配器
		return nil, fmt.Errorf("订单簿协议适配器开发中")

	// === 混合型协议 ===
	case "balancer":
		// TODO: 实现 Balancer 适配器
		return nil, fmt.Errorf("Balancer 协议适配器开发中")

	default:
		return nil, fmt.Errorf("不支持的协议: %s", protocolName)
	}
}

// GetSupportedProtocols 获取支持的协议列表
func (f *ProtocolFactory) GetSupportedProtocols() []string {
	return []string{
		// AMM - V2类型
		"uniswap_v2",
		"sushiswap",
		"pancakeswap_v2",
		"shibaswap",
		"biswap",

		// AMM - V3类型
		"uniswap_v3",
		"pancakeswap_v3",

		// StableSwap
		"curve",
		"ellipsis",

		// Aggregator（开发中）
		"1inch",
		"0x",
		"paraswap",
		"matcha",

		// OrderBook（开发中）
		"dydx",
		"serum",

		// Hybrid（开发中）
		"balancer",
	}
}

// GetProtocolType 获取协议类型（v2 或 v3）
func (f *ProtocolFactory) GetProtocolType(protocolName string) string {
	switch protocolName {
	case "uniswap_v3", "pancakeswap_v3":
		return "v3"
	case "curve", "ellipsis":
		return "stableswap"
	case "1inch", "0x", "paraswap", "matcha":
		return "aggregator"
	case "dydx", "serum":
		return "orderbook"
	case "balancer":
		return "hybrid"
	default:
		return "v2"
	}
}

// GetDexType 获取 DEX 大类
func (f *ProtocolFactory) GetDexType(protocolName string) string {
	switch protocolName {
	case "1inch", "0x", "paraswap", "matcha":
		return "aggregator"
	case "dydx", "serum":
		return "orderbook"
	case "balancer":
		return "hybrid"
	default:
		return "amm"
	}
}
