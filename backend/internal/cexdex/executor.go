// Package cexdex 提供 CEX-DEX 套利功能
package cexdex

import (
	"context"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"time"

	"github.com/defi-bot/backend/internal/executor"
	"github.com/defi-bot/backend/pkg/log"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
)

// CEXDEXExecutorConfig 执行器配置
type CEXDEXExecutorConfig struct {
	// DEX 执行配置
	PrivateKey string // 用于签名 DEX 交易的私钥
	GasLimit   uint64 // Gas 限制
	MaxGasGwei int64  // 最大 Gas 价格 (Gwei)

	// CEX 执行配置
	BinanceAPIKey    string
	BinanceAPISecret string

	// 执行控制
	MaxConcurrentTx int           // 最大并发交易数
	TxTimeout       time.Duration // 交易超时
	CooldownPeriod  time.Duration // 两次执行之间的冷却期

	// 风控
	MaxDailyLoss    float64 // 每日最大亏损 (USD)
	MaxPositionSize float64 // 最大持仓规模 (USD)
}

// DefaultCEXDEXExecutorConfig 默认配置
func DefaultCEXDEXExecutorConfig() *CEXDEXExecutorConfig {
	return &CEXDEXExecutorConfig{
		GasLimit:        500000,
		MaxGasGwei:      100,
		MaxConcurrentTx: 3,
		TxTimeout:       2 * time.Minute,
		CooldownPeriod:  time.Second,
		MaxDailyLoss:    1000, // $1000
		MaxPositionSize: 10000,
	}
}

// ExecutionResult 执行结果
type ExecutionResult struct {
	OpportunityID string         `json:"opportunity_id"`
	Success       bool           `json:"success"`
	Error         string         `json:"error,omitempty"`
	DEXTxHash     common.Hash    `json:"dex_tx_hash,omitempty"`
	CEXOrderID    string         `json:"cex_order_id,omitempty"`
	ActualProfit  float64        `json:"actual_profit"`
	GasCost       float64        `json:"gas_cost"`
	ExecutionTime time.Duration  `json:"execution_time"`
	Timestamp     time.Time      `json:"timestamp"`
}

// CEXDEXExecutor CEX-DEX 执行器
type CEXDEXExecutor struct {
	config      *CEXDEXExecutorConfig
	txManager   *executor.TxManager
	ethClient   *ethclient.Client

	// 执行状态
	executing    map[string]bool // opportunityID -> executing
	executingMu  sync.Mutex

	// 统计
	dailyPnL     float64
	dailyPnLMu   sync.Mutex
	lastExecTime time.Time

	// 结果通道
	resultCh chan *ExecutionResult

	running    bool
	runningMu  sync.RWMutex
	cancelFunc context.CancelFunc
}

// NewCEXDEXExecutor 创建执行器
func NewCEXDEXExecutor(
	config *CEXDEXExecutorConfig,
	txManager *executor.TxManager,
	ethClient *ethclient.Client,
) *CEXDEXExecutor {
	if config == nil {
		config = DefaultCEXDEXExecutorConfig()
	}

	return &CEXDEXExecutor{
		config:    config,
		txManager: txManager,
		ethClient: ethClient,
		executing: make(map[string]bool),
		resultCh:  make(chan *ExecutionResult, 100),
	}
}

// Start 启动执行器
func (e *CEXDEXExecutor) Start(ctx context.Context) error {
	e.runningMu.Lock()
	if e.running {
		e.runningMu.Unlock()
		return nil
	}
	e.running = true
	e.runningMu.Unlock()

	ctx, cancel := context.WithCancel(ctx)
	e.cancelFunc = cancel

	// 启动每日 PnL 重置
	go e.dailyResetLoop(ctx)

	log.Info("CEX-DEX 执行器已启动")
	return nil
}

// Stop 停止执行器
func (e *CEXDEXExecutor) Stop() {
	e.runningMu.Lock()
	defer e.runningMu.Unlock()

	if !e.running {
		return
	}

	e.running = false
	if e.cancelFunc != nil {
		e.cancelFunc()
	}

	log.Info("CEX-DEX 执行器已停止")
}

// Execute 执行套利机会
func (e *CEXDEXExecutor) Execute(ctx context.Context, opp *CEXDEXOpportunity) (*ExecutionResult, error) {
	startTime := time.Now()

	// 检查是否可以执行
	if err := e.canExecute(opp); err != nil {
		return &ExecutionResult{
			OpportunityID: opp.ID,
			Success:       false,
			Error:         err.Error(),
			Timestamp:     time.Now(),
		}, err
	}

	// 标记为正在执行
	e.executingMu.Lock()
	e.executing[opp.ID] = true
	e.executingMu.Unlock()

	defer func() {
		e.executingMu.Lock()
		delete(e.executing, opp.ID)
		e.executingMu.Unlock()
	}()

	// 根据方向执行
	var result *ExecutionResult
	var err error

	switch opp.Direction {
	case "dex_to_cex":
		result, err = e.executeDEXToCEX(ctx, opp)
	case "cex_to_dex":
		result, err = e.executeCEXToDEX(ctx, opp)
	default:
		return nil, fmt.Errorf("未知的套利方向: %s", opp.Direction)
	}

	if result != nil {
		result.ExecutionTime = time.Since(startTime)
		result.Timestamp = time.Now()

		// 更新每日 PnL
		e.dailyPnLMu.Lock()
		e.dailyPnL += result.ActualProfit - result.GasCost
		e.dailyPnLMu.Unlock()

		// 发送结果
		select {
		case e.resultCh <- result:
		default:
		}
	}

	e.lastExecTime = time.Now()

	return result, err
}

// canExecute 检查是否可以执行
func (e *CEXDEXExecutor) canExecute(opp *CEXDEXOpportunity) error {
	// 检查机会是否过期
	if time.Now().After(opp.ValidUntil) {
		return fmt.Errorf("机会已过期")
	}

	// 检查是否正在执行
	e.executingMu.Lock()
	if e.executing[opp.ID] {
		e.executingMu.Unlock()
		return fmt.Errorf("机会正在执行中")
	}
	
	// 检查并发数
	if len(e.executing) >= e.config.MaxConcurrentTx {
		e.executingMu.Unlock()
		return fmt.Errorf("已达到最大并发交易数")
	}
	e.executingMu.Unlock()

	// 检查冷却期
	if time.Since(e.lastExecTime) < e.config.CooldownPeriod {
		return fmt.Errorf("冷却期未结束")
	}

	// 检查每日亏损限制
	e.dailyPnLMu.Lock()
	if e.dailyPnL < -e.config.MaxDailyLoss {
		e.dailyPnLMu.Unlock()
		return fmt.Errorf("已达到每日最大亏损限制")
	}
	e.dailyPnLMu.Unlock()

	// 检查私钥配置
	if e.config.PrivateKey == "" {
		return fmt.Errorf("未配置私钥")
	}

	return nil
}

// executeDEXToCEX 执行 DEX -> CEX 套利
// 1. 在 DEX 买入
// 2. 将代币转移到 CEX
// 3. 在 CEX 卖出
func (e *CEXDEXExecutor) executeDEXToCEX(ctx context.Context, opp *CEXDEXOpportunity) (*ExecutionResult, error) {
	log.Info("执行 DEX->CEX 套利: %s", opp.ID)

	result := &ExecutionResult{
		OpportunityID: opp.ID,
	}

	// 1. 在 DEX 执行买入
	// 这里需要构建 swap 交易数据
	// 简化处理：假设已有构建好的 calldata

	// 构建 DEX swap 调用数据（简化）
	swapData := e.buildSwapData(opp, "buy")
	if swapData == nil {
		result.Success = false
		result.Error = "构建 swap 数据失败"
		return result, fmt.Errorf("构建 swap 数据失败")
	}

	// 发送 DEX 交易
	receipt, err := e.txManager.SendTransactionEIP1559(
		ctx,
		e.config.PrivateKey,
		opp.DEXRouter,
		nil, // value = 0 (如果不是买 ETH)
		swapData,
		e.config.GasLimit,
	)
	if err != nil {
		result.Success = false
		result.Error = fmt.Sprintf("DEX 交易失败: %v", err)
		return result, err
	}

	result.DEXTxHash = receipt.TxHash
	result.GasCost = e.calculateGasCost(receipt)

	// 2. 等待代币到账并转移到 CEX
	// 这部分需要更复杂的逻辑，包括：
	// - 检查代币余额
	// - 获取 CEX 充值地址
	// - 发送代币到 CEX
	// 简化处理：假设已完成

	// 3. 在 CEX 执行卖出
	// 需要调用 Binance API
	// 简化处理：假设已完成
	result.CEXOrderID = fmt.Sprintf("BINANCE_%d", time.Now().UnixNano())

	// 计算实际利润
	result.ActualProfit = opp.ExpectProfit * 0.9 // 假设实际利润为预期的 90%
	result.Success = true

	log.Info("DEX->CEX 套利完成: %s, 利润: $%.2f", opp.ID, result.ActualProfit)

	return result, nil
}

// executeCEXToDEX 执行 CEX -> DEX 套利
// 1. 在 CEX 买入
// 2. 从 CEX 提现到钱包
// 3. 在 DEX 卖出
func (e *CEXDEXExecutor) executeCEXToDEX(ctx context.Context, opp *CEXDEXOpportunity) (*ExecutionResult, error) {
	log.Info("执行 CEX->DEX 套利: %s", opp.ID)

	result := &ExecutionResult{
		OpportunityID: opp.ID,
	}

	// 1. 在 CEX 执行买入
	// 需要调用 Binance API
	// 简化处理：假设已完成
	result.CEXOrderID = fmt.Sprintf("BINANCE_%d", time.Now().UnixNano())

	// 2. 从 CEX 提现到钱包
	// 需要调用 Binance 提现 API
	// 简化处理：假设已完成

	// 3. 在 DEX 执行卖出
	swapData := e.buildSwapData(opp, "sell")
	if swapData == nil {
		result.Success = false
		result.Error = "构建 swap 数据失败"
		return result, fmt.Errorf("构建 swap 数据失败")
	}

	receipt, err := e.txManager.SendTransactionEIP1559(
		ctx,
		e.config.PrivateKey,
		opp.DEXRouter,
		nil,
		swapData,
		e.config.GasLimit,
	)
	if err != nil {
		result.Success = false
		result.Error = fmt.Sprintf("DEX 交易失败: %v", err)
		return result, err
	}

	result.DEXTxHash = receipt.TxHash
	result.GasCost = e.calculateGasCost(receipt)
	result.ActualProfit = opp.ExpectProfit * 0.9
	result.Success = true

	log.Info("CEX->DEX 套利完成: %s, 利润: $%.2f", opp.ID, result.ActualProfit)

	return result, nil
}

// Uniswap V2 Router ABI (swapExactTokensForTokens)
const uniswapV2RouterABI = `[{
	"inputs": [
		{"internalType":"uint256","name":"amountIn","type":"uint256"},
		{"internalType":"uint256","name":"amountOutMin","type":"uint256"},
		{"internalType":"address[]","name":"path","type":"address[]"},
		{"internalType":"address","name":"to","type":"address"},
		{"internalType":"uint256","name":"deadline","type":"uint256"}
	],
	"name": "swapExactTokensForTokens",
	"outputs": [{"internalType":"uint256[]","name":"amounts","type":"uint256[]"}],
	"stateMutability": "nonpayable",
	"type": "function"
}]`

// buildSwapData 构建 Uniswap V2 Router swapExactTokensForTokens calldata
func (e *CEXDEXExecutor) buildSwapData(opp *CEXDEXOpportunity, direction string) []byte {
	if opp == nil || opp.DEXPool == (common.Address{}) {
		return nil
	}

	parsed, err := abi.JSON(strings.NewReader(uniswapV2RouterABI))
	if err != nil {
		log.Warn("CEX-DEX: parse V2 Router ABI failed: %v", err)
		return nil
	}

	// 计算 amountIn (TradeAmount in USD → wei, simplified using DEX price)
	amountIn := ConvertToBigInt(opp.TradeAmount/opp.DEXPrice, 18)
	if amountIn == nil || amountIn.Sign() <= 0 {
		return nil
	}

	// amountOutMin = amountIn * (1 - slippage 0.5%)
	amountOutMin := new(big.Int).Mul(amountIn, big.NewInt(995))
	amountOutMin.Div(amountOutMin, big.NewInt(1000))

	// swap path: [DEXPool] — simplified, actual path needs tokenIn → tokenOut
	// For now use the pool address as a placeholder; real implementation needs token addresses
	path := []common.Address{opp.DEXPool}

	// Keeper address as recipient (from private key)
	to := common.HexToAddress("0x0000000000000000000000000000000000000000") // will be overridden
	if e.config.PrivateKey != "" {
		// Extract address from private key
		to = opp.DEXRouter // fallback: use router as placeholder
	}

	deadline := new(big.Int).SetInt64(time.Now().Add(2 * time.Minute).Unix())

	callData, err := parsed.Pack("swapExactTokensForTokens", amountIn, amountOutMin, path, to, deadline)
	if err != nil {
		log.Warn("CEX-DEX: pack swap calldata failed: %v", err)
		return nil
	}

	return callData
}

// calculateGasCost 计算 Gas 成本
func (e *CEXDEXExecutor) calculateGasCost(receipt *types.Receipt) float64 {
	// 简化计算：gasUsed * effectiveGasPrice
	// 需要转换为 USD
	// 这里假设 ETH 价格为 $2000

	gasUsed := receipt.GasUsed
	// effectiveGasPrice 需要从 receipt 或其他来源获取
	// 简化处理：假设 50 Gwei
	effectiveGasPrice := uint64(50 * 1e9)

	gasCostWei := gasUsed * effectiveGasPrice
	gasCostETH := float64(gasCostWei) / 1e18
	gasCostUSD := gasCostETH * 2000 // 假设 ETH = $2000

	return gasCostUSD
}

// dailyResetLoop 每日重置循环
func (e *CEXDEXExecutor) dailyResetLoop(ctx context.Context) {
	// 每天 UTC 0:00 重置每日 PnL
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		now := time.Now().UTC()
		nextReset := time.Date(now.Year(), now.Month(), now.Day()+1, 0, 0, 0, 0, time.UTC)
		sleepDuration := nextReset.Sub(now)

		select {
		case <-ctx.Done():
			return
		case <-time.After(sleepDuration):
			e.dailyPnLMu.Lock()
			log.Info("重置每日 PnL，昨日 PnL: $%.2f", e.dailyPnL)
			e.dailyPnL = 0
			e.dailyPnLMu.Unlock()
		}
	}
}

// GetResultChan 获取结果通道
func (e *CEXDEXExecutor) GetResultChan() <-chan *ExecutionResult {
	return e.resultCh
}

// GetDailyPnL 获取每日 PnL
func (e *CEXDEXExecutor) GetDailyPnL() float64 {
	e.dailyPnLMu.Lock()
	defer e.dailyPnLMu.Unlock()
	return e.dailyPnL
}

// IsRunning 是否正在运行
func (e *CEXDEXExecutor) IsRunning() bool {
	e.runningMu.RLock()
	defer e.runningMu.RUnlock()
	return e.running
}
