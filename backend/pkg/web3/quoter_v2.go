package web3

import (
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
)

// QuoterV2 ABI（精简版，只包含常用方法）
const QuoterV2ABI = `[
	{
		"inputs": [
			{
				"components": [
					{"name": "tokenIn", "type": "address"},
					{"name": "tokenOut", "type": "address"},
					{"name": "amountIn", "type": "uint256"},
					{"name": "fee", "type": "uint24"},
					{"name": "sqrtPriceLimitX96", "type": "uint160"}
				],
				"name": "params",
				"type": "tuple"
			}
		],
		"name": "quoteExactInputSingle",
		"outputs": [
			{"name": "amountOut", "type": "uint256"},
			{"name": "sqrtPriceX96After", "type": "uint160"},
			{"name": "initializedTicksCrossed", "type": "uint32"},
			{"name": "gasEstimate", "type": "uint256"}
		],
		"stateMutability": "nonpayable",
		"type": "function"
	}
]`

// QuoteResult Quoter 查询结果
type QuoteResult struct {
	AmountOut               *big.Int
	SqrtPriceX96After       *big.Int
	InitializedTicksCrossed uint32
	GasEstimate             uint64
}

// QuoteExactInputSingle 使用 QuoterV2 模拟单跳交换
// 这是业界标准的V3深度查询方法
func (c *Client) QuoteExactInputSingle(
	quoterAddress string,
	tokenIn string,
	tokenOut string,
	amountIn *big.Int,
	fee uint32,
) (*QuoteResult, error) {
	quoterAddr := common.HexToAddress(quoterAddress)
	tokenInAddr := common.HexToAddress(tokenIn)
	tokenOutAddr := common.HexToAddress(tokenOut)

	// 解析 ABI
	parsedABI, err := abi.JSON(strings.NewReader(QuoterV2ABI))
	if err != nil {
		return nil, err
	}

	// 创建绑定
	contract := bind.NewBoundContract(quoterAddr, parsedABI, c.client, nil, nil)

	// 构造参数（使用 struct）
	params := struct {
		TokenIn           common.Address
		TokenOut          common.Address
		AmountIn          *big.Int
		Fee               *big.Int
		SqrtPriceLimitX96 *big.Int
	}{
		TokenIn:           tokenInAddr,
		TokenOut:          tokenOutAddr,
		AmountIn:          amountIn,
		Fee:               big.NewInt(int64(fee)),
		SqrtPriceLimitX96: big.NewInt(0), // 0 = 不限制价格
	}

	// 调用 quoteExactInputSingle
	var out []interface{}
	err = contract.Call(nil, &out, "quoteExactInputSingle", params)
	if err != nil {
		return nil, err
	}

	return &QuoteResult{
		AmountOut:               out[0].(*big.Int),
		SqrtPriceX96After:       out[1].(*big.Int),
		InitializedTicksCrossed: out[2].(uint32),
		GasEstimate:             out[3].(*big.Int).Uint64(),
	}, nil
}

// BatchQuote 批量查询多个金额的输出（用于深度采集）
// 这是业界推荐的深度数据采集方式
func (c *Client) BatchQuote(
	quoterAddress string,
	tokenIn string,
	tokenOut string,
	fee uint32,
	amounts []*big.Int,
) ([]*QuoteResult, error) {
	results := make([]*QuoteResult, 0, len(amounts))

	for _, amount := range amounts {
		result, err := c.QuoteExactInputSingle(quoterAddress, tokenIn, tokenOut, amount, fee)
		if err != nil {
			// 跳过失败的查询（可能金额过大）
			continue
		}
		results = append(results, result)
	}

	return results, nil
}

// CalculatePriceImpact 计算价格影响（滑点）
func (c *Client) CalculatePriceImpact(
	sqrtPriceBefore *big.Int,
	sqrtPriceAfter *big.Int,
) float64 {
	if sqrtPriceBefore.Sign() == 0 {
		return 0
	}

	// priceImpact = |priceAfter - priceBefore| / priceBefore * 100
	priceBefore := new(big.Float).SetInt(sqrtPriceBefore)
	priceAfter := new(big.Float).SetInt(sqrtPriceAfter)

	diff := new(big.Float).Sub(priceAfter, priceBefore)
	diff = diff.Abs(diff)

	impact := new(big.Float).Quo(diff, priceBefore)
	impact.Mul(impact, big.NewFloat(100))

	result, _ := impact.Float64()
	return result
}
