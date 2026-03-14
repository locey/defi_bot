// cmd/mega-scan/main.go
// 全面 DEX 套利扫描器:
// 1. 1inch 聚合器 vs 本地 V3 报价 (找更优路由)
// 2. Curve 稳定币池 (USDT/USDC/USDCe 2pool)
// 3. Balancer V2 (weighted pools)
// 4. V3 fee tier 套利 (只看高流动性 500 vs 3000)
// 5. CEX-DEX 多币对扫描
package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
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
	WETH  = common.HexToAddress("0x82aF49447D8a07e3bd95BD0d56f35241523fBab1")
	USDT  = common.HexToAddress("0xFd086bC7CD5C481DCC9C85ebE478A1C0b69FCbb9")
	USDC  = common.HexToAddress("0xaf88d065e77c8cC2239327C5EDb3A432268e5831")
	USDCe = common.HexToAddress("0xFF970A61A04b1cA14834A43f5dE4533eBDDB5CC8")
	DAI   = common.HexToAddress("0xDA10009cBd5D07dd0CeCc66161FC93D7c9000da1")
	WBTC  = common.HexToAddress("0x2f2a2543B76A4166549F7aaB2e75Bef0aefC5B0f")
	LINK  = common.HexToAddress("0xf97f4df75117a78c1A5a0DBb814Af92458539FB4")
	ARB   = common.HexToAddress("0x912CE59144191C1204E64559FE8253a0e49E6548")

	QuoterV2 = common.HexToAddress("0x61fFE014bA17989E743c5F6cB21bF9697530B21e")

	// Curve on Arbitrum
	Curve2Pool = common.HexToAddress("0x7f90122BF0700F9E7e1F688fe926940E8839F353") // USDC/USDT

	// Balancer V2 Vault
	BalancerVault = common.HexToAddress("0xBA12222222228d8Ba445958a75a0704d566BF2C8")
)

type token struct {
	name string
	addr common.Address
	dec  int
}

func main() {
	client, err := ethclient.Dial("https://warmhearted-wild-road.arbitrum-mainnet.quiknode.pro/1ed461039e0b8a3008160cbc130479deb34798c9/")
	if err != nil {
		fmt.Printf("RPC: %v\n", err)
		return
	}
	defer client.Close()

	// ==========================
	// 1. 1inch vs V3 comparison
	// ==========================
	fmt.Println("=== 1INCH vs UNISWAP V3 ===")
	fmt.Println("Compare 1inch aggregator routes vs direct V3 for $30 trades")
	fmt.Println()

	oneInchPairs := []struct {
		src, dst token
		amount   float64
	}{
		{token{"USDT", USDT, 6}, token{"WETH", WETH, 18}, 30},
		{token{"USDT", USDT, 6}, token{"LINK", LINK, 18}, 30},
		{token{"USDT", USDT, 6}, token{"WBTC", WBTC, 8}, 30},
		{token{"USDT", USDT, 6}, token{"ARB", ARB, 18}, 30},
		{token{"WETH", WETH, 18}, token{"USDT", USDT, 6}, 0.015},
		{token{"WETH", WETH, 18}, token{"LINK", LINK, 18}, 0.015},
		{token{"USDC", USDC, 6}, token{"USDT", USDT, 6}, 100},
		{token{"USDT", USDT, 6}, token{"USDC", USDC, 6}, 100},
		{token{"USDCe", USDCe, 6}, token{"USDT", USDT, 6}, 100},
	}

	for _, p := range oneInchPairs {
		// V3 quote (via WETH multi-hop for non-WETH pairs, direct for WETH pairs)
		var v3Out float64
		if p.src.addr == WETH || p.dst.addr == WETH {
			v3Out = quoteSingleFee(client, p.src.addr, p.dst.addr, p.amount, p.src.dec, p.dst.dec, 500)
		} else {
			// multi-hop via WETH
			mid := quoteSingleFee(client, p.src.addr, WETH, p.amount, p.src.dec, 18, 500)
			if mid > 0 {
				v3Out = quoteSingleFee(client, WETH, p.dst.addr, mid, 18, p.dst.dec, 500)
			}
			// also try direct
			direct := quoteSingleFee(client, p.src.addr, p.dst.addr, p.amount, p.src.dec, p.dst.dec, 500)
			if direct > v3Out {
				v3Out = direct
			}
			direct100 := quoteSingleFee(client, p.src.addr, p.dst.addr, p.amount, p.src.dec, p.dst.dec, 100)
			if direct100 > v3Out {
				v3Out = direct100
			}
		}

		// 1inch quote
		inchOut := get1inchQuote(p.src.addr, p.dst.addr, p.amount, p.src.dec, p.dst.dec)

		if v3Out > 0 || inchOut > 0 {
			improvement := float64(0)
			if v3Out > 0 && inchOut > 0 {
				improvement = (inchOut - v3Out) / v3Out * 100
			}
			mark := ""
			if improvement > 0.3 {
				mark = " ** OPPORTUNITY"
			} else if improvement > 0.1 {
				mark = " *"
			}
			fmt.Printf("  %s→%s ($%.1f): V3=%.6f 1inch=%.6f (%+.3f%%)%s\n",
				p.src.name, p.dst.name, p.amount, v3Out, inchOut, improvement, mark)
		}
		time.Sleep(300 * time.Millisecond) // 1inch rate limit
	}

	// ==========================
	// 2. Curve Stablecoin Pools
	// ==========================
	fmt.Println()
	fmt.Println("=== CURVE STABLE POOLS ===")
	fmt.Println()

	// Curve 2pool: USDC(0), USDT(1) on Arbitrum
	// Try exchange: get_dy(i, j, amount)
	curveOut := getCurveQuote(client, Curve2Pool, 0, 1, 100, 6, 6) // USDC→USDT $100
	if curveOut > 0 {
		v3Direct := quoteSingleFee(client, USDC, USDT, 100, 6, 6, 100) // V3 fee=0.01%
		spread := float64(0)
		if v3Direct > 0 {
			spread = (curveOut - v3Direct) / v3Direct * 100
		}
		fmt.Printf("  USDC→USDT $100: Curve=%.6f V3(100)=%.6f spread=%.4f%%\n", curveOut, v3Direct, spread)
	} else {
		fmt.Println("  Curve 2pool: no response (may not exist at this address)")
		// Try alternate Curve addresses on Arbitrum
		altCurve := []common.Address{
			common.HexToAddress("0x960ea3e3C7FB317332d990873d354E18d7645590"), // Curve USDC/USDT/DAI
			common.HexToAddress("0xbF7E49483881C76487b0989CD7d9A8239B20CA41"), // Curve 2pool alt
		}
		for _, c := range altCurve {
			out := getCurveQuote(client, c, 0, 1, 100, 6, 6)
			if out > 0 {
				fmt.Printf("  Alt Curve %s: USDC→USDT $100 = %.6f\n", c.Hex()[:10], out)
			}
		}
	}

	// ==========================
	// 3. Balancer V2
	// ==========================
	fmt.Println()
	fmt.Println("=== BALANCER V2 ===")
	fmt.Println()

	// Balancer on Arbitrum — query popular pool IDs
	// WETH/USDC weighted pool
	balPools := []struct {
		name   string
		poolID string
		tokenIn, tokenOut common.Address
		amount float64
		inDec, outDec int
	}{
		{
			"WETH→USDC",
			"0x64541216bafffeec8ea535bb71fbc927831d0595000100000000000000000002", // WETH/USDC/USDT
			WETH, USDC, 0.015, 18, 6,
		},
		{
			"WETH→USDT",
			"0x64541216bafffeec8ea535bb71fbc927831d0595000100000000000000000002",
			WETH, USDT, 0.015, 18, 6,
		},
	}

	for _, bp := range balPools {
		balOut := getBalancerQuote(client, bp.poolID, bp.tokenIn, bp.tokenOut, bp.amount, bp.inDec, bp.outDec)
		v3Out := quoteSingleFee(client, bp.tokenIn, bp.tokenOut, bp.amount, bp.inDec, bp.outDec, 500)
		if balOut > 0 || v3Out > 0 {
			spread := float64(0)
			if balOut > 0 && v3Out > 0 {
				spread = (balOut - v3Out) / v3Out * 100
			}
			fmt.Printf("  %s: Bal=%.6f V3(500)=%.6f spread=%.4f%%\n", bp.name, balOut, v3Out, spread)
		}
	}

	// ==========================
	// 4. V3 Fee Tier Arb (high liq only: 500 vs 3000)
	// ==========================
	fmt.Println()
	fmt.Println("=== V3 FEE TIER ARB (500 vs 3000 only) ===")
	fmt.Println()

	tierPairs := []struct {
		name string
		t    common.Address
		dec  int
		amt  float64 // in WETH
	}{
		{"USDT", USDT, 6, 0.015},
		{"USDC", USDC, 6, 0.015},
		{"LINK", LINK, 18, 0.015},
		{"ARB", ARB, 18, 0.015},
		{"WBTC", WBTC, 8, 0.015},
	}

	for _, p := range tierPairs {
		p500 := quoteSingleFee(client, WETH, p.t, p.amt, 18, p.dec, 500)
		p3000 := quoteSingleFee(client, WETH, p.t, p.amt, 18, p.dec, 3000)
		if p500 > 0 && p3000 > 0 {
			spread := (p500 - p3000) / p3000 * 100
			// Round-trip: buy cheap, sell expensive
			if p500 > p3000 {
				// Buy at 3000, sell at 500
				sellWETH := quoteSingleFee(client, p.t, WETH, p3000, p.dec, 18, 500)
				if sellWETH > 0 {
					net := (sellWETH - p.amt) / p.amt * 100
					fmt.Printf("  WETH/%s: 500=%.6f 3000=%.6f sp=%.3f%% rt=%+.3f%%\n",
						p.name, p500, p3000, spread, net)
				}
			} else {
				sellWETH := quoteSingleFee(client, p.t, WETH, p500, p.dec, 18, 3000)
				if sellWETH > 0 {
					net := (sellWETH - p.amt) / p.amt * 100
					fmt.Printf("  WETH/%s: 500=%.6f 3000=%.6f sp=%.3f%% rt=%+.3f%%\n",
						p.name, p500, p3000, spread, net)
				}
			}
		}
	}

	// ==========================
	// 5. CEX-DEX Extended Pairs
	// ==========================
	fmt.Println()
	fmt.Println("=== CEX-DEX EXTENDED (via 1inch) ===")
	fmt.Println("Compare 1inch best DEX route vs Binance CEX")
	fmt.Println()

	cexPairs := []struct {
		name   string
		symbol string // Binance
		t      token
		fee    int64 // V3 fee tier for WETH pair
	}{
		{"LINK", "LINKUSDT", token{"LINK", LINK, 18}, 500},
		{"ARB", "ARBUSDT", token{"ARB", ARB, 18}, 500},
		{"WBTC", "WBTCUSDT", token{"WBTC", WBTC, 8}, 500},
		{"GMX", "GMXUSDT", token{"GMX", common.HexToAddress("0xfc5A1A6EB076a2C7aD06eD22C90d7E710E35ad0a"), 18}, 3000},
		{"PENDLE", "PENDLEUSDT", token{"PENDLE", common.HexToAddress("0x0c880f6761F1af8d9Aa9C466984b80DAb9a8c9e8"), 18}, 3000},
		{"UNI", "UNIUSDT", token{"UNI", common.HexToAddress("0xFa7F8980b0f1E64A2062791cc3b0871572f1F7f0"), 18}, 3000},
		{"AAVE", "AAVEUSDT", token{"AAVE", common.HexToAddress("0xba5DdD1f9d7F570dc94a51479a000E3BCE967196"), 18}, 3000},
	}

	for _, p := range cexPairs {
		cexBid := getBidPrice(p.symbol)
		if cexBid == 0 {
			continue
		}

		// 1inch quote: buy token with $30 USDT
		inchBuy := get1inchQuote(USDT, p.t.addr, 30, 6, p.t.dec)
		// V3 quote
		v3Buy := quoteMultiHop(client, USDT, WETH, p.t.addr, 30, 6, p.t.dec, 500, p.fee)

		bestDex := v3Buy
		source := "V3"
		if inchBuy > v3Buy {
			bestDex = inchBuy
			source = "1inch"
		}

		if bestDex > 0 {
			dexPrice := 30 / bestDex
			fwdSpread := (cexBid - dexPrice) / dexPrice * 100

			mark := ""
			if fwdSpread > 0.5 {
				mark = " ** ACTIONABLE"
			} else if fwdSpread > 0.3 {
				mark = " *"
			}
			fmt.Printf("  %s: cex=$%.4f dex=$%.4f(%s) fwd=%+.3f%%%s\n",
				p.name, cexBid, dexPrice, source, fwdSpread, mark)
		}
		time.Sleep(300 * time.Millisecond)
	}
}

// ============ 1inch Quote ============

func get1inchQuote(src, dst common.Address, amount float64, srcDec, dstDec int) float64 {
	amtWei := floatToWei(amount, srcDec)
	url := fmt.Sprintf("https://api.1inch.dev/swap/v6.0/42161/quote?src=%s&dst=%s&amount=%s",
		src.Hex(), dst.Hex(), amtWei.String())

	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Accept", "application/json")
	// No API key needed for basic quotes (rate limited)

	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return 0
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		// Try without auth
		return 0
	}

	var result struct {
		DstAmount string `json:"dstAmount"`
	}
	if json.Unmarshal(body, &result) != nil || result.DstAmount == "" {
		return 0
	}

	out, ok := new(big.Int).SetString(result.DstAmount, 10)
	if !ok {
		return 0
	}
	return weiToFloat(out, dstDec)
}

// ============ Curve Quote ============

func getCurveQuote(client *ethclient.Client, pool common.Address, i, j int, amount float64, inDec, outDec int) float64 {
	// get_dy(int128 i, int128 j, uint256 dx) returns (uint256)
	abiJSON := `[{"name":"get_dy","inputs":[{"name":"i","type":"int128"},{"name":"j","type":"int128"},{"name":"dx","type":"uint256"}],"outputs":[{"name":"","type":"uint256"}],"stateMutability":"view","type":"function"}]`
	parsedABI, err := abi.JSON(strings.NewReader(abiJSON))
	if err != nil {
		return 0
	}
	data, err := parsedABI.Pack("get_dy", big.NewInt(int64(i)), big.NewInt(int64(j)), floatToWei(amount, inDec))
	if err != nil {
		return 0
	}

	result, err := client.CallContract(context.Background(), ethereum.CallMsg{To: &pool, Data: data}, nil)
	if err != nil || len(result) < 32 {
		return 0
	}

	out := new(big.Int).SetBytes(result[:32])
	return weiToFloat(out, outDec)
}

// ============ Balancer V2 Quote ============

func getBalancerQuote(client *ethclient.Client, poolIDHex string, tokenIn, tokenOut common.Address, amount float64, inDec, outDec int) float64 {
	// Balancer V2 uses queryBatchSwap on the Vault
	// For simplicity, we'll use a single swap
	abiJSON := `[{"inputs":[{"internalType":"enum IVault.SwapKind","name":"kind","type":"uint8"},{"components":[{"internalType":"bytes32","name":"poolId","type":"bytes32"},{"internalType":"uint256","name":"assetInIndex","type":"uint256"},{"internalType":"uint256","name":"assetOutIndex","type":"uint256"},{"internalType":"uint256","name":"amount","type":"uint256"},{"internalType":"bytes","name":"userData","type":"bytes"}],"internalType":"struct IVault.BatchSwapStep[]","name":"swaps","type":"tuple[]"},{"internalType":"contract IAsset[]","name":"assets","type":"address[]"},{"components":[{"internalType":"address","name":"sender","type":"address"},{"internalType":"bool","name":"fromInternalBalance","type":"bool"},{"internalType":"address payable","name":"recipient","type":"address"},{"internalType":"bool","name":"toInternalBalance","type":"bool"}],"internalType":"struct IVault.FundManagement","name":"funds","type":"tuple"}],"name":"queryBatchSwap","outputs":[{"internalType":"int256[]","name":"","type":"int256[]"}],"stateMutability":"nonpayable","type":"function"}]`

	parsedABI, err := abi.JSON(strings.NewReader(abiJSON))
	if err != nil {
		return 0
	}

	poolID := common.FromHex(poolIDHex)
	var poolIDBytes [32]byte
	copy(poolIDBytes[:], poolID)

	type BatchSwapStep struct {
		PoolId        [32]byte
		AssetInIndex  *big.Int
		AssetOutIndex *big.Int
		Amount        *big.Int
		UserData      []byte
	}

	type FundManagement struct {
		Sender              common.Address
		FromInternalBalance bool
		Recipient           common.Address
		ToInternalBalance   bool
	}

	swaps := []BatchSwapStep{
		{
			PoolId:        poolIDBytes,
			AssetInIndex:  big.NewInt(0),
			AssetOutIndex: big.NewInt(1),
			Amount:        floatToWei(amount, inDec),
			UserData:      []byte{},
		},
	}

	assets := []common.Address{tokenIn, tokenOut}
	funds := FundManagement{
		Sender:    common.Address{},
		Recipient: common.Address{},
	}

	data, err := parsedABI.Pack("queryBatchSwap",
		uint8(0), // GIVEN_IN
		swaps,
		assets,
		funds,
	)
	if err != nil {
		return 0
	}

	result, err := client.CallContract(context.Background(), ethereum.CallMsg{To: &BalancerVault, Data: data}, nil)
	if err != nil || len(result) < 64 {
		return 0
	}

	// Result is int256[] — the second element (index 1) is the negative of output amount
	// We need to decode the dynamic array
	// offset at bytes 0-31, length at offset, then elements
	if len(result) < 128 {
		return 0
	}
	outRaw := new(big.Int).SetBytes(result[96:128])
	// It's a signed int256 — if negative, it represents output
	// Check if the high bit is set (negative)
	if result[96] >= 0x80 {
		// Two's complement
		max := new(big.Int).Lsh(big.NewInt(1), 256)
		outRaw = new(big.Int).Sub(max, outRaw)
	}
	return weiToFloat(outRaw, outDec)
}

// ============ V3 Quotes ============

func quoteMultiHop(client *ethclient.Client, a, mid, b common.Address, amt float64, aDec, bDec int, fee1, fee2 int64) float64 {
	midOut := quoteSingleFee(client, a, mid, amt, aDec, 18, fee1)
	if midOut <= 0 {
		return 0
	}
	return quoteSingleFee(client, mid, b, midOut, 18, bDec, fee2)
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

// ============ CEX Prices ============

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

// ============ Utils ============

func getBalance(client *ethclient.Client, token, owner common.Address, dec int) float64 {
	sig, _ := hex.DecodeString("70a08231")
	data := append(sig, common.LeftPadBytes(owner.Bytes(), 32)...)
	result, err := client.CallContract(context.Background(), ethereum.CallMsg{To: &token, Data: data}, nil)
	if err != nil || len(result) < 32 {
		return 0
	}
	return weiToFloat(new(big.Int).SetBytes(result[:32]), dec)
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
