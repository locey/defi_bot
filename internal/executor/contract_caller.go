// internal/executor/contract_caller.go
package executor

import (
    "context"
    "fmt"
    "math/big"
    "strings"

    "github.com/ethereum/go-ethereum/accounts/abi"
    "github.com/ethereum/go-ethereum/accounts/abi/bind"
    "github.com/ethereum/go-ethereum/common"
    "github.com/ethereum/go-ethereum/core/types"
    "your-project/pkg/web3"
)

// ArbitrageCore ABI
const ArbitrageCoreABI = `[
    {
        "inputs": [
            {
                "components": [
                    {"name": "tokenIn", "type": "address"},
                    {"name": "amountIn", "type": "uint256"},
                    {"name": "swapPath", "type": "address[]"},
                    {"name": "dexes", "type": "address[]"},
                    {"name": "minProfit", "type": "uint256"},
                    {"name": "useFlashLoan", "type": "bool"},
                    {"name": "flashLoanPlatform", "type": "uint8"}
                ],
                "name": "params",
                "type": "tuple"
            }
        ],
        "name": "executeArbitrage",
        "outputs": [{"name": "profit", "type": "uint256"}],
        "stateMutability": "nonpayable",
        "type": "function"
    }
]`

// ContractCaller 合约调用器
type ContractCaller struct {
    web3Client      *web3.Client
    contractAddress common.Address
    contractABI     abi.ABI
}

// NewContractCaller 创建合约调用器
func NewContractCaller(
    web3Client *web3.Client,
    contractAddress common.Address,
) *ContractCaller {
    
    parsedABI, err := abi.JSON(strings.NewReader(ArbitrageCoreABI))
    if err != nil {
        panic(fmt.Sprintf("failed to parse ABI: %v", err))
    }
    
    return &ContractCaller{
        web3Client:      web3Client,
        contractAddress: contractAddress,
        contractABI:     parsedABI,
    }
}

// ExecuteArbitrage 执行套利
func (cc *ContractCaller) ExecuteArbitrage(
    ctx context.Context,
    params *ArbitrageParams,
) (*types.Transaction, error) {
    
    // 1. 构建调用数据
    callData, err := cc.buildCallData(params)
    if err != nil {
        return nil, fmt.Errorf("build call data: %w", err)
    }
    
    // 2. 获取交易选项
    auth, err := cc.web3Client.GetTransactOpts(ctx)
    if err != nil {
        return nil, fmt.Errorf("get transact opts: %w", err)
    }
    
    // 3. 估算Gas
    gasLimit, err := cc.estimateGas(ctx, callData)
    if err != nil {
        // 使用默认Gas限制
        gasLimit = 800000
    }
    auth.GasLimit = gasLimit
    
    // 4. 发送交易
    tx, err := cc.sendTransaction(ctx, auth, callData)
    if err != nil {
        return nil, fmt.Errorf("send transaction: %w", err)
    }
    
    return tx, nil
}

// buildCallData 构建调用数据
func (cc *ContractCaller) buildCallData(params *ArbitrageParams) ([]byte, error) {
    
    // 构建参数结构体
    paramsStruct := struct {
        TokenIn           common.Address
        AmountIn          *big.Int
        SwapPath          []common.Address
        Dexes             []common.Address
        MinProfit         *big.Int
        UseFlashLoan      bool
        FlashLoanPlatform uint8
    }{
        TokenIn:           params.TokenIn,
        AmountIn:          params.AmountIn,
        SwapPath:          params.SwapPath,
        Dexes:             params.Dexes,
        MinProfit:         params.MinProfit,
        UseFlashLoan:      params.UseFlashLoan,
        FlashLoanPlatform: 0, // Aave V2
    }
    
    // 编码调用数据
    callData, err := cc.contractABI.Pack("executeArbitrage", paramsStruct)
    if err != nil {
        return nil, err
    }
    
    return callData, nil
}

// estimateGas 估算Gas
func (cc *ContractCaller) estimateGas(
    ctx context.Context,
    callData []byte,
) (uint64, error) {
    
    gasLimit, err := cc.web3Client.EstimateGasForCall(
        ctx,
        cc.contractAddress,
        callData,
    )
    if err != nil {
        return 0, err
    }
    
    // 添加20%安全边际
    return gasLimit * 120 / 100, nil
}

// sendTransaction 发送交易
func (cc *ContractCaller) sendTransaction(
    ctx context.Context,
    auth *bind.TransactOpts,
    callData []byte,
) (*types.Transaction, error) {
    
    // 获取nonce
    nonce, err := cc.web3Client.PendingNonceAt(ctx, auth.From)
    if err != nil {
        return nil, err
    }
    
    // 获取Gas价格
    gasPrice, err := cc.web3Client.SuggestGasPrice(ctx)
    if err != nil {
        return nil, err
    }
    
    // 构建交易
    tx := types.NewTransaction(
        nonce,
        cc.contractAddress,
        big.NewInt(0), // value
        auth.GasLimit,
        gasPrice,
        callData,
    )
    
    // 签名交易
    signedTx, err := auth.Signer(auth.From, tx)
    if err != nil {
        return nil, err
    }
    
    // 发送交易
    err = cc.web3Client.SendTransaction(ctx, signedTx)
    if err != nil {
        return nil, err
    }
    
    return signedTx, nil
}

// SimulateArbitrage 模拟套利（不发送交易）
func (cc *ContractCaller) SimulateArbitrage(
    ctx context.Context,
    params *ArbitrageParams,
) (*big.Int, error) {
    
    callData, err := cc.buildCallData(params)
    if err != nil {
        return nil, err
    }
    
    // 调用而不发送交易
    result, err := cc.web3Client.CallContract(ctx, cc.contractAddress, callData)
    if err != nil {
        return nil, err
    }
    
    // 解析返回值
    profit := new(big.Int).SetBytes(result)
    return profit, nil
}