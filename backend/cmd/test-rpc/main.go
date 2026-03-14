// cmd/test-rpc/main.go
// RPC 延迟测试 — 测量 eth_blockNumber、eth_call、WebSocket 订阅延迟
package main

import (
	"context"
	"fmt"
	"math/big"
	"os"
	"time"

	"github.com/defi-bot/backend/internal/config"
	"github.com/defi-bot/backend/pkg/log"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
)

func main() {
	configPath := "configs/config.yaml"
	if len(os.Args) > 1 {
		configPath = os.Args[1]
	}

	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		fmt.Printf("❌ 配置加载失败: %v\n", err)
		os.Exit(1)
	}
	_ = log.Init(&cfg.Log)

	fmt.Println("========================================")
	fmt.Println("RPC 延迟测试")
	fmt.Println("========================================")

	rpcURL := cfg.Blockchain.RPCURL
	wsURL := cfg.Blockchain.WSURL
	fmt.Printf("HTTP: %s\n", rpcURL[:60]+"...")
	fmt.Printf("WS:   %s\n\n", wsURL[:60]+"...")

	ctx := context.Background()

	// ---- 1. HTTP RPC 连通 ----
	fmt.Print("1. Connect HTTP RPC ... ")
	start := time.Now()
	client, err := ethclient.Dial(rpcURL)
	if err != nil {
		fmt.Printf("❌ %v\n", err)
		os.Exit(1)
	}
	defer client.Close()
	fmt.Printf("✅ (%s)\n", time.Since(start).Round(time.Millisecond))

	// ---- 2. eth_blockNumber (5次取平均) ----
	fmt.Println("2. eth_blockNumber latency (5 calls):")
	var totalBlock time.Duration
	for i := 0; i < 5; i++ {
		start = time.Now()
		block, err := client.BlockNumber(ctx)
		elapsed := time.Since(start)
		totalBlock += elapsed
		if err != nil {
			fmt.Printf("   #%d: ❌ %v\n", i+1, err)
		} else {
			fmt.Printf("   #%d: block=%d (%s)\n", i+1, block, elapsed.Round(time.Millisecond))
		}
	}
	avgBlock := totalBlock / 5
	fmt.Printf("   AVG: %s\n\n", avgBlock.Round(time.Millisecond))

	// ---- 3. eth_call (模拟 WETH.balanceOf) ----
	fmt.Println("3. eth_call latency (WETH.balanceOf, 5 calls):")
	weth := common.HexToAddress("0x82aF49447D8a07e3bd95BD0d56f35241523fBab1")
	// balanceOf(address) selector = 0x70a08231
	callData := common.Hex2Bytes("70a08231000000000000000000000000" + "82aF49447D8a07e3bd95BD0d56f35241523fBab1")
	var totalCall time.Duration
	for i := 0; i < 5; i++ {
		start = time.Now()
		result, err := client.CallContract(ctx, ethereum.CallMsg{
			To:   &weth,
			Data: callData,
		}, nil)
		elapsed := time.Since(start)
		totalCall += elapsed
		if err != nil {
			fmt.Printf("   #%d: ❌ %v\n", i+1, err)
		} else {
			balance := new(big.Int).SetBytes(result)
			balFloat := new(big.Float).Quo(new(big.Float).SetInt(balance), new(big.Float).SetFloat64(1e18))
			fmt.Printf("   #%d: %s WETH (%s)\n", i+1, balFloat.Text('f', 4), elapsed.Round(time.Millisecond))
		}
	}
	avgCall := totalCall / 5
	fmt.Printf("   AVG: %s\n\n", avgCall.Round(time.Millisecond))

	// ---- 4. eth_getBalance (Keeper 地址) ----
	if cfg.Keeper.Address != "" {
		fmt.Print("4. Keeper balance ... ")
		keeper := common.HexToAddress(cfg.Keeper.Address)
		start = time.Now()
		balance, err := client.BalanceAt(ctx, keeper, nil)
		elapsed := time.Since(start)
		if err != nil {
			fmt.Printf("❌ %v\n", err)
		} else {
			balFloat := new(big.Float).Quo(new(big.Float).SetInt(balance), new(big.Float).SetFloat64(1e18))
			fmt.Printf("%s ETH (%s)\n", balFloat.Text('f', 6), elapsed.Round(time.Millisecond))
		}
	}

	// ---- 5. WebSocket 连通性 ----
	fmt.Print("\n5. WebSocket connect ... ")
	start = time.Now()
	wsClient, err := ethclient.Dial(wsURL)
	if err != nil {
		fmt.Printf("❌ %v\n", err)
	} else {
		fmt.Printf("✅ (%s)\n", time.Since(start).Round(time.Millisecond))

		// 订阅新区块
		fmt.Print("6. NewHead subscription (wait 15s for 1 block) ... ")
		headers := make(chan *types.Header, 1)
		subCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()

		sub, err := wsClient.SubscribeNewHead(subCtx, headers)
		if err != nil {
			fmt.Printf("❌ %v\n", err)
		} else {
			start = time.Now()
			select {
			case header := <-headers:
				elapsed := time.Since(start)
				fmt.Printf("✅ block=%d (%s)\n", header.Number.Uint64(), elapsed.Round(time.Millisecond))
			case err := <-sub.Err():
				fmt.Printf("❌ subscription error: %v\n", err)
			case <-subCtx.Done():
				fmt.Println("⏳ timeout (no block in 15s)")
			}
			sub.Unsubscribe()
		}
		wsClient.Close()
	}

	// ---- Summary ----
	fmt.Println("\n========================================")
	fmt.Println("Summary:")
	fmt.Printf("  eth_blockNumber avg: %s\n", avgBlock.Round(time.Millisecond))
	fmt.Printf("  eth_call avg:        %s\n", avgCall.Round(time.Millisecond))
	if avgCall < 100*time.Millisecond {
		fmt.Println("  Status: ✅ 延迟 <100ms，适合套利")
	} else if avgCall < 500*time.Millisecond {
		fmt.Println("  Status: ⚠️  延迟 100-500ms，勉强可用")
	} else {
		fmt.Println("  Status: ❌ 延迟 >500ms，不适合高频套利")
	}
	fmt.Println("========================================")
}
