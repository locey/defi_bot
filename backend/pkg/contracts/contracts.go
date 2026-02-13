// Package contracts 提供合约绑定和调用封装
//
// 使用方法：
// 1. 运行 `make generate-bindings` 生成合约绑定
// 2. 导入相应的子包使用
//
// 子包结构：
//   - pkg/contracts/core/      - 核心合约（ArbitrageCore, ArbitrageVault 等）
//   - pkg/contracts/integration/ - 集成合约（DoubleRouterIntegration 等）
//   - pkg/contracts/router/    - 路由合约（FlashLoanRouter）
//
// 注意：合约绑定文件由 abigen 自动生成，请勿手动修改
package contracts

import (
	"github.com/ethereum/go-ethereum/common"
)

// ContractAddresses 合约地址集合
type ContractAddresses struct {
	// 核心合约
	ArbitrageCore  common.Address
	ConfigManager  common.Address
	Vault          common.Address
	SpotArbitrage  common.Address
	FlashLoanRouter common.Address

	// 集成合约
	DoubleRouterIntegration common.Address
	UniswapV2Integration    common.Address

	// 工具合约
	Multicall common.Address

	// 外部合约
	AaveLendingPool common.Address
}

// ArbitrumAddresses Arbitrum One 上的合约地址
var ArbitrumAddresses = ContractAddresses{
	ArbitrageCore:           common.HexToAddress("0x27ea15F931328474d75BE5B0a493278ba7041C74"),
	ConfigManager:           common.HexToAddress("0x121D230710dc710f5AA29b73EFc8d403707173A3"),
	Vault:                   common.HexToAddress("0xB7e37Fb429795E10A8D25752fFC69Fbab71df677"),
	SpotArbitrage:           common.HexToAddress("0x7e9eC412aF1f8657b99Df8f419c5D98F11F41bd5"),
	FlashLoanRouter:         common.HexToAddress("0x4A89d8B8B0376Fc8c7e2119445c828719BeC019E"),
	DoubleRouterIntegration: common.HexToAddress("0x67d905Da6b09733f1695db12A962066Ca85A95cC"),
	UniswapV2Integration:    common.HexToAddress("0xf9680677Da105F3B10538A55F6e214F9c72AA120"),
	Multicall:               common.HexToAddress("0xcA11bde05977b3631167028862bE2a173976CA11"),
	AaveLendingPool:         common.HexToAddress("0x794a61358D6845594F94dc1DB02A252b5b4814aD"),
}

// GetAddressesForChain 根据链 ID 获取合约地址
func GetAddressesForChain(chainID uint64) *ContractAddresses {
	switch chainID {
	case 42161: // Arbitrum One
		return &ArbitrumAddresses
	default:
		return nil
	}
}
