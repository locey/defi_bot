// internal/strategy/v2v3_strategy.go
package strategy

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

// UniswapV2V3Integration ABI
const UniswapV2V3IntegrationABI = `[
	{
		"inputs": [
			{
				"components": [
					{"internalType":"address","name":"recipient","type":"address"},
					{"internalType":"address","name":"tokenIn","type":"address"},
					{"internalType":"address","name":"tokenOut","type":"address"},
					{"internalType":"uint24","name":"v3Fee","type":"uint24"},
					{"internalType":"uint256","name":"amountIn","type":"uint256"},
					{"internalType":"uint256","name":"minProfit","type":"uint256"}
				],
				"internalType":"struct UniswapV2V3Integration.ArbitrageV2ToV3Param",
				"name":"param",
				"type":"tuple"
			}
		],
		"name": "arbitrageV2ToV3",
		"outputs": [
			{"internalType":"uint256","name":"finalAmountOut","type":"uint256"},
			{"internalType":"uint256","name":"profit","type":"uint256"}
		],
		"stateMutability": "nonpayable",
		"type": "function"
	},
	{
		"inputs": [
			{
				"components": [
					{"internalType":"address","name":"recipient","type":"address"},
					{"internalType":"address","name":"tokenIn","type":"address"},
					{"internalType":"address","name":"tokenOut","type":"address"},
					{"internalType":"uint24","name":"v3Fee","type":"uint24"},
					{"internalType":"uint256","name":"amountIn","type":"uint256"},
					{"internalType":"uint256","name":"minProfit","type":"uint256"}
				],
				"internalType":"struct UniswapV2V3Integration.ArbitrageV3ToV2Param",
				"name":"param",
				"type":"tuple"
			}
		],
		"name": "arbitrageV3ToV2",
		"outputs": [
			{"internalType":"uint256","name":"finalAmountOut","type":"uint256"},
			{"internalType":"uint256","name":"profit","type":"uint256"}
		],
		"stateMutability": "nonpayable",
		"type": "function"
	},
	{
		"anonymous": false,
		"inputs": [
			{"indexed": true, "internalType":"address","name":"tokenA","type":"address"},
			{"indexed": true, "internalType":"address","name":"tokenB","type":"address"},
			{"indexed": false, "internalType":"uint24","name":"v3Fee","type":"uint24"},
			{"indexed": false, "internalType":"uint256","name":"amountInV2","type":"uint256"},
			{"indexed": false, "internalType":"uint256","name":"amountOutV3","type":"uint256"},
			{"indexed": false, "internalType":"uint256","name":"profit","type":"uint256"},
			{"indexed": false, "internalType":"uint256","name":"timestamp","type":"uint256"}
		],
		"name": "ArbitrageV2ToV3",
		"type": "event"
	},
	{
		"anonymous": false,
		"inputs": [
			{"indexed": true, "internalType":"address","name":"tokenA","type":"address"},
			{"indexed": true, "internalType":"address","name":"tokenB","type":"address"},
			{"indexed": false, "internalType":"uint24","name":"v3Fee","type":"uint24"},
			{"indexed": false, "internalType":"uint256","name":"amountInV3","type":"uint256"},
			{"indexed": false, "internalType":"uint256","name":"amountOutV2","type":"uint256"},
			{"indexed": false, "internalType":"uint256","name":"profit","type":"uint256"},
			{"indexed": false, "internalType":"uint256","name":"timestamp","type":"uint256"}
		],
		"name": "ArbitrageV3ToV2",
		"type": "event"
	}
]`

// V3FeeTier Uniswap V3 费率等级
type V3FeeTier uint32

const (
	V3Fee005  V3FeeTier = 500   // 0.05%
	V3Fee030  V3FeeTier = 3000  // 0.3%
	V3Fee100  V3FeeTier = 10000 // 1%
)

// ArbitrageDirection 套利方向
type ArbitrageDirection int

const (
	DirectionV2ToV3 ArbitrageDirection = iota // V2 买入 → V3 卖出
	DirectionV3ToV2                           // V3 买入 → V2 卖出
)

// V2V3ArbitrageParams V2/V3 跨版本套利参数
type V2V3ArbitrageParams struct {
	Direction   ArbitrageDirection // 套利方向
	Recipient   common.Address     // 接收代币的地址
	TokenIn     common.Address     // 输入代币地址
	TokenOut    common.Address     // 输出代币地址
	V3Fee       V3FeeTier          // V3 池费率
	AmountIn    *big.Int           // 输入金额
	MinProfit   *big.Int           // 最小利润
}

// V2V3ArbitrageOpportunity V2/V3 套利机会
type V2V3ArbitrageOpportunity struct {
	ID            string             // 机会 ID
	Direction     ArbitrageDirection // 套利方向
	TokenIn       common.Address     // 输入代币
	TokenOut      common.Address     // 输出代币
	V3Fee         V3FeeTier          // V3 费率
	AmountIn      *big.Int           // 建议输入金额
	ExpectedOut   *big.Int           // 预期输出
	ExpectedProfit *big.Int          // 预期利润
	ProfitRate    float64            // 利润率 (%)
	V2Price       *big.Float         // V2 价格
	V3Price       *big.Float         // V3 价格
	PriceGap      float64            // 价差 (%)
}

// V2V3Strategy V2/V3 跨版本套利策略
type V2V3Strategy struct {
	web3Client        *web3.Client
	integrationAddress common.Address
	integrationABI    abi.ABI
}

// NewV2V3Strategy 创建 V2/V3 套利策略
func NewV2V3Strategy(web3Client *web3.Client, integrationAddress common.Address) (*V2V3Strategy, error) {
	parsedABI, err := abi.JSON(strings.NewReader(UniswapV2V3IntegrationABI))
	if err != nil {
		return nil, fmt.Errorf("failed to parse V2V3 integration ABI: %w", err)
	}

	return &V2V3Strategy{
		web3Client:        web3Client,
		integrationAddress: integrationAddress,
		integrationABI:    parsedABI,
	}, nil
}

// FindOpportunities 寻找 V2/V3 套利机会
// 通过比较同一交易对在 V2 和 V3 不同费率池的价格
func (s *V2V3Strategy) FindOpportunities(
	ctx context.Context,
	tokenA, tokenB common.Address,
	amountIn *big.Int,
	minProfitRate float64, // 最小利润率 (%)
) ([]*V2V3ArbitrageOpportunity, error) {
	var opportunities []*V2V3ArbitrageOpportunity

	// 对每个 V3 费率等级检查套利机会
	feeTiers := []V3FeeTier{V3Fee005, V3Fee030, V3Fee100}

	for _, feeTier := range feeTiers {
		// 检查 V2 → V3 方向
		opp, err := s.checkOpportunity(ctx, DirectionV2ToV3, tokenA, tokenB, feeTier, amountIn, minProfitRate)
		if err == nil && opp != nil {
			opportunities = append(opportunities, opp)
		}

		// 检查 V3 → V2 方向
		opp, err = s.checkOpportunity(ctx, DirectionV3ToV2, tokenA, tokenB, feeTier, amountIn, minProfitRate)
		if err == nil && opp != nil {
			opportunities = append(opportunities, opp)
		}
	}

	return opportunities, nil
}

// checkOpportunity 检查单个套利机会
func (s *V2V3Strategy) checkOpportunity(
	ctx context.Context,
	direction ArbitrageDirection,
	tokenIn, tokenOut common.Address,
	feeTier V3FeeTier,
	amountIn *big.Int,
	minProfitRate float64,
) (*V2V3ArbitrageOpportunity, error) {
	// TODO: 实现价格查询和比较逻辑
	// 1. 查询 V2 价格：通过 UniswapV2Router.getAmountsOut
	// 2. 查询 V3 价格：通过 UniswapV3Quoter.quoteExactInputSingle
	// 3. 比较价差，判断是否有套利机会
	// 4. 计算预期利润

	// 目前返回 nil，实际实现需要根据具体报价逻辑
	return nil, nil
}

// ExecuteV2ToV3 执行 V2 → V3 套利
func (s *V2V3Strategy) ExecuteV2ToV3(
	ctx context.Context,
	privateKey string,
	params *V2V3ArbitrageParams,
) (*types.Transaction, error) {
	if params.Direction != DirectionV2ToV3 {
		return nil, fmt.Errorf("invalid direction for V2ToV3")
	}

	// 构建调用参数
	paramsStruct := struct {
		Recipient common.Address
		TokenIn   common.Address
		TokenOut  common.Address
		V3Fee     uint32
		AmountIn  *big.Int
		MinProfit *big.Int
	}{
		Recipient: params.Recipient,
		TokenIn:   params.TokenIn,
		TokenOut:  params.TokenOut,
		V3Fee:     uint32(params.V3Fee),
		AmountIn:  params.AmountIn,
		MinProfit: params.MinProfit,
	}

	callData, err := s.integrationABI.Pack("arbitrageV2ToV3", paramsStruct)
	if err != nil {
		return nil, fmt.Errorf("pack arbitrageV2ToV3 failed: %w", err)
	}

	return s.sendTransaction(ctx, privateKey, callData)
}

// ExecuteV3ToV2 执行 V3 → V2 套利
func (s *V2V3Strategy) ExecuteV3ToV2(
	ctx context.Context,
	privateKey string,
	params *V2V3ArbitrageParams,
) (*types.Transaction, error) {
	if params.Direction != DirectionV3ToV2 {
		return nil, fmt.Errorf("invalid direction for V3ToV2")
	}

	// 构建调用参数
	paramsStruct := struct {
		Recipient common.Address
		TokenIn   common.Address
		TokenOut  common.Address
		V3Fee     uint32
		AmountIn  *big.Int
		MinProfit *big.Int
	}{
		Recipient: params.Recipient,
		TokenIn:   params.TokenIn,
		TokenOut:  params.TokenOut,
		V3Fee:     uint32(params.V3Fee),
		AmountIn:  params.AmountIn,
		MinProfit: params.MinProfit,
	}

	callData, err := s.integrationABI.Pack("arbitrageV3ToV2", paramsStruct)
	if err != nil {
		return nil, fmt.Errorf("pack arbitrageV3ToV2 failed: %w", err)
	}

	return s.sendTransaction(ctx, privateKey, callData)
}

// Execute 执行 V2/V3 套利（自动判断方向）
func (s *V2V3Strategy) Execute(
	ctx context.Context,
	privateKey string,
	params *V2V3ArbitrageParams,
) (*types.Transaction, error) {
	switch params.Direction {
	case DirectionV2ToV3:
		return s.ExecuteV2ToV3(ctx, privateKey, params)
	case DirectionV3ToV2:
		return s.ExecuteV3ToV2(ctx, privateKey, params)
	default:
		return nil, fmt.Errorf("unknown direction: %d", params.Direction)
	}
}

// sendTransaction 发送交易
func (s *V2V3Strategy) sendTransaction(ctx context.Context, privateKeyHex string, data []byte) (*types.Transaction, error) {
	pk, err := crypto.HexToECDSA(strings.TrimPrefix(privateKeyHex, "0x"))
	if err != nil {
		return nil, fmt.Errorf("invalid private key: %w", err)
	}

	from := crypto.PubkeyToAddress(pk.PublicKey)
	client := s.web3Client.GetClient()

	nonce, err := client.PendingNonceAt(ctx, from)
	if err != nil {
		return nil, fmt.Errorf("get nonce failed: %w", err)
	}

	gasLimit, err := client.EstimateGas(ctx, ethereum.CallMsg{
		From: from,
		To:   &s.integrationAddress,
		Data: data,
	})
	if err != nil {
		gasLimit = 500000 // V2V3 套利需要更多 Gas
	}
	gasLimit = gasLimit * 130 / 100 // 加 30% buffer（跨协议操作更复杂）

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
		ChainID:   s.web3Client.GetChainID(),
		Nonce:     nonce,
		To:        &s.integrationAddress,
		Gas:       gasLimit,
		GasTipCap: tipCap,
		GasFeeCap: feeCap,
		Value:     big.NewInt(0),
		Data:      data,
	})

	signer := types.LatestSignerForChainID(s.web3Client.GetChainID())
	signedTx, err := types.SignTx(tx, signer, pk)
	if err != nil {
		return nil, fmt.Errorf("sign tx failed: %w", err)
	}

	if err := client.SendTransaction(ctx, signedTx); err != nil {
		return nil, fmt.Errorf("send tx failed: %w", err)
	}

	return signedTx, nil
}

// DirectionString 返回方向的字符串表示
func (d ArbitrageDirection) String() string {
	switch d {
	case DirectionV2ToV3:
		return "V2→V3"
	case DirectionV3ToV2:
		return "V3→V2"
	default:
		return "Unknown"
	}
}

// V3FeeString 返回费率的字符串表示
func (f V3FeeTier) String() string {
	switch f {
	case V3Fee005:
		return "0.05%"
	case V3Fee030:
		return "0.3%"
	case V3Fee100:
		return "1%"
	default:
		return fmt.Sprintf("%d bps", f)
	}
}
