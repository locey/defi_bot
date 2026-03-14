// internal/liquidation/executor.go
// 清算执行器 — 支持双闪电贷源：
//   1. BalancerLiquidator（优先）— Balancer V2 免费闪电贷，0% 费率
//   2. FlashLoanLiquidator（备用）— Aave V3 闪电贷，0.05% 费率
package liquidation

import (
	"context"
	"crypto/ecdsa"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/defi-bot/backend/pkg/log"
	"github.com/defi-bot/backend/pkg/web3"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

// LiquidationExecutor 清算执行器
type LiquidationExecutor struct {
	web3Client    *web3.Client
	privateKey    *ecdsa.PrivateKey // 缓存解析后的私钥，避免每次交易重新解析
	keeperAddress common.Address

	// 主合约：BalancerLiquidator（Balancer 免费闪电贷）
	balancerAddress common.Address
	balancerABI     abi.ABI
	useBalancer     bool

	// 备用合约：FlashLoanLiquidator（Aave 闪电贷 0.05%）
	aaveAddress common.Address
	aaveABI     abi.ABI

	chainID *big.Int

	// 配置
	dryRun      bool
	maxGasPrice *big.Int // 默认 50 gwei，防止 gas 飙升
}

// NewLiquidationExecutor 创建执行器
// balancerLiquidatorAddr 为空时自动 fallback 到 Aave FlashLoanLiquidator
func NewLiquidationExecutor(
	web3Client *web3.Client,
	aaveLiquidatorAddr common.Address,
	balancerLiquidatorAddr common.Address,
	keeperPrivateKey string,
	chainID int64,
	dryRun bool,
) (*LiquidationExecutor, error) {
	// 解析 Aave FlashLoanLiquidator ABI（始终需要，作为备用）
	aaveParsedABI, err := abi.JSON(strings.NewReader(FlashLoanLiquidatorABI))
	if err != nil {
		return nil, fmt.Errorf("parse aave liquidator ABI: %w", err)
	}

	// 解析 BalancerLiquidator ABI
	balancerParsedABI, err := abi.JSON(strings.NewReader(BalancerLiquidatorABI))
	if err != nil {
		return nil, fmt.Errorf("parse balancer liquidator ABI: %w", err)
	}

	var keeperAddr common.Address
	var pk *ecdsa.PrivateKey
	if keeperPrivateKey != "" {
		var err2 error
		pk, err2 = crypto.HexToECDSA(strings.TrimPrefix(keeperPrivateKey, "0x"))
		if err2 != nil {
			return nil, fmt.Errorf("invalid keeper private key: %w", err2)
		}
		keeperAddr = crypto.PubkeyToAddress(pk.PublicKey)
	}

	useBalancer := balancerLiquidatorAddr != (common.Address{})

	if useBalancer {
		log.Executor().Info().
			Str("balancer", balancerLiquidatorAddr.Hex()).
			Str("aave_fallback", aaveLiquidatorAddr.Hex()).
			Msg("清算执行器：使用 Balancer 免费闪电贷（主）+ Aave 0.05%（备）")
	} else {
		log.Executor().Info().
			Str("aave", aaveLiquidatorAddr.Hex()).
			Msg("清算执行器：使用 Aave 闪电贷（0.05% 费率）")
	}

	// 默认 maxGasPrice = 50 gwei（Arbitrum 通常 0.1-0.5 gwei，50 gwei 为安全上限）
	defaultMaxGas := new(big.Int).Mul(big.NewInt(50), big.NewInt(1_000_000_000))

	return &LiquidationExecutor{
		web3Client:      web3Client,
		privateKey:      pk,
		keeperAddress:   keeperAddr,
		balancerAddress: balancerLiquidatorAddr,
		balancerABI:     balancerParsedABI,
		useBalancer:     useBalancer,
		aaveAddress:     aaveLiquidatorAddr,
		aaveABI:         aaveParsedABI,
		chainID:         big.NewInt(chainID),
		dryRun:          dryRun,
		maxGasPrice:     defaultMaxGas,
	}, nil
}

// Execute 执行清算（优先 Balancer，失败则 fallback 到 Aave）
func (e *LiquidationExecutor) Execute(ctx context.Context, opp *LiquidationOpportunity) (*LiquidationResult, error) {
	result := &LiquidationResult{
		OpportunityID: opp.ID,
		Timestamp:     time.Now(),
	}

	// Step 1: 检查有效期
	if time.Now().After(opp.ValidUntil) {
		result.Error = "opportunity expired"
		return result, fmt.Errorf("opportunity expired")
	}

	// Step 2: 计算 minProfit（expectedProfit * 50%，防 MEV 三明治攻击）
	minProfit := new(big.Int).Div(opp.ExpectedProfit, big.NewInt(2))
	if minProfit.Sign() <= 0 {
		minProfit = big.NewInt(1)
	}

	// Step 3: 尝试 Balancer（免费闪电贷）
	if e.useBalancer {
		callData, targetAddr, err := e.packBalancerCall(opp, minProfit)
		if err == nil {
			simResult, simErr := e.simulateAndExecute(ctx, opp, result, callData, targetAddr, "Balancer")
			if simErr == nil {
				return simResult, nil
			}
			// Balancer 失败，fallback 到 Aave
			log.Executor().Warn().Err(simErr).
				Str("user", opp.User.Hex()).
				Msg("清算：Balancer 闪电贷失败，fallback 到 Aave")
		}
	}

	// Step 4: Aave 闪电贷（0.05% 费率）
	callData, targetAddr, err := e.packAaveCall(opp, minProfit)
	if err != nil {
		result.Error = fmt.Sprintf("pack aave call data: %v", err)
		return result, err
	}

	return e.simulateAndExecute(ctx, opp, result, callData, targetAddr, "Aave")
}

// packBalancerCall 编码 BalancerLiquidator.executeLiquidation 调用
func (e *LiquidationExecutor) packBalancerCall(opp *LiquidationOpportunity, minProfit *big.Int) ([]byte, common.Address, error) {
	// ABI 中 swapFeeTier 是 uint24（最大 16777215），go-ethereum 要求 *big.Int
	if opp.SwapFeeTier > 0xFFFFFF {
		return nil, e.balancerAddress, fmt.Errorf("swap fee tier %d exceeds uint24 max", opp.SwapFeeTier)
	}
	feeTier := new(big.Int).SetUint64(uint64(opp.SwapFeeTier))
	callData, err := e.balancerABI.Pack(
		"executeLiquidation",
		opp.CollateralAsset,
		opp.DebtAsset,
		opp.User,
		opp.DebtToCover,
		opp.SwapRouter,
		feeTier,
		minProfit,
	)
	return callData, e.balancerAddress, err
}

// packAaveCall 编码 FlashLoanLiquidator.executeLiquidationWithMinProfit 调用
func (e *LiquidationExecutor) packAaveCall(opp *LiquidationOpportunity, minProfit *big.Int) ([]byte, common.Address, error) {
	feeTier := new(big.Int).SetUint64(uint64(opp.SwapFeeTier))
	callData, err := e.aaveABI.Pack(
		"executeLiquidationWithMinProfit",
		opp.CollateralAsset,
		opp.DebtAsset,
		opp.User,
		opp.DebtToCover,
		opp.SwapRouter,
		feeTier,
		minProfit,
	)
	return callData, e.aaveAddress, err
}

// simulateAndExecute eth_call 模拟 → 发送交易
func (e *LiquidationExecutor) simulateAndExecute(
	ctx context.Context,
	opp *LiquidationOpportunity,
	result *LiquidationResult,
	callData []byte,
	targetAddr common.Address,
	source string,
) (*LiquidationResult, error) {
	// eth_call 模拟
	simMsg := ethereum.CallMsg{
		From: e.keeperAddress,
		To:   &targetAddr,
		Data: callData,
	}

	_, err := e.web3Client.GetClient().CallContract(ctx, simMsg, nil)
	if err != nil {
		result.Error = fmt.Sprintf("[%s] simulation failed: %v", source, err)
		log.Executor().Warn().Err(err).
			Str("source", source).
			Str("user", opp.User.Hex()).
			Str("debt", opp.DebtSymbol).
			Str("collateral", opp.CollateralSymbol).
			Msg("清算：模拟执行失败")
		return result, err
	}

	log.Executor().Info().
		Str("source", source).
		Str("user", opp.User.Hex()).
		Str("debt", opp.DebtSymbol).
		Str("collateral", opp.CollateralSymbol).
		Float64("health_factor", opp.HealthFactor).
		Str("debt_to_cover", opp.DebtToCover.String()).
		Msg("清算：模拟执行成功")

	// 干运行模式
	if e.dryRun {
		result.Success = true
		result.Error = fmt.Sprintf("dry-run mode [%s], simulation passed", source)
		log.Executor().Info().
			Str("opp_id", opp.ID).
			Str("source", source).
			Msg("清算：干运行模式，跳过真实交易")
		return result, nil
	}

	// 发送真实交易
	tx, err := e.sendTransaction(ctx, callData, targetAddr)
	if err != nil {
		result.Error = fmt.Sprintf("[%s] send tx: %v", source, err)
		return result, err
	}

	result.TxHash = tx.Hash()

	log.Executor().Info().
		Str("tx_hash", tx.Hash().Hex()).
		Str("source", source).
		Str("user", opp.User.Hex()).
		Msg("清算：交易已发送")

	// 等待确认
	receipt, err := e.waitForReceipt(ctx, tx.Hash())
	if err != nil {
		result.Error = fmt.Sprintf("wait receipt: %v", err)
		return result, err
	}

	result.GasUsed = receipt.GasUsed
	effectiveGasPrice := receipt.EffectiveGasPrice
	if effectiveGasPrice == nil {
		effectiveGasPrice = tx.GasPrice()
	}
	if effectiveGasPrice == nil {
		effectiveGasPrice = big.NewInt(100_000_000) // 0.1 gwei fallback
	}
	result.GasCost = new(big.Int).Mul(
		new(big.Int).SetUint64(receipt.GasUsed),
		effectiveGasPrice,
	)

	if receipt.Status == types.ReceiptStatusSuccessful {
		result.Success = true
		log.Executor().Info().
			Str("tx_hash", tx.Hash().Hex()).
			Str("source", source).
			Uint64("gas_used", receipt.GasUsed).
			Msg("清算：执行成功！")
	} else {
		result.Error = "transaction reverted"
		log.Executor().Warn().
			Str("tx_hash", tx.Hash().Hex()).
			Str("source", source).
			Msg("清算：交易 revert")
	}

	return result, nil
}

// sendTransaction 构建并发送 EIP-1559 交易
func (e *LiquidationExecutor) sendTransaction(ctx context.Context, callData []byte, target common.Address) (*types.Transaction, error) {
	if e.privateKey == nil {
		return nil, fmt.Errorf("keeper private key not set")
	}

	client := e.web3Client.GetClient()

	nonce, err := client.PendingNonceAt(ctx, e.keeperAddress)
	if err != nil {
		return nil, fmt.Errorf("get nonce: %w", err)
	}

	head, err := client.HeaderByNumber(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("get block header: %w", err)
	}
	baseFee := head.BaseFee
	if baseFee == nil {
		baseFee = big.NewInt(100_000_000)
	}

	tip, err := client.SuggestGasTipCap(ctx)
	if err != nil {
		tip = big.NewInt(1_000_000)
	}

	// 清算竞速：提高 gas tip 以获得 Arbitrum FCFS 排序优先
	// Arbitrum 正常 tip ~0.01 gwei，清算时 10x boost = ~0.1 gwei（仍然很便宜）
	boostedTip := new(big.Int).Mul(tip, big.NewInt(10))
	// 最低 0.1 gwei tip（确保竞争力）
	minTip := big.NewInt(100_000_000) // 0.1 gwei
	if boostedTip.Cmp(minTip) < 0 {
		boostedTip = minTip
	}
	tip = boostedTip

	maxFee := new(big.Int).Mul(baseFee, big.NewInt(2))
	maxFee.Add(maxFee, tip)

	if e.maxGasPrice != nil && maxFee.Cmp(e.maxGasPrice) > 0 {
		return nil, fmt.Errorf("gas fee %s exceeds max %s", maxFee.String(), e.maxGasPrice.String())
	}

	gasLimit, err := client.EstimateGas(ctx, ethereum.CallMsg{
		From: e.keeperAddress,
		To:   &target,
		Data: callData,
	})
	if err != nil {
		return nil, fmt.Errorf("estimate gas: %w", err)
	}
	// 20% buffer，先除再乘避免 uint64 overflow
	gasLimit = gasLimit/100*120 + gasLimit%100*120/100

	tx := types.NewTx(&types.DynamicFeeTx{
		ChainID:   e.chainID,
		Nonce:     nonce,
		GasTipCap: tip,
		GasFeeCap: maxFee,
		Gas:       gasLimit,
		To:        &target,
		Value:     big.NewInt(0),
		Data:      callData,
	})

	signer := types.LatestSignerForChainID(e.chainID)
	signedTx, err := types.SignTx(tx, signer, e.privateKey)
	if err != nil {
		return nil, fmt.Errorf("sign tx: %w", err)
	}

	if err := client.SendTransaction(ctx, signedTx); err != nil {
		return nil, fmt.Errorf("send tx: %w", err)
	}

	return signedTx, nil
}

// waitForReceipt 等待交易收据
func (e *LiquidationExecutor) waitForReceipt(ctx context.Context, txHash common.Hash) (*types.Receipt, error) {
	client := e.web3Client.GetClient()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	timeout := time.After(60 * time.Second)

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timeout:
			return nil, fmt.Errorf("receipt timeout after 60s")
		case <-ticker.C:
			receipt, err := client.TransactionReceipt(ctx, txHash)
			if err != nil {
				continue
			}
			return receipt, nil
		}
	}
}
