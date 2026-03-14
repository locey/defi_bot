// cmd/full-scan/main.go
// 全面价差扫描: CEX vs DEX, DEX vs DEX, 1inch 对比
package main

import (
	"context"
	"encoding/hex"
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
	QuoterV2 = common.HexToAddress("0x61fFE014bA17989E743c5F6cB21bF9697530B21e") // Uniswap

	// SushiSwap V2 Router
	SushiRouter  = common.HexToAddress("0x1b02dA8Cb0d097eB8D57A175b88c7D8b47997506")
	SushiFactory = common.HexToAddress("0xc35DADB65012eC5796536bD9864eD8773aBc74C4")

	// Camelot V2 Router
	CamelotRouter  = common.HexToAddress("0xc873fEcbd354f5A56E00E710B90EF4201db2448d")
	CamelotFactory = common.HexToAddress("0x6EcCab422D763aC031210895C81787E87B43A652")
)

type token struct {
	name     string
	addr     common.Address
	dec      int
	binance  string // Binance symbol (empty = not on Binance)
}

var tokens = []token{
	{"LINK", common.HexToAddress("0xf97f4df75117a78c1A5a0DBb814Af92458539FB4"), 18, "LINKUSDT"},
	{"ARB", common.HexToAddress("0x912CE59144191C1204E64559FE8253a0e49E6548"), 18, "ARBUSDT"},
	{"UNI", common.HexToAddress("0xFa7F8980b0f1E64A2062791cc3b0871572f1F7f0"), 18, "UNIUSDT"},
	{"PENDLE", common.HexToAddress("0x0c880f6761F1af8d9Aa9C466984b80DAb9a8c9e8"), 18, "PENDLEUSDT"},
	{"WBTC", common.HexToAddress("0x2f2a2543B76A4166549F7aaB2e75Bef0aefC5B0f"), 8, "WBTCUSDT"},
	{"AAVE", common.HexToAddress("0xba5DdD1f9d7F570dc94a51479a000E3BCE967196"), 18, "AAVEUSDT"},
	{"GRT", common.HexToAddress("0x9623063377AD1B27544C965cCd7342f7EA7e88C7"), 18, "GRTUSDT"},
	{"COMP", common.HexToAddress("0x354A6dA3fcde098F8389cad84b0182725c6C91dE"), 18, "COMPUSDT"},
	{"CRV", common.HexToAddress("0x11cDb42B0EB46D95f990BeDD4695A6e3fA034978"), 18, "CRVUSDT"},
	{"LDO", common.HexToAddress("0x13Ad51ed4F1B7e9Dc168d8a00cB3f4dDD85EfA60"), 18, "LDOUSDT"},
	{"RDNT", common.HexToAddress("0x3082CC23568eA640225c2467653dB90e9250AaA0"), 18, "RDNTUSDT"},
	{"SUSHI", common.HexToAddress("0xd4d42F0b6DEF4CE0383636770eF773390d85c61A"), 18, "SUSHIUSDT"},
	{"STG", common.HexToAddress("0x6694340fc020c5E6B96567843da2df01b2CE1eb6"), 18, "STGUSDT"},
	{"GMX", common.HexToAddress("0xfc5A1A6EB076a2C7aD06eD22C90d7E710E35ad0a"), 18, "GMXUSDT"},
	{"MAGIC", common.HexToAddress("0x539bdE0d7Dbd336b79148AA742883198BBF60342"), 18, "MAGICUSDT"},
	{"DPX", common.HexToAddress("0x6C2C06790b3E3E3c38e12Ee22F8183b37a13EE55"), 18, "DPXUSDT"},
	{"GRAIL", common.HexToAddress("0x3d9907F9a368ad0a51Be60f7Da3b97cf940982D8"), 18, ""},
	{"WLD", common.HexToAddress("0xB0fFa8000886e57F86dd5264b987B9993e693fB6"), 18, "WLDUSDT"},
	{"RPL", common.HexToAddress("0xB766039cc6DB368759C1E56B79AFfE831d0Cc507"), 18, "RPLUSDT"},
}

func main() {
	client, _ := ethclient.Dial("https://warmhearted-wild-road.arbitrum-mainnet.quiknode.pro/1ed461039e0b8a3008160cbc130479deb34798c9/")
	defer client.Close()

	testUSDT := 30.0 // 用 $30 测试

	// ============ Part 1: CEX vs DEX (Uniswap V3) ============
	fmt.Println("=== Part 1: Binance vs Uniswap V3 (Arbitrum) ===")
	fmt.Printf("%-10s | %9s | %9s | %7s | %s\n", "Token", "CEX Bid", "DEX Buy", "Spread", "Note")
	fmt.Println(strings.Repeat("-", 65))

	type opportunity struct {
		name    string
		spread  float64
		details string
	}
	var opps []opportunity

	for _, t := range tokens {
		if t.binance == "" {
			continue
		}
		cexBid := getBidPrice(t.binance)
		if cexBid == 0 {
			continue
		}

		// Try multiple fee tiers on Uniswap V3
		bestDexPrice := 0.0
		bestFee := ""
		for _, fee := range []int64{500, 3000, 10000} {
			// Multi-hop via WETH
			price := getUniV3BuyPrice(client, t.addr, t.dec, testUSDT, fee)
			if price > 0 && (bestDexPrice == 0 || price < bestDexPrice) {
				bestDexPrice = price
				bestFee = fmt.Sprintf("%dbps", fee)
			}
		}

		if bestDexPrice == 0 {
			fmt.Printf("%-10s | %9.4f | %9s | %7s | no pool\n", t.name, cexBid, "---", "---")
			continue
		}

		spread := (cexBid - bestDexPrice) / bestDexPrice * 100
		mark := ""
		if spread > 1.0 {
			mark = " ***"
		} else if spread > 0.5 {
			mark = " **"
		} else if spread > 0.3 {
			mark = " *"
		}

		fmt.Printf("%-10s | %9.4f | %9.4f | %+6.2f%% | %s%s\n",
			t.name, cexBid, bestDexPrice, spread, bestFee, mark)

		if spread > 0.3 {
			opps = append(opps, opportunity{t.name, spread,
				fmt.Sprintf("CEX=$%.4f DEX=$%.4f fee=%s", cexBid, bestDexPrice, bestFee)})
		}
		time.Sleep(100 * time.Millisecond)
	}

	// ============ Part 2: DEX vs DEX (Uniswap vs SushiSwap vs Camelot) ============
	fmt.Println()
	fmt.Println("=== Part 2: DEX vs DEX (Uniswap V3 vs SushiSwap V2 vs Camelot V2) ===")
	fmt.Printf("%-10s | %9s | %9s | %9s | %s\n", "Token", "UniV3", "Sushi", "Camelot", "Best Arb")
	fmt.Println(strings.Repeat("-", 70))

	for _, t := range tokens {
		// Uniswap V3 price (best fee tier)
		uniPrice := 0.0
		for _, fee := range []int64{500, 3000, 10000} {
			p := getUniV3BuyPrice(client, t.addr, t.dec, testUSDT, fee)
			if p > 0 && (uniPrice == 0 || p < uniPrice) {
				uniPrice = p
			}
		}

		// SushiSwap V2 price
		sushiPrice := getV2BuyPrice(client, SushiFactory, t.addr, t.dec, testUSDT)

		// Camelot V2 price
		camelotPrice := getV2BuyPrice(client, CamelotFactory, t.addr, t.dec, testUSDT)

		if uniPrice == 0 && sushiPrice == 0 && camelotPrice == 0 {
			continue
		}

		// Find best spread between any two DEXes
		prices := map[string]float64{"Uni": uniPrice, "Sushi": sushiPrice, "Camelot": camelotPrice}
		bestArb := ""
		bestArbSpread := 0.0
		for n1, p1 := range prices {
			for n2, p2 := range prices {
				if n1 == n2 || p1 == 0 || p2 == 0 {
					continue
				}
				spread := (p2 - p1) / p1 * 100 // buy on n1 (cheap), sell on n2 (expensive)
				if spread > bestArbSpread {
					bestArbSpread = spread
					bestArb = fmt.Sprintf("buy %s sell %s %.2f%%", n1, n2, spread)
				}
			}
		}

		uStr := "---"
		sStr := "---"
		cStr := "---"
		if uniPrice > 0 {
			uStr = fmt.Sprintf("%.4f", uniPrice)
		}
		if sushiPrice > 0 {
			sStr = fmt.Sprintf("%.4f", sushiPrice)
		}
		if camelotPrice > 0 {
			cStr = fmt.Sprintf("%.4f", camelotPrice)
		}

		mark := ""
		if bestArbSpread > 1.0 {
			mark = " ***"
		} else if bestArbSpread > 0.5 {
			mark = " **"
		}

		fmt.Printf("%-10s | %9s | %9s | %9s | %s%s\n", t.name, uStr, sStr, cStr, bestArb, mark)
		time.Sleep(100 * time.Millisecond)
	}

	// ============ Summary ============
	fmt.Println()
	fmt.Println("=== OPPORTUNITIES (spread > 0.3%) ===")
	if len(opps) == 0 {
		fmt.Println("  None found above 0.3%")
	}
	for _, o := range opps {
		profitAt30 := o.spread / 100 * 30
		fmt.Printf("  %s: %.2f%% → $%.3f/trade(30$) | %s\n", o.name, o.spread, profitAt30, o.details)
	}
}

// ============ Uniswap V3 Quote ============

func getUniV3BuyPrice(client *ethclient.Client, token common.Address, dec int, testUSDT float64, fee int64) float64 {
	// Multi-hop: USDT→WETH(500)→Token(fee)
	midOut := quoteSingleV3(client, USDT, WETH, testUSDT, 6, 18, 500)
	if midOut <= 0 {
		return 0
	}
	tokOut := quoteSingleV3(client, WETH, token, midOut, 18, dec, fee)
	if tokOut <= 0 {
		return 0
	}
	return testUSDT / tokOut
}

func quoteSingleV3(client *ethclient.Client, tokenIn, tokenOut common.Address, amtIn float64, inDec, outDec int, fee int64) float64 {
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

// ============ V2 AMM Quote (SushiSwap, Camelot) ============

func getV2BuyPrice(client *ethclient.Client, factory common.Address, token common.Address, dec int, testUSDT float64) float64 {
	// Check WETH/USDT pair first, then WETH/Token
	wethUSDT := getV2PairReserves(client, factory, WETH, USDT)
	if wethUSDT[0] == 0 || wethUSDT[1] == 0 {
		return 0
	}

	// Buy WETH with USDT: amountIn=testUSDT(6dec), reserveIn=USDT, reserveOut=WETH
	wethOut := v2AmountOut(testUSDT, wethUSDT[1], wethUSDT[0], 6, 18) // USDT→WETH
	if wethOut <= 0 {
		return 0
	}

	wethTok := getV2PairReserves(client, factory, WETH, token)
	if wethTok[0] == 0 || wethTok[1] == 0 {
		return 0
	}

	// Buy Token with WETH
	tokOut := v2AmountOut(wethOut, wethTok[0], wethTok[1], 18, dec) // WETH→Token
	if tokOut <= 0 {
		return 0
	}

	return testUSDT / tokOut
}

func getV2PairReserves(client *ethclient.Client, factory, tokenA, tokenB common.Address) [2]float64 {
	// getPair(tokenA, tokenB)
	sig, _ := hex.DecodeString("e6a43905")
	data := append(sig, common.LeftPadBytes(tokenA.Bytes(), 32)...)
	data = append(data, common.LeftPadBytes(tokenB.Bytes(), 32)...)
	result, err := client.CallContract(context.Background(), ethereum.CallMsg{To: &factory, Data: data}, nil)
	if err != nil || len(result) < 32 {
		return [2]float64{}
	}
	pairAddr := common.BytesToAddress(result[12:32])
	if pairAddr == (common.Address{}) {
		return [2]float64{}
	}

	// getReserves()
	sig2, _ := hex.DecodeString("0902f1ac")
	result2, err := client.CallContract(context.Background(), ethereum.CallMsg{To: &pairAddr, Data: sig2}, nil)
	if err != nil || len(result2) < 64 {
		return [2]float64{}
	}
	reserve0 := new(big.Int).SetBytes(result2[:32])
	reserve1 := new(big.Int).SetBytes(result2[32:64])

	// token0()
	sig3, _ := hex.DecodeString("0dfe1681")
	result3, err := client.CallContract(context.Background(), ethereum.CallMsg{To: &pairAddr, Data: sig3}, nil)
	if err != nil || len(result3) < 32 {
		return [2]float64{}
	}
	token0 := common.BytesToAddress(result3[12:32])

	// Order: [tokenA reserve, tokenB reserve]
	if token0 == tokenA {
		return [2]float64{weiToFloatRaw(reserve0), weiToFloatRaw(reserve1)}
	}
	return [2]float64{weiToFloatRaw(reserve1), weiToFloatRaw(reserve0)}
}

func v2AmountOut(amtIn, reserveIn, reserveOut float64, inDec, outDec int) float64 {
	// Standard AMM formula: amountOut = (amtIn * 997 * reserveOut) / (reserveIn * 1000 + amtIn * 997)
	// But we're working with raw reserve values (not normalized), need to be careful
	amtInWei := amtIn * pow10(inDec)
	reserveInWei := reserveIn
	reserveOutWei := reserveOut
	num := amtInWei * 997 * reserveOutWei
	den := reserveInWei*1000 + amtInWei*997
	if den == 0 {
		return 0
	}
	outWei := num / den
	return outWei / pow10(outDec)
}

func weiToFloatRaw(wei *big.Int) float64 {
	if wei == nil {
		return 0
	}
	f, _ := new(big.Float).SetInt(wei).Float64()
	return f
}

func pow10(dec int) float64 {
	r := 1.0
	for i := 0; i < dec; i++ {
		r *= 10
	}
	return r
}

// ============ Utils ============

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
