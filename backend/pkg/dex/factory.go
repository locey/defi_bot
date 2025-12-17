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
	case "uniswap_v2", "sushiswap", "pancakeswap_v2", "":
		// 空字符串默认为 V2（向后兼容）
		return NewUniswapV2Protocol(f.web3Client), nil

	case "uniswap_v3", "pancakeswap_v3":
		return NewUniswapV3Protocol(f.web3Client), nil

	default:
		return nil, fmt.Errorf("不支持的协议: %s", protocolName)
	}
}

// GetSupportedProtocols 获取支持的协议列表
func (f *ProtocolFactory) GetSupportedProtocols() []string {
	return []string{
		"uniswap_v2",
		"uniswap_v3",
		"sushiswap",
		"pancakeswap_v2",
		"pancakeswap_v3",
	}
}

// GetProtocolType 获取协议类型（v2 或 v3）
func (f *ProtocolFactory) GetProtocolType(protocolName string) string {
	switch protocolName {
	case "uniswap_v3", "pancakeswap_v3":
		return "v3"
	default:
		return "v2"
	}
}
