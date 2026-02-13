// internal/executor/private_tx.go
// Phase 1.4: Flashbots Protect / 私有交易提交
// 通过私有 RPC 通道提交交易，防止被 MEV 搜索者抢跑（front-run）
package executor

import (
	"context"
	"fmt"
	"math/big"
	"strings"

	"github.com/defi-bot/backend/pkg/log"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
)

// PrivateTxConfig 私有交易配置
type PrivateTxConfig struct {
	// Flashbots Protect RPC（Ethereum mainnet）
	FlashbotsRPC string // 默认: https://rpc.flashbots.net

	// Arbitrum 私有提交（直连 Sequencer）
	ArbitrumSequencerRPC string // 默认使用标准 RPC（Arbitrum 有 FCFS 排序）

	// 链 ID（决定使用哪个私有通道）
	ChainID int64

	// 是否启用私有提交
	Enabled bool
}

// PrivateTxSender 私有交易发送器
type PrivateTxSender struct {
	config       *PrivateTxConfig
	privateClient *ethclient.Client
}

// NewPrivateTxSender 创建私有交易发送器
func NewPrivateTxSender(config *PrivateTxConfig) (*PrivateTxSender, error) {
	if config == nil {
		config = DefaultPrivateTxConfig()
	}

	sender := &PrivateTxSender{
		config: config,
	}

	if config.Enabled {
		rpcURL := sender.getPrivateRPC()
		if rpcURL != "" {
			client, err := ethclient.Dial(rpcURL)
			if err != nil {
				log.Executor().Warn().Err(err).Str("rpc", rpcURL).
					Msg("Failed to connect to private RPC, will use public RPC")
			} else {
				sender.privateClient = client
				log.Executor().Info().Str("rpc", rpcURL).
					Msg("Connected to private transaction RPC")
			}
		}
	}

	return sender, nil
}

// DefaultPrivateTxConfig 默认配置
func DefaultPrivateTxConfig() *PrivateTxConfig {
	return &PrivateTxConfig{
		FlashbotsRPC:         "https://rpc.flashbots.net",
		ArbitrumSequencerRPC: "", // 使用标准 RPC
		ChainID:              42161, // Arbitrum
		Enabled:              true,
	}
}

// getPrivateRPC 根据链 ID 获取对应的私有 RPC
func (s *PrivateTxSender) getPrivateRPC() string {
	switch s.config.ChainID {
	case 1: // Ethereum mainnet
		return s.config.FlashbotsRPC
	case 42161: // Arbitrum One
		// Arbitrum 使用 FCFS（先到先服务）排序，不需要 Flashbots
		// 但可以使用专用的低延迟 Sequencer 端点
		if s.config.ArbitrumSequencerRPC != "" {
			return s.config.ArbitrumSequencerRPC
		}
		return "" // 使用标准 RPC
	case 10: // Optimism
		return "" // Optimism 同样使用 FCFS
	default:
		return ""
	}
}

// SendTransaction 通过私有通道发送交易
// 如果私有通道不可用，回退到传统方式
func (s *PrivateTxSender) SendTransaction(
	ctx context.Context,
	signedTx *types.Transaction,
	fallbackClient *ethclient.Client,
) error {
	if s.privateClient != nil {
		err := s.privateClient.SendTransaction(ctx, signedTx)
		if err == nil {
			log.Executor().Info().
				Str("tx_hash", signedTx.Hash().Hex()).
				Msg("Transaction sent via private RPC")
			return nil
		}
		log.Executor().Warn().Err(err).
			Msg("Private RPC send failed, falling back to public RPC")
	}

	// 回退到公共 RPC
	if fallbackClient != nil {
		return fallbackClient.SendTransaction(ctx, signedTx)
	}
	return fmt.Errorf("no available RPC to send transaction")
}

// SignAndSendPrivate 签名并通过私有通道发送交易
func (s *PrivateTxSender) SignAndSendPrivate(
	ctx context.Context,
	tx *types.Transaction,
	privateKey string,
	chainID *big.Int,
	fallbackClient *ethclient.Client,
) (*types.Transaction, error) {
	pk, err := crypto.HexToECDSA(strings.TrimPrefix(privateKey, "0x"))
	if err != nil {
		return nil, fmt.Errorf("invalid private key: %w", err)
	}

	signer := types.LatestSignerForChainID(chainID)
	signedTx, err := types.SignTx(tx, signer, pk)
	if err != nil {
		return nil, fmt.Errorf("sign tx failed: %w", err)
	}

	if err := s.SendTransaction(ctx, signedTx, fallbackClient); err != nil {
		return nil, fmt.Errorf("send tx failed: %w", err)
	}

	return signedTx, nil
}

// Close 关闭私有 RPC 连接
func (s *PrivateTxSender) Close() {
	if s.privateClient != nil {
		s.privateClient.Close()
	}
}
