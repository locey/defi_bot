// internal/executor/executor.go
package executor

import (
    "context"
    "fmt"
    "log"
    "math/big"
    "sync"
    "time"

    "github.com/ethereum/go-ethereum/accounts/abi/bind"
    "github.com/ethereum/go-ethereum/common"
    "github.com/ethereum/go-ethereum/core/types"
    "your-project/internal/strategy"
    "your-project/pkg/web3"
)

// ArbitrageExecutor 套利执行器
type ArbitrageExecutor struct {
    web3Client     *web3.Client
    contractCaller *ContractCaller
    
    // 配置
    arbitrageCoreAddress common.Address
    keeperPrivateKey     string
    
    // 状态
    pendingTx      map[string]*types.Transaction
    pendingTxMu    sync.Mutex
    
    // 统计
    totalExecuted  int64
    totalProfit    *big.Int
    totalGasSpent  *big.Int
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
        log.Printf("Low confidence opportunity: %.2f", opp.Confidence)
    }
    
    // 3. 构建交易参数
    params := &ArbitrageParams{
        TokenIn:     opp.SwapPath[0],
        AmountIn:    opp.AmountIn,
        SwapPath:    opp.SwapPath,
        Dexes:       opp.Dexes,
        MinProfit:   opp.MinProfit,
        UseFlashLoan: true, // 平台模式使用闪电贷
    }
    
    // 4. 执行交易
    tx, err := e.contractCaller.ExecuteArbitrage(ctx, params)
    if err != nil {
        return &ExecutionResult{
            Success:   false,
            Error:     err.Error(),
            Timestamp: time.Now(),
        }, err
    }
    
    // 5. 记录待确认交易
    e.pendingTxMu.Lock()
    e.pendingTx[tx.Hash().Hex()] = tx
    e.pendingTxMu.Unlock()
    
    // 6. 等待交易确认
    receipt, err := e.waitForReceipt(ctx, tx)
    if err != nil {
        return &ExecutionResult{
            Success:   false,
            TxHash:    tx.Hash().Hex(),
            Error:     err.Error(),
            Timestamp: time.Now(),
        }, err
    }
    
    // 7. 解析执行结果
    result := e.parseExecutionResult(opp, tx, receipt, startTime)
    
    // 8. 更新统计
    if result.Success {
        e.totalExecuted++
        e.totalProfit.Add(e.totalProfit, result.ActualProfit)
        e.totalGasSpent.Add(e.totalGasSpent, result.GasCost)
    }
    
    // 9. 清理待确认交易
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
            receipt, err := e.web3Client.TransactionReceipt(ctx, tx.Hash())
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
    // 解析ArbitrageExecuted事件
    // 这里需要根据你的合约事件定义来实现
    
    for _, vLog := range receipt.Logs {
        // 检查事件签名
        // event ArbitrageExecuted(address indexed initiator, ...)
        // 解析利润字段
    }
    
    return big.NewInt(0) // 默认返回0，需要实现解析逻辑
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

// ArbitrageParams 套利参数
type ArbitrageParams struct {
    TokenIn      common.Address
    AmountIn     *big.Int
    SwapPath     []common.Address
    Dexes        []common.Address
    MinProfit    *big.Int
    UseFlashLoan bool
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