// Package executor 提供交易执行相关功能
package executor

import (
	"context"
	"sync"

	"github.com/defi-bot/backend/pkg/log"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
)

// NonceTracker Nonce 追踪器
// 维护每个地址的 Nonce 状态，避免 Nonce 冲突
type NonceTracker struct {
	mu     sync.Mutex
	nonces map[common.Address]uint64
	inited map[common.Address]bool
}

// NewNonceTracker 创建 Nonce 追踪器
func NewNonceTracker() *NonceTracker {
	return &NonceTracker{
		nonces: make(map[common.Address]uint64),
		inited: make(map[common.Address]bool),
	}
}

// InitIfNeeded 如果需要则初始化 Nonce
func (n *NonceTracker) InitIfNeeded(ctx context.Context, client *ethclient.Client, address common.Address) error {
	n.mu.Lock()
	defer n.mu.Unlock()

	if n.inited[address] {
		return nil
	}

	nonce, err := client.PendingNonceAt(ctx, address)
	if err != nil {
		return err
	}

	n.nonces[address] = nonce
	n.inited[address] = true
	log.Info("初始化 Nonce: %s = %d", address.Hex(), nonce)

	return nil
}

// GetAndIncrement 获取当前 Nonce 并自增
func (n *NonceTracker) GetAndIncrement(address common.Address) uint64 {
	n.mu.Lock()
	defer n.mu.Unlock()

	nonce := n.nonces[address]
	n.nonces[address]++
	return nonce
}

// Reserve 预留 Nonce（不自增），配合 Confirm 使用
// 用于"发送成功后再递增"的安全模式
func (n *NonceTracker) Reserve(address common.Address) uint64 {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.nonces[address]
}

// Confirm 确认 Nonce 已使用（发送成功后调用），递增到下一个
func (n *NonceTracker) Confirm(address common.Address, usedNonce uint64) {
	n.mu.Lock()
	defer n.mu.Unlock()
	// 只在 usedNonce 匹配当前值时递增（防止并发错乱）
	if n.nonces[address] == usedNonce {
		n.nonces[address]++
	}
}

// Get 获取当前 Nonce（不自增）
func (n *NonceTracker) Get(address common.Address) uint64 {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.nonces[address]
}

// Set 设置 Nonce
func (n *NonceTracker) Set(address common.Address, nonce uint64) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.nonces[address] = nonce
	n.inited[address] = true
}

// Refresh 从链上刷新 Nonce
func (n *NonceTracker) Refresh(ctx context.Context, client *ethclient.Client, address common.Address) error {
	nonce, err := client.PendingNonceAt(ctx, address)
	if err != nil {
		return err
	}

	n.mu.Lock()
	defer n.mu.Unlock()

	oldNonce := n.nonces[address]
	n.nonces[address] = nonce
	n.inited[address] = true

	if oldNonce != nonce {
		log.Info("刷新 Nonce: %s, %d -> %d", address.Hex(), oldNonce, nonce)
	}

	return nil
}

// Decrement 回退 Nonce（交易失败时使用）
func (n *NonceTracker) Decrement(address common.Address) {
	n.mu.Lock()
	defer n.mu.Unlock()

	if n.nonces[address] > 0 {
		n.nonces[address]--
	}
}

// Reset 重置指定地址的 Nonce 状态
func (n *NonceTracker) Reset(address common.Address) {
	n.mu.Lock()
	defer n.mu.Unlock()

	delete(n.nonces, address)
	delete(n.inited, address)
}

// ResetAll 重置所有 Nonce 状态
func (n *NonceTracker) ResetAll() {
	n.mu.Lock()
	defer n.mu.Unlock()

	n.nonces = make(map[common.Address]uint64)
	n.inited = make(map[common.Address]bool)
}
