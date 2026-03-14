// internal/solver/uniswapx_filler.go
//
// UniswapX Filler — 监听 UniswapX Dutch Auction 订单并填单获利
// Permissionless: 任何人都可以成为 Filler，无需质押
//
// ==================== 模块说明 ====================
//
// 什么是 UniswapX？
//   用户在 Uniswap 前端提交 swap 意图（如 "1 ETH → USDC"），
//   系统生成 Dutch Auction 订单（价格从高到低递减），
//   第三方 Filler（如我们）轮询 API 发现订单 → 用 1inch 询价对比 → 有利可图则链上填单。
//
// 工作流程：
//   1. 每 2 秒轮询 UniswapX API (https://api.uniswap.org/v2/orders)
//   2. 解析订单的 input/output token 和当前衰减后的价格
//   3. 调用 1inch 获取市场报价，比较是否有利润空间
//   4. 有利润 → 调用 Reactor.execute() 原子填单，差价即利润
//
// 当前状态：已禁用（保留代码）
//   - 2026-03-13 测试结果：Arbitrum (chainId=42161) 和以太坊主网 (chainId=1) 均无 open orders
//   - UniswapX 目前几乎只活跃在以太坊主网，且主网订单量也很低
//   - L2 上 gas 便宜，用户直接走 AMM，不需要拍卖系统
//   - 代码保留以备 Uniswap 未来推广 L2 时快速启用
//
// 启用条件：
//   - UniswapX 在目标链上有稳定订单量（可用 API 检查）
//   - 需要 Keeper 私钥 + 1inch API（已具备）
//   - main.go 中取消禁用注释即可启动
//
// ================================================================
package solver

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/defi-bot/backend/internal/executor"
	"github.com/defi-bot/backend/pkg/aggregator"
	"github.com/defi-bot/backend/pkg/log"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
)

// FillerConfig 配置
type FillerConfig struct {
	// UniswapX API
	OrdersAPIURL string // UniswapX 订单 API
	ChainID      int64

	// 执行参数
	PrivateKey    string
	KeeperAddress common.Address
	GasLimit      uint64

	// 利润阈值
	MinProfitUSD   float64 // 最小利润 (USD)
	MinProfitRate  float64 // 最小利润率
	MaxTradeAmount float64 // 最大单笔金额 (USD)

	// 轮询间隔
	PollInterval time.Duration
}

// DefaultFillerConfig 默认配置
func DefaultFillerConfig() *FillerConfig {
	return &FillerConfig{
		OrdersAPIURL:   "https://api.uniswap.org/v2/orders",
		ChainID:        42161, // Arbitrum
		GasLimit:       600000,
		MinProfitUSD:   1.0,
		MinProfitRate:  0.001, // 0.1%
		MaxTradeAmount: 5000,
		PollInterval:   2 * time.Second,
	}
}

// UniswapXOrder UniswapX 订单
type UniswapXOrder struct {
	OrderHash   string         `json:"orderHash"`
	ChainID     int64          `json:"chainId"`
	Type        string         `json:"type"` // "Dutch_V2", "Limit"
	Swapper     common.Address `json:"swapper"`
	Input       OrderInput     `json:"input"`
	Outputs     []OrderOutput  `json:"outputs"`
	Reactor     common.Address `json:"reactor"`
	Deadline    int64          `json:"deadline"`
	CreatedAt   int64          `json:"createdAt"`
	EncodedOrder string        `json:"encodedOrder"`
	Signature   string         `json:"signature"`
}

// OrderInput 订单输入
type OrderInput struct {
	Token       common.Address `json:"token"`
	StartAmount string         `json:"startAmount"`
	EndAmount   string         `json:"endAmount"`
}

// OrderOutput 订单输出
type OrderOutput struct {
	Token       common.Address `json:"token"`
	StartAmount string         `json:"startAmount"`
	EndAmount   string         `json:"endAmount"`
	Recipient   common.Address `json:"recipient"`
}

// FillResult 填单结果
type FillResult struct {
	OrderHash string      `json:"order_hash"`
	Success   bool        `json:"success"`
	TxHash    common.Hash `json:"tx_hash,omitempty"`
	Profit    float64     `json:"profit"`
	Error     string      `json:"error,omitempty"`
}

// UniswapXFiller UniswapX Filler
type UniswapXFiller struct {
	config    *FillerConfig
	oneInch   *aggregator.OneInchClient
	txManager *executor.TxManager
	ethClient *ethclient.Client

	// 统计
	totalFills   int
	totalProfit  float64
	statsMu      sync.Mutex

	running    bool
	runningMu  sync.RWMutex
	cancelFunc context.CancelFunc
}

// NewUniswapXFiller 创建 Filler
func NewUniswapXFiller(
	config *FillerConfig,
	oneInch *aggregator.OneInchClient,
	txManager *executor.TxManager,
	ethClient *ethclient.Client,
) *UniswapXFiller {
	if config == nil {
		config = DefaultFillerConfig()
	}

	return &UniswapXFiller{
		config:    config,
		oneInch:   oneInch,
		txManager: txManager,
		ethClient: ethClient,
	}
}

// Start 启动 Filler
func (f *UniswapXFiller) Start(ctx context.Context) error {
	f.runningMu.Lock()
	if f.running {
		f.runningMu.Unlock()
		return nil
	}
	f.running = true
	f.runningMu.Unlock()

	ctx, cancel := context.WithCancel(ctx)
	f.cancelFunc = cancel

	go f.watchLoop(ctx)

	log.Info("UniswapX Filler 已启动, chainID=%d", f.config.ChainID)
	return nil
}

// Stop 停止 Filler
func (f *UniswapXFiller) Stop() {
	f.runningMu.Lock()
	defer f.runningMu.Unlock()

	if !f.running {
		return
	}
	f.running = false
	if f.cancelFunc != nil {
		f.cancelFunc()
	}
	log.Info("UniswapX Filler 已停止")
}

// watchLoop 监听订单循环
func (f *UniswapXFiller) watchLoop(ctx context.Context) {
	ticker := time.NewTicker(f.config.PollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			orders, err := f.fetchOpenOrders(ctx)
			if err != nil {
				log.Main().Debug().Err(err).Msg("获取 UniswapX 订单失败")
				continue
			}
			for _, order := range orders {
				f.evaluateAndFill(ctx, &order)
			}
		}
	}
}

// fetchOpenOrders 从 UniswapX API 获取开放订单
func (f *UniswapXFiller) fetchOpenOrders(ctx context.Context) ([]UniswapXOrder, error) {
	reqURL := fmt.Sprintf("%s?chainId=%d&orderStatus=open&limit=20",
		f.config.OrdersAPIURL, f.config.ChainID)

	req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("UniswapX API error (%d): %s", resp.StatusCode, string(body))
	}

	var result struct {
		Orders []UniswapXOrder `json:"orders"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parse orders: %w", err)
	}

	return result.Orders, nil
}

// evaluateAndFill 评估订单并尝试填单
func (f *UniswapXFiller) evaluateAndFill(ctx context.Context, order *UniswapXOrder) {
	// 检查过期
	if time.Now().Unix() > order.Deadline {
		return
	}

	// 只处理 Dutch Auction 订单
	if order.Type != "Dutch_V2" && order.Type != "Dutch" {
		return
	}

	if len(order.Outputs) == 0 {
		return
	}

	// 计算当前 Dutch Auction 价格
	// Dutch Auction: 价格从 startAmount 线性下降到 endAmount
	inputAmount, ok := new(big.Int).SetString(order.Input.StartAmount, 10)
	if !ok {
		return
	}

	// 需要向用户提供的最小输出量
	outputNeeded, ok := new(big.Int).SetString(order.Outputs[0].StartAmount, 10)
	if !ok {
		return
	}

	// 用 1inch 查询：将 inputToken swap 到 outputToken 能获得多少
	if f.oneInch == nil {
		return
	}

	quote, err := f.oneInch.GetQuote(ctx, &aggregator.QuoteParams{
		Src:    order.Input.Token,
		Dst:    order.Outputs[0].Token,
		Amount: inputAmount,
	})
	if err != nil {
		log.Main().Debug().Err(err).Str("order", order.OrderHash[:12]).Msg("1inch 报价失败")
		return
	}

	// 利润 = 实际可获得 - 需向用户提供
	profit := new(big.Int).Sub(quote.DstAmount, outputNeeded)
	if profit.Sign() <= 0 {
		return // 无利可图
	}

	// 计算利润率
	profitRate := new(big.Float).Quo(
		new(big.Float).SetInt(profit),
		new(big.Float).SetInt(outputNeeded),
	)
	profitRateF, _ := profitRate.Float64()

	if profitRateF < f.config.MinProfitRate {
		return
	}

	log.Info("UniswapX 可填单: %s, input=%s %s, profit_rate=%.4f%%",
		order.OrderHash[:12], inputAmount.String(), order.Input.Token.Hex()[:10], profitRateF*100)

	// 执行填单
	result := f.fillOrder(ctx, order, inputAmount)
	if result.Success {
		f.statsMu.Lock()
		f.totalFills++
		f.totalProfit += result.Profit
		f.statsMu.Unlock()
	}
}

// UniswapX Reactor execute ABI
const reactorExecuteABI = `[{
	"inputs": [
		{"name": "order", "type": "bytes"},
		{"name": "sig", "type": "bytes"},
		{"name": "fillContract", "type": "address"},
		{"name": "fillData", "type": "bytes"}
	],
	"name": "execute",
	"outputs": [],
	"stateMutability": "payable",
	"type": "function"
}]`

// fillOrder 执行填单
func (f *UniswapXFiller) fillOrder(ctx context.Context, order *UniswapXOrder, _ *big.Int) *FillResult {
	result := &FillResult{OrderHash: order.OrderHash}

	// 1. 获取 1inch swap calldata（将用户的输入 token 换为输出 token）
	swapResult, err := f.oneInch.GetSwap(ctx, &aggregator.SwapParams{
		Src:      order.Input.Token,
		Dst:      order.Outputs[0].Token,
		Amount:   mustParseBigInt(order.Input.StartAmount),
		From:     f.config.KeeperAddress,
		Slippage: 0.5,
	})
	if err != nil {
		result.Error = fmt.Sprintf("1inch swap 失败: %v", err)
		return result
	}

	// 2. 构造 Reactor.execute() calldata
	parsed, err := abi.JSON(strings.NewReader(reactorExecuteABI))
	if err != nil {
		result.Error = fmt.Sprintf("parse ABI: %v", err)
		return result
	}

	encodedOrder := common.FromHex(order.EncodedOrder)
	sig := common.FromHex(order.Signature)

	callData, err := parsed.Pack("execute",
		encodedOrder,
		sig,
		swapResult.TxTo,   // fillContract = 1inch Router
		swapResult.TxData, // fillData = 1inch swap calldata
	)
	if err != nil {
		result.Error = fmt.Sprintf("pack execute: %v", err)
		return result
	}

	// 3. 发送交易到 Reactor
	receipt, err := f.txManager.SendTransactionEIP1559(
		ctx,
		f.config.PrivateKey,
		order.Reactor,
		nil, // value = 0
		callData,
		f.config.GasLimit,
	)
	if err != nil {
		result.Error = fmt.Sprintf("tx 失败: %v", err)
		return result
	}

	result.Success = receipt.Status == 1
	result.TxHash = receipt.TxHash

	if result.Success {
		log.Info("UniswapX 填单成功: %s, tx=%s", order.OrderHash[:12], receipt.TxHash.Hex())
	} else {
		result.Error = "交易 revert"
		log.Warn("UniswapX 填单 revert: %s", order.OrderHash[:12])
	}

	return result
}

// Stats 统计
func (f *UniswapXFiller) Stats() (fills int, profit float64) {
	f.statsMu.Lock()
	defer f.statsMu.Unlock()
	return f.totalFills, f.totalProfit
}

// IsRunning 是否运行中
func (f *UniswapXFiller) IsRunning() bool {
	f.runningMu.RLock()
	defer f.runningMu.RUnlock()
	return f.running
}

func mustParseBigInt(s string) *big.Int {
	n, ok := new(big.Int).SetString(s, 10)
	if !ok {
		return big.NewInt(0)
	}
	return n
}
