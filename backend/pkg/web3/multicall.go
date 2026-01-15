// pkg/web3/multicall.go
// Multicall3批量查询，大幅减少RPC调用次数
package web3

import (
	"context"
	"fmt"
	"math/big"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
)

// Multicall3 地址（所有主流链相同）
const Multicall3Address = "0xcA11bde05977b3631167028862bE2a173976CA11"

// Multicall3 ABI (aggregate3)
const multicall3ABI = `[
	{
		"inputs": [
			{
				"components": [
					{"name": "target", "type": "address"},
					{"name": "allowFailure", "type": "bool"},
					{"name": "callData", "type": "bytes"}
				],
				"name": "calls",
				"type": "tuple[]"
			}
		],
		"name": "aggregate3",
		"outputs": [
			{
				"components": [
					{"name": "success", "type": "bool"},
					{"name": "returnData", "type": "bytes"}
				],
				"name": "returnData",
				"type": "tuple[]"
			}
		],
		"stateMutability": "payable",
		"type": "function"
	}
]`

// UniswapV2 Pair ABI (getReserves)
const uniswapV2PairABI = `[
	{
		"constant": true,
		"inputs": [],
		"name": "getReserves",
		"outputs": [
			{"name": "_reserve0", "type": "uint112"},
			{"name": "_reserve1", "type": "uint112"},
			{"name": "_blockTimestampLast", "type": "uint32"}
		],
		"payable": false,
		"stateMutability": "view",
		"type": "function"
	}
]`

// ERC20 ABI (balanceOf)
const erc20ABI = `[
	{
		"constant": true,
		"inputs": [{"name": "_owner", "type": "address"}],
		"name": "balanceOf",
		"outputs": [{"name": "balance", "type": "uint256"}],
		"payable": false,
		"stateMutability": "view",
		"type": "function"
	}
]`

// Call3 单个调用
type Call3 struct {
	Target       common.Address
	AllowFailure bool
	CallData     []byte
}

// Result3 单个结果
type Result3 struct {
	Success    bool
	ReturnData []byte
}

// ReserveResult 储备量查询结果
type ReserveResult struct {
	PoolAddress    string
	Reserve0       *big.Int
	Reserve1       *big.Int
	BlockTimestamp uint32
	Success        bool
	Error          error
}

// Multicall Multicall3客户端
type Multicall struct {
	client           *Client
	multicallAddr    common.Address
	multicallABI     abi.ABI
	pairABI          abi.ABI
	erc20ABI         abi.ABI
	maxCallsPerBatch int
	timeout          time.Duration
}

// NewMulticall 创建Multicall客户端
func NewMulticall(client *Client, maxCallsPerBatch int, timeout time.Duration) (*Multicall, error) {
	if maxCallsPerBatch <= 0 {
		maxCallsPerBatch = 500 // 默认每批500个调用
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	multicallABI, err := abi.JSON(strings.NewReader(multicall3ABI))
	if err != nil {
		return nil, fmt.Errorf("parse multicall ABI failed: %w", err)
	}

	pairABI, err := abi.JSON(strings.NewReader(uniswapV2PairABI))
	if err != nil {
		return nil, fmt.Errorf("parse pair ABI failed: %w", err)
	}

	erc20ABI, err := abi.JSON(strings.NewReader(erc20ABI))
	if err != nil {
		return nil, fmt.Errorf("parse erc20 ABI failed: %w", err)
	}

	return &Multicall{
		client:           client,
		multicallAddr:    common.HexToAddress(Multicall3Address),
		multicallABI:     multicallABI,
		pairABI:          pairABI,
		erc20ABI:         erc20ABI,
		maxCallsPerBatch: maxCallsPerBatch,
		timeout:          timeout,
	}, nil
}

// GetReservesBatch 批量获取多个池子的储备量
// 这是套利机器人最常用的方法，一次RPC调用获取500个池子的数据
func (m *Multicall) GetReservesBatch(ctx context.Context, poolAddrs []string) ([]*ReserveResult, error) {
	if len(poolAddrs) == 0 {
		return nil, nil
	}

	results := make([]*ReserveResult, 0, len(poolAddrs))

	// 分批处理
	for i := 0; i < len(poolAddrs); i += m.maxCallsPerBatch {
		end := i + m.maxCallsPerBatch
		if end > len(poolAddrs) {
			end = len(poolAddrs)
		}

		batch := poolAddrs[i:end]
		batchResults, err := m.executeReservesBatch(ctx, batch)
		if err != nil {
			// 如果整批失败，为每个地址返回错误
			for _, addr := range batch {
				results = append(results, &ReserveResult{
					PoolAddress: addr,
					Success:     false,
					Error:       err,
				})
			}
			continue
		}

		results = append(results, batchResults...)
	}

	return results, nil
}

// executeReservesBatch 执行单批储备量查询
func (m *Multicall) executeReservesBatch(ctx context.Context, poolAddrs []string) ([]*ReserveResult, error) {
	// 构建调用数据
	getReservesData, err := m.pairABI.Pack("getReserves")
	if err != nil {
		return nil, fmt.Errorf("pack getReserves failed: %w", err)
	}

	calls := make([]Call3, len(poolAddrs))
	for i, addr := range poolAddrs {
		calls[i] = Call3{
			Target:       common.HexToAddress(addr),
			AllowFailure: true, // 允许单个调用失败
			CallData:     getReservesData,
		}
	}

	// 执行multicall
	rawResults, err := m.executeMulticall(ctx, calls)
	if err != nil {
		return nil, err
	}

	// 解析结果
	results := make([]*ReserveResult, len(poolAddrs))
	for i, raw := range rawResults {
		result := &ReserveResult{
			PoolAddress: poolAddrs[i],
			Success:     raw.Success,
		}

		if raw.Success && len(raw.ReturnData) >= 64 {
			// 解析getReserves返回值
			reserve0 := new(big.Int).SetBytes(raw.ReturnData[0:32])
			reserve1 := new(big.Int).SetBytes(raw.ReturnData[32:64])
			var blockTimestamp uint32
			if len(raw.ReturnData) >= 96 {
				blockTimestamp = uint32(new(big.Int).SetBytes(raw.ReturnData[64:96]).Uint64())
			}

			result.Reserve0 = reserve0
			result.Reserve1 = reserve1
			result.BlockTimestamp = blockTimestamp
		} else if !raw.Success {
			result.Error = fmt.Errorf("call failed")
		} else {
			result.Error = fmt.Errorf("invalid return data length: %d", len(raw.ReturnData))
		}

		results[i] = result
	}

	return results, nil
}

// GetBalancesBatch 批量获取代币余额
func (m *Multicall) GetBalancesBatch(ctx context.Context, tokenAddr string, owners []string) ([]*big.Int, error) {
	if len(owners) == 0 {
		return nil, nil
	}

	results := make([]*big.Int, len(owners))

	// 分批处理
	for i := 0; i < len(owners); i += m.maxCallsPerBatch {
		end := i + m.maxCallsPerBatch
		if end > len(owners) {
			end = len(owners)
		}

		batch := owners[i:end]
		batchResults, err := m.executeBalancesBatch(ctx, tokenAddr, batch)
		if err != nil {
			// 失败的设为0
			for j := range batch {
				results[i+j] = big.NewInt(0)
			}
			continue
		}

		copy(results[i:], batchResults)
	}

	return results, nil
}

// executeBalancesBatch 执行单批余额查询
func (m *Multicall) executeBalancesBatch(ctx context.Context, tokenAddr string, owners []string) ([]*big.Int, error) {
	calls := make([]Call3, len(owners))
	token := common.HexToAddress(tokenAddr)

	for i, owner := range owners {
		callData, err := m.erc20ABI.Pack("balanceOf", common.HexToAddress(owner))
		if err != nil {
			return nil, fmt.Errorf("pack balanceOf failed: %w", err)
		}
		calls[i] = Call3{
			Target:       token,
			AllowFailure: true,
			CallData:     callData,
		}
	}

	rawResults, err := m.executeMulticall(ctx, calls)
	if err != nil {
		return nil, err
	}

	results := make([]*big.Int, len(owners))
	for i, raw := range rawResults {
		if raw.Success && len(raw.ReturnData) >= 32 {
			results[i] = new(big.Int).SetBytes(raw.ReturnData[0:32])
		} else {
			results[i] = big.NewInt(0)
		}
	}

	return results, nil
}

// executeMulticall 执行multicall调用
func (m *Multicall) executeMulticall(ctx context.Context, calls []Call3) ([]Result3, error) {
	// 设置超时
	ctx, cancel := context.WithTimeout(ctx, m.timeout)
	defer cancel()

	// 将Call3转换为ABI编码需要的格式
	type abiCall struct {
		Target       common.Address
		AllowFailure bool
		CallData     []byte
	}

	abiCalls := make([]abiCall, len(calls))
	for i, c := range calls {
		abiCalls[i] = abiCall{
			Target:       c.Target,
			AllowFailure: c.AllowFailure,
			CallData:     c.CallData,
		}
	}

	// 编码调用数据
	callData, err := m.multicallABI.Pack("aggregate3", abiCalls)
	if err != nil {
		return nil, fmt.Errorf("pack aggregate3 failed: %w", err)
	}

	// 执行调用
	msg := ethereum.CallMsg{
		To:   &m.multicallAddr,
		Data: callData,
	}

	result, err := m.client.GetClient().CallContract(ctx, msg, nil)
	if err != nil {
		return nil, fmt.Errorf("call contract failed: %w", err)
	}

	// 解码结果
	type abiResult struct {
		Success    bool
		ReturnData []byte
	}

	var abiResults []abiResult

	// 手动解析返回数据（aggregate3返回tuple数组）
	decoded, err := m.multicallABI.Unpack("aggregate3", result)
	if err != nil {
		return nil, fmt.Errorf("unpack aggregate3 failed: %w", err)
	}

	if len(decoded) == 0 {
		return nil, fmt.Errorf("empty result")
	}

	// 转换结果
	rawResults, ok := decoded[0].([]struct {
		Success    bool   `json:"success"`
		ReturnData []byte `json:"returnData"`
	})

	if !ok {
		return nil, fmt.Errorf("invalid result type")
	}

	results := make([]Result3, len(rawResults))
	for i, r := range rawResults {
		results[i] = Result3{
			Success:    r.Success,
			ReturnData: r.ReturnData,
		}
	}

	_ = abiResults // 消除未使用变量警告

	return results, nil
}

// ============================================================
// 便捷方法
// ============================================================

// GetSingleReserves 获取单个池子的储备量（用于测试或单个查询）
func (m *Multicall) GetSingleReserves(ctx context.Context, poolAddr string) (*ReserveResult, error) {
	results, err := m.GetReservesBatch(ctx, []string{poolAddr})
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, fmt.Errorf("no result")
	}
	return results[0], nil
}

// BatchSize 返回当前批大小设置
func (m *Multicall) BatchSize() int {
	return m.maxCallsPerBatch
}

// SetBatchSize 设置批大小
func (m *Multicall) SetBatchSize(size int) {
	if size > 0 {
		m.maxCallsPerBatch = size
	}
}
