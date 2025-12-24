package web3

import (
	"context"
	"fmt"
	"log"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
)

// Client Web3 客户端
type Client struct {
	client  *ethclient.Client
	chainID *big.Int
	timeout time.Duration
}

// NewClient 创建新的 Web3 客户端
func NewClient(rpcURL string, chainID int64, timeout int) (*Client, error) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeout)*time.Second)
	defer cancel()

	client, err := ethclient.DialContext(ctx, rpcURL)
	if err != nil {
		return nil, fmt.Errorf("连接 RPC 失败: %w", err)
	}

	// 验证连接
	_, err = client.ChainID(ctx)
	if err != nil {
		return nil, fmt.Errorf("获取 ChainID 失败: %w", err)
	}

	log.Printf("Web3 客户端连接成功: %s (ChainID: %d)", rpcURL, chainID)

	return &Client{
		client:  client,
		chainID: big.NewInt(chainID),
		timeout: time.Duration(timeout) * time.Second,
	}, nil
}

// GetClient 获取原始客户端
func (c *Client) GetClient() *ethclient.Client {
	return c.client
}

// GetChainID 获取链 ID
func (c *Client) GetChainID() *big.Int {
	return c.chainID
}

// GetBlockNumber 获取当前区块号
func (c *Client) GetBlockNumber() (uint64, error) {
	ctx, cancel := context.WithTimeout(context.Background(), c.timeout)
	defer cancel()

	blockNumber, err := c.client.BlockNumber(ctx)
	if err != nil {
		return 0, fmt.Errorf("获取区块号失败: %w", err)
	}

	return blockNumber, nil
}

// GetCallOpts 获取调用选项
func (c *Client) GetCallOpts() *bind.CallOpts {
	return &bind.CallOpts{
		Context: context.Background(),
	}
}

// Close 关闭客户端
func (c *Client) Close() {
	if c.client != nil {
		c.client.Close()
	}
}

// IsValidAddress 检查地址是否有效
func IsValidAddress(address string) bool {
	return common.IsHexAddress(address)
}

// ToAddress 转换为 common.Address
func ToAddress(address string) common.Address {
	return common.HexToAddress(address)
}
