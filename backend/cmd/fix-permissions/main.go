// cmd/fix-permissions/main.go
// 修复链上合约权限配置
// SpotArbitrage.backendCaller 需要设为 ArbitrageCore 地址
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

var dryRun = flag.Bool("dry-run", false, "只检查不执行")

func main() {
	flag.Parse()
	godotenv.Load("../.env")
	godotenv.Load(".env")

	pk := os.Getenv("KEEPER_PRIVATE_KEY")
	rpc := os.Getenv("BLOCKCHAIN_RPC_URL")
	if pk == "" || rpc == "" {
		fmt.Println("需要 KEEPER_PRIVATE_KEY 和 BLOCKCHAIN_RPC_URL")
		os.Exit(1)
	}

	client, err := ethclient.Dial(rpc)
	if err != nil { fmt.Println("RPC:", err); os.Exit(1) }
	ctx := context.Background()

	privateKey, _ := crypto.HexToECDSA(strings.TrimPrefix(pk, "0x"))
	from := crypto.PubkeyToAddress(privateKey.PublicKey)
	chainID, _ := client.ChainID(ctx)

	spotArbitrage := common.HexToAddress("0xceCCf5D3f5ab1dAB5b13Fd5Cf7deeA9C3DBaad17")
	arbitrageCore := common.HexToAddress("0x0D14428b4e297344C2C51D087934a3e3a9b822B9")

	setterABI, _ := abi.JSON(strings.NewReader(`[
		{"inputs":[{"name":"_backendCaller","type":"address"}],"name":"setBackendCaller","outputs":[],"stateMutability":"nonpayable","type":"function"},
		{"inputs":[],"name":"backendCaller","outputs":[{"name":"","type":"address"}],"stateMutability":"view","type":"function"}
	]`))

	// 读取当前值
	readData, _ := setterABI.Pack("backendCaller")
	result, _ := client.CallContract(ctx, ethereum.CallMsg{To: &spotArbitrage, Data: readData}, nil)
	current := common.BytesToAddress(result)

	fmt.Println("========================================")
	fmt.Println("  修复 SpotArbitrage 权限")
	fmt.Println("========================================")
	fmt.Printf("  SpotArbitrage:         %s\n", spotArbitrage.Hex())
	fmt.Printf("  当前 backendCaller:    %s\n", current.Hex())
	fmt.Printf("  目标 backendCaller:    %s (ArbitrageCore)\n", arbitrageCore.Hex())
	fmt.Printf("  发送者 (owner):        %s\n", from.Hex())
	fmt.Println()

	if current == arbitrageCore {
		fmt.Println("  已经是正确的值，无需修改！")
		return
	}

	if *dryRun {
		fmt.Println("  [dry-run] 将会调用 setBackendCaller(ArbitrageCore)")
		return
	}

	fmt.Printf("确认修改？(y/N): ")
	var confirm string
	fmt.Scanln(&confirm)
	if strings.ToLower(confirm) != "y" {
		fmt.Println("已取消")
		return
	}

	// 发送交易
	data, _ := setterABI.Pack("setBackendCaller", arbitrageCore)
	tx, err := sendTx(ctx, client, privateKey, chainID, &spotArbitrage, big.NewInt(0), data, from)
	if err != nil {
		fmt.Printf("交易失败: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("  TX: %s\n", tx.Hash().Hex())
	receipt := waitReceipt(ctx, client, tx.Hash())
	if receipt != nil && receipt.Status == 1 {
		fmt.Println("\n  权限修复成功！")
	} else {
		fmt.Println("\n  交易 reverted！")
	}

	// 验证
	result2, _ := client.CallContract(ctx, ethereum.CallMsg{To: &spotArbitrage, Data: readData}, nil)
	newVal := common.BytesToAddress(result2)
	fmt.Printf("  新的 backendCaller: %s\n", newVal.Hex())
}

func sendTx(ctx context.Context, c *ethclient.Client, pk *ecdsa.PrivateKey, chainID *big.Int, to *common.Address, value *big.Int, data []byte, from common.Address) (*types.Transaction, error) {
	nonce, _ := c.PendingNonceAt(ctx, from)
	gas, err := c.EstimateGas(ctx, ethereum.CallMsg{From: from, To: to, Value: value, Data: data})
	if err != nil { return nil, err }
	gas = gas * 130 / 100
	header, _ := c.HeaderByNumber(ctx, nil)
	tip, _ := c.SuggestGasTipCap(ctx)
	if tip == nil { tip = big.NewInt(100000000) }
	fee := new(big.Int).Add(new(big.Int).Mul(header.BaseFee, big.NewInt(2)), tip)
	tx := types.NewTx(&types.DynamicFeeTx{ChainID: chainID, Nonce: nonce, To: to, Gas: gas, GasTipCap: tip, GasFeeCap: fee, Value: value, Data: data})
	signed, _ := types.SignTx(tx, types.LatestSignerForChainID(chainID), pk)
	return signed, c.SendTransaction(ctx, signed)
}

func waitReceipt(ctx context.Context, c *ethclient.Client, h common.Hash) *types.Receipt {
	fmt.Printf("  等待确认...")
	for i := 0; i < 60; i++ {
		r, err := c.TransactionReceipt(ctx, h)
		if err == nil {
			if r.Status == 1 { fmt.Printf(" 成功 (block %d)\n", r.BlockNumber.Uint64()) } else { fmt.Println(" REVERTED") }
			return r
		}
		time.Sleep(500 * time.Millisecond)
	}
	fmt.Println(" 超时")
	return nil
}
