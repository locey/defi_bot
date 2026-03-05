#!/bin/bash
# =============================================================
# ArbitrageX 链上余额检查脚本
# 用法: bash docs/scripts/check_balances.sh
# 前置: 需要网络连接 (Arbitrum RPC)
# =============================================================

cd "$(dirname "$0")/../../backend"

# 从 config.yaml 中读取地址
CONFIG_FILE="configs/config.yaml"
KEEPER_ADDR=$(grep 'address:' "$CONFIG_FILE" | grep -v '#' | head -1 | awk '{print $2}' | tr -d '"')
VAULT_ADDR=$(grep 'vault:' "$CONFIG_FILE" | head -1 | awk '{print $2}' | tr -d '"')

if [ -z "$KEEPER_ADDR" ] || [ -z "$VAULT_ADDR" ]; then
    echo "无法从 config.yaml 读取 Keeper/Vault 地址"
    exit 1
fi

cat > /tmp/check_balances_arb.go << GOEOF
package main

import (
	"context"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
)

func main() {
	client, err := ethclient.Dial("https://arb1.arbitrum.io/rpc")
	if err != nil {
		fmt.Println("RPC error:", err)
		return
	}
	defer client.Close()

	keeper := common.HexToAddress("$KEEPER_ADDR")
	vault := common.HexToAddress("$VAULT_ADDR")
	weth := common.HexToAddress("0x82aF49447D8a07e3bd95BD0d56f35241523fBab1")

	fmt.Println("============================================")
	fmt.Println("  ArbitrageX 链上余额")
	fmt.Println("============================================")

	bal, err := client.BalanceAt(context.Background(), keeper, nil)
	if err != nil {
		fmt.Println("Keeper ETH error:", err)
	} else {
		ethBal := new(big.Float).Quo(new(big.Float).SetInt(bal), big.NewFloat(1e18))
		fmt.Printf("  Keeper ETH:  %s ETH\n", ethBal.Text('f', 6))
	}

	erc20ABI, _ := abi.JSON(strings.NewReader(\`[{"constant":true,"inputs":[{"name":"account","type":"address"}],"name":"balanceOf","outputs":[{"name":"","type":"uint256"}],"type":"function"}]\`))
	callData, _ := erc20ABI.Pack("balanceOf", vault)
	result, err := client.CallContract(context.Background(), ethereum.CallMsg{To: &weth, Data: callData}, nil)
	if err != nil {
		fmt.Println("Vault WETH error:", err)
	} else {
		wethBal := new(big.Int).SetBytes(result)
		wethFloat := new(big.Float).Quo(new(big.Float).SetInt(wethBal), big.NewFloat(1e18))
		fmt.Printf("  Vault WETH:  %s WETH\n", wethFloat.Text('f', 6))
	}

	block, _ := client.BlockNumber(context.Background())
	fmt.Printf("  Block:       %d\n", block)
	fmt.Println("============================================")
}
GOEOF

go run /tmp/check_balances_arb.go
