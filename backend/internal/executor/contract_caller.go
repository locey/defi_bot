// internal/executor/contract_caller.go
package executor

import (
	"context"
	"fmt"
	"math"
	"math/big"
	"strings"

	"github.com/defi-bot/backend/pkg/web3"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

// ArbitrageCore ABI（最小化：executeStrategy + 相关事件）
// 合约变更：移除 StrategyTypes 枚举参数，ArbitrageParams 新增 isCex 字段，移除闪电贷相关函数和事件
const ArbitrageCoreABI = `[
    {
        "inputs": [
            {
                "components": [
                    {"internalType":"address","name":"asset","type":"address"},
                    {"internalType":"address","name":"tokenOut","type":"address"},
                    {"internalType":"uint256","name":"amountIn","type":"uint256"},
                    {"internalType":"address[]","name":"swapPath","type":"address[]"},
                    {"internalType":"address[]","name":"dexes","type":"address[]"},
                    {"internalType":"uint256","name":"expectProfit","type":"uint256"},
                    {"internalType":"uint256","name":"minProfit","type":"uint256"},
                    {"internalType":"bool","name":"isCex","type":"bool"}
                ],
                "internalType":"struct IArbitrage.ArbitrageParams",
                "name":"params",
                "type":"tuple"
            }
        ],
        "name": "executeStrategy",
        "outputs": [],
        "stateMutability": "nonpayable",
        "type": "function"
    },
    {
        "anonymous": false,
        "inputs": [
            {"indexed": true, "internalType":"address", "name":"vault", "type":"address"},
            {"indexed": true, "internalType":"address", "name":"asset", "type":"address"},
            {"indexed": false, "internalType":"uint256", "name":"amountIn", "type":"uint256"},
            {"indexed": false, "internalType":"uint256", "name":"profit", "type":"uint256"},
            {"indexed": false, "internalType":"uint256", "name":"platFormFee", "type":"uint256"},
            {"indexed": false, "internalType":"uint256", "name":"netProfitToVault", "type":"uint256"},
            {"indexed": false, "internalType":"uint256", "name":"timestamp", "type":"uint256"}
        ],
        "name": "VaultArbitrageExecuted",
        "type": "event"
    }
]`

// ContractCaller 合约调用器
type ContractCaller struct {
	web3Client      *web3.Client
	contractAddress common.Address
	contractABI     abi.ABI
}

// NewContractCaller 创建合约调用器
func NewContractCaller(
	web3Client *web3.Client,
	contractAddress common.Address,
) *ContractCaller {

	parsedABI, err := abi.JSON(strings.NewReader(ArbitrageCoreABI))
	if err != nil {
		panic(fmt.Sprintf("failed to parse ABI: %v", err))
	}

	return &ContractCaller{
		web3Client:      web3Client,
		contractAddress: contractAddress,
		contractABI:     parsedABI,
	}
}

// ExecuteArbitrage 执行套利
func (cc *ContractCaller) ExecuteArbitrage(
	ctx context.Context,
	keeperPrivateKey string,
	params *ArbitrageParams,
) (*types.Transaction, error) {

	if keeperPrivateKey == "" {
		return nil, fmt.Errorf("keeper private key is empty")
	}

	callData, err := cc.buildCallData(params)
	if err != nil {
		return nil, err
	}

	pk, err := crypto.HexToECDSA(strings.TrimPrefix(keeperPrivateKey, "0x"))
	if err != nil {
		return nil, fmt.Errorf("invalid keeper private key: %w", err)
	}

	from := crypto.PubkeyToAddress(pk.PublicKey)
	client := cc.web3Client.GetClient()

	nonce, err := client.PendingNonceAt(ctx, from)
	if err != nil {
		return nil, fmt.Errorf("get nonce failed: %w", err)
	}

	// gas estimate
	gasLimit, err := client.EstimateGas(ctx, ethereum.CallMsg{
		From:  from,
		To:    &cc.contractAddress,
		Data:  callData,
		Value: big.NewInt(0),
	})
	if err != nil {
		// 估算失败时给一个保守默认值
		gasLimit = 800000
	}

	// 加 20% buffer
	gasLimit = uint64(math.Ceil(float64(gasLimit) * 1.2))

	// Prefer EIP-1559 if baseFee is available
	header, hErr := client.HeaderByNumber(ctx, nil)
	if hErr == nil && header != nil && header.BaseFee != nil {
		tipCap, tipErr := client.SuggestGasTipCap(ctx)
		if tipErr != nil {
			tipCap = big.NewInt(2_000_000_000) // 2 gwei fallback
		}
		feeCap := new(big.Int).Mul(header.BaseFee, big.NewInt(2))
		feeCap.Add(feeCap, tipCap)

		tx := types.NewTx(&types.DynamicFeeTx{
			ChainID:   cc.web3Client.GetChainID(),
			Nonce:     nonce,
			To:        &cc.contractAddress,
			Gas:       gasLimit,
			GasTipCap: tipCap,
			GasFeeCap: feeCap,
			Value:     big.NewInt(0),
			Data:      callData,
		})

		signer := types.LatestSignerForChainID(cc.web3Client.GetChainID())
		signed, err := types.SignTx(tx, signer, pk)
		if err != nil {
			return nil, fmt.Errorf("sign tx failed: %w", err)
		}
		if err := client.SendTransaction(ctx, signed); err != nil {
			return nil, fmt.Errorf("send tx failed: %w", err)
		}
		return signed, nil
	}

	// Legacy fallback
	gasPrice, err := client.SuggestGasPrice(ctx)
	if err != nil {
		return nil, fmt.Errorf("suggest gas price failed: %w", err)
	}

	tx := types.NewTx(&types.LegacyTx{
		Nonce:    nonce,
		To:       &cc.contractAddress,
		Gas:      gasLimit,
		GasPrice: gasPrice,
		Value:    big.NewInt(0),
		Data:     callData,
	})

	signer := types.LatestSignerForChainID(cc.web3Client.GetChainID())
	signed, err := types.SignTx(tx, signer, pk)
	if err != nil {
		return nil, fmt.Errorf("sign tx failed: %w", err)
	}

	if err := client.SendTransaction(ctx, signed); err != nil {
		return nil, fmt.Errorf("send tx failed: %w", err)
	}

	return signed, nil
}

// buildCallData 构建 executeStrategy(ArbitrageParams) 调用数据
func (cc *ContractCaller) buildCallData(params *ArbitrageParams) ([]byte, error) {

	// 构建参数结构体（与合约 IArbitrage.ArbitrageParams 一一对应）
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

	// 编码调用数据
	callData, err := cc.contractABI.Pack("executeStrategy", paramsStruct)
	if err != nil {
		return nil, err
	}

	return callData, nil
}

// estimateGas 估算Gas
func (cc *ContractCaller) estimateGas(
	ctx context.Context,
	callData []byte,
) (uint64, error) {
	// 使用默认 Gas 限制
	// TODO: 实现精确的 Gas 估算
	return 800000, nil
}

// sendTransaction 发送交易
func (cc *ContractCaller) sendTransaction(
	ctx context.Context,
	auth interface{},
	callData []byte,
) (*types.Transaction, error) {
	// TODO: 实现交易发送逻辑
	return nil, fmt.Errorf("transaction sending not implemented")
}

// SimulateArbitrage 模拟套利（不发送交易）
func (cc *ContractCaller) SimulateArbitrage(
	ctx context.Context,
	params *ArbitrageParams,
) (*big.Int, error) {
	// TODO: 实现套利模拟
	return big.NewInt(0), fmt.Errorf("simulation not implemented")
}

// DebugCallData 返回 executeStrategy 的 calldata（便于排查/打印）
func (cc *ContractCaller) DebugCallData(params *ArbitrageParams) (string, error) {
	data, err := cc.buildCallData(params)
	if err != nil {
		return "", err
	}
	return hexutil.Encode(data), nil
}
