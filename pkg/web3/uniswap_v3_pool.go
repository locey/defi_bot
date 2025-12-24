package web3

import (
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
)

// Uniswap V3 Pool ABI
const UniswapV3PoolABI = `[
	{
		"inputs": [],
		"name": "slot0",
		"outputs": [
			{"name": "sqrtPriceX96", "type": "uint160"},
			{"name": "tick", "type": "int24"},
			{"name": "observationIndex", "type": "uint16"},
			{"name": "observationCardinality", "type": "uint16"},
			{"name": "observationCardinalityNext", "type": "uint16"},
			{"name": "feeProtocol", "type": "uint8"},
			{"name": "unlocked", "type": "bool"}
		],
		"stateMutability": "view",
		"type": "function"
	},
	{
		"inputs": [],
		"name": "liquidity",
		"outputs": [{"name": "", "type": "uint128"}],
		"stateMutability": "view",
		"type": "function"
	},
	{
		"inputs": [],
		"name": "token0",
		"outputs": [{"name": "", "type": "address"}],
		"stateMutability": "view",
		"type": "function"
	},
	{
		"inputs": [],
		"name": "token1",
		"outputs": [{"name": "", "type": "address"}],
		"stateMutability": "view",
		"type": "function"
	}
]`

// Uniswap V3 Factory ABI
const UniswapV3FactoryABI = `[
	{
		"inputs": [
			{"name": "tokenA", "type": "address"},
			{"name": "tokenB", "type": "address"},
			{"name": "fee", "type": "uint24"}
		],
		"name": "getPool",
		"outputs": [{"name": "pool", "type": "address"}],
		"stateMutability": "view",
		"type": "function"
	}
]`

// V3Slot0 V3 Pool 的 slot0 返回值
type V3Slot0 struct {
	SqrtPriceX96 *big.Int
	Tick         int32
}

// GetV3PoolSlot0 获取 V3 Pool 的 slot0 数据
func (c *Client) GetV3PoolSlot0(poolAddress string) (*V3Slot0, error) {
	poolAddr := common.HexToAddress(poolAddress)

	// 解析 ABI
	parsedABI, err := abi.JSON(strings.NewReader(UniswapV3PoolABI))
	if err != nil {
		return nil, err
	}

	// 创建绑定
	contract := bind.NewBoundContract(poolAddr, parsedABI, c.client, nil, nil)

	// 调用 slot0
	var out []interface{}
	err = contract.Call(nil, &out, "slot0")
	if err != nil {
		return nil, err
	}

	// Tick 可能是 *big.Int，需要转换
	var tick int32
	switch v := out[1].(type) {
	case int32:
		tick = v
	case *big.Int:
		tick = int32(v.Int64())
	default:
		return nil, fmt.Errorf("unexpected tick type: %T", out[1])
	}

	return &V3Slot0{
		SqrtPriceX96: out[0].(*big.Int),
		Tick:         tick,
	}, nil
}

// GetV3PoolLiquidity 获取 V3 Pool 的流动性
func (c *Client) GetV3PoolLiquidity(poolAddress string) (*big.Int, error) {
	poolAddr := common.HexToAddress(poolAddress)

	// 解析 ABI
	parsedABI, err := abi.JSON(strings.NewReader(UniswapV3PoolABI))
	if err != nil {
		return nil, err
	}

	// 创建绑定
	contract := bind.NewBoundContract(poolAddr, parsedABI, c.client, nil, nil)

	// 调用 liquidity
	var out []interface{}
	err = contract.Call(nil, &out, "liquidity")
	if err != nil {
		return nil, err
	}

	return out[0].(*big.Int), nil
}

// GetV3Pool 从 V3 Factory 获取 Pool 地址
func (c *Client) GetV3Pool(factoryAddress, token0, token1 string, fee uint32) (string, error) {
	factoryAddr := common.HexToAddress(factoryAddress)
	token0Addr := common.HexToAddress(token0)
	token1Addr := common.HexToAddress(token1)

	// 解析 ABI
	parsedABI, err := abi.JSON(strings.NewReader(UniswapV3FactoryABI))
	if err != nil {
		return "", err
	}

	// 创建绑定
	contract := bind.NewBoundContract(factoryAddr, parsedABI, c.client, nil, nil)

	// 调用 getPool
	var out []interface{}
	err = contract.Call(nil, &out, "getPool", token0Addr, token1Addr, big.NewInt(int64(fee)))
	if err != nil {
		return "", err
	}

	poolAddr := out[0].(common.Address)

	// 检查是否为零地址（Pool不存在）
	if poolAddr == (common.Address{}) {
		return "", nil
	}

	return poolAddr.Hex(), nil
}

// GetV3PoolTokens 获取 V3 Pool 的代币地址
func (c *Client) GetV3PoolTokens(poolAddress string) (token0, token1 string, err error) {
	poolAddr := common.HexToAddress(poolAddress)

	// 解析 ABI
	parsedABI, err := abi.JSON(strings.NewReader(UniswapV3PoolABI))
	if err != nil {
		return "", "", err
	}

	// 创建绑定
	contract := bind.NewBoundContract(poolAddr, parsedABI, c.client, nil, nil)

	// 调用 token0
	var out0 []interface{}
	err = contract.Call(nil, &out0, "token0")
	if err != nil {
		return "", "", err
	}

	// 调用 token1
	var out1 []interface{}
	err = contract.Call(nil, &out1, "token1")
	if err != nil {
		return "", "", err
	}

	return out0[0].(common.Address).Hex(), out1[0].(common.Address).Hex(), nil
}
