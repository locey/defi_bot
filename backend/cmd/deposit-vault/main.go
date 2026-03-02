// cmd/deposit-vault/main.go
// 自动化脚本：将 ETH 包装成 WETH 并存入 ArbitrageVault
// 用法: go run cmd/deposit-vault/main.go -amount 0.001
package main

import (
	"context"
	"crypto/ecdsa"
	"flag"
	"fmt"
	"math/big"
	"os"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/joho/godotenv"
)

var (
	amountETH = flag.Float64("amount", 0.001, "存入的 ETH 数量（会先包装成 WETH）")
	dryRun    = flag.Bool("dry-run", false, "只模拟不执行")
	rpcURL    = flag.String("rpc", "", "RPC URL（默认从 .env 读取）")
)

// 合约地址（Arbitrum One）
var (
	wethAddr          = common.HexToAddress("0x82aF49447D8a07e3bd95BD0d56f35241523fBab1")
	vaultAddr         = common.HexToAddress("0xBE312B103f89489Df5bFf0A59d327c4C8B19d94F")
	arbitrageCoreAddr = common.HexToAddress("0x0D14428b4e297344C2C51D087934a3e3a9b822B9")
)

// ABI 定义
const wethABI = `[{"constant":false,"inputs":[],"name":"deposit","outputs":[],"payable":true,"stateMutability":"payable","type":"function"},{"constant":false,"inputs":[{"name":"spender","type":"address"},{"name":"amount","type":"uint256"}],"name":"approve","outputs":[{"name":"","type":"bool"}],"stateMutability":"nonpayable","type":"function"},{"constant":true,"inputs":[{"name":"account","type":"address"}],"name":"balanceOf","outputs":[{"name":"","type":"uint256"}],"stateMutability":"view","type":"function"}]`

const vaultABI = `[{"inputs":[{"name":"assets","type":"uint256"},{"name":"receiver","type":"address"}],"name":"deposit","outputs":[{"name":"shares","type":"uint256"}],"stateMutability":"nonpayable","type":"function"},{"inputs":[],"name":"totalAssets","outputs":[{"name":"","type":"uint256"}],"stateMutability":"view","type":"function"},{"inputs":[],"name":"paused","outputs":[{"name":"","type":"bool"}],"stateMutability":"view","type":"function"},{"inputs":[],"name":"minDepositAmount","outputs":[{"name":"","type":"uint256"}],"stateMutability":"view","type":"function"}]`

func main() {
	flag.Parse()

	// 加载 .env
	godotenv.Load("../.env")
	godotenv.Load(".env")

	privateKeyHex := os.Getenv("KEEPER_PRIVATE_KEY")
	if privateKeyHex == "" {
		fmt.Println("错误: KEEPER_PRIVATE_KEY 未设置")
		os.Exit(1)
	}

	rpc := *rpcURL
	if rpc == "" {
		rpc = os.Getenv("BLOCKCHAIN_RPC_URL")
	}
	if rpc == "" {
		rpc = "https://arb1.arbitrum.io/rpc"
	}

	// 连接 RPC
	ctx := context.Background()
	client, err := ethclient.Dial(rpc)
	if err != nil {
		fmt.Printf("RPC 连接失败: %v\n", err)
		os.Exit(1)
	}

	// 解析私钥
	privateKey, err := crypto.HexToECDSA(strings.TrimPrefix(privateKeyHex, "0x"))
	if err != nil {
		fmt.Printf("私钥无效: %v\n", err)
		os.Exit(1)
	}
	fromAddr := crypto.PubkeyToAddress(privateKey.PublicKey)

	// 金额
	amountWei := ethToWei(*amountETH)

	fmt.Println("========================================")
	fmt.Println("  Vault 存款脚本 (Arbitrum One)")
	fmt.Println("========================================")
	fmt.Printf("  钱包地址:   %s\n", fromAddr.Hex())
	fmt.Printf("  存入金额:   %f ETH → WETH\n", *amountETH)
	fmt.Printf("  Vault 地址: %s\n", vaultAddr.Hex())
	fmt.Printf("  Dry Run:    %v\n", *dryRun)
	fmt.Println()

	// 检查 ETH 余额
	ethBalance, err := client.BalanceAt(ctx, fromAddr, nil)
	if err != nil {
		fmt.Printf("获取余额失败: %v\n", err)
		os.Exit(1)
	}
	ethFloat := new(big.Float).Quo(new(big.Float).SetInt(ethBalance), new(big.Float).SetFloat64(1e18))
	fmt.Printf("  当前 ETH 余额: %s\n", ethFloat.Text('f', 6))

	if ethBalance.Cmp(amountWei) < 0 {
		fmt.Printf("  错误: ETH 余额不足（需要 %s wei，有 %s wei）\n", amountWei.String(), ethBalance.String())
		os.Exit(1)
	}

	// 检查 WETH 余额
	wethParsed, _ := abi.JSON(strings.NewReader(wethABI))
	wethBalanceData, _ := wethParsed.Pack("balanceOf", fromAddr)
	wethBalanceResult, err := client.CallContract(ctx, ethereum.CallMsg{To: &wethAddr, Data: wethBalanceData}, nil)
	if err == nil {
		wethBal := new(big.Int).SetBytes(wethBalanceResult)
		wethFloat := new(big.Float).Quo(new(big.Float).SetInt(wethBal), new(big.Float).SetFloat64(1e18))
		fmt.Printf("  当前 WETH 余额: %s\n", wethFloat.Text('f', 6))
	}

	// 检查 Vault 状态
	vaultParsed, _ := abi.JSON(strings.NewReader(vaultABI))
	totalAssetsData, _ := vaultParsed.Pack("totalAssets")
	totalResult, _ := client.CallContract(ctx, ethereum.CallMsg{To: &vaultAddr, Data: totalAssetsData}, nil)
	if len(totalResult) > 0 {
		total := new(big.Int).SetBytes(totalResult)
		totalFloat := new(big.Float).Quo(new(big.Float).SetInt(total), new(big.Float).SetFloat64(1e18))
		fmt.Printf("  Vault 当前总资产: %s WETH\n", totalFloat.Text('f', 6))
	}

	fmt.Println()

	if *dryRun {
		fmt.Println("=== Dry Run 模式，不发送交易 ===")
		fmt.Println("去掉 -dry-run 参数执行真实交易")
		return
	}

	// 确认
	fmt.Printf("确认存入 %f ETH 到 Vault？(y/N): ", *amountETH)
	var confirm string
	fmt.Scanln(&confirm)
	if strings.ToLower(confirm) != "y" {
		fmt.Println("已取消")
		return
	}

	chainID, _ := client.ChainID(ctx)

	// Step 1: 包装 ETH → WETH
	fmt.Println("\n=== Step 1/3: 包装 ETH → WETH ===")
	wrapData, _ := wethParsed.Pack("deposit")
	tx1, err := sendTx(ctx, client, privateKey, chainID, &wethAddr, amountWei, wrapData, fromAddr)
	if err != nil {
		fmt.Printf("WETH 包装失败: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("  TX: %s\n", tx1.Hash().Hex())
	waitForReceipt(ctx, client, tx1.Hash())

	// Step 2: Approve WETH 给 Vault
	fmt.Println("\n=== Step 2/3: Approve WETH → Vault ===")
	approveData, _ := wethParsed.Pack("approve", vaultAddr, amountWei)
	tx2, err := sendTx(ctx, client, privateKey, chainID, &wethAddr, big.NewInt(0), approveData, fromAddr)
	if err != nil {
		fmt.Printf("Approve 失败: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("  TX: %s\n", tx2.Hash().Hex())
	waitForReceipt(ctx, client, tx2.Hash())

	// Step 3: Deposit WETH 到 Vault
	fmt.Println("\n=== Step 3/3: Deposit WETH → Vault ===")
	depositData, _ := vaultParsed.Pack("deposit", amountWei, fromAddr)
	tx3, err := sendTx(ctx, client, privateKey, chainID, &vaultAddr, big.NewInt(0), depositData, fromAddr)
	if err != nil {
		fmt.Printf("Deposit 失败: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("  TX: %s\n", tx3.Hash().Hex())
	receipt := waitForReceipt(ctx, client, tx3.Hash())

	if receipt.Status == 1 {
		fmt.Println("\n========================================")
		fmt.Println("  存款成功！")
		fmt.Printf("  存入: %f WETH\n", *amountETH)
		fmt.Printf("  Gas Used: %d\n", receipt.GasUsed)
		fmt.Println("========================================")
	} else {
		fmt.Println("\n  存款交易 reverted！")
	}
}

func ethToWei(eth float64) *big.Int {
	weiFloat := new(big.Float).Mul(big.NewFloat(eth), new(big.Float).SetFloat64(1e18))
	wei, _ := weiFloat.Int(nil)
	return wei
}

func sendTx(ctx context.Context, client *ethclient.Client, pk *ecdsa.PrivateKey, chainID *big.Int, to *common.Address, value *big.Int, data []byte, from common.Address) (*types.Transaction, error) {
	nonce, err := client.PendingNonceAt(ctx, from)
	if err != nil {
		return nil, fmt.Errorf("get nonce: %w", err)
	}

	gasLimit, err := client.EstimateGas(ctx, ethereum.CallMsg{From: from, To: to, Value: value, Data: data})
	if err != nil {
		return nil, fmt.Errorf("estimate gas: %w", err)
	}
	gasLimit = gasLimit * 120 / 100 // +20% buffer

	header, err := client.HeaderByNumber(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("get header: %w", err)
	}

	tipCap, err := client.SuggestGasTipCap(ctx)
	if err != nil {
		tipCap = big.NewInt(100_000_000) // 0.1 gwei
	}
	feeCap := new(big.Int).Add(new(big.Int).Mul(header.BaseFee, big.NewInt(2)), tipCap)

	tx := types.NewTx(&types.DynamicFeeTx{
		ChainID:   chainID,
		Nonce:     nonce,
		To:        to,
		Gas:       gasLimit,
		GasTipCap: tipCap,
		GasFeeCap: feeCap,
		Value:     value,
		Data:      data,
	})

	signer := types.LatestSignerForChainID(chainID)
	signed, err := types.SignTx(tx, signer, pk)
	if err != nil {
		return nil, fmt.Errorf("sign: %w", err)
	}

	err = client.SendTransaction(ctx, signed)
	if err != nil {
		return nil, fmt.Errorf("send: %w", err)
	}

	return signed, nil
}

func waitForReceipt(ctx context.Context, client *ethclient.Client, txHash common.Hash) *types.Receipt {
	fmt.Printf("  等待确认...")
	for i := 0; i < 60; i++ {
		receipt, err := client.TransactionReceipt(ctx, txHash)
		if err == nil {
			if receipt.Status == 1 {
				fmt.Printf(" 成功 (block %d)\n", receipt.BlockNumber.Uint64())
			} else {
				fmt.Printf(" REVERTED (block %d)\n", receipt.BlockNumber.Uint64())
			}
			return receipt
		}
		time.Sleep(500 * time.Millisecond)
	}
	fmt.Println(" 超时")
	return nil
}
