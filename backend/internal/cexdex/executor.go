// Package cexdex 提供 CEX-DEX 套利功能
package cexdex

import (
	"context"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/defi-bot/backend/internal/executor"
	"github.com/defi-bot/backend/pkg/cex"
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

	// 执行控制
	MaxConcurrentTx int           // 最大并发交易数
	TxTimeout       time.Duration // 交易超时
	CooldownPeriod  time.Duration // 两次执行之间的冷却期

	// 风控
	MaxDailyLoss    float64 // 每日最大亏损 (USD)
	MaxPositionSize float64 // 最大持仓规模 (USD)

	// Keeper 地址
	KeeperAddress common.Address
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
	OpportunityID string        `json:"opportunity_id"`
	Success       bool          `json:"success"`
	Error         string        `json:"error,omitempty"`
	DEXTxHash     common.Hash   `json:"dex_tx_hash,omitempty"`
	CEXOrderID    int64         `json:"cex_order_id,omitempty"`
	CEXFillPrice  float64       `json:"cex_fill_price,omitempty"`
	ActualProfit  float64       `json:"actual_profit"`
	GasCost       float64       `json:"gas_cost"`
	ExecutionTime time.Duration `json:"execution_time"`
	Timestamp     time.Time     `json:"timestamp"`
}

// CEXDEXExecutor CEX-DEX 执行器
// 策略：在 CEX 和 DEX 上同时维持资产，通过差价套利
// - dex_to_cex: DEX 价格低 → DEX 买入 + CEX 卖出（同时执行）
// - cex_to_dex: CEX 价格低 → CEX 买入 + DEX 卖出（同时执行）
type CEXDEXExecutor struct {
	config    *CEXDEXExecutorConfig
	txManager *executor.TxManager
	ethClient *ethclient.Client
	trader    *cex.BinanceTrader

	// 执行状态
	executing   map[string]bool // opportunityID -> executing
	executingMu sync.Mutex

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
	trader *cex.BinanceTrader,
) *CEXDEXExecutor {
	if config == nil {
		config = DefaultCEXDEXExecutorConfig()
	}

	return &CEXDEXExecutor{
		config:    config,
		txManager: txManager,
		ethClient: ethClient,
		trader:    trader,
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
	close(e.resultCh)

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
		result, err = e.executeDEXBuyCEXSell(ctx, opp)
	case "cex_to_dex":
		result, err = e.executeCEXBuyDEXSell(ctx, opp)
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

		// 发送结果（Stop 后 channel 已关闭，不再发送）
		e.runningMu.Lock()
		isRunning := e.running
		e.runningMu.Unlock()
		if isRunning {
			select {
			case e.resultCh <- result:
			default:
			}
		}
	}

	e.lastExecTime = time.Now()

	return result, err
}

// canExecute 检查是否可以执行
func (e *CEXDEXExecutor) canExecute(opp *CEXDEXOpportunity) error {
	if time.Now().After(opp.ValidUntil) {
		return fmt.Errorf("机会已过期")
	}

	e.executingMu.Lock()
	if e.executing[opp.ID] {
		e.executingMu.Unlock()
		return fmt.Errorf("机会正在执行中")
	}
	if len(e.executing) >= e.config.MaxConcurrentTx {
		e.executingMu.Unlock()
		return fmt.Errorf("已达到最大并发交易数")
	}
	e.executingMu.Unlock()

	if time.Since(e.lastExecTime) < e.config.CooldownPeriod {
		return fmt.Errorf("冷却期未结束")
	}

	e.dailyPnLMu.Lock()
	if e.dailyPnL < -e.config.MaxDailyLoss {
		e.dailyPnLMu.Unlock()
		return fmt.Errorf("已达到每日最大亏损限制")
	}
	e.dailyPnLMu.Unlock()

	if e.config.PrivateKey == "" {
		return fmt.Errorf("未配置私钥")
	}

	if e.trader == nil {
		return fmt.Errorf("未配置 CEX 交易客户端")
	}

	return nil
}

// executeDEXBuyCEXSell DEX 买入 + CEX 卖出
// 场景：DEX 价格低于 CEX → 在 DEX 上买入代币，同时在 CEX 上卖出相同代币
func (e *CEXDEXExecutor) executeDEXBuyCEXSell(ctx context.Context, opp *CEXDEXOpportunity) (*ExecutionResult, error) {
	log.Info("执行 DEX买入+CEX卖出: %s, DEX=%.4f, CEX=%.4f, 价差=%.4f%%",
		opp.Symbol, opp.DEXPrice, opp.CEXPrice, opp.ProfitRate*100)

	result := &ExecutionResult{OpportunityID: opp.ID}

	// 计算交易数量（以基础资产计）
	baseQty := opp.TradeAmount / opp.DEXPrice
	cexSymbol := opp.Symbol // e.g. "ETHUSDT"

	// 并行执行 CEX 和 DEX
	type legResult struct {
		dexReceipt *types.Receipt
		cexOrder   *cex.OrderResult
		err        error
		leg        string
	}
	ch := make(chan legResult, 2)

	// 1. DEX 买入（on-chain swap: USDT/USDC → Token）
	go func() {
		swapData := e.buildV3SwapData(opp.TokenIn, opp.TokenOut, opp.FeeTier, opp.TradeAmount, opp.DEXPrice)
		if swapData == nil {
			ch <- legResult{err: fmt.Errorf("构建 DEX swap 数据失败"), leg: "dex"}
			return
		}
		receipt, err := e.txManager.SendTransactionEIP1559(
			ctx, e.config.PrivateKey, opp.DEXRouter, nil, swapData, e.config.GasLimit,
		)
		ch <- legResult{dexReceipt: receipt, err: err, leg: "dex"}
	}()

	// 2. CEX 卖出（Binance market sell）
	go func() {
		qty := formatQty(baseQty, cexSymbol)
		order, err := e.trader.MarketSell(cexSymbol, qty)
		ch <- legResult{cexOrder: order, err: err, leg: "cex"}
	}()

	// 收集结果
	var dexErr, cexErr error
	for i := 0; i < 2; i++ {
		r := <-ch
		switch r.leg {
		case "dex":
			if r.err != nil {
				dexErr = r.err
			} else if r.dexReceipt != nil {
				result.DEXTxHash = r.dexReceipt.TxHash
				result.GasCost = calculateGasCostUSD(r.dexReceipt)
			}
		case "cex":
			if r.err != nil {
				cexErr = r.err
			} else if r.cexOrder != nil {
				result.CEXOrderID = r.cexOrder.OrderID
				result.CEXFillPrice = r.cexOrder.GetAvgFillPrice()
			}
		}
	}

	// 判断结果
	if dexErr != nil && cexErr != nil {
		result.Success = false
		result.Error = fmt.Sprintf("双腿都失败: DEX=%v, CEX=%v", dexErr, cexErr)
		return result, fmt.Errorf("双腿都失败")
	}
	if dexErr != nil {
		result.Success = false
		result.Error = fmt.Sprintf("DEX腿失败(CEX已成交): %v — 需要手动平仓", dexErr)
		log.Warn("⚠️ CEX-DEX 单腿失败，CEX已成交但DEX失败，需手动处理: %s", opp.ID)
		return result, dexErr
	}
	if cexErr != nil {
		result.Success = false
		result.Error = fmt.Sprintf("CEX腿失败(DEX已成交): %v — 需要手动平仓", cexErr)
		log.Warn("⚠️ CEX-DEX 单腿失败，DEX已成交但CEX失败，需手动处理: %s", opp.ID)
		return result, cexErr
	}

	// 计算实际利润
	// CEX 卖出收入 - DEX 买入成本 - Gas
	cexRevenue := result.CEXFillPrice * baseQty
	dexCost := opp.TradeAmount // 已知 TradeAmount = 投入的 USD
	result.ActualProfit = cexRevenue - dexCost - result.GasCost
	result.Success = true

	log.Info("DEX买入+CEX卖出完成: %s, CEX成交价=%.4f, 利润=$%.2f, Gas=$%.4f",
		opp.ID, result.CEXFillPrice, result.ActualProfit, result.GasCost)

	return result, nil
}

// executeCEXBuyDEXSell CEX 买入 + DEX 卖出
// 场景：CEX 价格低于 DEX → 在 CEX 上买入代币，同时在 DEX 上卖出相同代币
func (e *CEXDEXExecutor) executeCEXBuyDEXSell(ctx context.Context, opp *CEXDEXOpportunity) (*ExecutionResult, error) {
	log.Info("执行 CEX买入+DEX卖出: %s, CEX=%.4f, DEX=%.4f, 价差=%.4f%%",
		opp.Symbol, opp.CEXPrice, opp.DEXPrice, opp.ProfitRate*100)

	result := &ExecutionResult{OpportunityID: opp.ID}

	baseQty := opp.TradeAmount / opp.CEXPrice
	cexSymbol := opp.Symbol

	type legResult struct {
		dexReceipt *types.Receipt
		cexOrder   *cex.OrderResult
		err        error
		leg        string
	}
	ch := make(chan legResult, 2)

	// 1. CEX 买入
	go func() {
		quoteQty := fmt.Sprintf("%.2f", opp.TradeAmount)
		order, err := e.trader.MarketBuy(cexSymbol, quoteQty)
		ch <- legResult{cexOrder: order, err: err, leg: "cex"}
	}()

	// 2. DEX 卖出（on-chain swap: Token → USDT/USDC）
	go func() {
		// 卖出方向：TokenOut 是稳定币
		swapData := e.buildV3SwapData(opp.TokenOut, opp.TokenIn, opp.FeeTier, baseQty*opp.DEXPrice, opp.DEXPrice)
		if swapData == nil {
			ch <- legResult{err: fmt.Errorf("构建 DEX swap 数据失败"), leg: "dex"}
			return
		}
		receipt, err := e.txManager.SendTransactionEIP1559(
			ctx, e.config.PrivateKey, opp.DEXRouter, nil, swapData, e.config.GasLimit,
		)
		ch <- legResult{dexReceipt: receipt, err: err, leg: "dex"}
	}()

	// 收集结果
	var dexErr, cexErr error
	for i := 0; i < 2; i++ {
		r := <-ch
		switch r.leg {
		case "dex":
			if r.err != nil {
				dexErr = r.err
			} else if r.dexReceipt != nil {
				result.DEXTxHash = r.dexReceipt.TxHash
				result.GasCost = calculateGasCostUSD(r.dexReceipt)
			}
		case "cex":
			if r.err != nil {
				cexErr = r.err
			} else if r.cexOrder != nil {
				result.CEXOrderID = r.cexOrder.OrderID
				result.CEXFillPrice = r.cexOrder.GetAvgFillPrice()
			}
		}
	}

	if dexErr != nil && cexErr != nil {
		result.Success = false
		result.Error = fmt.Sprintf("双腿都失败: DEX=%v, CEX=%v", dexErr, cexErr)
		return result, fmt.Errorf("双腿都失败")
	}
	if dexErr != nil {
		result.Success = false
		result.Error = fmt.Sprintf("DEX腿失败(CEX已成交): %v — 需要手动平仓", dexErr)
		log.Warn("⚠️ CEX-DEX 单腿失败: %s", opp.ID)
		return result, dexErr
	}
	if cexErr != nil {
		result.Success = false
		result.Error = fmt.Sprintf("CEX腿失败(DEX已成交): %v — 需要手动平仓", cexErr)
		log.Warn("⚠️ CEX-DEX 单腿失败: %s", opp.ID)
		return result, cexErr
	}

	// 利润 = DEX 卖出收入 - CEX 买入成本 - Gas
	cexCost := result.CEXFillPrice * baseQty
	dexRevenue := opp.TradeAmount // 预期 DEX 卖出收到的 USD
	result.ActualProfit = dexRevenue - cexCost - result.GasCost
	result.Success = true

	log.Info("CEX买入+DEX卖出完成: %s, CEX成交价=%.4f, 利润=$%.2f", opp.ID, result.CEXFillPrice, result.ActualProfit)

	return result, nil
}

// Uniswap V3 SwapRouter exactInputSingle ABI
const uniswapV3SwapABI = `[{
	"inputs": [{
		"components": [
			{"name": "tokenIn", "type": "address"},
			{"name": "tokenOut", "type": "address"},
			{"name": "fee", "type": "uint24"},
			{"name": "recipient", "type": "address"},
			{"name": "deadline", "type": "uint256"},
			{"name": "amountIn", "type": "uint256"},
			{"name": "amountOutMinimum", "type": "uint256"},
			{"name": "sqrtPriceLimitX96", "type": "uint160"}
		],
		"name": "params",
		"type": "tuple"
	}],
	"name": "exactInputSingle",
	"outputs": [{"name": "amountOut", "type": "uint256"}],
	"stateMutability": "payable",
	"type": "function"
}]`

// buildV3SwapData 构建 Uniswap V3 exactInputSingle calldata
func (e *CEXDEXExecutor) buildV3SwapData(
	tokenIn, tokenOut common.Address,
	feeTier uint32,
	tradeAmountUSD, tokenPrice float64,
) []byte {
	if tokenIn == (common.Address{}) || tokenOut == (common.Address{}) {
		return nil
	}

	parsed, err := abi.JSON(strings.NewReader(uniswapV3SwapABI))
	if err != nil {
		log.Warn("CEX-DEX: parse V3 Router ABI failed: %v", err)
		return nil
	}

	// 计算 amountIn（简化：假设 tokenIn 是稳定币 6 decimals 或 ETH 18 decimals）
	decimals := 18
	amountInFloat := tradeAmountUSD / tokenPrice
	// 如果 tokenIn 看起来像稳定币地址（USDC/USDT 通常 6 decimals）
	// 简化处理：如果价格接近 1.0 说明是稳定币
	if tokenPrice > 0.9 && tokenPrice < 1.1 {
		decimals = 6
		amountInFloat = tradeAmountUSD
	}

	amountIn := ConvertToBigInt(amountInFloat, decimals)
	if amountIn == nil || amountIn.Sign() <= 0 {
		return nil
	}

	// amountOutMinimum = amountIn * (1 - 0.5%) slippage
	amountOutMin := new(big.Int).Mul(amountIn, big.NewInt(995))
	amountOutMin.Div(amountOutMin, big.NewInt(1000))

	fee := new(big.Int).SetUint64(uint64(feeTier))
	if fee.Sign() == 0 {
		fee = big.NewInt(3000) // 默认 0.3%
	}

	recipient := e.config.KeeperAddress
	deadline := new(big.Int).SetInt64(time.Now().Add(2 * time.Minute).Unix())

	// Pack as struct
	callData, err := parsed.Pack("exactInputSingle", struct {
		TokenIn           common.Address
		TokenOut          common.Address
		Fee               *big.Int
		Recipient         common.Address
		Deadline          *big.Int
		AmountIn          *big.Int
		AmountOutMinimum  *big.Int
		SqrtPriceLimitX96 *big.Int
	}{
		TokenIn:           tokenIn,
		TokenOut:          tokenOut,
		Fee:               fee,
		Recipient:         recipient,
		Deadline:          deadline,
		AmountIn:          amountIn,
		AmountOutMinimum:  amountOutMin,
		SqrtPriceLimitX96: big.NewInt(0),
	})
	if err != nil {
		log.Warn("CEX-DEX: pack V3 swap calldata failed: %v", err)
		return nil
	}

	return callData
}

// formatQty 格式化 CEX 下单数量（不同交易对精度不同）
func formatQty(qty float64, symbol string) string {
	switch {
	case strings.HasPrefix(symbol, "BTC"):
		return strconv.FormatFloat(qty, 'f', 5, 64) // BTC 5 位小数
	case strings.HasPrefix(symbol, "ETH"):
		return strconv.FormatFloat(qty, 'f', 4, 64) // ETH 4 位小数
	default:
		return strconv.FormatFloat(qty, 'f', 2, 64) // 其他 2 位小数
	}
}

// calculateGasCostUSD 计算 Gas 成本 (USD)
func calculateGasCostUSD(receipt *types.Receipt) float64 {
	if receipt == nil {
		return 0
	}
	// Arbitrum: gasUsed * effectiveGasPrice
	gasUsed := receipt.GasUsed
	// effectiveGasPrice from receipt (EIP-1559)
	var gasPriceWei uint64
	if receipt.EffectiveGasPrice != nil {
		gasPriceWei = receipt.EffectiveGasPrice.Uint64()
	} else {
		gasPriceWei = 100_000_000 // 0.1 Gwei fallback (Arbitrum typical)
	}
	gasCostWei := gasUsed * gasPriceWei
	gasCostETH := float64(gasCostWei) / 1e18
	return gasCostETH * 2500 // ETH price estimate
}

// dailyResetLoop 每日重置循环
func (e *CEXDEXExecutor) dailyResetLoop(ctx context.Context) {
	for {
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
