// cmd/dex-dex-arb/main.go
// DEX-DEX 单次套利: SushiSwap V2 买 + Uniswap V3 卖 (同一笔交易里原子执行)
// 路径: USDT→WETH(V3 500)→Token(Sushi V2)→WETH(V3 fee)→USDT(V3 500)
// 实际可行路径: 分两笔交易 — 先在便宜的 DEX 买，再在贵的 DEX 卖
package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
)

var (
	WETH         = common.HexToAddress("0x82aF49447D8a07e3bd95BD0d56f35241523fBab1")
	USDT         = common.HexToAddress("0xFd086bC7CD5C481DCC9C85ebE478A1C0b69FCbb9")
	WBTC         = common.HexToAddress("0x2f2a2543B76A4166549F7aaB2e75Bef0aefC5B0f")
	LINK         = common.HexToAddress("0xf97f4df75117a78c1A5a0DBb814Af92458539FB4")
	V3Router     = common.HexToAddress("0xE592427A0AEce92De3Edee1F18E0157C05861564")
	QuoterV2     = common.HexToAddress("0x61fFE014bA17989E743c5F6cB21bF9697530B21e")
	SushiRouter  = common.HexToAddress("0x1b02dA8Cb0d097eB8D57A175b88c7D8b47997506")
	SushiFactory = common.HexToAddress("0xc35DADB65012eC5796536bD9864eD8773aBc74C4")
	chainID      = big.NewInt(42161)
)

func main() {
	pk := os.Getenv("KEEPER_PRIVATE_KEY")
	if strings.HasPrefix(pk, "0x") {
		pk = pk[2:]
	}
	privKey, _ := crypto.HexToECDSA(pk)
	keeper := crypto.PubkeyToAddress(privKey.PublicKey)

	client, _ := ethclient.Dial("https://warmhearted-wild-road.arbitrum-mainnet.quiknode.pro/1ed461039e0b8a3008160cbc130479deb34798c9/")
	defer client.Close()

	fmt.Println("=== DEX-DEX Arbitrage Scanner ===")
	fmt.Printf("Keeper: %s\n\n", keeper.Hex())

	// 扫描方向: 每个 pair 检查两个方向
	// Direction A: Sushi 买 (便宜) → V3 卖 (贵) → 赚差价
	// Direction B: V3 买 (便宜) → Sushi 卖 (贵) → 赚差价

	type pair struct {
		name    string
		token   common.Address
		dec     int
		v3Fee   int64
		testETH float64 // WETH amount to test
	}

	testPairs := []pair{
		{"WBTC", WBTC, 8, 500, 0.015},
		{"LINK", LINK, 18, 500, 0.015},
		{"USDT", USDT, 6, 500, 0.015},
	}

	type opportunity struct {
		pair      pair
		direction string // "sushi→v3" or "v3→sushi"
		buyPrice  float64
		sellPrice float64
		spread    float64
		grossProfit float64
		netProfit   float64
	}
	var opps []opportunity

	for _, p := range testPairs {
		fmt.Printf("--- %s ---\n", p.name)

		// V3 price: WETH → Token
		v3Out := quoteSingleFee(client, WETH, p.token, p.testETH, 18, p.dec, p.v3Fee)
		// Sushi price: WETH → Token
		sushiOut := getV2Output(client, SushiFactory, WETH, p.token, p.testETH, 18, p.dec)

		// V3 reverse: Token → WETH
		v3Rev := float64(0)
		if v3Out > 0 {
			v3Rev = quoteSingleFee(client, p.token, WETH, v3Out, p.dec, 18, p.v3Fee)
		}

		if v3Out > 0 {
			fmt.Printf("  V3(%d):   %.8f %s (eff=%.6f %s/WETH)\n", p.v3Fee, v3Out, p.name, v3Out/p.testETH, p.name)
		}
		if sushiOut > 0 {
			fmt.Printf("  Sushi:   %.8f %s (eff=%.6f %s/WETH)\n", sushiOut, p.name, sushiOut/p.testETH, p.name)
		}

		// Direction A: Buy on Sushi (cheaper), Sell on V3 (more expensive)
		if sushiOut > 0 && v3Rev > 0 {
			// Buy token on Sushi with WETH, sell token on V3 for WETH
			// But we need to check: buy on Sushi, then sell THAT amount on V3
			sellWETH := quoteSingleFee(client, p.token, WETH, sushiOut, p.dec, 18, p.v3Fee)
			if sellWETH > 0 {
				spread := (sellWETH - p.testETH) / p.testETH * 100
				grossETH := sellWETH - p.testETH
				gasETH := 0.000015 // ~$0.03 for 2 swaps
				netETH := grossETH - gasETH
				netUSD := netETH * 2060
				fmt.Printf("  Sushi→V3: buy %.6f → sell %.6f WETH (spread=%+.4f%% net=$%+.4f)\n",
					p.testETH, sellWETH, spread, netUSD)

				if spread > 0 {
					opps = append(opps, opportunity{p, "sushi→v3", p.testETH, sellWETH, spread, grossETH * 2060, netUSD})
				}
			}
		}

		// Direction B: Buy on V3 (cheaper), Sell on Sushi (more expensive)
		if v3Out > 0 {
			sellWETH := getV2Output(client, SushiFactory, p.token, WETH, v3Out, p.dec, 18)
			if sellWETH > 0 {
				spread := (sellWETH - p.testETH) / p.testETH * 100
				grossETH := sellWETH - p.testETH
				gasETH := 0.000015
				netETH := grossETH - gasETH
				netUSD := netETH * 2060
				fmt.Printf("  V3→Sushi: buy %.6f → sell %.6f WETH (spread=%+.4f%% net=$%+.4f)\n",
					p.testETH, sellWETH, spread, netUSD)

				if spread > 0 {
					opps = append(opps, opportunity{p, "v3→sushi", p.testETH, sellWETH, spread, grossETH * 2060, netUSD})
				}
			}
		}
		fmt.Println()
	}

	// 直接测试 round-trip: USDT→V3→WETH→Sushi→LINK→V3→WETH→V3→USDT
	fmt.Println("--- ROUND TRIP: $30 USDT ---")
	{
		// Step 1: USDT→WETH via V3(500)
		weth := quoteSingleFee(client, USDT, WETH, 30, 6, 18, 500)
		if weth > 0 {
			fmt.Printf("  USDT→WETH(V3 500): $30 → %.6f WETH\n", weth)

			// Step 2: WETH→WBTC via Sushi
			wbtc := getV2Output(client, SushiFactory, WETH, WBTC, weth, 18, 8)
			if wbtc > 0 {
				fmt.Printf("  WETH→WBTC(Sushi): → %.8f WBTC\n", wbtc)

				// Step 3: WBTC→WETH via V3(500)
				weth2 := quoteSingleFee(client, WBTC, WETH, wbtc, 8, 18, 500)
				if weth2 > 0 {
					fmt.Printf("  WBTC→WETH(V3 500): → %.6f WETH\n", weth2)

					// Step 4: WETH→USDT via V3(500)
					usdt := quoteSingleFee(client, WETH, USDT, weth2, 18, 6, 500)
					if usdt > 0 {
						profit := usdt - 30
						pct := profit / 30 * 100
						fmt.Printf("  WETH→USDT(V3 500): → $%.4f (%+.4f%%) net=$%+.4f\n",
							usdt, pct, profit-0.03)
					}
				}
			}

			// Also try LINK path
			link := getV2Output(client, SushiFactory, WETH, LINK, weth, 18, 18)
			if link > 0 {
				fmt.Printf("  WETH→LINK(Sushi): → %.6f LINK\n", link)
				weth3 := quoteSingleFee(client, LINK, WETH, link, 18, 18, 500)
				if weth3 > 0 {
					usdt2 := quoteSingleFee(client, WETH, USDT, weth3, 18, 6, 500)
					if usdt2 > 0 {
						profit := usdt2 - 30
						pct := profit / 30 * 100
						fmt.Printf("  LINK→WETH(V3)→USDT: → $%.4f (%+.4f%%) net=$%+.4f\n",
							usdt2, pct, profit-0.03)
					}
				}
			}
		}
	}

	// Summary
	fmt.Println()
	fmt.Println("=== PROFITABLE OPPORTUNITIES ===")
	found := false
	for _, o := range opps {
		if o.netProfit > 0.01 {
			found = true
			fmt.Printf("  %s %s: spread=%.4f%% gross=$%.4f net=$%.4f\n",
				o.pair.name, o.direction, o.spread, o.grossProfit, o.netProfit)
		}
	}
	if !found {
		fmt.Println("  None found with net > $0.01")
	}

	// Also check allowances
	fmt.Println()
	fmt.Println("=== ALLOWANCE CHECK ===")
	checkAllowance(client, USDT, keeper, V3Router, "USDT→V3Router")
	checkAllowance(client, USDT, keeper, SushiRouter, "USDT→SushiRouter")
	checkAllowance(client, WETH, keeper, V3Router, "WETH→V3Router")
	checkAllowance(client, WETH, keeper, SushiRouter, "WETH→SushiRouter")
	checkAllowance(client, WBTC, keeper, V3Router, "WBTC→V3Router")
	checkAllowance(client, WBTC, keeper, SushiRouter, "WBTC→SushiRouter")
	checkAllowance(client, LINK, keeper, V3Router, "LINK→V3Router")
	checkAllowance(client, LINK, keeper, SushiRouter, "LINK→SushiRouter")

	_ = privKey
}

func checkAllowance(client *ethclient.Client, token, owner, spender common.Address, label string) {
	sig, _ := hex.DecodeString("dd62ed3e") // allowance(address,address)
	data := make([]byte, 0, 68)
	data = append(data, sig...)
	data = append(data, common.LeftPadBytes(owner.Bytes(), 32)...)
	data = append(data, common.LeftPadBytes(spender.Bytes(), 32)...)
	result, err := client.CallContract(context.Background(), ethereum.CallMsg{To: &token, Data: data}, nil)
	if err != nil || len(result) < 32 {
		fmt.Printf("  %s: ERROR\n", label)
		return
	}
	val := new(big.Int).SetBytes(result[:32])
	if val.Cmp(big.NewInt(1e15)) > 0 {
		fmt.Printf("  %s: OK (approved)\n", label)
	} else {
		fmt.Printf("  %s: NEEDS APPROVAL\n", label)
	}
}

// ============ V3 Quote ============

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

// ============ V2 Quote ============

func getV2Output(client *ethclient.Client, factory, tokenIn, tokenOut common.Address, amtIn float64, inDec, outDec int) float64 {
	// getPair
	getPairSig, _ := hex.DecodeString("e6a43905")
	data := make([]byte, 0, 68)
	data = append(data, getPairSig...)
	data = append(data, common.LeftPadBytes(tokenIn.Bytes(), 32)...)
	data = append(data, common.LeftPadBytes(tokenOut.Bytes(), 32)...)

	result, err := client.CallContract(context.Background(), ethereum.CallMsg{To: &factory, Data: data}, nil)
	if err != nil || len(result) < 32 {
		return 0
	}
	pairAddr := common.BytesToAddress(result[12:32])
	if pairAddr == (common.Address{}) {
		return 0
	}

	// getReserves
	resSig, _ := hex.DecodeString("0902f1ac")
	result, err = client.CallContract(context.Background(), ethereum.CallMsg{To: &pairAddr, Data: resSig}, nil)
	if err != nil || len(result) < 64 {
		return 0
	}
	reserve0 := new(big.Int).SetBytes(result[:32])
	reserve1 := new(big.Int).SetBytes(result[32:64])

	// token0
	t0Sig, _ := hex.DecodeString("0dfe1681")
	t0Result, _ := client.CallContract(context.Background(), ethereum.CallMsg{To: &pairAddr, Data: t0Sig}, nil)
	if len(t0Result) < 32 {
		return 0
	}
	token0 := common.BytesToAddress(t0Result[12:32])

	var reserveIn, reserveOut *big.Int
	if token0 == tokenIn {
		reserveIn = reserve0
		reserveOut = reserve1
	} else {
		reserveIn = reserve1
		reserveOut = reserve0
	}

	amountInWei := floatToWei(amtIn, inDec)
	if amountInWei.Cmp(reserveIn) >= 0 {
		return 0
	}

	// amountOut = reserveOut * amountIn * 997 / (reserveIn * 1000 + amountIn * 997)
	num := new(big.Int).Mul(reserveOut, new(big.Int).Mul(amountInWei, big.NewInt(997)))
	den := new(big.Int).Add(
		new(big.Int).Mul(reserveIn, big.NewInt(1000)),
		new(big.Int).Mul(amountInWei, big.NewInt(997)),
	)
	outWei := new(big.Int).Div(num, den)
	return weiToFloat(outWei, outDec)
}

// ============ Price Utils ============

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
