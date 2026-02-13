// internal/executor/flashbots.go
package executor

import (
	"context"
	"fmt"
	"math/big"
	"time"

	"github.com/defi-bot/backend/pkg/log"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/rlp"
)

const (
	// DefaultFlashbotsRPC is the Flashbots Protect RPC endpoint for Ethereum mainnet.
	// It accepts standard eth_sendRawTransaction calls and routes them privately,
	// bypassing the public mempool to prevent frontrunning/sandwich attacks.
	DefaultFlashbotsRPC = "https://rpc.flashbots.net"

	// ArbitrumChainID is the chain ID for Arbitrum One.
	// Arbitrum uses a centralized sequencer with FCFS ordering and no public mempool,
	// so private submission is unnecessary.
	ArbitrumChainID int64 = 42161

	// EthereumMainnetChainID is the chain ID for Ethereum mainnet.
	EthereumMainnetChainID int64 = 1

	// flashbotsDialTimeout is the timeout for connecting to the Flashbots RPC endpoint.
	flashbotsDialTimeout = 10 * time.Second
)

// FlashbotsConfig holds configuration for Flashbots Protect integration.
type FlashbotsConfig struct {
	// ChainID identifies the target chain. Determines whether private submission is applicable.
	ChainID int64

	// FlashbotsRPC is the Flashbots Protect RPC URL.
	// Defaults to "https://rpc.flashbots.net" for Ethereum mainnet.
	// Should be left empty for L2 chains (Arbitrum, Optimism, etc.) where it is not applicable.
	FlashbotsRPC string

	// UsePrivateSubmission enables routing transactions through Flashbots Protect
	// instead of the standard public RPC. Only effective on supported chains (Ethereum mainnet).
	// On L2s like Arbitrum, this flag is ignored since the sequencer already provides FCFS ordering.
	UsePrivateSubmission bool
}

// FlashbotsClient wraps an ethclient connected to the Flashbots Protect RPC endpoint.
// It provides MEV-protected transaction submission by routing signed transactions
// through the Flashbots private transaction pool instead of the public mempool.
//
// For Ethereum mainnet: transactions are sent via the Flashbots Protect RPC,
// which uses standard eth_sendRawTransaction but routes privately.
//
// For Arbitrum (chain ID 42161): private submission is a no-op since the
// centralized sequencer already provides FCFS ordering with no public mempool.
type FlashbotsClient struct {
	client *ethclient.Client
	config FlashbotsConfig
}

// NewFlashbotsClient creates a new FlashbotsClient connected to the appropriate RPC endpoint.
//
// For Ethereum mainnet (chain ID 1), it connects to the Flashbots Protect RPC
// (default: https://rpc.flashbots.net). The Flashbots RPC accepts standard
// eth_sendRawTransaction calls — no special bundle signing is required.
//
// For Arbitrum (chain ID 42161) or other L2s, the client is created with a nil
// underlying ethclient since private submission is not needed; the standard RPC
// should be used directly. The UsePrivateSubmission flag will be forced to false.
func NewFlashbotsClient(config FlashbotsConfig) (*FlashbotsClient, error) {
	fc := &FlashbotsClient{
		config: config,
	}

	// Determine effective RPC URL
	rpcURL := config.FlashbotsRPC
	if rpcURL == "" && config.ChainID == EthereumMainnetChainID {
		rpcURL = DefaultFlashbotsRPC
	}

	// For L2 chains, private submission is not applicable
	if !isFlashbotsSupported(config.ChainID) {
		fc.config.UsePrivateSubmission = false
		log.Info("Flashbots: chain %d does not support private submission (sequencer provides FCFS ordering)", config.ChainID)
		return fc, nil
	}

	if !config.UsePrivateSubmission {
		log.Info("Flashbots: private submission disabled by config for chain %d", config.ChainID)
		return fc, nil
	}

	if rpcURL == "" {
		return nil, fmt.Errorf("flashbots RPC URL is required for chain %d when private submission is enabled", config.ChainID)
	}

	// Connect to the Flashbots Protect RPC
	ctx, cancel := context.WithTimeout(context.Background(), flashbotsDialTimeout)
	defer cancel()

	client, err := ethclient.DialContext(ctx, rpcURL)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to Flashbots RPC %s: %w", rpcURL, err)
	}

	fc.client = client
	log.Info("Flashbots: connected to %s for chain %d (private submission enabled)", rpcURL, config.ChainID)

	return fc, nil
}

// SendPrivateTransaction submits a signed transaction via the Flashbots Protect RPC.
//
// For Ethereum mainnet: the signed transaction is RLP-encoded and sent through
// the Flashbots Protect endpoint using standard eth_sendRawTransaction. The
// transaction is routed privately, bypassing the public mempool.
//
// For Arbitrum or other unsupported chains: returns an error indicating that
// private submission is not available. The caller should fall back to standard
// RPC submission.
func (fc *FlashbotsClient) SendPrivateTransaction(ctx context.Context, signedTx *types.Transaction) (common.Hash, error) {
	if !fc.config.UsePrivateSubmission {
		return common.Hash{}, fmt.Errorf("private submission is not enabled")
	}

	if fc.client == nil {
		return common.Hash{}, fmt.Errorf("flashbots client is not connected")
	}

	if signedTx == nil {
		return common.Hash{}, fmt.Errorf("signed transaction is nil")
	}

	// RLP-encode the signed transaction
	rawTx, err := rlp.EncodeToBytes(signedTx)
	if err != nil {
		return common.Hash{}, fmt.Errorf("failed to RLP-encode transaction: %w", err)
	}

	// Send via eth_sendRawTransaction to the Flashbots Protect endpoint.
	// The Flashbots RPC handles this identically to a normal JSON-RPC call,
	// but routes the transaction through their private pool.
	var txHash common.Hash
	err = fc.client.Client().CallContext(ctx, &txHash, "eth_sendRawTransaction", fmt.Sprintf("0x%x", rawTx))
	if err != nil {
		return common.Hash{}, fmt.Errorf("flashbots eth_sendRawTransaction failed: %w", err)
	}

	log.Info("Flashbots: submitted private transaction %s", txHash.Hex())
	return txHash, nil
}

// IsPrivateSubmissionEnabled returns whether this client is configured and connected
// for private transaction submission.
func (fc *FlashbotsClient) IsPrivateSubmissionEnabled() bool {
	return fc.config.UsePrivateSubmission && fc.client != nil
}

// GetConfig returns the current FlashbotsConfig (read-only copy).
func (fc *FlashbotsClient) GetConfig() FlashbotsConfig {
	return fc.config
}

// Close closes the underlying ethclient connection, if any.
func (fc *FlashbotsClient) Close() {
	if fc.client != nil {
		fc.client.Close()
		fc.client = nil
		log.Info("Flashbots: client connection closed")
	}
}

// isFlashbotsSupported returns true if the given chain ID supports Flashbots Protect
// or an equivalent private transaction submission mechanism.
// Currently only Ethereum mainnet is supported.
func isFlashbotsSupported(chainID int64) bool {
	switch chainID {
	case EthereumMainnetChainID:
		return true
	case ArbitrumChainID:
		// Arbitrum uses a centralized sequencer with FCFS ordering.
		// No public mempool means no MEV risk from frontrunning.
		return false
	default:
		// Conservative default: assume unsupported for unknown chains.
		// Goerli/Sepolia Flashbots support could be added here if needed.
		return false
	}
}

// NewFlashbotsConfigForChain returns a sensible default FlashbotsConfig for the given chain.
// This is a convenience constructor for common chain configurations.
func NewFlashbotsConfigForChain(chainID int64, enablePrivate bool) FlashbotsConfig {
	cfg := FlashbotsConfig{
		ChainID:              chainID,
		UsePrivateSubmission: enablePrivate,
	}

	switch chainID {
	case EthereumMainnetChainID:
		cfg.FlashbotsRPC = DefaultFlashbotsRPC
	default:
		// L2s and other chains: no Flashbots RPC, force disable
		cfg.FlashbotsRPC = ""
		cfg.UsePrivateSubmission = false
	}

	return cfg
}

// GetChainID returns the chain ID configured for this client.
func (fc *FlashbotsClient) GetChainID() *big.Int {
	return big.NewInt(fc.config.ChainID)
}
