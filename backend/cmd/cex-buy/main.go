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

	// 查余额
	usdtF, _, _ := trader.GetBalance("USDT")
	linkF, _, _ := trader.GetBalance("LINK")
	fmt.Printf("Before: USDT=%.4f LINK=%.4f\n", usdtF, linkF)

	// 买 $30 LINK
	order, err := trader.MarketBuy("LINKUSDT", "30.00")
	if err != nil {
		fmt.Printf("Buy error: %v\n", err)
		return
	}
	fmt.Printf("Order: filled=%s LINK, cost=$%s, avgPrice=$%.4f\n",
		order.ExecutedQty, order.CumulativeQuoteQty, order.GetAvgFillPrice())

	// 查余额
	usdtF2, _, _ := trader.GetBalance("USDT")
	linkF2, _, _ := trader.GetBalance("LINK")
	fmt.Printf("After: USDT=%.4f LINK=%.4f\n", usdtF2, linkF2)
}
