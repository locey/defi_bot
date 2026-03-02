// internal/executor/executor.go
package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"sync"
	"time"

	"github.com/defi-bot/backend/internal/models"
	"github.com/defi-bot/backend/internal/strategy"
	"github.com/defi-bot/backend/pkg/log"
	"github.com/defi-bot/backend/pkg/web3"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"gorm.io/gorm"
)

// ArbitrageExecutor 套利执行器
type ArbitrageExecutor struct {
	web3Client     *web3.Client
	contractCaller *ContractCaller
	db             *gorm.DB // 数据库连接

	// 配置
	arbitrageCoreAddress common.Address
	keeperPrivateKey     string

	// 状态
	pendingTx   map[string]*types.Transaction
	pendingTxMu sync.Mutex

	// 统计
	totalExecuted int64
	totalProfit   *big.Int
	totalGasSpent *big.Int
}

// NewArbitrageExecutor 创建执行器
func NewArbitrageExecutor(
	web3Client *web3.Client,
	arbitrageCoreAddress common.Address,
	keeperPrivateKey string,
) *ArbitrageExecutor {

	executor := &ArbitrageExecutor{
		web3Client:           web3Client,
		arbitrageCoreAddress: arbitrageCoreAddress,
		keeperPrivateKey:     keeperPrivateKey,
		pendingTx:            make(map[string]*types.Transaction),
		totalProfit:          big.NewInt(0),
		totalGasSpent:        big.NewInt(0),
	}

	executor.contractCaller = NewContractCaller(web3Client, arbitrageCoreAddress)

	return executor
}

// SetDB 设置数据库连接
func (e *ArbitrageExecutor) SetDB(db *gorm.DB) {
	e.db = db
}

// Execute 执行套利机会
func (e *ArbitrageExecutor) Execute(
	ctx context.Context,
	opp *strategy.ArbitrageOpportunity,
) (*ExecutionResult, error) {

	startTime := time.Now()

	// 1. 验证机会仍然有效
	if time.Now().After(opp.ValidUntil) {
		return nil, fmt.Errorf("opportunity expired")
	}

	// 2. 检查置信度
	if opp.Confidence < 0.7 {
		log.Warn("Low confidence opportunity: %.2f", opp.Confidence)
	}

	// 3. 构建交易参数
	// 套利路径为环形：起点 = 终点（如 WETH -> USDC -> WETH），tokenOut = 路径最后一个地址
	tokenOut := opp.SwapPath[0]
	if len(opp.SwapPath) > 1 {
		tokenOut = opp.SwapPath[len(opp.SwapPath)-1]
	}

	params := &ArbitrageParams{
		Asset:        opp.SwapPath[0],
		TokenOut:     tokenOut,
		AmountIn:     opp.AmountIn,
		SwapPath:     opp.SwapPath,
		Dexes:        opp.Dexes,
		ExpectProfit: opp.ExpectProfit,
		MinProfit:    opp.MinProfit,
		IsCex:        opp.IsCex,
	}

	// 4. 跳过 eth_call 模拟（速度优先），直接提交交易
	// 合约内部有 require(profit >= minProfit) 保护，revert 只损失 Gas
	log.Executor().Info().
		Str("path", opp.ID).
		Str("amount_in", params.AmountIn.String()).
		Int("dexes", len(params.Dexes)).
		Msg("🚀 Submitting transaction (no simulation)")

	// 5. 执行交易
	tx, err := e.contractCaller.ExecuteArbitrage(ctx, e.keeperPrivateKey, params)
	if err != nil {
		return &ExecutionResult{
			Success:   false,
			Error:     err.Error(),
			Timestamp: time.Now(),
		}, err
	}

	// 6. 记录待确认交易
	e.pendingTxMu.Lock()
	e.pendingTx[tx.Hash().Hex()] = tx
	e.pendingTxMu.Unlock()

	// 7. 等待交易确认
	receipt, err := e.waitForReceipt(ctx, tx)
	if err != nil {
		return &ExecutionResult{
			Success:   false,
			TxHash:    tx.Hash().Hex(),
			Error:     err.Error(),
			Timestamp: time.Now(),
		}, err
	}

	// 8. 解析执行结果
	result := e.parseExecutionResult(opp, tx, receipt, startTime)

	// 9. 更新统计
	if result.Success {
		e.totalExecuted++
		e.totalProfit.Add(e.totalProfit, result.ActualProfit)
		e.totalGasSpent.Add(e.totalGasSpent, result.GasCost)
	}

	// 10. 保存执行记录到数据库
	if err := e.saveExecutionRecord(opp, result); err != nil {
		log.Warn("Save execution record failed: %v", err)
	}

	// 11. 清理待确认交易
	e.pendingTxMu.Lock()
	delete(e.pendingTx, tx.Hash().Hex())
	e.pendingTxMu.Unlock()

	return result, nil
}

// waitForReceipt 等待交易确认
func (e *ArbitrageExecutor) waitForReceipt(
	ctx context.Context,
	tx *types.Transaction,
) (*types.Receipt, error) {

	// 最多等待2分钟
	timeout := time.After(2 * time.Minute)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-timeout:
			return nil, fmt.Errorf("transaction timeout")
		case <-ticker.C:
			client := e.web3Client.GetClient()
			receipt, err := client.TransactionReceipt(ctx, tx.Hash())
			if err != nil {
				continue // 还没确认
			}
			return receipt, nil
		}
	}
}

// parseExecutionResult 解析执行结果
func (e *ArbitrageExecutor) parseExecutionResult(
	opp *strategy.ArbitrageOpportunity,
	tx *types.Transaction,
	receipt *types.Receipt,
	startTime time.Time,
) *ExecutionResult {

	result := &ExecutionResult{
		TxHash:         tx.Hash().Hex(),
		GasUsed:        receipt.GasUsed,
		GasPrice:       tx.GasPrice(),
		BlockNumber:    receipt.BlockNumber.Uint64(),
		Timestamp:      time.Now(),
		ExecutionTime:  time.Since(startTime),
		OpportunityID:  opp.ID,
		PathLength:     opp.PathLength,
		ExpectedProfit: opp.ExpectProfit,
	}

	// 计算Gas成本
	result.GasCost = new(big.Int).Mul(
		new(big.Int).SetUint64(receipt.GasUsed),
		tx.GasPrice(),
	)

	if receipt.Status == 1 {
		result.Success = true
		// 从事件日志解析实际利润
		result.ActualProfit = e.parseActualProfit(receipt)
	} else {
		result.Success = false
		result.Error = "transaction reverted"
	}

	return result
}

// parseActualProfit 从事件日志解析实际利润
func (e *ArbitrageExecutor) parseActualProfit(receipt *types.Receipt) *big.Int {
	// ArbitrageCore 事件：
	// event VaultArbitrageExecuted(address indexed vault, address indexed asset, uint256 amountIn, uint256 profit, uint256 platFormFee, uint256 netProfitToVault, uint256 timestamp);

	vaultSig := crypto.Keccak256Hash([]byte("VaultArbitrageExecuted(address,address,uint256,uint256,uint256,uint256,uint256)"))

	for _, lg := range receipt.Logs {
		if len(lg.Topics) == 0 {
			continue
		}

		if lg.Topics[0] == vaultSig {
			// topics: [sig, vault, asset]
			// data: amountIn(0:32), profit(32:64), platFormFee(64:96), netProfitToVault(96:128), timestamp(128:160)
			if len(lg.Data) < 64 {
				continue
			}
			return new(big.Int).SetBytes(lg.Data[32:64]) // profit
		}
	}

	return big.NewInt(0)
}

// ExecutionResult 执行结果
type ExecutionResult struct {
	Success        bool          `json:"success"`
	TxHash         string        `json:"tx_hash"`
	GasUsed        uint64        `json:"gas_used"`
	GasPrice       *big.Int      `json:"gas_price"`
	GasCost        *big.Int      `json:"gas_cost"`
	BlockNumber    uint64        `json:"block_number"`
	Timestamp      time.Time     `json:"timestamp"`
	ExecutionTime  time.Duration `json:"execution_time"`
	OpportunityID  string        `json:"opportunity_id"`
	PathLength     int           `json:"path_length"`
	ExpectedProfit *big.Int      `json:"expected_profit"`
	ActualProfit   *big.Int      `json:"actual_profit"`
	Error          string        `json:"error,omitempty"`
}

// ArbitrageParams 套利参数（与合约 IArbitrage.ArbitrageParams 对应）
type ArbitrageParams struct {
	Asset        common.Address
	TokenOut     common.Address // 输出代币地址
	AmountIn     *big.Int
	SwapPath     []common.Address
	Dexes        []common.Address
	ExpectProfit *big.Int
	MinProfit    *big.Int
	IsCex        bool // 是否为 CEX-DEX 套利
}

// GetStats 获取统计信息
func (e *ArbitrageExecutor) GetStats() *ExecutorStats {
	return &ExecutorStats{
		TotalExecuted: e.totalExecuted,
		TotalProfit:   new(big.Int).Set(e.totalProfit),
		TotalGasSpent: new(big.Int).Set(e.totalGasSpent),
		PendingTxs:    len(e.pendingTx),
	}
}

// ExecutorStats 执行器统计
type ExecutorStats struct {
	TotalExecuted int64    `json:"total_executed"`
	TotalProfit   *big.Int `json:"total_profit"`
	TotalGasSpent *big.Int `json:"total_gas_spent"`
	PendingTxs    int      `json:"pending_txs"`
}

// ============================================================
// P1: 执行记录数据库保存
// ============================================================

// saveExecutionRecord 保存执行记录到数据库
func (e *ArbitrageExecutor) saveExecutionRecord(
	opp *strategy.ArbitrageOpportunity,
	result *ExecutionResult,
) error {
	if e.db == nil {
		return fmt.Errorf("db is nil")
	}

	// 查找代币 ID
	var tokenIn, tokenOut models.Token
	if len(opp.SwapPath) > 0 {
		e.db.Where("LOWER(address) = LOWER(?)", opp.SwapPath[0].Hex()).First(&tokenIn)
	}
	if len(opp.SwapPath) > 1 {
		e.db.Where("LOWER(address) = LOWER(?)", opp.SwapPath[len(opp.SwapPath)-1].Hex()).First(&tokenOut)
	}

	// 序列化路径
	swapPathJSON, _ := json.Marshal(addressesToStrings(opp.SwapPath))
	dexPathJSON, _ := json.Marshal(opp.DexNames)

	// 确定状态
	status := "failed"
	if result.Success {
		status = "success"
	}

	// 计算实际输出金额
	amountOut := "0"
	if result.ActualProfit != nil && opp.AmountIn != nil {
		amountOutBig := new(big.Int).Add(opp.AmountIn, result.ActualProfit)
		amountOut = amountOutBig.String()
	}

	// 计算利润率
	profitRate := 0.0
	if result.ActualProfit != nil && opp.AmountIn != nil && opp.AmountIn.Sign() > 0 {
		profitRateFloat := new(big.Float).Quo(
			new(big.Float).SetInt(result.ActualProfit),
			new(big.Float).SetInt(opp.AmountIn),
		)
		profitRate, _ = profitRateFloat.Float64()
		profitRate = profitRate * 100 // 转换为百分比
	}

	// 实际利润
	actualProfit := "0"
	if result.ActualProfit != nil {
		actualProfit = result.ActualProfit.String()
	}

	// Gas 价格
	gasPrice := "0"
	if result.GasPrice != nil {
		gasPrice = result.GasPrice.String()
	}

	// 查找对应的套利机会 ID
	var opportunityID *uint
	var dbOpp models.ArbitrageOpportunity
	if err := e.db.Where("swap_path = ? AND status = ?", string(swapPathJSON), "pending").
		Order("created_at DESC").First(&dbOpp).Error; err == nil {
		opportunityID = &dbOpp.ID
		// 更新套利机会状态
		newStatus := "executed"
		if !result.Success {
			newStatus = "failed"
		}
		e.db.Model(&dbOpp).Update("status", newStatus)
	}

	// 准备 TokenID 指针
	var tokenInIDPtr, tokenOutIDPtr *uint
	if tokenIn.ID > 0 {
		tokenInIDPtr = &tokenIn.ID
	}
	if tokenOut.ID > 0 {
		tokenOutIDPtr = &tokenOut.ID
	}

	// 创建执行记录
	execution := &models.ArbitrageExecution{
		OpportunityID:   opportunityID,
		VaultAddress:    e.arbitrageCoreAddress.Hex(),
		TokenInID:       tokenInIDPtr,
		TokenOutID:      tokenOutIDPtr,
		AmountIn:        opp.AmountIn.String(),
		AmountOut:       amountOut,
		ActualProfit:    actualProfit,
		ProfitRate:      profitRate,
		SwapPath:        string(swapPathJSON),
		DexPath:         string(dexPathJSON),
		GasUsed:         result.GasUsed,
		GasPrice:        gasPrice,
		TxHash:          result.TxHash,
		BlockNumber:     result.BlockNumber,
		Status:          status,
		ErrorMessage:    result.Error,
		ExecutionTimeMs: result.ExecutionTime.Milliseconds(),
		Timestamp:       result.Timestamp,
	}

	if err := e.db.Create(execution).Error; err != nil {
		return fmt.Errorf("create execution record failed: %w", err)
	}

	log.Info("✅ Saved execution record: txHash=%s, status=%s, profit=%s",
		result.TxHash, status, actualProfit)

	return nil
}

// addressesToStrings 将地址数组转换为字符串数组
func addressesToStrings(addrs []common.Address) []string {
	result := make([]string, len(addrs))
	for i, addr := range addrs {
		result[i] = addr.Hex()
	}
	return result
}
