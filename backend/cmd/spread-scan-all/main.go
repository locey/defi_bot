package main

import (
	"fmt"
	"math"

	"github.com/defi-bot/backend/pkg/trading"

	"github.com/ethereum/go-ethereum/ethclient"
)

func main() {
	client, err := ethclient.Dial("https://arb1.arbitrum.io/rpc")
	if err != nil {
		fmt.Printf("RPC error: %v\n", err)
		return
	}
	defer client.Close()

	fmt.Println("=== Arbitrum CEX-DEX Spread Scanner ===")
	fmt.Println("Scanning all registered tokens...")
	fmt.Println()

	results := trading.ScanAllSpreads(client, -999) // 所有代币，包括负价差

	fmt.Printf("%-8s %10s %10s %8s %s\n", "Token", "CEX", "DEX", "Spread%", "Status")
	fmt.Println("──────────────────────────────────────────────────────")

	for _, r := range results {
		status := ""
		if math.Abs(r.Spread) > 0.5 {
			status = "← ARB"
		}
		fmt.Printf("%-8s %10.4f %10.4f %+7.3f%% %s\n",
			r.Token.Symbol, r.CexPrice, r.DexPrice, r.Spread, status)
	}

	fmt.Printf("\n共 %d 个代币有效报价\n", len(results))

	// 筛选可套利的
	fmt.Println("\n=== 可套利代币 (spread > 0.5%) ===")
	for _, r := range results {
		if r.Spread > 0.5 {
			estProfit := 10.0 * r.Spread / 100 * 0.65 // 扣 35% 费用
			fmt.Printf("  %s: spread %.3f%%, est profit $%.4f/trade\n",
				r.Token.Symbol, r.Spread, estProfit)
		}
	}
}
