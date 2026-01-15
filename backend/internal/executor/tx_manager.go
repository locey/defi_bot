// Package executor 提供交易执行相关功能
package executor

import (
	"context"
	"errors"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"time"

	"github.com/defi-bot/backend/pkg/log"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
)

// TxManagerConfig 交易管理器配置
type TxManagerConfig struct {
	// 重试配置
	MaxRetries    int           // 最大重试次数
	InitialDelay  time.Duration // 初始重试延迟
	MaxDelay      time.Duration // 最大重试延迟
	BackoffFactor float64       // 退避因子

	// Gas 配置
	GasBoostPercent int   // Gas 价格提升百分比（每次重试）
	MaxGasPrice     int64 // 最大 Gas 价格（Gwei）

	// 超时配置
	TxTimeout time.Duration // 交易确认超时
}

// DefaultTxManagerConfig 默认配置
func DefaultTxManagerConfig() *TxManagerConfig {
	return &TxManagerConfig{
		MaxRetries:      3,
		InitialDelay:    time.Second,
		MaxDelay:        30 * time.Second,
		BackoffFactor:   2.0,
		GasBoostPercent: 10,
		MaxGasPrice:     500, // 500 Gwei
		TxTimeout:       2 * time.Minute,
	}
}

// TxManager 交易管理器
// 提供 Nonce 管理、重试机制、Gas 动态调整等功能
type TxManager struct {
	client       *ethclient.Client
	chainID      *big.Int
	config       *TxManagerConfig
	nonceTracker *NonceTracker
	gasPricer    *GasPricer

	// 待处理交易
	pendingTxs sync.Map // map[string]*PendingTx
}

// PendingTx 待处理交易
type PendingTx struct {
	Hash      common.Hash
	Nonce     uint64
	GasPrice  *big.Int
	Timestamp time.Time
	Retries   int
}

// NewTxManager 创建交易管理器
func NewTxManager(client *ethclient.Client, chainID *big.Int, config *TxManagerConfig) *TxManager {
	if config == nil {
		config = DefaultTxManagerConfig()
	}

	return &TxManager{
		client:       client,
		chainID:      chainID,
		config:       config,
		nonceTracker: NewNonceTracker(),
		gasPricer:    NewGasPricer(client, config.MaxGasPrice),
	}
}

// SendTransaction 发送交易（带重试）
func (m *TxManager) SendTransaction(
	ctx context.Context,
	privateKey string,
	to common.Address,
	value *big.Int,
	data []byte,
	gasLimit uint64,
) (*types.Receipt, error) {
	// 解析私钥
	pk, err := crypto.HexToECDSA(strings.TrimPrefix(privateKey, "0x"))
	if err != nil {
		return nil, fmt.Errorf("无效的私钥: %w", err)
	}
	from := crypto.PubkeyToAddress(pk.PublicKey)

	// 初始化 Nonce（如果需要）
	if err := m.nonceTracker.InitIfNeeded(ctx, m.client, from); err != nil {
		return nil, fmt.Errorf("初始化 Nonce 失败: %w", err)
	}

	var lastErr error
	delay := m.config.InitialDelay

	for attempt := 0; attempt <= m.config.MaxRetries; attempt++ {
		if attempt > 0 {
			log.Info("交易重试 #%d, 延迟 %v", attempt, delay)
			time.Sleep(delay)
			delay = m.calculateNextDelay(delay)
		}

		// 获取 Nonce
		nonce := m.nonceTracker.GetAndIncrement(from)

		// 获取 Gas 价格（每次重试提升）
		gasPrice, err := m.gasPricer.GetGasPrice(ctx, attempt)
		if err != nil {
			lastErr = fmt.Errorf("获取 Gas 价格失败: %w", err)
			continue
		}

		// 构建交易
		tx := types.NewTransaction(nonce, to, value, gasLimit, gasPrice, data)

		// 签名
		signedTx, err := types.SignTx(tx, types.NewEIP155Signer(m.chainID), pk)
		if err != nil {
			lastErr = fmt.Errorf("签名交易失败: %w", err)
			continue
		}

		// 发送交易
		err = m.client.SendTransaction(ctx, signedTx)
		if err != nil {
			lastErr = err
			log.Warn("发送交易失败 (attempt %d): %v", attempt+1, err)

			// 根据错误类型处理
			if m.isNonceError(err) {
				log.Info("Nonce 错误，刷新 Nonce...")
				m.nonceTracker.Refresh(ctx, m.client, from)
				continue
			}

			if m.isUnderpricedError(err) {
				log.Info("Gas 价格过低，下次重试将提升 Gas...")
				continue
			}

			if m.isNonRetryableError(err) {
				return nil, err
			}

			continue
		}

		// 记录待处理交易
		m.pendingTxs.Store(signedTx.Hash().Hex(), &PendingTx{
			Hash:      signedTx.Hash(),
			Nonce:     nonce,
			GasPrice:  gasPrice,
			Timestamp: time.Now(),
			Retries:   attempt,
		})

		// 等待交易确认
		receipt, err := m.waitForReceipt(ctx, signedTx.Hash())
		if err != nil {
			lastErr = err
			m.pendingTxs.Delete(signedTx.Hash().Hex())
			continue
		}

		// 交易成功
		m.pendingTxs.Delete(signedTx.Hash().Hex())

		if receipt.Status == types.ReceiptStatusFailed {
			return receipt, fmt.Errorf("交易执行失败 (reverted)")
		}

		return receipt, nil
	}

	return nil, fmt.Errorf("交易失败，已重试 %d 次: %w", m.config.MaxRetries, lastErr)
}

// SendTransactionEIP1559 发送 EIP-1559 交易（带重试）
func (m *TxManager) SendTransactionEIP1559(
	ctx context.Context,
	privateKey string,
	to common.Address,
	value *big.Int,
	data []byte,
	gasLimit uint64,
) (*types.Receipt, error) {
	// 解析私钥
	pk, err := crypto.HexToECDSA(strings.TrimPrefix(privateKey, "0x"))
	if err != nil {
		return nil, fmt.Errorf("无效的私钥: %w", err)
	}
	from := crypto.PubkeyToAddress(pk.PublicKey)

	// 初始化 Nonce
	if err := m.nonceTracker.InitIfNeeded(ctx, m.client, from); err != nil {
		return nil, fmt.Errorf("初始化 Nonce 失败: %w", err)
	}

	var lastErr error
	delay := m.config.InitialDelay

	for attempt := 0; attempt <= m.config.MaxRetries; attempt++ {
		if attempt > 0 {
			log.Info("交易重试 #%d, 延迟 %v", attempt, delay)
			time.Sleep(delay)
			delay = m.calculateNextDelay(delay)
		}

		// 获取 Nonce
		nonce := m.nonceTracker.GetAndIncrement(from)

		// 获取 EIP-1559 Gas 参数
		gasTipCap, gasFeeCap, err := m.gasPricer.GetEIP1559GasPrice(ctx, attempt)
		if err != nil {
			// 回退到 Legacy 模式
			return m.SendTransaction(ctx, privateKey, to, value, data, gasLimit)
		}

		// 构建 EIP-1559 交易
		tx := types.NewTx(&types.DynamicFeeTx{
			ChainID:   m.chainID,
			Nonce:     nonce,
			GasTipCap: gasTipCap,
			GasFeeCap: gasFeeCap,
			Gas:       gasLimit,
			To:        &to,
			Value:     value,
			Data:      data,
		})

		// 签名
		signedTx, err := types.SignTx(tx, types.NewLondonSigner(m.chainID), pk)
		if err != nil {
			lastErr = fmt.Errorf("签名交易失败: %w", err)
			continue
		}

		// 发送交易
		err = m.client.SendTransaction(ctx, signedTx)
		if err != nil {
			lastErr = err
			log.Warn("发送 EIP-1559 交易失败 (attempt %d): %v", attempt+1, err)

			if m.isNonceError(err) {
				m.nonceTracker.Refresh(ctx, m.client, from)
				continue
			}

			if m.isNonRetryableError(err) {
				return nil, err
			}

			continue
		}

		// 等待确认
		receipt, err := m.waitForReceipt(ctx, signedTx.Hash())
		if err != nil {
			lastErr = err
			continue
		}

		if receipt.Status == types.ReceiptStatusFailed {
			return receipt, fmt.Errorf("交易执行失败 (reverted)")
		}

		return receipt, nil
	}

	return nil, fmt.Errorf("交易失败，已重试 %d 次: %w", m.config.MaxRetries, lastErr)
}

// waitForReceipt 等待交易确认
func (m *TxManager) waitForReceipt(ctx context.Context, txHash common.Hash) (*types.Receipt, error) {
	timeout := time.After(m.config.TxTimeout)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timeout:
			return nil, errors.New("交易确认超时")
		case <-ticker.C:
			receipt, err := m.client.TransactionReceipt(ctx, txHash)
			if err == nil {
				return receipt, nil
			}
			if !errors.Is(err, ethereum.NotFound) {
				log.Warn("获取交易收据失败: %v", err)
			}
		}
	}
}

// calculateNextDelay 计算下次重试延迟
func (m *TxManager) calculateNextDelay(current time.Duration) time.Duration {
	next := time.Duration(float64(current) * m.config.BackoffFactor)
	if next > m.config.MaxDelay {
		return m.config.MaxDelay
	}
	return next
}

// isNonceError 判断是否为 Nonce 错误
func (m *TxManager) isNonceError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "nonce") ||
		strings.Contains(msg, "replacement transaction underpriced")
}

// isUnderpricedError 判断是否为 Gas 价格过低错误
func (m *TxManager) isUnderpricedError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "underpriced") ||
		strings.Contains(msg, "gas price") ||
		strings.Contains(msg, "max fee per gas")
}

// isNonRetryableError 判断是否为不可重试错误
func (m *TxManager) isNonRetryableError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "insufficient funds") ||
		strings.Contains(msg, "execution reverted") ||
		strings.Contains(msg, "invalid sender")
}

// GetPendingTxCount 获取待处理交易数量
func (m *TxManager) GetPendingTxCount() int {
	count := 0
	m.pendingTxs.Range(func(_, _ interface{}) bool {
		count++
		return true
	})
	return count
}
