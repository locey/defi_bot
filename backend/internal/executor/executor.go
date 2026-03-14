// internal/executor/executor.go
package executor

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"strings"
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
	flashLoanAddress     common.Address // FlashLoanArbitrage 合约地址
	keeperPrivateKey     string

	// 状态
	pendingTx   map[string]*types.Transaction
	pendingTxMu sync.Mutex

	// 统计
	totalExecuted int64
	totalProfit   *big.Int
	totalGasSpent *big.Int

	// 安全：日累计 Gas 损失限制
	dailyGasLoss   *big.Int  // 当日累计 revert Gas 损失（wei）
	dailyGasLossMu sync.Mutex
	dailyGasDate   string // 日期字符串，用于日切重置
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
		dailyGasLoss:         big.NewInt(0),
		dailyGasDate:         time.Now().Format("2006-01-02"),
	}

	executor.contractCaller = NewContractCaller(web3Client, arbitrageCoreAddress)

	// 设置 Keeper 地址到 ContractCaller（用于 eth_call 的 From 字段）
	if keeperPrivateKey != "" {
		pk, err := crypto.HexToECDSA(strings.TrimPrefix(keeperPrivateKey, "0x"))
		if err == nil {
			keeperAddr := crypto.PubkeyToAddress(pk.PublicKey)
			executor.contractCaller.SetKeeperAddress(keeperAddr)
		}
	}

	return executor
}

// SetDB 设置数据库连接
func (e *ArbitrageExecutor) SetDB(db *gorm.DB) {
	e.db = db
}

// SetFlashLoanAddress 设置 FlashLoanArbitrage 合约地址
func (e *ArbitrageExecutor) SetFlashLoanAddress(addr common.Address) {
	e.flashLoanAddress = addr
	if e.contractCaller != nil {
		e.contractCaller.SetFlashLoanAddress(addr)
	}
}

// HasFlashLoan 检查是否配置了 Flash Loan 合约
func (e *ArbitrageExecutor) HasFlashLoan() bool {
	return e.flashLoanAddress != (common.Address{})
}

// DailyGasLossExceeded 检查日累计 Gas 损失是否超限（默认上限 $5 ≈ 0.002 ETH ≈ 2e15 wei）
func (e *ArbitrageExecutor) DailyGasLossExceeded() bool {
	e.dailyGasLossMu.Lock()
	defer e.dailyGasLossMu.Unlock()

	today := time.Now().Format("2006-01-02")
	if today != e.dailyGasDate {
		e.dailyGasLoss = big.NewInt(0)
		e.dailyGasDate = today
	}

	// 上限 0.005 ETH ≈ $12.5（按 ETH=$2500 估算，给 Flash Loan 更多试错空间）
	limit := new(big.Int).SetUint64(5_000_000_000_000_000) // 5e15 wei
	return e.dailyGasLoss.Cmp(limit) >= 0
}

// recordGasLoss 记录 revert 导致的 Gas 损失
func (e *ArbitrageExecutor) recordGasLoss(gasCost *big.Int) {
	if gasCost == nil || gasCost.Sign() <= 0 {
		return
	}
	e.dailyGasLossMu.Lock()
	defer e.dailyGasLossMu.Unlock()

	today := time.Now().Format("2006-01-02")
	if today != e.dailyGasDate {
		e.dailyGasLoss = big.NewInt(0)
		e.dailyGasDate = today
	}
	e.dailyGasLoss.Add(e.dailyGasLoss, gasCost)
}

// GetVaultAvailable 查询指定 token 对应 Vault 的可用余额
// 供调度器在执行前 cap amountIn，防止 `amountIn too much` revert
func (e *ArbitrageExecutor) GetVaultAvailable(ctx context.Context, asset common.Address) (*big.Int, error) {
	if e.contractCaller == nil {
		return nil, fmt.Errorf("contract caller not initialized")
	}
	return e.contractCaller.GetVaultAvailable(ctx, asset)
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

	// 确保 FeeTiers 长度与 Dexes 一致（合约要求 feeTiers.length == dexes.length）
	feeTiers := opp.FeeTiers
	if len(feeTiers) != len(opp.Dexes) {
		feeTiers = make([]uint32, len(opp.Dexes)) // 默认全 0 (V2)
	}

	params := &ArbitrageParams{
		Asset:        opp.SwapPath[0],
		TokenOut:     tokenOut,
		AmountIn:     opp.AmountIn,
		SwapPath:     opp.SwapPath,
		Dexes:        opp.Dexes,
		FeeTiers:     feeTiers,
		ExpectProfit: opp.ExpectProfit,
		MinProfit:    opp.MinProfit,
		IsCex:        opp.IsCex,
	}

	// 4. 日累计 Gas 损失检查（防止 bug 快速烧完 Gas）
	if e.DailyGasLossExceeded() {
		return nil, fmt.Errorf("daily gas loss limit exceeded, execution paused")
	}

	// eth_call 二次模拟验证（executor 层安全门，防止提交会 revert 的交易浪费 gas）
	if e.contractCaller != nil {
		simGas, simErr := e.contractCaller.SimulateArbitrage(ctx, params)
		if simErr != nil {
			log.Executor().Warn().Err(simErr).Str("path", opp.ID).Msg("Executor simulation reverted, skipping (saved gas)")
			return nil, fmt.Errorf("executor simulation failed: %w", simErr)
		}
		log.Executor().Info().
			Str("path", opp.ID).
			Str("amount_in", params.AmountIn.String()).
			Int("dexes", len(params.Dexes)).
			Str("sim_gas", simGas.String()).
			Msg("🚀 Simulation passed, submitting transaction")
	}

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

// ExecuteWithFlashLoan 通过 Flash Loan 执行套利（零资本风险）
// Flash Loan 路径：借 Aave 资金 → 执行套利 → 还款 + 利润
// 金额由池子流动性决定，不依赖 Vault 余额
func (e *ArbitrageExecutor) ExecuteWithFlashLoan(
	ctx context.Context,
	opp *strategy.ArbitrageOpportunity,
) (*ExecutionResult, error) {
	startTime := time.Now()

	if !e.HasFlashLoan() {
		return nil, fmt.Errorf("flash loan contract not configured")
	}

	if time.Now().After(opp.ValidUntil) {
		return nil, fmt.Errorf("opportunity expired")
	}

	// 日累计 Gas 损失检查
	if e.DailyGasLossExceeded() {
		return nil, fmt.Errorf("daily gas loss limit exceeded, execution paused")
	}

	// 构建 Flash Loan 参数
	flParams := &FlashLoanParams{
		Platform:     1, // Aave_V3 (Arbitrum)
		TokenIn:      opp.SwapPath[0],
		AmountIn:     opp.AmountIn,
		SwapPath:     opp.SwapPath,
		Dexes:        opp.Dexes,
		FeeTiers:     opp.FeeTiers,
		ExpectProfit: opp.ExpectProfit,
		MinProfit:    opp.MinProfit,
	}

	// 调试：打印 FlashLoan 参数
	pathStrs := make([]string, len(flParams.SwapPath))
	for i, p := range flParams.SwapPath {
		pathStrs[i] = p.Hex()[:10]
	}
	dexStrs := make([]string, len(flParams.Dexes))
	for i, d := range flParams.Dexes {
		dexStrs[i] = d.Hex()[:10]
	}
	log.Executor().Info().
		Str("path", opp.ID).
		Uint8("platform", flParams.Platform).
		Str("tokenIn", flParams.TokenIn.Hex()[:14]).
		Str("amountIn", flParams.AmountIn.String()).
		Strs("swapPath", pathStrs).
		Strs("dexes", dexStrs).
		Uints32("feeTiers", flParams.FeeTiers).
		Str("minProfit", flParams.MinProfit.String()).
		Msg("FlashLoan simulation params")

	// eth_call 预检：避免提交 revert 交易浪费 gas
	simGas, simErr := e.contractCaller.SimulateFlashLoan(ctx, flParams)
	if simErr != nil {
		log.Executor().Warn().Err(simErr).Str("path", opp.ID).Msg("Flash Loan simulation reverted, skipping")
		return nil, fmt.Errorf("flash loan simulation failed: %w", simErr)
	}

	log.Executor().Info().
		Str("path", opp.ID).
		Str("amount_in", flParams.AmountIn.String()).
		Str("token", flParams.TokenIn.Hex()[:14]).
		Int("dexes", len(flParams.Dexes)).
		Str("sim_gas", simGas.String()).
		Msg("⚡ Flash Loan simulation passed, submitting tx")

	// 执行 Flash Loan 交易
	tx, err := e.contractCaller.ExecuteFlashLoanArbitrage(ctx, e.keeperPrivateKey, flParams)
	if err != nil {
		return &ExecutionResult{
			Success:   false,
			Error:     err.Error(),
			Timestamp: time.Now(),
		}, err
	}

	// 记录待确认交易
	e.pendingTxMu.Lock()
	e.pendingTx[tx.Hash().Hex()] = tx
	e.pendingTxMu.Unlock()

	// 等待确认
	receipt, err := e.waitForReceipt(ctx, tx)
	if err != nil {
		return &ExecutionResult{
			Success:   false,
			TxHash:    tx.Hash().Hex(),
			Error:     err.Error(),
			Timestamp: time.Now(),
		}, err
	}

	// 解析结果
	result := e.parseExecutionResult(opp, tx, receipt, startTime)

	if result.Success {
		e.totalExecuted++
		e.totalProfit.Add(e.totalProfit, result.ActualProfit)
		e.totalGasSpent.Add(e.totalGasSpent, result.GasCost)
	}

	if err := e.saveExecutionRecord(opp, result); err != nil {
		log.Warn("Save flash loan execution record failed: %v", err)
	}

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

	// 最多等待2分钟，每 250ms 轮询一次（Arbitrum 出块约 250ms）
	timeout := time.After(2 * time.Minute)
	ticker := time.NewTicker(250 * time.Millisecond)
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

		// 预期 vs 实际利润对比（调参用）
		if opp.ExpectProfit != nil && result.ActualProfit != nil && opp.ExpectProfit.Sign() > 0 {
			variance := new(big.Int).Sub(result.ActualProfit, opp.ExpectProfit)
			variancePct := new(big.Float).Quo(
				new(big.Float).SetInt(variance),
				new(big.Float).SetInt(opp.ExpectProfit),
			)
			variancePctFloat, _ := variancePct.Float64()
			log.Executor().Info().
				Str("tx", result.TxHash[:14]).
				Str("expected", opp.ExpectProfit.String()).
				Str("actual", result.ActualProfit.String()).
				Float64("variance_pct", variancePctFloat*100).
				Msg("📊 Profit variance: expected vs actual")
		}
	} else {
		result.Success = false
		// 解析 revert 原因
		result.Error = parseRevertReason(receipt, tx)
		// 记录 revert Gas 损失到日累计
		e.recordGasLoss(result.GasCost)
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
			// 报表使用机器人净收益（netProfitToVault），不含平台费
			if len(lg.Data) >= 128 {
				return new(big.Int).SetBytes(lg.Data[96:128]) // netProfitToVault
			}
			if len(lg.Data) >= 64 {
				return new(big.Int).SetBytes(lg.Data[32:64]) // fallback: profit
			}
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
	FeeTiers     []uint32 // 每步 V3 fee tier (500/3000/10000); 0 = V2
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

// parseRevertReason 从 receipt 和 tx 解析 revert 原因
// 已知合约 revert 字符串：
//   - "DoubleRouter: insufficient profit"
//   - "ArbitrageCore: no profit"
//   - "ArbitrageCore: amountIn too much"
//   - "insufficient balance" (ERC20)
func parseRevertReason(receipt *types.Receipt, tx *types.Transaction) string {
	// receipt.Status == 0 表示 revert
	// Arbitrum 不在 receipt 中附带 revert reason，需要用 eth_call 重放获取
	// 但我们可以根据 gas 使用量推断原因
	if receipt.GasUsed < 50000 {
		return "reverted early (likely permission/validation check)"
	}
	if receipt.GasUsed > 900000 {
		return "reverted late (likely insufficient profit after swaps)"
	}
	return fmt.Sprintf("transaction reverted (gas_used=%d)", receipt.GasUsed)
}

// addressesToStrings 将地址数组转换为字符串数组
func addressesToStrings(addrs []common.Address) []string {
	result := make([]string, len(addrs))
	for i, addr := range addrs {
		result[i] = addr.Hex()
	}
	return result
}
