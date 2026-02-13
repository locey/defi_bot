// internal/vault/vault_manager.go
package vault

import (
	"context"
	"fmt"
	"math/big"
	"strings"

	"github.com/defi-bot/backend/pkg/web3"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

// ArbitrageVault ABI（核心查询和操作函数）
const ArbitrageVaultABI = `[
	{
		"inputs": [],
		"name": "totalAssets",
		"outputs": [{"internalType":"uint256","name":"","type":"uint256"}],
		"stateMutability": "view",
		"type": "function"
	},
	{
		"inputs": [],
		"name": "totalSupply",
		"outputs": [{"internalType":"uint256","name":"","type":"uint256"}],
		"stateMutability": "view",
		"type": "function"
	},
	{
		"inputs": [],
		"name": "sharePrice",
		"outputs": [{"internalType":"uint256","name":"","type":"uint256"}],
		"stateMutability": "view",
		"type": "function"
	},
	{
		"inputs": [],
		"name": "getVaultStats",
		"outputs": [
			{"internalType":"uint256","name":"_totalAssets","type":"uint256"},
			{"internalType":"uint256","name":"_totalSupply","type":"uint256"},
			{"internalType":"uint256","name":"_sharePrice","type":"uint256"},
			{"internalType":"uint256","name":"_totalProfit","type":"uint256"},
			{"internalType":"uint256","name":"_totalFees","type":"uint256"},
			{"internalType":"uint256","name":"_lastArbitrageTime","type":"uint256"}
		],
		"stateMutability": "view",
		"type": "function"
	},
	{
		"inputs": [],
		"name": "getAvailableForArbitrage",
		"outputs": [{"internalType":"uint256","name":"","type":"uint256"}],
		"stateMutability": "view",
		"type": "function"
	},
	{
		"inputs": [{"internalType":"address","name":"user","type":"address"}],
		"name": "sharesOf",
		"outputs": [{"internalType":"uint256","name":"","type":"uint256"}],
		"stateMutability": "view",
		"type": "function"
	},
	{
		"inputs": [{"internalType":"address","name":"user","type":"address"}],
		"name": "assetsOf",
		"outputs": [{"internalType":"uint256","name":"","type":"uint256"}],
		"stateMutability": "view",
		"type": "function"
	},
	{
		"inputs": [{"internalType":"address","name":"user","type":"address"}],
		"name": "getUserBalance",
		"outputs": [{"internalType":"uint256","name":"","type":"uint256"}],
		"stateMutability": "view",
		"type": "function"
	},
	{
		"inputs": [{"internalType":"uint256","name":"assets","type":"uint256"}],
		"name": "previewDeposit",
		"outputs": [{"internalType":"uint256","name":"","type":"uint256"}],
		"stateMutability": "view",
		"type": "function"
	},
	{
		"inputs": [{"internalType":"uint256","name":"shares","type":"uint256"}],
		"name": "previewRedeem",
		"outputs": [{"internalType":"uint256","name":"","type":"uint256"}],
		"stateMutability": "view",
		"type": "function"
	},
	{
		"inputs": [{"internalType":"uint256","name":"assets","type":"uint256"}],
		"name": "convertToShares",
		"outputs": [{"internalType":"uint256","name":"","type":"uint256"}],
		"stateMutability": "view",
		"type": "function"
	},
	{
		"inputs": [{"internalType":"uint256","name":"shares","type":"uint256"}],
		"name": "convertToAssets",
		"outputs": [{"internalType":"uint256","name":"","type":"uint256"}],
		"stateMutability": "view",
		"type": "function"
	},
	{
		"inputs": [
			{"internalType":"uint256","name":"assets","type":"uint256"},
			{"internalType":"address","name":"receiver","type":"address"}
		],
		"name": "deposit",
		"outputs": [{"internalType":"uint256","name":"shares","type":"uint256"}],
		"stateMutability": "nonpayable",
		"type": "function"
	},
	{
		"inputs": [
			{"internalType":"uint256","name":"shares","type":"uint256"},
			{"internalType":"address","name":"receiver","type":"address"},
			{"internalType":"address","name":"owner","type":"address"}
		],
		"name": "redeem",
		"outputs": [{"internalType":"uint256","name":"assets","type":"uint256"}],
		"stateMutability": "nonpayable",
		"type": "function"
	},
	{
		"inputs": [],
		"name": "assetAddress",
		"outputs": [{"internalType":"address","name":"","type":"address"}],
		"stateMutability": "view",
		"type": "function"
	},
	{
		"anonymous": false,
		"inputs": [
			{"indexed": true, "internalType":"address","name":"sender","type":"address"},
			{"indexed": true, "internalType":"address","name":"owner","type":"address"},
			{"indexed": false, "internalType":"uint256","name":"assets","type":"uint256"},
			{"indexed": false, "internalType":"uint256","name":"shares","type":"uint256"}
		],
		"name": "Deposit",
		"type": "event"
	},
	{
		"anonymous": false,
		"inputs": [
			{"indexed": true, "internalType":"address","name":"sender","type":"address"},
			{"indexed": true, "internalType":"address","name":"receiver","type":"address"},
			{"indexed": true, "internalType":"address","name":"owner","type":"address"},
			{"indexed": false, "internalType":"uint256","name":"assets","type":"uint256"},
			{"indexed": false, "internalType":"uint256","name":"shares","type":"uint256"}
		],
		"name": "Withdraw",
		"type": "event"
	}
]`

// VaultManager 金库管理器
type VaultManager struct {
	web3Client   *web3.Client
	vaultAddress common.Address
	vaultABI     abi.ABI
}

// VaultStats 金库统计信息
type VaultStats struct {
	TotalAssets         *big.Int `json:"total_assets"`
	TotalSupply         *big.Int `json:"total_supply"`
	SharePrice          *big.Int `json:"share_price"`
	TotalProfit         *big.Int `json:"total_profit"`
	TotalFees           *big.Int `json:"total_fees"`
	LastArbitrageTime   uint64   `json:"last_arbitrage_time"`
	AvailableForArb     *big.Int `json:"available_for_arbitrage"`
	AssetAddress        string   `json:"asset_address"`
}

// UserVaultInfo 用户金库信息
type UserVaultInfo struct {
	Address       string   `json:"address"`
	Shares        *big.Int `json:"shares"`         // 持有的份额
	AssetsValue   *big.Int `json:"assets_value"`   // 份额对应的资产价值
	DepositedAmount *big.Int `json:"deposited_amount"` // 用户本金
	ProfitLoss    *big.Int `json:"profit_loss"`    // 盈亏 = 资产价值 - 本金
}

// DepositPreview 存款预览
type DepositPreview struct {
	Assets       *big.Int `json:"assets"`        // 存入的资产数量
	Shares       *big.Int `json:"shares"`        // 预计获得的份额
	SharePrice   *big.Int `json:"share_price"`   // 当前份额价格
}

// RedeemPreview 赎回预览
type RedeemPreview struct {
	Shares       *big.Int `json:"shares"`        // 赎回的份额数量
	Assets       *big.Int `json:"assets"`        // 预计获得的资产
	SharePrice   *big.Int `json:"share_price"`   // 当前份额价格
}

// NewVaultManager 创建金库管理器
func NewVaultManager(web3Client *web3.Client, vaultAddress common.Address) (*VaultManager, error) {
	parsedABI, err := abi.JSON(strings.NewReader(ArbitrageVaultABI))
	if err != nil {
		return nil, fmt.Errorf("failed to parse vault ABI: %w", err)
	}

	return &VaultManager{
		web3Client:   web3Client,
		vaultAddress: vaultAddress,
		vaultABI:     parsedABI,
	}, nil
}

// GetVaultStats 获取金库统计信息
func (vm *VaultManager) GetVaultStats(ctx context.Context) (*VaultStats, error) {
	// 调用 getVaultStats
	callData, err := vm.vaultABI.Pack("getVaultStats")
	if err != nil {
		return nil, fmt.Errorf("pack getVaultStats failed: %w", err)
	}

	result, err := vm.call(ctx, callData)
	if err != nil {
		return nil, fmt.Errorf("call getVaultStats failed: %w", err)
	}

	// 解析返回值
	values, err := vm.vaultABI.Unpack("getVaultStats", result)
	if err != nil {
		return nil, fmt.Errorf("unpack getVaultStats failed: %w", err)
	}

	stats := &VaultStats{
		TotalAssets:       values[0].(*big.Int),
		TotalSupply:       values[1].(*big.Int),
		SharePrice:        values[2].(*big.Int),
		TotalProfit:       values[3].(*big.Int),
		TotalFees:         values[4].(*big.Int),
		LastArbitrageTime: values[5].(*big.Int).Uint64(),
	}

	// 获取可用于套利的资金
	availableData, _ := vm.vaultABI.Pack("getAvailableForArbitrage")
	availableResult, err := vm.call(ctx, availableData)
	if err == nil {
		availableValues, _ := vm.vaultABI.Unpack("getAvailableForArbitrage", availableResult)
		if len(availableValues) > 0 {
			stats.AvailableForArb = availableValues[0].(*big.Int)
		}
	}

	// 获取底层资产地址
	assetData, _ := vm.vaultABI.Pack("assetAddress")
	assetResult, err := vm.call(ctx, assetData)
	if err == nil {
		assetValues, _ := vm.vaultABI.Unpack("assetAddress", assetResult)
		if len(assetValues) > 0 {
			stats.AssetAddress = assetValues[0].(common.Address).Hex()
		}
	}

	return stats, nil
}

// GetUserInfo 获取用户金库信息
func (vm *VaultManager) GetUserInfo(ctx context.Context, userAddress common.Address) (*UserVaultInfo, error) {
	info := &UserVaultInfo{
		Address: userAddress.Hex(),
	}

	// 获取用户份额
	sharesData, _ := vm.vaultABI.Pack("sharesOf", userAddress)
	sharesResult, err := vm.call(ctx, sharesData)
	if err != nil {
		return nil, fmt.Errorf("get shares failed: %w", err)
	}
	sharesValues, _ := vm.vaultABI.Unpack("sharesOf", sharesResult)
	info.Shares = sharesValues[0].(*big.Int)

	// 获取份额对应的资产价值
	assetsData, _ := vm.vaultABI.Pack("assetsOf", userAddress)
	assetsResult, err := vm.call(ctx, assetsData)
	if err != nil {
		return nil, fmt.Errorf("get assets value failed: %w", err)
	}
	assetsValues, _ := vm.vaultABI.Unpack("assetsOf", assetsResult)
	info.AssetsValue = assetsValues[0].(*big.Int)

	// 获取用户本金
	balanceData, _ := vm.vaultABI.Pack("getUserBalance", userAddress)
	balanceResult, err := vm.call(ctx, balanceData)
	if err != nil {
		return nil, fmt.Errorf("get user balance failed: %w", err)
	}
	balanceValues, _ := vm.vaultABI.Unpack("getUserBalance", balanceResult)
	info.DepositedAmount = balanceValues[0].(*big.Int)

	// 计算盈亏
	info.ProfitLoss = new(big.Int).Sub(info.AssetsValue, info.DepositedAmount)

	return info, nil
}

// PreviewDeposit 预览存款
func (vm *VaultManager) PreviewDeposit(ctx context.Context, assets *big.Int) (*DepositPreview, error) {
	preview := &DepositPreview{
		Assets: assets,
	}

	// 预览存款获得的份额
	previewData, _ := vm.vaultABI.Pack("previewDeposit", assets)
	previewResult, err := vm.call(ctx, previewData)
	if err != nil {
		return nil, fmt.Errorf("preview deposit failed: %w", err)
	}
	previewValues, _ := vm.vaultABI.Unpack("previewDeposit", previewResult)
	preview.Shares = previewValues[0].(*big.Int)

	// 获取当前份额价格
	priceData, _ := vm.vaultABI.Pack("sharePrice")
	priceResult, err := vm.call(ctx, priceData)
	if err == nil {
		priceValues, _ := vm.vaultABI.Unpack("sharePrice", priceResult)
		preview.SharePrice = priceValues[0].(*big.Int)
	}

	return preview, nil
}

// PreviewRedeem 预览赎回
func (vm *VaultManager) PreviewRedeem(ctx context.Context, shares *big.Int) (*RedeemPreview, error) {
	preview := &RedeemPreview{
		Shares: shares,
	}

	// 预览赎回获得的资产
	previewData, _ := vm.vaultABI.Pack("previewRedeem", shares)
	previewResult, err := vm.call(ctx, previewData)
	if err != nil {
		return nil, fmt.Errorf("preview redeem failed: %w", err)
	}
	previewValues, _ := vm.vaultABI.Unpack("previewRedeem", previewResult)
	preview.Assets = previewValues[0].(*big.Int)

	// 获取当前份额价格
	priceData, _ := vm.vaultABI.Pack("sharePrice")
	priceResult, err := vm.call(ctx, priceData)
	if err == nil {
		priceValues, _ := vm.vaultABI.Unpack("sharePrice", priceResult)
		preview.SharePrice = priceValues[0].(*big.Int)
	}

	return preview, nil
}

// Deposit 执行存款（需要用户私钥签名）
func (vm *VaultManager) Deposit(
	ctx context.Context,
	privateKey string,
	assets *big.Int,
	receiver common.Address,
) (*types.Transaction, error) {
	callData, err := vm.vaultABI.Pack("deposit", assets, receiver)
	if err != nil {
		return nil, fmt.Errorf("pack deposit failed: %w", err)
	}

	return vm.sendTransaction(ctx, privateKey, callData)
}

// Redeem 执行赎回（需要用户私钥签名）
func (vm *VaultManager) Redeem(
	ctx context.Context,
	privateKey string,
	shares *big.Int,
	receiver common.Address,
	owner common.Address,
) (*types.Transaction, error) {
	callData, err := vm.vaultABI.Pack("redeem", shares, receiver, owner)
	if err != nil {
		return nil, fmt.Errorf("pack redeem failed: %w", err)
	}

	return vm.sendTransaction(ctx, privateKey, callData)
}

// call 执行只读调用
func (vm *VaultManager) call(ctx context.Context, data []byte) ([]byte, error) {
	client := vm.web3Client.GetClient()
	return client.CallContract(ctx, ethereum.CallMsg{
		To:   &vm.vaultAddress,
		Data: data,
	}, nil)
}

// sendTransaction 发送交易
func (vm *VaultManager) sendTransaction(ctx context.Context, privateKeyHex string, data []byte) (*types.Transaction, error) {
	pk, err := crypto.HexToECDSA(strings.TrimPrefix(privateKeyHex, "0x"))
	if err != nil {
		return nil, fmt.Errorf("invalid private key: %w", err)
	}

	from := crypto.PubkeyToAddress(pk.PublicKey)
	client := vm.web3Client.GetClient()

	nonce, err := client.PendingNonceAt(ctx, from)
	if err != nil {
		return nil, fmt.Errorf("get nonce failed: %w", err)
	}

	gasLimit, err := client.EstimateGas(ctx, ethereum.CallMsg{
		From: from,
		To:   &vm.vaultAddress,
		Data: data,
	})
	if err != nil {
		gasLimit = 300000 // 默认值
	}
	gasLimit = gasLimit * 120 / 100 // 加 20% buffer

	// 使用 EIP-1559
	header, err := client.HeaderByNumber(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("get header failed: %w", err)
	}

	tipCap, _ := client.SuggestGasTipCap(ctx)
	if tipCap == nil {
		tipCap = big.NewInt(2_000_000_000) // 2 gwei
	}

	feeCap := new(big.Int).Mul(header.BaseFee, big.NewInt(2))
	feeCap.Add(feeCap, tipCap)

	tx := types.NewTx(&types.DynamicFeeTx{
		ChainID:   vm.web3Client.GetChainID(),
		Nonce:     nonce,
		To:        &vm.vaultAddress,
		Gas:       gasLimit,
		GasTipCap: tipCap,
		GasFeeCap: feeCap,
		Value:     big.NewInt(0),
		Data:      data,
	})

	signer := types.LatestSignerForChainID(vm.web3Client.GetChainID())
	signedTx, err := types.SignTx(tx, signer, pk)
	if err != nil {
		return nil, fmt.Errorf("sign tx failed: %w", err)
	}

	if err := client.SendTransaction(ctx, signedTx); err != nil {
		return nil, fmt.Errorf("send tx failed: %w", err)
	}

	return signedTx, nil
}
