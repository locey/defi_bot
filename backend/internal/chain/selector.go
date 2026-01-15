// Package chain 提供多链管理功能
package chain

import (
	"context"
	"fmt"
	"sync"

	"github.com/defi-bot/backend/internal/config"
	"github.com/defi-bot/backend/pkg/web3"
	"github.com/ethereum/go-ethereum/ethclient"
)

// Selector 链选择器，管理多链配置和客户端
type Selector struct {
	mu          sync.RWMutex
	config      *config.Config
	activeChain string
	clients     map[string]*web3.Client    // Web3 客户端缓存
	wsClients   map[string]*ethclient.Client // WebSocket 客户端缓存
}

// NewSelector 创建链选择器
func NewSelector(cfg *config.Config) *Selector {
	activeChain := cfg.ActiveChain
	if activeChain == "" {
		// 向后兼容：如果没有配置多链，使用 "default" 表示单链模式
		activeChain = "default"
	}

	return &Selector{
		config:      cfg,
		activeChain: activeChain,
		clients:     make(map[string]*web3.Client),
		wsClients:   make(map[string]*ethclient.Client),
	}
}

// GetActiveChain 获取当前活跃链名称
func (s *Selector) GetActiveChain() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.activeChain
}

// SetActiveChain 切换活跃链
func (s *Selector) SetActiveChain(chainName string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 检查链是否已配置
	if _, ok := s.config.Chains[chainName]; !ok {
		return fmt.Errorf("链 %s 未配置", chainName)
	}

	s.activeChain = chainName
	s.config.ActiveChain = chainName
	return nil
}

// GetClient 获取当前活跃链的 Web3 客户端
func (s *Selector) GetClient() (*web3.Client, error) {
	return s.GetClientForChain(s.GetActiveChain())
}

// GetClientForChain 获取指定链的 Web3 客户端
func (s *Selector) GetClientForChain(chainName string) (*web3.Client, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 检查缓存
	if client, ok := s.clients[chainName]; ok {
		return client, nil
	}

	// 获取链配置
	chainCfg, err := s.getChainConfig(chainName)
	if err != nil {
		return nil, err
	}

	// 创建客户端
	timeout := chainCfg.Timeout
	if timeout == 0 {
		timeout = 30
	}

	client, err := web3.NewClient(chainCfg.RPCURL, chainCfg.ChainID, timeout)
	if err != nil {
		return nil, fmt.Errorf("创建 %s 客户端失败: %w", chainName, err)
	}

	s.clients[chainName] = client
	return client, nil
}

// GetWSClient 获取当前活跃链的 WebSocket 客户端
func (s *Selector) GetWSClient(ctx context.Context) (*ethclient.Client, error) {
	return s.GetWSClientForChain(ctx, s.GetActiveChain())
}

// GetWSClientForChain 获取指定链的 WebSocket 客户端
func (s *Selector) GetWSClientForChain(ctx context.Context, chainName string) (*ethclient.Client, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// 检查缓存
	if client, ok := s.wsClients[chainName]; ok {
		return client, nil
	}

	// 获取链配置
	chainCfg, err := s.getChainConfig(chainName)
	if err != nil {
		return nil, err
	}

	if chainCfg.WSURL == "" {
		return nil, fmt.Errorf("链 %s 未配置 WebSocket URL", chainName)
	}

	// 创建 WebSocket 客户端
	client, err := ethclient.DialContext(ctx, chainCfg.WSURL)
	if err != nil {
		return nil, fmt.Errorf("连接 %s WebSocket 失败: %w", chainName, err)
	}

	s.wsClients[chainName] = client
	return client, nil
}

// GetChainConfig 获取当前活跃链的配置
func (s *Selector) GetChainConfig() (*config.ChainConfig, error) {
	return s.GetChainConfigByName(s.GetActiveChain())
}

// GetChainConfigByName 获取指定链的配置
func (s *Selector) GetChainConfigByName(chainName string) (*config.ChainConfig, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.getChainConfig(chainName)
}

// getChainConfig 内部方法，获取链配置（不加锁）
func (s *Selector) getChainConfig(chainName string) (*config.ChainConfig, error) {
	// 单链模式
	if chainName == "default" || len(s.config.Chains) == 0 {
		return &config.ChainConfig{
			Name:      "Default",
			ChainID:   s.config.Blockchain.ChainID,
			RPCURL:    s.config.Blockchain.RPCURL,
			RPCURLs:   s.config.Blockchain.RPCURLs,
			WSURL:     s.config.Blockchain.WSURL,
			Timeout:   s.config.Blockchain.Timeout,
			Dexes:     s.config.Dexes,
			Tokens:    s.config.Tokens,
		}, nil
	}

	// 多链模式
	chainCfg, ok := s.config.Chains[chainName]
	if !ok {
		return nil, fmt.Errorf("链 %s 未配置", chainName)
	}

	return &chainCfg, nil
}

// GetAllChainNames 获取所有已配置的链名称
func (s *Selector) GetAllChainNames() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(s.config.Chains) == 0 {
		return []string{"default"}
	}

	names := make([]string, 0, len(s.config.Chains))
	for name := range s.config.Chains {
		names = append(names, name)
	}
	return names
}

// GetDexes 获取当前活跃链的 DEX 配置
func (s *Selector) GetDexes() []config.DexConfig {
	chainCfg, err := s.GetChainConfig()
	if err != nil {
		return s.config.Dexes
	}
	return chainCfg.Dexes
}

// GetTokens 获取当前活跃链的代币配置
func (s *Selector) GetTokens() []config.TokenConfig {
	chainCfg, err := s.GetChainConfig()
	if err != nil {
		return s.config.Tokens
	}
	return chainCfg.Tokens
}

// GetChainID 获取当前活跃链的 Chain ID
func (s *Selector) GetChainID() int64 {
	chainCfg, err := s.GetChainConfig()
	if err != nil {
		return s.config.Blockchain.ChainID
	}
	return chainCfg.ChainID
}

// IsL2 当前活跃链是否为 L2
func (s *Selector) IsL2() bool {
	chainCfg, err := s.GetChainConfig()
	if err != nil {
		return false
	}
	return chainCfg.IsL2
}

// GetBlockTime 获取当前活跃链的区块时间（秒）
func (s *Selector) GetBlockTime() float64 {
	chainCfg, err := s.GetChainConfig()
	if err != nil {
		return 12.0 // 默认以太坊主网
	}
	if chainCfg.BlockTime == 0 {
		return 12.0
	}
	return chainCfg.BlockTime
}

// Close 关闭所有客户端连接
func (s *Selector) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, client := range s.clients {
		client.Close()
	}
	for _, client := range s.wsClients {
		client.Close()
	}

	s.clients = make(map[string]*web3.Client)
	s.wsClients = make(map[string]*ethclient.Client)
}
