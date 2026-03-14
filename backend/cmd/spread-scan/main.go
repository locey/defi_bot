// cmd/spread-scan/main.go
// 扫描 Arbitrum DEX vs Binance CEX 价差，找最优套利对
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
)

var (
	WETH     = common.HexToAddress("0x82aF49447D8a07e3bd95BD0d56f35241523fBab1")
	USDT     = common.HexToAddress("0xFd086bC7CD5C481DCC9C85ebE478A1C0b69FCbb9")
	USDC     = common.HexToAddress("0xaf88d065e77c8cC2239327C5EDb3A432268e5831")
	QuoterV2 = common.HexToAddress("0x61fFE014bA17989E743c5F6cB21bF9697530B21e")
)

type pair struct {
	symbol   string // Binance symbol, e.g. "ARBUSDT"
	token    common.Address
	decimals int
	testAmt  float64 // 用多少 USDT 测试
	feeTier  int64   // V3 fee tier (500=0.05%, 3000=0.3%, 10000=1%)
}

func main() {
	client, err := ethclient.Dial("https://warmhearted-wild-road.arbitrum-mainnet.quiknode.pro/1ed461039e0b8a3008160cbc130479deb34798c9/")
	if err != nil {
		fmt.Printf("RPC error: %v\n", err)
		return
	}
	defer client.Close()

	pairs := []pair{
		{"LINKUSDT", common.HexToAddress("0xf97f4df75117a78c1A5a0DBb814Af92458539FB4"), 18, 30, 500},
		{"ARBUSDT", common.HexToAddress("0x912CE59144191C1204E64559FE8253a0e49E6548"), 18, 30, 500},
		{"UNIUSDT", common.HexToAddress("0xFa7F8980b0f1E64A2062791cc3b0871572f1F7f0"), 18, 30, 3000},
		{"GMXUSDT", common.HexToAddress("0xfc5A1A6EB076a2C7aD06eD22C90d7E710E35ad0a"), 18, 30, 3000},
		{"PENDLEUSDT", common.HexToAddress("0x0c880f6761F1af8d9Aa9C466984b80DAb9a8c9e8"), 18, 30, 3000},
		{"WBTCUSDT", common.HexToAddress("0x2f2a2543B76A4166549F7aaB2e75Bef0aefC5B0f"), 8, 30, 500},
		{"AAVEUSDT", common.HexToAddress("0xba5DdD1f9d7F570dc94a51479a000E3BCE967196"), 18, 30, 3000},
		{"RDNTUSDT", common.HexToAddress("0x3082CC23568eA640225c2467653dB90e9250AaA0"), 18, 30, 3000},
		{"MAGICUSDT", common.HexToAddress("0x539bdE0d7Dbd336b79148AA742883198BBF60342"), 18, 30, 3000},
		{"GRAILUSDT", common.HexToAddress("0x3d9907F9a368ad0a51Be60f7Da3b97cf940982D8"), 18, 30, 3000},
		{"WLDUSDT", common.HexToAddress("0xB0fFa8000886e57F86dd5264b987B9993e693fB6"), 18, 30, 3000},
	}

	fmt.Println("=== CEX-DEX Spread Scanner (Arbitrum vs Binance) ===")
	fmt.Printf("%-12s | %8s | %8s | %8s | %7s | %s\n", "Pair", "CEX Bid", "DEX Buy", "DEX Sell", "Spread", "Direction")
	fmt.Println(strings.Repeat("-", 80))

	type result struct {
		symbol                        string
		cexBid, cexAsk                float64
		dexBuyPrice, dexSellPrice     float64
		fwdSpread, revSpread          float64
	}
	var results []result

	for _, p := range pairs {
		cexBid := getBidPrice(p.symbol)
		cexAsk := getAskPrice(p.symbol)
		if cexBid == 0 {
			fmt.Printf("%-12s | %8s (not on Binance or error)\n", p.symbol, "N/A")
			continue
		}

		// DEX buy price: spend USDT → get token (via WETH if fee=500, direct otherwise)
		var dexBuyPrice, dexSellPrice float64

		if p.feeTier == 500 {
			// Multi-hop: USDT→WETH(500)→Token(500)
			linkOut := quoteMultiHop(client, USDT, WETH, p.token, p.testAmt, 6, p.decimals, 500, 500)
			if linkOut > 0 {
				dexBuyPrice = p.testAmt / linkOut
			}
			// Sell: Token→WETH(500)→USDT(500)
			testSell := p.testAmt / cexBid // ~同等金额的 token
			usdtOut := quoteMultiHop(client, p.token, WETH, USDT, testSell, p.decimals, 6, 500, 500)
			if usdtOut > 0 {
				dexSellPrice = usdtOut / testSell
			}
		} else {
			// Try direct USDT pool first (fee=3000)
			linkOut := quoteSingleFee(client, USDT, p.token, p.testAmt, 6, p.decimals, p.feeTier)
			if linkOut > 0 {
				dexBuyPrice = p.testAmt / linkOut
			} else {
				// Fallback: multi-hop via WETH
				linkOut = quoteMultiHop(client, USDT, WETH, p.token, p.testAmt, 6, p.decimals, 500, 3000)
				if linkOut > 0 {
					dexBuyPrice = p.testAmt / linkOut
				}
			}
			testSell := p.testAmt / cexBid
			usdtOut := quoteSingleFee(client, p.token, USDT, testSell, p.decimals, 6, p.feeTier)
			if usdtOut > 0 {
				dexSellPrice = usdtOut / testSell
			} else {
				usdtOut = quoteMultiHop(client, p.token, WETH, USDT, testSell, p.decimals, 6, 3000, 500)
				if usdtOut > 0 {
					dexSellPrice = usdtOut / testSell
				}
			}
		}

		fwd := 0.0
		rev := 0.0
		if dexBuyPrice > 0 {
			fwd = (cexBid - dexBuyPrice) / dexBuyPrice * 100
		}
		if dexSellPrice > 0 && cexAsk > 0 {
			rev = (dexSellPrice - cexAsk) / cexAsk * 100
		}

		dir := "---"
		bestSpread := fwd
		if fwd > 0.1 {
			dir = "dex→cex"
		}
		if rev > fwd && rev > 0.1 {
			dir = "cex→dex"
			bestSpread = rev
		}

		r := result{p.symbol, cexBid, cexAsk, dexBuyPrice, dexSellPrice, fwd, rev}
		results = append(results, r)

		mark := " "
		if bestSpread > 1.0 {
			mark = "**"
		} else if bestSpread > 0.5 {
			mark = "* "
		}

		fmt.Printf("%-12s | %8.4f | %8.4f | %8.4f | %+6.2f%% | %s %s\n",
			p.symbol, cexBid, dexBuyPrice, dexSellPrice, bestSpread, dir, mark)

		time.Sleep(200 * time.Millisecond) // rate limit
	}

	// 汇总
	fmt.Println()
	fmt.Println("=== TOP OPPORTUNITIES ===")
	for _, r := range results {
		if r.fwdSpread > 0.5 || r.revSpread > 0.5 {
			fmt.Printf("%s: fwd=%.3f%% rev=%.3f%%\n", r.symbol, r.fwdSpread, r.revSpread)
			if r.fwdSpread > 0.5 {
				netProfit := r.fwdSpread/100*30 - 0.10 // $30 trade, $0.10 gas+fees
				fmt.Printf("  Forward ($30 trade): gross=$%.4f net=$%.4f\n", r.fwdSpread/100*30, netProfit)
			}
		}
	}
}

func quoteMultiHop(client *ethclient.Client, tokenIn, mid, tokenOut common.Address, amtIn float64, inDec, outDec int, fee1, fee2 int64) float64 {
	midOut := quoteSingleFee(client, tokenIn, mid, amtIn, inDec, 18, fee1)
	if midOut <= 0 {
		return 0
	}
	return quoteSingleFee(client, mid, tokenOut, midOut, 18, outDec, fee2)
}

func quoteSingleFee(client *ethclient.Client, tokenIn, tokenOut common.Address, amtIn float64, inDec, outDec int, fee int64) float64 {
	abiJSON := `[{"inputs":[{"components":[{"name":"tokenIn","type":"address"},{"name":"tokenOut","type":"address"},{"name":"amountIn","type":"uint256"},{"name":"fee","type":"uint24"},{"name":"sqrtPriceLimitX96","type":"uint160"}],"name":"params","type":"tuple"}],"name":"quoteExactInputSingle","outputs":[{"name":"amountOut","type":"uint256"},{"name":"sqrtPriceX96After","type":"uint160"},{"name":"initializedTicksCrossed","type":"uint32"},{"name":"gasEstimate","type":"uint256"}],"stateMutability":"nonpayable","type":"function"}]`
	parsedABI, _ := abi.JSON(strings.NewReader(abiJSON))
	type P struct {
		TokenIn, TokenOut                common.Address
		AmountIn, Fee, SqrtPriceLimitX96 *big.Int
	}
	data, _ := parsedABI.Pack("quoteExactInputSingle", P{
		tokenIn, tokenOut, floatToWei(amtIn, inDec), big.NewInt(fee), big.NewInt(0),
	})
	result, err := client.CallContract(context.Background(), ethereum.CallMsg{To: &QuoterV2, Data: data}, nil)
	if err != nil || len(result) < 32 {
		return 0
	}
	return weiToFloat(new(big.Int).SetBytes(result[:32]), outDec)
}

func weiToFloat(wei *big.Int, dec int) float64 {
	if wei == nil {
		return 0
	}
	f, _ := new(big.Float).Quo(
		new(big.Float).SetInt(wei),
		new(big.Float).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(dec)), nil)),
	).Float64()
	return f
}

func floatToWei(amt float64, dec int) *big.Int {
	r, _ := new(big.Float).Mul(
		new(big.Float).SetFloat64(amt),
		new(big.Float).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(dec)), nil)),
	).Int(nil)
	return r
}

func getBidPrice(symbol string) float64 {
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Get(
		"https://api.binance.com/api/v3/ticker/bookTicker?symbol=" + symbol)
	if err != nil {
		return 0
	}
	defer resp.Body.Close()
	var d struct{ BidPrice string `json:"bidPrice"` }
	json.NewDecoder(resp.Body).Decode(&d)
	v := 0.0
	fmt.Sscanf(d.BidPrice, "%f", &v)
	return v
}

func getAskPrice(symbol string) float64 {
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Get(
		"https://api.binance.com/api/v3/ticker/bookTicker?symbol=" + symbol)
	if err != nil {
		return 0
	}
	defer resp.Body.Close()
	var d struct{ AskPrice string `json:"askPrice"` }
	json.NewDecoder(resp.Body).Decode(&d)
	v := 0.0
	fmt.Sscanf(d.AskPrice, "%f", &v)
	return v
}
