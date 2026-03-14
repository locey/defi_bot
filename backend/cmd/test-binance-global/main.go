package main

import (
	"fmt"
	"os"

	"github.com/defi-bot/backend/pkg/cex"
)

func main() {
	apiKey := os.Getenv("BINANCE_API_KEY")
	apiSecret := os.Getenv("BINANCE_API_SECRET")
	if apiKey == "" {
		apiKey = "ajsfUuRt0oo0lVM0Yym3NMDASKPPbIhab1D1IosKJYpJmxIAhGleB0Qlfx0rbOiO"
		apiSecret = "z1RY4P7lxIdR0nSzm74RsD2BINYMjOjK5e91uB4657xcB9C85jUFORNs8Ge1U1NP"
	}

	endpoints := []string{
		"https://api.binance.com",
		"https://api.binance.us",
		"https://api1.binance.com",
	}

	for _, ep := range endpoints {
		trader := cex.NewBinanceTrader(&cex.BinanceConfig{
			APIEndpoint: ep,
			APIKey:      apiKey,
			APISecret:   apiSecret,
			RateLimit:   1200,
		})

		err := trader.TestConnectivity()
		if err != nil {
			fmt.Printf("%s: ❌ %v\n", ep, err)
		} else {
			fmt.Printf("%s: ✅ canTrade\n", ep)
			account, _ := trader.GetAccountInfo()
			if account != nil {
				hasBalance := false
				for _, b := range account.Balances {
					if b.Free != "0.00000000" || b.Locked != "0.00000000" {
						fmt.Printf("  %s: free=%s locked=%s\n", b.Asset, b.Free, b.Locked)
						hasBalance = true
					}
				}
				if !hasBalance {
					fmt.Println("  (no balances)")
				}
			}
		}
	}
}
