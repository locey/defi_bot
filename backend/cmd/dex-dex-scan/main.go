// cmd/dex-dex-scan/main.go
// 扫描 DEX-DEX 套利机会:
// 1. Uniswap V3 不同 fee tier 之间 (500 vs 3000 vs 10000)
// 2. Uniswap V3 vs SushiSwap V2 (有流动性的池子)
// 3. Uniswap V3 vs Camelot V2
// 4. Curve 稳定币池
package main

import (
	"context"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
)

var (
	WETH     = common.HexToAddress("0x82aF49447D8a07e3bd95BD0d56f35241523fBab1")
	USDT     = common.HexToAddress("0xFd086bC7CD5C481DCC9C85ebE478A1C0b69FCbb9")
	USDC     = common.HexToAddress("0xaf88d065e77c8cC2239327C5EDb3A432268e5831")
	USDCe    = common.HexToAddress("0xFF970A61A04b1cA14834A43f5dE4533eBDDB5CC8") // bridged USDC
	DAI      = common.HexToAddress("0xDA10009cBd5D07dd0CeCc66161FC93D7c9000da1")
	WBTC     = common.HexToAddress("0x2f2a2543B76A4166549F7aaB2e75Bef0aefC5B0f")
	LINK     = common.HexToAddress("0xf97f4df75117a78c1A5a0DBb814Af92458539FB4")
	ARB      = common.HexToAddress("0x912CE59144191C1204E64559FE8253a0e49E6548")
	GMX      = common.HexToAddress("0xfc5A1A6EB076a2C7aD06eD22C90d7E710E35ad0a")
	PENDLE   = common.HexToAddress("0x0c880f6761F1af8d9Aa9C466984b80DAb9a8c9e8")
	QuoterV2 = common.HexToAddress("0x61fFE014bA17989E743c5F6cB21bF9697530B21e")

	// V2 DEX routers & factories
	SushiFactory   = common.HexToAddress("0xc35DADB65012eC5796536bD9864eD8773aBc74C4")
	CamelotFactory = common.HexToAddress("0x6EcCab422D763aC031210895C81787E87B43A652")
)

type token struct {
	name     string
	addr     common.Address
	decimals int
}

func main() {
	client, err := ethclient.Dial("https://warmhearted-wild-road.arbitrum-mainnet.quiknode.pro/1ed461039e0b8a3008160cbc130479deb34798c9/")
	if err != nil {
		fmt.Printf("RPC: %v\n", err)
		return
	}
	defer client.Close()

	tokens := []token{
		{"WETH", WETH, 18},
		{"USDT", USDT, 6},
		{"USDC", USDC, 6},
		{"USDCe", USDCe, 6},
		{"DAI", DAI, 18},
		{"WBTC", WBTC, 8},
		{"LINK", LINK, 18},
		{"ARB", ARB, 18},
		{"GMX", GMX, 18},
		{"PENDLE", PENDLE, 18},
	}

	// ============================
	// 1. V3 Fee Tier Arbitrage
	// ============================
	fmt.Println("=== V3 FEE TIER ARBITRAGE ===")
	fmt.Println("同一币对，不同 fee tier 之间的价差")
	fmt.Println()

	feeTiers := []int64{100, 500, 3000, 10000}
	// 测试 WETH 对各主要 token 在不同 fee tier 的价格
	for _, t := range tokens {
		if t.addr == WETH {
			continue
		}
		// 用 $30 USDT 的等价量测试
		type result struct {
			fee  int64
			price float64
		}
		var results []result

		for _, fee := range feeTiers {
			var price float64
			if t.name == "USDT" || t.name == "USDC" || t.name == "USDCe" || t.name == "DAI" {
				// WETH → stablecoin
				out := quoteSingleFee(client, WETH, t.addr, 0.015, 18, t.decimals, fee)
				if out > 0 {
					price = out / 0.015
				}
			} else {
				// WETH → token
				out := quoteSingleFee(client, WETH, t.addr, 0.015, 18, t.decimals, fee)
				if out > 0 {
					price = out / 0.015
				}
			}
			if price > 0 {
				results = append(results, result{fee, price})
			}
		}

		if len(results) < 2 {
			continue
		}

		// 找最大价差
		minP, maxP := results[0].price, results[0].price
		var minFee, maxFee int64
		minFee, maxFee = results[0].fee, results[0].fee
		for _, r := range results[1:] {
			if r.price < minP {
				minP = r.price
				minFee = r.fee
			}
			if r.price > maxP {
				maxP = r.price
				maxFee = r.fee
			}
		}

		spread := (maxP - minP) / minP * 100
		if spread > 0.05 { // 只显示 >0.05% 的
			mark := ""
			if spread > 0.3 {
				mark = " **"
			} else if spread > 0.1 {
				mark = " *"
			}
			fmt.Printf("  WETH/%s: %.4f%% (buy@%d sell@%d)%s\n", t.name, spread, minFee, maxFee, mark)
			for _, r := range results {
				fmt.Printf("    fee=%5d: %.6f\n", r.fee, r.price)
			}
		}
	}

	// ============================
	// 2. V3 vs V2 (SushiSwap)
	// ============================
	fmt.Println()
	fmt.Println("=== V3 vs SUSHI V2 ===")
	fmt.Println("Uniswap V3 vs SushiSwap V2 (only pools with real liquidity)")
	fmt.Println()

	v2Pairs := []struct {
		t    token
		amt  float64 // test amount in WETH
	}{
		{token{"LINK", LINK, 18}, 0.015},
		{token{"ARB", ARB, 18}, 0.015},
		{token{"GMX", GMX, 18}, 0.015},
		{token{"PENDLE", PENDLE, 18}, 0.015},
		{token{"USDT", USDT, 6}, 0.015},
		{token{"USDC", USDC, 6}, 0.015},
		{token{"WBTC", WBTC, 8}, 0.015},
	}

	for _, p := range v2Pairs {
		// V3 quote (fee 500)
		v3Out := quoteSingleFee(client, WETH, p.t.addr, p.amt, 18, p.t.decimals, 500)
		// V3 quote (fee 3000)
		v3Out3000 := quoteSingleFee(client, WETH, p.t.addr, p.amt, 18, p.t.decimals, 3000)

		// Sushi V2 quote
		sushiOut, sushiLiq := getV2Quote(client, SushiFactory, WETH, p.t.addr, p.amt, 18, p.t.decimals)

		// Camelot V2 quote
		camOut, camLiq := getV2Quote(client, CamelotFactory, WETH, p.t.addr, p.amt, 18, p.t.decimals)

		if v3Out <= 0 && v3Out3000 <= 0 {
			continue
		}

		bestV3 := v3Out
		bestV3Fee := int64(500)
		if v3Out3000 > v3Out {
			bestV3 = v3Out3000
			bestV3Fee = 3000
		}

		if bestV3 <= 0 {
			continue
		}

		// Compare V3 vs Sushi
		if sushiOut > 0 && sushiLiq > 1 { // >$1 liquidity
			spread := (sushiOut - bestV3) / bestV3 * 100
			if spread < 0 {
				spread = (bestV3 - sushiOut) / sushiOut * 100
			}
			dir := "V3→Sushi"
			if bestV3 > sushiOut {
				dir = "Sushi→V3"
			}
			mark := ""
			if spread > 0.3 {
				mark = " **"
			}
			fmt.Printf("  WETH/%s V3(%d) vs Sushi: %.4f%% %s liq=$%.0f%s\n",
				p.t.name, bestV3Fee, spread, dir, sushiLiq, mark)
		}

		// Compare V3 vs Camelot
		if camOut > 0 && camLiq > 1 {
			spread := (camOut - bestV3) / bestV3 * 100
			if spread < 0 {
				spread = (bestV3 - camOut) / camOut * 100
			}
			dir := "V3→Cam"
			if bestV3 > camOut {
				dir = "Cam→V3"
			}
			mark := ""
			if spread > 0.3 {
				mark = " **"
			}
			fmt.Printf("  WETH/%s V3(%d) vs Camelot: %.4f%% %s liq=$%.0f%s\n",
				p.t.name, bestV3Fee, spread, dir, camLiq, mark)
		}
	}

	// ============================
	// 3. Stablecoin Arbitrage
	// ============================
	fmt.Println()
	fmt.Println("=== STABLECOIN ARB ===")
	fmt.Println("USDT/USDC/USDCe/DAI 之间的价差")
	fmt.Println()

	stables := []token{
		{"USDT", USDT, 6},
		{"USDC", USDC, 6},
		{"USDCe", USDCe, 6},
		{"DAI", DAI, 18},
	}

	for i, a := range stables {
		for j, b := range stables {
			if i >= j {
				continue
			}
			// Try fee=100 (0.01%) for stablecoins
			out := quoteSingleFee(client, a.addr, b.addr, 100, a.decimals, b.decimals, 100)
			if out <= 0 {
				// Try fee=500
				out = quoteSingleFee(client, a.addr, b.addr, 100, a.decimals, b.decimals, 500)
			}
			if out > 0 {
				spread := (out - 100) / 100 * 100
				if spread < 0 {
					spread = -spread
				}
				fmt.Printf("  %s→%s: %.6f (spread=%.4f%%)\n", a.name, b.name, out, spread)
			}
		}
	}

	// ============================
	// 4. Triangular Arbitrage
	// ============================
	fmt.Println()
	fmt.Println("=== TRIANGULAR ARB ===")
	fmt.Println("A→B→C→A 三角套利")
	fmt.Println()

	// USDT→WETH→LINK→USDT vs USDT→LINK direct
	{
		// Path 1: USDT→WETH(500)→LINK(500) (multi-hop, 0.10% total)
		link1 := quoteMultiHop(client, USDT, WETH, LINK, 30, 6, 18, 500, 500)

		// Path 2: USDT→LINK direct (3000)
		link2 := quoteSingleFee(client, USDT, LINK, 30, 6, 18, 3000)

		if link1 > 0 && link2 > 0 {
			spread := (link1 - link2) / link2 * 100
			fmt.Printf("  USDT→LINK: via WETH(500+500)=%.6f vs direct(3000)=%.6f spread=%.4f%%\n",
				link1, link2, spread)
		}

		// If we can buy cheap via one and sell via the other
		// Buy LINK on cheaper path, sell on expensive path
		// Check reverse: LINK→USDT both ways
		usdt1 := quoteMultiHop(client, LINK, WETH, USDT, 3.0, 18, 6, 500, 500) // ~$30 LINK
		usdt2 := quoteSingleFee(client, LINK, USDT, 3.0, 18, 6, 3000)

		if usdt1 > 0 && usdt2 > 0 {
			spread := (usdt1 - usdt2) / usdt2 * 100
			fmt.Printf("  LINK→USDT: via WETH(500+500)=$%.4f vs direct(3000)=$%.4f spread=%.4f%%\n",
				usdt1, usdt2, spread)
		}

		// 三角: USDT→WETH(500)→LINK(500)→USDT(3000)
		if link1 > 0 {
			finalUSDT := quoteSingleFee(client, LINK, USDT, link1, 18, 6, 3000)
			if finalUSDT > 0 {
				profit := finalUSDT - 30
				pct := profit / 30 * 100
				fmt.Printf("  △ USDT→WETH→LINK→USDT: $30→%.4f LINK→$%.4f (%+.4f%%) net=$%+.4f\n",
					link1, finalUSDT, pct, profit-0.02)
			}
		}
	}

	// WETH→USDC→USDT→WETH
	{
		usdc := quoteSingleFee(client, WETH, USDC, 0.015, 18, 6, 500)
		if usdc > 0 {
			usdt := quoteSingleFee(client, USDC, USDT, usdc, 6, 6, 100)
			if usdt > 0 {
				weth := quoteSingleFee(client, USDT, WETH, usdt, 6, 18, 500)
				if weth > 0 {
					profit := (weth - 0.015) / 0.015 * 100
					fmt.Printf("  △ WETH→USDC→USDT→WETH: 0.015→%.4f USDC→%.4f USDT→%.6f WETH (%+.4f%%)\n",
						usdc, usdt, weth, profit)
				}
			}
		}
	}

	// ARB triangular
	{
		arb := quoteSingleFee(client, USDT, ARB, 30, 6, 18, 500)
		if arb > 0 {
			weth := quoteSingleFee(client, ARB, WETH, arb, 18, 18, 500)
			if weth > 0 {
				usdt := quoteSingleFee(client, WETH, USDT, weth, 18, 6, 500)
				if usdt > 0 {
					profit := (usdt - 30) / 30 * 100
					fmt.Printf("  △ USDT→ARB→WETH→USDT: $30→%.2f ARB→%.6f WETH→$%.4f (%+.4f%%)\n",
						arb, weth, usdt, profit)
				}
			}
		}
	}
}

// ============ V3 Quotes ============

func quoteMultiHop(client *ethclient.Client, a, b, c common.Address, amt float64, aDec, cDec int, fee1, fee2 int64) float64 {
	mid := quoteSingleFee(client, a, b, amt, aDec, 18, fee1)
	if mid <= 0 {
		return 0
	}
	bDec := 18
	if b == USDT || b == USDC || b == USDCe {
		bDec = 6
	} else if b == WBTC {
		bDec = 8
	}
	_ = bDec
	return quoteSingleFee(client, b, c, mid, 18, cDec, fee2)
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

// ============ V2 Quotes ============

func getV2Quote(client *ethclient.Client, factory, tokenA, tokenB common.Address, amtIn float64, aDec, bDec int) (amtOut float64, liqUSD float64) {
	// getPair(tokenA, tokenB)
	getPairSig, _ := hex.DecodeString("e6a43905")
	data := make([]byte, 0, 68)
	data = append(data, getPairSig...)
	data = append(data, common.LeftPadBytes(tokenA.Bytes(), 32)...)
	data = append(data, common.LeftPadBytes(tokenB.Bytes(), 32)...)

	result, err := client.CallContract(context.Background(), ethereum.CallMsg{To: &factory, Data: data}, nil)
	if err != nil || len(result) < 32 {
		return 0, 0
	}

	pairAddr := common.BytesToAddress(result[12:32])
	if pairAddr == (common.Address{}) {
		return 0, 0
	}

	// getReserves()
	resSig, _ := hex.DecodeString("0902f1ac")
	result, err = client.CallContract(context.Background(), ethereum.CallMsg{To: &pairAddr, Data: resSig}, nil)
	if err != nil || len(result) < 64 {
		return 0, 0
	}

	reserve0 := new(big.Int).SetBytes(result[:32])
	reserve1 := new(big.Int).SetBytes(result[32:64])

	// token0()
	t0Sig, _ := hex.DecodeString("0dfe1681")
	t0Result, _ := client.CallContract(context.Background(), ethereum.CallMsg{To: &pairAddr, Data: t0Sig}, nil)
	if len(t0Result) < 32 {
		return 0, 0
	}
	token0 := common.BytesToAddress(t0Result[12:32])

	var reserveIn, reserveOut *big.Int
	var outDec int
	if token0 == tokenA {
		reserveIn = reserve0
		reserveOut = reserve1
		outDec = bDec
	} else {
		reserveIn = reserve1
		reserveOut = reserve0
		outDec = bDec
	}

	// Check liquidity (in USD terms, rough)
	rInF := weiToFloat(reserveIn, aDec)
	if tokenA == WETH {
		liqUSD = rInF * 2000 * 2 // rough ETH price × 2 sides
	} else {
		liqUSD = rInF * 2
	}

	// V2 AMM: amountOut = reserveOut * amountIn * 997 / (reserveIn * 1000 + amountIn * 997)
	amountInWei := floatToWei(amtIn, aDec)
	if amountInWei.Cmp(reserveIn) >= 0 {
		return 0, liqUSD // trade too large for pool
	}

	num := new(big.Int).Mul(reserveOut, new(big.Int).Mul(amountInWei, big.NewInt(997)))
	den := new(big.Int).Add(
		new(big.Int).Mul(reserveIn, big.NewInt(1000)),
		new(big.Int).Mul(amountInWei, big.NewInt(997)),
	)
	outWei := new(big.Int).Div(num, den)
	_ = outDec
	return weiToFloat(outWei, bDec), liqUSD
}

// ============ Utils ============

func weiToFloat(wei *big.Int, dec int) float64 {
	if wei == nil || wei.Sign() == 0 {
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
