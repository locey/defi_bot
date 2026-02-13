// cmd/test-executor/main.go
// 测试套利执行器模块
package main

import (
	"context"
	"fmt"
	"math/big"
	"os"

	"github.com/defi-bot/backend/internal/executor"
	"github.com/defi-bot/backend/pkg/web3"
	"github.com/ethereum/go-ethereum/common"
)

// Arbitrum 部署的合约地址
const (
	ArbitrageCoreAddress = "0x27ea15F931328474d75BE5B0a493278ba7041C74"
	VaultAddress         = "0xB7e37Fb429795E10A8D25752fFC69Fbab71df677"
	ConfigManagerAddress = "0x121D230710dc710f5AA29b73EFc8d403707173A3"
	
	// Arbitrum 主网代币
	WETH_ARB  = "0x82aF49447D8a07e3bd95BD0d56f35241523fBab1"
	USDC_ARB  = "0xaf88d065e77c8cC2239327C5EDb3A432268e5831"
	USDT_ARB  = "0xFd086bC7CD5C481DCC9C85ebE478A1C0b69FCbb9"
	
	// DEX Routers on Arbitrum
	UniswapV2Router = "0x4752ba5DBc23f44D87826276BF6Fd6b1C372aD24"
	SushiSwapRouter = "0x1b02dA8Cb0d097eB8D57A175b88c7D8b47997506"
)

func main() {
	fmt.Println("============================================================")
	fmt.Println("         套利执行器模块测试 (Arbitrum)")
	fmt.Println("============================================================")
	
	ctx := context.Background()
	
	// 1. 连接 Arbitrum RPC
	fmt.Println("\n📋 [1/5] 连接 Arbitrum RPC...")
	rpcURL := "https://arb1.arbitrum.io/rpc"
	
	web3Client, err := web3.NewClient(rpcURL, 42161, 30)
	if err != nil {
		fmt.Printf("❌ Web3 客户端创建失败: %v\n", err)
		os.Exit(1)
	}
	
	// 测试连接
	blockNum, blockErr := web3Client.GetBlockNumber()
	if blockErr != nil || blockNum == 0 {
		fmt.Printf("❌ 无法获取区块号: %v\n", blockErr)
		os.Exit(1)
	}
	fmt.Printf("✅ RPC 连接成功，当前区块: %d\n", blockNum)
	
	chainID := web3Client.GetChainID()
	fmt.Printf("   Chain ID: %d\n", chainID)
	
	// 3. 测试合约调用器
	fmt.Println("\n📋 [3/5] 测试合约调用器...")
	testContractCaller(ctx, web3Client)
	
	// 4. 测试执行器初始化
	fmt.Println("\n📋 [4/5] 测试执行器初始化...")
	testExecutorInit(web3Client)
	
	// 5. 测试 CallData 构建
	fmt.Println("\n📋 [5/5] 测试 CallData 构建...")
	testCallDataBuild(web3Client)
	
	fmt.Println("\n============================================================")
	fmt.Println("         🎉 执行器模块测试完成！")
	fmt.Println("============================================================")
}

func testContractCaller(ctx context.Context, web3Client *web3.Client) {
	contractAddr := common.HexToAddress(ArbitrageCoreAddress)
	
	// 创建合约调用器
	caller := executor.NewContractCaller(web3Client, contractAddr)
	if caller == nil {
		fmt.Println("❌ ContractCaller 创建失败")
		return
	}
	fmt.Println("✅ ContractCaller 创建成功")
	fmt.Printf("   合约地址: %s\n", ArbitrageCoreAddress)
	
	// 验证合约存在（检查代码）
	client := web3Client.GetClient()
	code, err := client.CodeAt(ctx, contractAddr, nil)
	if err != nil {
		fmt.Printf("⚠️  获取合约代码失败: %v\n", err)
	} else if len(code) == 0 {
		fmt.Println("❌ 合约地址没有代码（可能未部署）")
	} else {
		fmt.Printf("✅ 合约已部署，代码大小: %d bytes\n", len(code))
	}
	
	// 验证 Vault 合约
	vaultAddr := common.HexToAddress(VaultAddress)
	vaultCode, err := client.CodeAt(ctx, vaultAddr, nil)
	if err != nil {
		fmt.Printf("⚠️  获取 Vault 代码失败: %v\n", err)
	} else if len(vaultCode) == 0 {
		fmt.Println("❌ Vault 地址没有代码")
	} else {
		fmt.Printf("✅ Vault 已部署，代码大小: %d bytes\n", len(vaultCode))
	}
	
	// 验证 ConfigManager 合约
	configAddr := common.HexToAddress(ConfigManagerAddress)
	configCode, err := client.CodeAt(ctx, configAddr, nil)
	if err != nil {
		fmt.Printf("⚠️  获取 ConfigManager 代码失败: %v\n", err)
	} else if len(configCode) == 0 {
		fmt.Println("❌ ConfigManager 地址没有代码")
	} else {
		fmt.Printf("✅ ConfigManager 已部署，代码大小: %d bytes\n", len(configCode))
	}
}

func testExecutorInit(web3Client *web3.Client) {
	contractAddr := common.HexToAddress(ArbitrageCoreAddress)
	
	// 使用空私钥初始化（仅测试结构）
	exec := executor.NewArbitrageExecutor(
		web3Client,
		contractAddr,
		"", // 空私钥，不实际执行
	)
	
	if exec == nil {
		fmt.Println("❌ ArbitrageExecutor 创建失败")
		return
	}
	fmt.Println("✅ ArbitrageExecutor 创建成功")
	
	// 获取统计
	stats := exec.GetStats()
	fmt.Printf("   初始统计:\n")
	fmt.Printf("   - 已执行: %d\n", stats.TotalExecuted)
	fmt.Printf("   - 总利润: %s\n", stats.TotalProfit.String())
	fmt.Printf("   - Gas消耗: %s\n", stats.TotalGasSpent.String())
	fmt.Printf("   - 待确认: %d\n", stats.PendingTxs)
}

func testCallDataBuild(web3Client *web3.Client) {
	contractAddr := common.HexToAddress(ArbitrageCoreAddress)
	caller := executor.NewContractCaller(web3Client, contractAddr)
	
	// 构建测试参数 (WETH -> USDC -> WETH)
	params := &executor.ArbitrageParams{
		Asset:    common.HexToAddress(WETH_ARB),
		TokenOut: common.HexToAddress(WETH_ARB), // 环形套利：输出 = 输入代币
		AmountIn: big.NewInt(1e16), // 0.01 ETH
		SwapPath: []common.Address{
			common.HexToAddress(WETH_ARB),
			common.HexToAddress(USDC_ARB),
			common.HexToAddress(WETH_ARB),
		},
		Dexes: []common.Address{
			common.HexToAddress(UniswapV2Router),
			common.HexToAddress(SushiSwapRouter),
		},
		ExpectProfit: big.NewInt(1e14), // 0.0001 ETH
		MinProfit:    big.NewInt(1e13), // 0.00001 ETH
		IsCex:        false,            // DEX-DEX 套利
	}
	
	// 生成 CallData
	callData, err := caller.DebugCallData(params)
	if err != nil {
		fmt.Printf("❌ CallData 构建失败: %v\n", err)
		return
	}
	
	fmt.Println("✅ CallData 构建成功")
	fmt.Printf("   函数: executeStrategy(params)\n")
	fmt.Printf("   路径: WETH -> USDC -> WETH\n")
	fmt.Printf("   IsCex: false\n")
	fmt.Printf("   DEXs: Uniswap V2 -> SushiSwap\n")
	fmt.Printf("   输入金额: 0.01 ETH\n")
	fmt.Printf("   CallData 长度: %d bytes\n", len(callData)/2-1)
	
	// 显示 CallData 前缀（函数选择器）
	if len(callData) >= 10 {
		fmt.Printf("   函数选择器: %s\n", callData[:10])
	}
	
	// 模拟 Gas 估算 (不实际调用)
	fmt.Println("\n📊 模拟执行参数:")
	fmt.Printf("   To: %s\n", ArbitrageCoreAddress)
	fmt.Printf("   Value: 0\n")
	fmt.Printf("   预估 Gas: ~800,000\n")
	
	// 打印完整 CallData（截断）
	if len(callData) > 100 {
		fmt.Printf("   CallData: %s...(%d more chars)\n", callData[:100], len(callData)-100)
	} else {
		fmt.Printf("   CallData: %s\n", callData)
	}
}

// 辅助函数：缩短地址显示
func shortAddr(addr string) string {
	if len(addr) < 12 {
		return addr
	}
	return addr[:6] + "..." + addr[len(addr)-4:]
}
