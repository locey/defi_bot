package web3

import (
	"context"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
)

// UniswapV2Factory ABI（简化版，只包含 getPair 方法）
const UniswapV2FactoryABI = `[
	{
		"constant": true,
		"inputs": [
			{"name": "tokenA", "type": "address"},
			{"name": "tokenB", "type": "address"}
		],
		"name": "getPair",
		"outputs": [{"name": "pair", "type": "address"}],
		"stateMutability": "view",
		"type": "function"
	}
]`

// UniswapV2Pair ABI（简化版，只包含需要的方法）
const UniswapV2PairABI = `[
	{
		"constant": true,
		"inputs": [],
		"name": "getReserves",
		"outputs": [
			{"name": "reserve0", "type": "uint112"},
			{"name": "reserve1", "type": "uint112"},
			{"name": "blockTimestampLast", "type": "uint32"}
		],
		"stateMutability": "view",
		"type": "function"
	},
	{
		"constant": true,
		"inputs": [],
		"name": "token0",
		"outputs": [{"name": "", "type": "address"}],
		"stateMutability": "view",
		"type": "function"
	},
	{
		"constant": true,
		"inputs": [],
		"name": "token1",
		"outputs": [{"name": "", "type": "address"}],
		"stateMutability": "view",
		"type": "function"
	}
]`

// GetPairFromFactory 从 Factory 合约获取交易对地址
func (c *Client) GetPairFromFactory(factoryAddress, token0Address, token1Address string) (string, error) {
	// 解析 ABI
	parsedABI, err := abi.JSON(strings.NewReader(UniswapV2FactoryABI))
	if err != nil {
		return "", fmt.Errorf("解析 Factory ABI 失败: %w", err)
	}

	// 准备调用参数
	token0 := common.HexToAddress(token0Address)
	token1 := common.HexToAddress(token1Address)

	// 打包调用数据
	data, err := parsedABI.Pack("getPair", token0, token1)
	if err != nil {
		return "", fmt.Errorf("打包调用数据失败: %w", err)
	}

	// 调用合约
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()

	msg := ethereum.CallMsg{
		To:   &[]common.Address{common.HexToAddress(factoryAddress)}[0],
		Data: data,
	}

	result, err := c.client.CallContract(ctx, msg, nil)
	if err != nil {
		return "", fmt.Errorf("调用 Factory.getPair 失败: %w", err)
	}

	// 解析返回值
	var pairAddress common.Address
	err = parsedABI.UnpackIntoInterface(&pairAddress, "getPair", result)
	if err != nil {
		return "", fmt.Errorf("解析返回值失败: %w", err)
	}

	// 检查是否为零地址
	if pairAddress == (common.Address{}) {
		return "", nil // 交易对不存在
	}

	return pairAddress.Hex(), nil
}

// PairReserves 交易对储备量结构
type PairReserves struct {
	Reserve0           *big.Int
	Reserve1           *big.Int
	BlockTimestampLast uint32
}

// GetPairReservesFromContract 从 Pair 合约获取储备量
func (c *Client) GetPairReservesFromContract(pairAddress string) (*PairReserves, error) {
	// 解析 ABI
	parsedABI, err := abi.JSON(strings.NewReader(UniswapV2PairABI))
	if err != nil {
		return nil, fmt.Errorf("解析 Pair ABI 失败: %w", err)
	}

	// 打包调用数据
	data, err := parsedABI.Pack("getReserves")
	if err != nil {
		return nil, fmt.Errorf("打包调用数据失败: %w", err)
	}

	// 调用合约
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()

	msg := ethereum.CallMsg{
		To:   &[]common.Address{common.HexToAddress(pairAddress)}[0],
		Data: data,
	}

	result, err := c.client.CallContract(ctx, msg, nil)
	if err != nil {
		return nil, fmt.Errorf("调用 Pair.getReserves 失败: %w", err)
	}

	// 解析返回值
	var reserves struct {
		Reserve0           *big.Int
		Reserve1           *big.Int
		BlockTimestampLast uint32
	}

	err = parsedABI.UnpackIntoInterface(&reserves, "getReserves", result)
	if err != nil {
		return nil, fmt.Errorf("解析储备量失败: %w", err)
	}

	return &PairReserves{
		Reserve0:           reserves.Reserve0,
		Reserve1:           reserves.Reserve1,
		BlockTimestampLast: reserves.BlockTimestampLast,
	}, nil
}

// GetTokenFromPair 从 Pair 合约获取 token0 或 token1 地址
func (c *Client) GetTokenFromPair(pairAddress string, tokenIndex int) (string, error) {
	// 解析 ABI
	parsedABI, err := abi.JSON(strings.NewReader(UniswapV2PairABI))
	if err != nil {
		return "", fmt.Errorf("解析 Pair ABI 失败: %w", err)
	}

	// 确定要调用的方法
	method := "token0"
	if tokenIndex == 1 {
		method = "token1"
	}

	// 打包调用数据
	data, err := parsedABI.Pack(method)
	if err != nil {
		return "", fmt.Errorf("打包调用数据失败: %w", err)
	}

	// 调用合约
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()

	msg := ethereum.CallMsg{
		To:   &[]common.Address{common.HexToAddress(pairAddress)}[0],
		Data: data,
	}

	result, err := c.client.CallContract(ctx, msg, nil)
	if err != nil {
		return "", fmt.Errorf("调用 Pair.%s 失败: %w", method, err)
	}

	// 解析返回值
	var tokenAddress common.Address
	err = parsedABI.UnpackIntoInterface(&tokenAddress, method, result)
	if err != nil {
		return "", fmt.Errorf("解析代币地址失败: %w", err)
	}

	return tokenAddress.Hex(), nil
}
