// internal/executor/simulator.go
// eth_call 模拟验证 — 在提交真实交易前模拟执行确认利润
package executor

import (
	"context"
	"fmt"
	"math/big"
	"strings"

	"github.com/defi-bot/backend/pkg/log"
	"github.com/defi-bot/backend/pkg/web3"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
)

// Simulator 套利模拟器
// 使用 eth_call 在当前区块状态上模拟执行套利合约
// 确认链上真实利润 > Gas 成本后才提交真实交易
type Simulator struct {
	client          *web3.Client
	contractAddress common.Address
	contractABI     abi.ABI
	keeperAddress   common.Address
}

// SimResult 模拟结果
type SimResult struct {
	Profitable     bool     // 是否有利可图
	ExpectedProfit *big.Int // 预期利润（wei）
	GasCost        *big.Int // 预期 Gas 成本（wei）
	NetProfit      *big.Int // 净利润 = ExpectedProfit - GasCost
	GasUsed        uint64   // 预估 Gas 使用量
	Error          string   // 如果模拟失败，记录原因
}

// NewSimulator 创建模拟器
func NewSimulator(
	client *web3.Client,
	contractAddress common.Address,
	keeperPrivateKey string,
) (*Simulator, error) {
	parsedABI, err := abi.JSON(strings.NewReader(ArbitrageCoreABI))
	if err != nil {
		return nil, fmt.Errorf("simulator: parse ABI: %w", err)
	}

	var keeperAddr common.Address
	if keeperPrivateKey != "" {
		pk, err := crypto.HexToECDSA(strings.TrimPrefix(keeperPrivateKey, "0x"))
		if err == nil {
			keeperAddr = crypto.PubkeyToAddress(pk.PublicKey)
		}
	}

	return &Simulator{
		client:          client,
		contractAddress: contractAddress,
		contractABI:     parsedABI,
		keeperAddress:   keeperAddr,
	}, nil
}

// SimulateArbitrage 模拟套利交易
// 返回模拟结果，包括预期利润、Gas 成本和净利润
func (s *Simulator) SimulateArbitrage(
	ctx context.Context,
	params *ArbitrageParams,
) (*SimResult, error) {
	result := &SimResult{
		ExpectedProfit: big.NewInt(0),
		GasCost:        big.NewInt(0),
		NetProfit:      big.NewInt(0),
	}

	// 1. 构建 calldata（与 ExecuteArbitrage 相同，统一调用 executeStrategy）
	callData, err := s.buildCallData(params)
	if err != nil {
		result.Error = fmt.Sprintf("build calldata: %v", err)
		return result, nil
	}

	ethClient := s.client.GetClient()

	// 2. 用 eth_call 模拟执行
	callMsg := ethereum.CallMsg{
		From:  s.keeperAddress,
		To:    &s.contractAddress,
		Data:  callData,
		Value: big.NewInt(0),
	}

	_, err = ethClient.CallContract(ctx, callMsg, nil)
	if err != nil {
		result.Error = fmt.Sprintf("eth_call reverted: %v", err)
		result.Profitable = false
		return result, nil
	}

	// 3. 估算 Gas
	gasUsed, err := ethClient.EstimateGas(ctx, callMsg)
	if err != nil {
		result.Error = fmt.Sprintf("gas estimation failed: %v", err)
		// eth_call 成功但 gas 估算失败，仍然认为有利可图但无法精确估算成本
		result.Profitable = true
		result.ExpectedProfit = params.MinProfit
		return result, nil
	}
	result.GasUsed = gasUsed

	// 4. 计算 Gas 成本
	header, err := ethClient.HeaderByNumber(ctx, nil)
	if err == nil && header != nil && header.BaseFee != nil {
		tipCap, tipErr := ethClient.SuggestGasTipCap(ctx)
		if tipErr != nil {
			tipCap = big.NewInt(100_000_000) // 0.1 gwei fallback (Arbitrum gas is cheap)
		}
		effectiveGasPrice := new(big.Int).Add(header.BaseFee, tipCap)
		result.GasCost = new(big.Int).Mul(effectiveGasPrice, new(big.Int).SetUint64(gasUsed))
	} else {
		gasPrice, gpErr := ethClient.SuggestGasPrice(ctx)
		if gpErr != nil {
			gasPrice = big.NewInt(100_000_000) // 0.1 gwei fallback
		}
		result.GasCost = new(big.Int).Mul(gasPrice, new(big.Int).SetUint64(gasUsed))
	}

	// 5. 计算净利润
	// eth_call 成功意味着合约的 require(profit >= minProfit) 通过了
	// 所以实际利润 >= minProfit
	result.ExpectedProfit = new(big.Int).Set(params.ExpectProfit)
	if result.ExpectedProfit.Sign() <= 0 {
		result.ExpectedProfit = new(big.Int).Set(params.MinProfit)
	}

	result.NetProfit = new(big.Int).Sub(result.ExpectedProfit, result.GasCost)
	result.Profitable = result.NetProfit.Sign() > 0

	log.Strategy().Debug().
		Bool("profitable", result.Profitable).
		Str("expected_profit", result.ExpectedProfit.String()).
		Str("gas_cost", result.GasCost.String()).
		Str("net_profit", result.NetProfit.String()).
		Uint64("gas_used", result.GasUsed).
		Msg("Simulation result")

	return result, nil
}

// buildCallData 构建 executeStrategy(ArbitrageParams) 调用数据
// 与 ContractCaller.buildCallData 相同逻辑
func (s *Simulator) buildCallData(params *ArbitrageParams) ([]byte, error) {
	paramsStruct := struct {
		Asset        common.Address
		TokenOut     common.Address
		AmountIn     *big.Int
		SwapPath     []common.Address
		Dexes        []common.Address
		ExpectProfit *big.Int
		MinProfit    *big.Int
		IsCex        bool
	}{
		Asset:        params.Asset,
		TokenOut:     params.TokenOut,
		AmountIn:     params.AmountIn,
		SwapPath:     params.SwapPath,
		Dexes:        params.Dexes,
		ExpectProfit: params.ExpectProfit,
		MinProfit:    params.MinProfit,
		IsCex:        params.IsCex,
	}
	return s.contractABI.Pack("executeStrategy", paramsStruct)
}
