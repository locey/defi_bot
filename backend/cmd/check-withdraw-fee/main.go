// 查询 Binance USDT 提现费用
package main

import (
	"fmt"
	"os"

	"github.com/defi-bot/backend/pkg/cex"
)

func main() {
	trader := cex.NewBinanceTrader(&cex.BinanceConfig{
		APIEndpoint: "https://api.binance.com",
		APIKey:      os.Getenv("BINANCE_API_KEY"),
		APISecret:   os.Getenv("BINANCE_API_SECRET"),
		RateLimit:   1200,
	})

	// 查询 USDT 和 LINK 的网络信息
	for _, coin := range []string{"USDT", "LINK"} {
		info, err := trader.GetCoinNetwork(coin)
		if err != nil {
			fmt.Printf("%s: error %v\n", coin, err)
			continue
		}
		fmt.Printf("=== %s ===\n", coin)
		for _, n := range info {
			fmt.Printf("  %-12s fee=%-8s min=%-10s enabled=%v\n",
				n.Network, n.WithdrawFee, n.WithdrawMin, n.WithdrawEnable)
		}
	}
}
