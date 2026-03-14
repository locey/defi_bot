// cmd/momentum-scanner/main.go
// 动量扫描器 — 实时扫描 Binance 上涨最快的币，在 Arbitrum DEX 上找套利机会
// 带止盈止损，发现 CEX-DEX 价差就套利
package main

import (
	"context"
	"crypto/ecdsa"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/defi-bot/backend/pkg/cex"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
)

// ============ Arbitrum Token Registry ============
// Binance symbol → Arbitrum address + V3 fee tier + decimals
// 仅包含同时在 Binance 和 Arbitrum Uniswap V3 上有流动性的代币

type arbToken struct {
	symbol   string         // Binance symbol suffix (e.g., "LINK")
	binPair  string         // Binance trading pair (e.g., "LINKUSDT")
	address  common.Address // Arbitrum ERC20 address
	decimals int
	feeTier  int64  // WETH pool fee tier
	minQty   string // Binance minimum sell qty
}

var tokenRegistry = []arbToken{
	// === Blue chips ===
	{"LINK", "LINKUSDT", common.HexToAddress("0xf97f4df75117a78c1A5a0DBb814Af92458539FB4"), 18, 500, "0.01"},
	{"AAVE", "AAVEUSDT", common.HexToAddress("0xba5DdD1f9d7F570dc94a51479a000E3BCE967196"), 18, 3000, "0.001"},
	{"UNI", "UNIUSDT", common.HexToAddress("0xFa7F8980b0f1E64A2062791cc3b0871572f1F7f0"), 18, 3000, "0.01"},
	{"ARB", "ARBUSDT", common.HexToAddress("0x912CE59144191C1204E64559FE8253a0e49E6548"), 18, 500, "0.1"},
	{"GMX", "GMXUSDT", common.HexToAddress("0xfc5A1A6EB076a2C7aD06eD22C90d7E710E35ad0a"), 18, 3000, "0.001"},
	{"PENDLE", "PENDLEUSDT", common.HexToAddress("0x0c880f6761F1af8d9Aa9C466984b80DAb9a8c9e8"), 18, 3000, "0.01"},
	{"GRT", "GRTUSDT", common.HexToAddress("0x9623063377AD1B27544C965cCd7342f7EA7e88C7"), 18, 3000, "1"},
	{"MAGIC", "MAGICUSDT", common.HexToAddress("0x539bdE0d7Dbd336b79148AA742883198BBF60342"), 18, 3000, "0.1"},
	{"LDO", "LDOUSDT", common.HexToAddress("0x13Ad51ed4F1B7e9Dc168d8a00cB3f4dDD85EfA60"), 18, 3000, "0.01"},
	{"WOO", "WOOUSDT", common.HexToAddress("0xcAFcD85D8ca7Ad1e1C6F82F651fA15E33AEfD07b"), 18, 3000, "1"},
	{"RDNT", "RDNTUSDT", common.HexToAddress("0x3082CC23568eA640225c2467653dB90e9250AaA0"), 18, 3000, "1"},
	{"DODO", "DODOUSDT", common.HexToAddress("0x69Eb4FA4a2fbd498C257C57Ea8b7655a2559A581"), 18, 3000, "1"},
	{"CRV", "CRVUSDT", common.HexToAddress("0x11cDb42B0EB46D95f990BeDD4695A6e3fA034978"), 18, 3000, "0.1"},
	{"SUSHI", "SUSHIUSDT", common.HexToAddress("0xd4d42F0b6DEF4CE0383636770eF773390d85c61A"), 18, 3000, "0.1"},
	{"COMP", "COMPUSDT", common.HexToAddress("0x354A6dA3fcde098F8389cad84b0182725c6C91dE"), 18, 3000, "0.001"},
	{"BAL", "BALUSDT", common.HexToAddress("0x040d1EdC9569d4Bab2D15287Dc5A4F10F56a56B8"), 18, 3000, "0.01"},
	// === Mid-caps / potential movers ===
	{"GRAIL", "GRAILUSDT", common.HexToAddress("0x3d9907F9a368ad0a51Be60f7Da3b97cf940982D8"), 18, 10000, "0.001"},
	{"JONES", "JONESUSDT", common.HexToAddress("0x10393c20975cF177a3513071bC110f7962CD67da"), 18, 10000, "0.1"},
	{"DPX", "DPXUSDT", common.HexToAddress("0x6C2C06790b3E3E3c38e12Ee22F8183b37a13EE55"), 18, 10000, "0.001"},
}

var (
	USDT     = common.HexToAddress("0xFd086bC7CD5C481DCC9C85ebE478A1C0b69FCbb9")
	WETH     = common.HexToAddress("0x82aF49447D8a07e3bd95BD0d56f35241523fBab1")
	V3Router = common.HexToAddress("0xE592427A0AEce92De3Edee1F18E0157C05861564")
	QuoterV2 = common.HexToAddress("0x61fFE014bA17989E743c5F6cB21bF9697530B21e")
	chainID  = big.NewInt(42161)
)

const (
	// 动量扫描参数
	scanInterval     = 3 * time.Minute  // 每 3 分钟扫描一次 Binance 涨幅榜
	minGain24h       = 3.0              // 24h 涨幅 > 3% 才关注
	topNTokens       = 10               // 最多关注前 10 个上涨的
	minBinanceVolume = 1000000.0        // 最低 24h 交易量 $1M

	// 套利参数
	dexFeePct   = 0.0010
	cexFeePct   = 0.0010
	slippagePct = 0.0015
	gasEstUSD   = 0.015
	minSpreadPct  = 0.35 // 动量代币降低门槛 (价格趋势向上)
	minEstProfit  = 0.02

	// 止盈止损参数 (动量策略更激进)
	takeProfitPct   = 0.05  // 5% 止盈 (动量币波动大)
	trailingStopPct = 0.025 // 2.5% trailing stop
	stopLossPct     = 0.03  // 3% 止损 (动量交易必须有止损)
	maxPositionUSD  = 30.0  // 单个动量持仓上限 $30
	maxTotalUSD     = 80.0  // 动量持仓总上限 $80

	pollInterval = 20 // 秒
)

// ============ Momentum Position ============

type momentumPos struct {
	token      arbToken
	entryPrice float64
	peakPrice  float64
	qty        float64   // DEX 上持仓量
	entryTime  time.Time
	totalCost  float64   // 总成本 (USD)
}

func (p *momentumPos) currentValue(price float64) float64 {
	return p.qty * price
}

func (p *momentumPos) pnl(price float64) float64 {
	return p.currentValue(price) - p.totalCost
}

func (p *momentumPos) pnlPct(price float64) float64 {
	if p.totalCost == 0 {
		return 0
	}
	return (p.currentValue(price) - p.totalCost) / p.totalCost * 100
}

// ============ Scanner State ============

type scanner struct {
	client    *ethclient.Client
	privKey   *ecdsa.PrivateKey
	keeper    common.Address
	trader    *cex.BinanceTrader

	// 发现的热门代币
	hotTokens []hotToken

	// 持仓
	positions map[string]*momentumPos // symbol → position

	// 统计
	totalTrades    int
	totalProfit    float64
	wins, losses   int
}

type hotToken struct {
	token      arbToken
	gain24h    float64 // 24h 涨幅 %
	volume24h  float64 // 24h 成交量 USD
	gain1h     float64 // 1h 涨幅 %
	hasArbPool bool    // Arbitrum 上有 V3 流动性
	dexPrice   float64 // DEX 实际价格
	cexPrice   float64 // CEX 价格
	spread     float64 // CEX-DEX 价差 %
}

// ============ Binance 24h Ticker ============

type binanceTicker struct {
	Symbol             string `json:"symbol"`
	PriceChangePercent string `json:"priceChangePercent"`
	LastPrice          string `json:"lastPrice"`
	QuoteVolume        string `json:"quoteVolume"`       // 24h volume in USDT
	PriceChange        string `json:"priceChange"`
}

func scanBinanceMomentum() []binanceTicker {
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Get(
		"https://api.binance.com/api/v3/ticker/24hr")
	if err != nil {
		fmt.Printf("[%s] Binance 24h ticker FAIL: %v\n", ts(), err)
		return nil
	}
	defer resp.Body.Close()

	var tickers []binanceTicker
	if err := json.NewDecoder(resp.Body).Decode(&tickers); err != nil {
		fmt.Printf("[%s] Parse tickers FAIL: %v\n", ts(), err)
		return nil
	}

	// 只保留 USDT 交易对
	var usdtPairs []binanceTicker
	for _, t := range tickers {
		if !strings.HasSuffix(t.Symbol, "USDT") {
			continue
		}
		// 过滤掉稳定币和杠杆代币
		base := strings.TrimSuffix(t.Symbol, "USDT")
		if base == "USDC" || base == "BUSD" || base == "DAI" || base == "TUSD" || base == "FDUSD" {
			continue
		}
		if strings.Contains(base, "UP") || strings.Contains(base, "DOWN") || strings.Contains(base, "BEAR") || strings.Contains(base, "BULL") {
			continue
		}
		usdtPairs = append(usdtPairs, t)
	}

	// 按涨幅排序
	sort.Slice(usdtPairs, func(i, j int) bool {
		gi := parseFloat(usdtPairs[i].PriceChangePercent)
		gj := parseFloat(usdtPairs[j].PriceChangePercent)
		return gi > gj
	})

	return usdtPairs
}

// ============ Main ============

func main() {
	fmt.Println("=== Momentum Scanner v1 — 热门代币扫描 + CEX-DEX 套利 + 止盈止损 ===")
	fmt.Printf("Scan: every %v | Min 24h gain: %.0f%% | Min volume: $%.0fM\n",
		scanInterval, minGain24h, minBinanceVolume/1e6)
	fmt.Printf("Take-profit: %.0f%% | Trailing-stop: %.1f%% | Stop-loss: %.0f%%\n",
		takeProfitPct*100, trailingStopPct*100, stopLossPct*100)
	fmt.Printf("Max position: $%.0f | Max total: $%.0f\n", maxPositionUSD, maxTotalUSD)
	fmt.Println()

	pk := os.Getenv("KEEPER_PRIVATE_KEY")
	if pk == "" {
		fmt.Println("ERROR: KEEPER_PRIVATE_KEY not set")
		return
	}
	if strings.HasPrefix(pk, "0x") {
		pk = pk[2:]
	}
	privKey, err := crypto.HexToECDSA(pk)
	if err != nil {
		fmt.Printf("ERROR: invalid key: %v\n", err)
		return
	}
	keeper := crypto.PubkeyToAddress(privKey.PublicKey)

	client, err := ethclient.Dial("https://warmhearted-wild-road.arbitrum-mainnet.quiknode.pro/1ed461039e0b8a3008160cbc130479deb34798c9/")
	if err != nil {
		fmt.Printf("ERROR: RPC: %v\n", err)
		return
	}
	defer client.Close()

	trader := cex.NewBinanceTrader(&cex.BinanceConfig{
		APIEndpoint: "https://api.binance.com",
		APIKey:      os.Getenv("BINANCE_API_KEY"),
		APISecret:   os.Getenv("BINANCE_API_SECRET"),
		RateLimit:   1200,
	})

	sc := &scanner{
		client:    client,
		privKey:   privKey,
		keeper:    keeper,
		trader:    trader,
		positions: make(map[string]*momentumPos),
	}

	ctx, cancel := context.WithCancel(context.Background())
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() { <-sigCh; fmt.Println("\nStopping..."); cancel() }()

	// 初始余额
	ethBal, _ := client.BalanceAt(context.Background(), keeper, nil)
	usdtBal := getBalance(client, USDT, keeper, 6)
	binUSDT, _, _ := trader.GetBalance("USDT")
	ethF := weiToFloat(ethBal, 18)
	ethPrice := getBidPrice("ETHUSDT")
	fmt.Printf("Keeper: %s\n", keeper.Hex())
	fmt.Printf("Arb: %.2f USDT + %.6f ETH ($%.2f)\n", usdtBal, ethF, ethF*ethPrice)
	fmt.Printf("Bin: %.2f USDT\n", binUSDT)
	fmt.Println()

	// 建立 symbol → token 的索引
	tokenIndex := make(map[string]*arbToken)
	for i := range tokenRegistry {
		tokenIndex[tokenRegistry[i].binPair] = &tokenRegistry[i]
	}

	lastScan := time.Time{} // 强制第一次立即扫描
	round := 0

	for {
		select {
		case <-ctx.Done():
			goto done
		default:
		}

		round++

		// === Phase A: 动量扫描 (每 scanInterval) ===
		if time.Since(lastScan) >= scanInterval {
			lastScan = time.Now()
			fmt.Printf("\n[%s] ═══ Scanning Binance Top Gainers ═══\n", ts())

			tickers := scanBinanceMomentum()
			if tickers == nil {
				goto wait
			}

			sc.hotTokens = nil
			displayed := 0

			for _, t := range tickers {
				if displayed >= topNTokens*2 { // 扫描更多但只显示 top
					break
				}

				gain := parseFloat(t.PriceChangePercent)
				vol := parseFloat(t.QuoteVolume)
				price := parseFloat(t.LastPrice)

				if gain < minGain24h || vol < minBinanceVolume {
					continue
				}

				// 检查是否在 Arbitrum 注册表中
				tok, ok := tokenIndex[t.Symbol]
				if !ok {
					if displayed < topNTokens {
						fmt.Printf("  %s +%.1f%% vol=$%.0fM — NO ARB POOL\n",
							t.Symbol, gain, vol/1e6)
						displayed++
					}
					continue
				}

				// 检查 Arbitrum DEX 价格 (QuoterV2)
				// 用 $10 模拟报价
				dexOut := quoteMultiHopFee(client, USDT, WETH, tok.address, 10.0, 6, tok.decimals, 500, tok.feeTier)
				dexPrice := 0.0
				if dexOut > 0 {
					dexPrice = 10.0 / dexOut
				}

				spread := 0.0
				if dexPrice > 0 {
					spread = (price - dexPrice) / dexPrice * 100
				}

				ht := hotToken{
					token:      *tok,
					gain24h:    gain,
					volume24h:  vol,
					hasArbPool: dexOut > 0,
					dexPrice:   dexPrice,
					cexPrice:   price,
					spread:     spread,
				}

				// 获取 1h 涨幅 (用 K 线)
				ht.gain1h = get1hGain(t.Symbol)

				sc.hotTokens = append(sc.hotTokens, ht)

				marker := ""
				if spread > minSpreadPct {
					marker = " ★ ARB"
				}
				if dexOut > 0 {
					fmt.Printf("  %s +%.1f%%(24h) +%.1f%%(1h) vol=$%.0fM | CEX=$%.4f DEX=$%.4f sp=%.2f%%%s\n",
						t.Symbol, gain, ht.gain1h, vol/1e6, price, dexPrice, spread, marker)
				} else {
					fmt.Printf("  %s +%.1f%%(24h) vol=$%.0fM — NO LIQUIDITY on Arb\n",
						t.Symbol, gain, vol/1e6)
				}
				displayed++
			}

			if len(sc.hotTokens) == 0 {
				fmt.Println("  No hot tokens with Arbitrum liquidity found")
			} else {
				// 按 spread 排序
				sort.Slice(sc.hotTokens, func(i, j int) bool {
					return sc.hotTokens[i].spread > sc.hotTokens[j].spread
				})
				fmt.Printf("  Found %d hot tokens with Arb pools, %d with positive spread\n",
					len(sc.hotTokens), countPositiveSpread(sc.hotTokens))
			}
		}

		// === Phase B: 止盈止损检查 ===
		sc.checkStopLossTakeProfit()

		// === Phase C: 套利执行 (热门代币) ===
		sc.tryMomentumArb()

		// === Phase D: 状态汇总 (每 3 轮) ===
		if round%3 == 1 {
			sc.printStatus(round)
		}

	wait:
		select {
		case <-ctx.Done():
			goto done
		case <-time.After(time.Duration(pollInterval) * time.Second):
		}
	}

done:
	fmt.Println("\n╔══════════════════════════════════════════╗")
	fmt.Println("║       MOMENTUM SCANNER REPORT            ║")
	fmt.Println("╚══════════════════════════════════════════╝")
	fmt.Printf("Trades: %d | Wins: %d | Losses: %d\n", sc.totalTrades, sc.wins, sc.losses)
	fmt.Printf("Total P&L: $%+.2f\n", sc.totalProfit)
	if len(sc.positions) > 0 {
		fmt.Println("\nOpen positions:")
		for sym, pos := range sc.positions {
			price := getBidPrice(pos.token.binPair)
			fmt.Printf("  %s: %.4f ($%.2f) entry=$%.4f now=$%.4f P&L=$%+.2f (%.1f%%)\n",
				sym, pos.qty, pos.currentValue(price),
				pos.entryPrice, price, pos.pnl(price), pos.pnlPct(price))
		}
	}
}

// ============ Stop-Loss / Take-Profit ============

func (sc *scanner) checkStopLossTakeProfit() {
	for sym, pos := range sc.positions {
		price := getBidPrice(pos.token.binPair)
		if price == 0 {
			continue
		}

		// 更新峰值
		if price > pos.peakPrice {
			pos.peakPrice = price
		}

		pnlPct := pos.pnlPct(price)
		dropFromPeak := 0.0
		if pos.peakPrice > 0 {
			dropFromPeak = (pos.peakPrice - price) / pos.peakPrice
		}
		peakGainPct := (pos.peakPrice - pos.entryPrice) / pos.entryPrice

		action := ""
		reason := ""

		// 1. 止损: 亏损 > stopLossPct
		if pnlPct < -stopLossPct*100 {
			action = "STOP_LOSS"
			reason = fmt.Sprintf("STOP LOSS: %s P&L=%.1f%% ($%+.2f) — cutting loss",
				sym, pnlPct, pos.pnl(price))
		}

		// 2. 止盈: 涨 > takeProfitPct
		if pnlPct >= takeProfitPct*100 {
			action = "TAKE_PROFIT"
			reason = fmt.Sprintf("TAKE PROFIT: %s +%.1f%% ($%+.2f) entry=$%.4f now=$%.4f",
				sym, pnlPct, pos.pnl(price), pos.entryPrice, price)
		}

		// 3. Trailing stop: 曾涨过目标，现在从峰值回撤
		if peakGainPct >= takeProfitPct && dropFromPeak >= trailingStopPct {
			action = "TRAILING_STOP"
			reason = fmt.Sprintf("TRAILING STOP: %s peak+%.1f%% now+%.1f%% (drop %.1f%%)",
				sym, peakGainPct*100, pnlPct, dropFromPeak*100)
		}

		if action == "" {
			continue
		}

		fmt.Printf("\n[%s] %s\n", ts(), reason)

		// 执行: 在 DEX 上卖 token → USDT
		sellQty := pos.qty * 0.95 // 卖 95%
		expectedUSDT := quoteMultiHopFee(sc.client, pos.token.address, WETH, USDT,
			sellQty, pos.token.decimals, 6, pos.token.feeTier, 500)

		if expectedUSDT < 2 {
			fmt.Printf("  Quote too low ($%.2f), skip sell\n", expectedUSDT)
			continue
		}

		fmt.Printf("  Selling %.4f %s → ~$%.2f USDT\n", sellQty, sym, expectedUSDT)

		// 需要先 approve
		if err := ensureApproval(sc.client, sc.privKey, sc.keeper, pos.token.address); err != nil {
			fmt.Printf("  Approve FAIL: %v\n", err)
			continue
		}

		txH, err := doSwapFee(sc.client, sc.privKey, sc.keeper,
			pos.token.address, WETH, USDT,
			sellQty, pos.token.decimals, 6,
			pos.token.feeTier, 500, expectedUSDT)

		if err != nil {
			fmt.Printf("  SELL FAIL: %v\n", err)
			continue
		}

		realizedPL := expectedUSDT - pos.totalCost
		sc.totalTrades++
		sc.totalProfit += realizedPL
		if realizedPL > 0 {
			sc.wins++
		} else {
			sc.losses++
		}

		fmt.Printf("  SELL OK: %s | Realized P&L: $%+.2f (%s)\n", txH[:20], realizedPL, action)
		delete(sc.positions, sym)
	}
}

// ============ Momentum Arbitrage ============

func (sc *scanner) tryMomentumArb() {
	// 检查总持仓限制
	totalPosValue := 0.0
	for _, pos := range sc.positions {
		price := getBidPrice(pos.token.binPair)
		totalPosValue += pos.currentValue(price)
	}
	if totalPosValue >= maxTotalUSD {
		return
	}

	// 检查可用 USDT (Arb 链上)
	arbUSDT := getBalance(sc.client, USDT, sc.keeper, 6)
	if arbUSDT < 8 {
		// Arb USDT 不足，仅监控不交易
		return
	}

	// 遍历热门代币找套利机会
	for _, ht := range sc.hotTokens {
		if !ht.hasArbPool || ht.spread < minSpreadPct {
			continue
		}

		// 跳过已有大持仓的
		if pos, ok := sc.positions[ht.token.symbol]; ok {
			price := getBidPrice(ht.token.binPair)
			if pos.currentValue(price) >= maxPositionUSD {
				continue
			}
		}

		// 检查 Binance 是否有库存可卖
		binBal, _, _ := sc.trader.GetBalance(ht.token.symbol)
		binValue := binBal * ht.cexPrice
		if binValue < 3 {
			// 没有 CEX 库存 — 考虑纯动量买入 (不套利，只看涨)
			if ht.gain1h > 2.0 && ht.gain24h > 5.0 && totalPosValue+15 < maxTotalUSD {
				sc.tryMomentumBuy(ht, arbUSDT)
			}
			continue
		}

		// 有 CEX 库存 — 执行 CEX-DEX 套利
		tradeUSDT := arbUSDT * 0.50 // 用 50% USDT (动量保守一些)
		if tradeUSDT > maxPositionUSD {
			tradeUSDT = maxPositionUSD
		}
		if tradeUSDT < 5 {
			continue
		}

		tokenOut := quoteMultiHopFee(sc.client, USDT, WETH, ht.token.address,
			tradeUSDT, 6, ht.token.decimals, 500, ht.token.feeTier)
		if tokenOut <= 0 {
			continue
		}

		// 限制到 Binance 库存
		sellQty := tokenOut
		if sellQty > binBal*0.98 {
			sellQty = binBal * 0.98
			tradeUSDT = sellQty * (tradeUSDT / tokenOut)
			tokenOut = sellQty
		}
		if tradeUSDT < 5 {
			continue
		}

		dexPrice := tradeUSDT / tokenOut
		cexBid := getBidPrice(ht.token.binPair) // 刷新价格
		spread := (cexBid - dexPrice) / dexPrice * 100

		cexRevenue := sellQty * cexBid * (1 - cexFeePct)
		estProfit := cexRevenue - tradeUSDT - tradeUSDT*slippagePct - gasEstUSD

		if spread < minSpreadPct || estProfit < minEstProfit {
			continue
		}

		fmt.Printf("\n[%s] MOMENTUM ARB: %s +%.1f%%(24h) sp=%.2f%% trade=$%.1f est=$%.4f\n",
			ts(), ht.token.binPair, ht.gain24h, spread, tradeUSDT, estProfit)

		// 需要先 approve USDT → V3Router
		if err := ensureApproval(sc.client, sc.privKey, sc.keeper, USDT); err != nil {
			fmt.Printf("  Approve FAIL: %v\n", err)
			continue
		}

		// 并行 DEX buy + CEX sell
		type leg struct {
			name, detail string
			err          error
		}
		ch := make(chan leg, 2)

		go func() {
			txH, err := doSwapFee(sc.client, sc.privKey, sc.keeper, USDT, WETH, ht.token.address,
				tradeUSDT, 6, ht.token.decimals, 500, ht.token.feeTier, tokenOut)
			d := ""
			if err == nil {
				d = txH
			}
			ch <- leg{"DEX", d, err}
		}()

		go func() {
			qty := formatQty(sellQty, ht.token.minQty)
			order, err := sc.trader.MarketSell(ht.token.binPair, qty)
			d := ""
			if err == nil && order != nil {
				d = fmt.Sprintf("id=%d qty=%s", order.OrderID, order.ExecutedQty)
			}
			ch <- leg{"CEX", d, err}
		}()

		var dL, cL leg
		for i := 0; i < 2; i++ {
			r := <-ch
			if r.name == "DEX" {
				dL = r
			} else {
				cL = r
			}
		}

		if dL.err != nil {
			fmt.Printf("  DEX FAIL: %v\n", dL.err)
		} else {
			fmt.Printf("  DEX OK: %s\n", dL.detail)
		}
		if cL.err != nil {
			fmt.Printf("  CEX FAIL: %v\n", cL.err)
		} else {
			fmt.Printf("  CEX OK: %s\n", cL.detail)
		}

		if dL.err == nil && cL.err == nil {
			sc.totalTrades++
			sc.totalProfit += estProfit
			if estProfit > 0 {
				sc.wins++
			} else {
				sc.losses++
			}
			fmt.Printf("  EST P&L: $%+.4f\n", estProfit)

			// 记录持仓 (DEX 上现在有 token)
			if pos, ok := sc.positions[ht.token.symbol]; ok {
				// 加仓
				pos.entryPrice = (pos.entryPrice*pos.qty + dexPrice*tokenOut) / (pos.qty + tokenOut)
				pos.qty += tokenOut
				pos.totalCost += tradeUSDT
			} else {
				sc.positions[ht.token.symbol] = &momentumPos{
					token:      ht.token,
					entryPrice: dexPrice,
					peakPrice:  cexBid,
					qty:        tokenOut,
					entryTime:  time.Now(),
					totalCost:  tradeUSDT,
				}
			}
		}

		time.Sleep(2 * time.Second)
		break // 每轮最多一笔套利
	}
}

// tryMomentumBuy — 纯动量买入 (没有 CEX 库存时，只看涨)
func (sc *scanner) tryMomentumBuy(ht hotToken, arbUSDT float64) {
	// 已经有该币种持仓就跳过
	if _, ok := sc.positions[ht.token.symbol]; ok {
		return
	}

	buyUSDT := 15.0 // 动量买入固定 $15
	if buyUSDT > arbUSDT*0.30 {
		buyUSDT = arbUSDT * 0.30
	}
	if buyUSDT < 5 {
		return
	}

	tokenOut := quoteMultiHopFee(sc.client, USDT, WETH, ht.token.address,
		buyUSDT, 6, ht.token.decimals, 500, ht.token.feeTier)
	if tokenOut <= 0 {
		return
	}

	dexPrice := buyUSDT / tokenOut

	fmt.Printf("\n[%s] MOMENTUM BUY: %s +%.1f%%(1h) +%.1f%%(24h) $%.1f → %.4f tokens\n",
		ts(), ht.token.symbol, ht.gain1h, ht.gain24h, buyUSDT, tokenOut)

	// Approve USDT
	if err := ensureApproval(sc.client, sc.privKey, sc.keeper, USDT); err != nil {
		fmt.Printf("  Approve FAIL: %v\n", err)
		return
	}

	txH, err := doSwapFee(sc.client, sc.privKey, sc.keeper, USDT, WETH, ht.token.address,
		buyUSDT, 6, ht.token.decimals, 500, ht.token.feeTier, tokenOut)
	if err != nil {
		fmt.Printf("  BUY FAIL: %v\n", err)
		return
	}

	fmt.Printf("  BUY OK: %s\n", txH[:20])

	sc.positions[ht.token.symbol] = &momentumPos{
		token:      ht.token,
		entryPrice: dexPrice,
		peakPrice:  ht.cexPrice,
		qty:        tokenOut,
		entryTime:  time.Now(),
		totalCost:  buyUSDT,
	}
}

// ============ Status ============

func (sc *scanner) printStatus(round int) {
	totalPos := 0.0
	fmt.Printf("\n[%s] R%d ═══ Momentum Status ═══\n", ts(), round)

	if len(sc.positions) > 0 {
		fmt.Println("  Open positions:")
		for sym, pos := range sc.positions {
			price := getBidPrice(pos.token.binPair)
			val := pos.currentValue(price)
			totalPos += val
			age := time.Since(pos.entryTime).Round(time.Minute)
			fmt.Printf("    %s: %.4f ($%.2f) entry=$%.4f now=$%.4f P&L=$%+.2f (%.1f%%) peak=$%.4f age=%v\n",
				sym, pos.qty, val, pos.entryPrice, price, pos.pnl(price), pos.pnlPct(price),
				pos.peakPrice, age)
		}
		fmt.Printf("  Total position value: $%.2f\n", totalPos)
	} else {
		fmt.Println("  No open positions")
	}

	fmt.Printf("  Trades: %d (W:%d L:%d) Total P&L: $%+.2f\n",
		sc.totalTrades, sc.wins, sc.losses, sc.totalProfit)

	if len(sc.hotTokens) > 0 {
		fmt.Printf("  Hot tokens: %d | Top: %s +%.1f%%",
			len(sc.hotTokens), sc.hotTokens[0].token.binPair, sc.hotTokens[0].gain24h)
		if sc.hotTokens[0].spread > 0 {
			fmt.Printf(" sp=%.2f%%", sc.hotTokens[0].spread)
		}
		fmt.Println()
	}
}

// ============ Approve Helper ============

func ensureApproval(client *ethclient.Client, privKey *ecdsa.PrivateKey, keeper, token common.Address) error {
	// Check current allowance
	allowanceSig, _ := hex.DecodeString("dd62ed3e")
	data := make([]byte, 0, 68)
	data = append(data, allowanceSig...)
	data = append(data, common.LeftPadBytes(keeper.Bytes(), 32)...)
	data = append(data, common.LeftPadBytes(V3Router.Bytes(), 32)...)

	result, err := client.CallContract(context.Background(), ethereum.CallMsg{To: &token, Data: data}, nil)
	if err != nil {
		return fmt.Errorf("check allowance: %w", err)
	}

	allowance := new(big.Int).SetBytes(result)
	// If allowance > 1e24, we're good
	threshold := new(big.Int).Exp(big.NewInt(10), big.NewInt(24), nil)
	if allowance.Cmp(threshold) >= 0 {
		return nil
	}

	// Approve max uint256
	approveSig, _ := hex.DecodeString("095ea7b3")
	maxUint := new(big.Int).Sub(new(big.Int).Exp(big.NewInt(2), big.NewInt(256), nil), big.NewInt(1))
	approveData := make([]byte, 0, 68)
	approveData = append(approveData, approveSig...)
	approveData = append(approveData, common.LeftPadBytes(V3Router.Bytes(), 32)...)
	approveData = append(approveData, common.LeftPadBytes(maxUint.Bytes(), 32)...)

	nonce, _ := client.PendingNonceAt(context.Background(), keeper)
	head, err2 := client.HeaderByNumber(context.Background(), nil)
	tip := big.NewInt(100000000)
	baseFee := big.NewInt(100000000)
	if err2 == nil && head != nil && head.BaseFee != nil {
		baseFee = head.BaseFee
	}
	maxFee := new(big.Int).Add(new(big.Int).Mul(baseFee, big.NewInt(2)), tip)

	tx := types.NewTx(&types.DynamicFeeTx{
		ChainID: chainID, Nonce: nonce, GasTipCap: tip, GasFeeCap: maxFee,
		Gas: 80000, To: &token, Value: big.NewInt(0), Data: approveData,
	})
	signer := types.LatestSignerForChainID(chainID)
	signedTx, _ := types.SignTx(tx, signer, privKey)
	if err := client.SendTransaction(context.Background(), signedTx); err != nil {
		return fmt.Errorf("send approve: %w", err)
	}

	// Wait for receipt
	ctxTO, cancelTO := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelTO()
	for {
		receipt, err := client.TransactionReceipt(ctxTO, signedTx.Hash())
		if err == nil {
			if receipt.Status == 1 {
				fmt.Printf("  Approved %s → V3Router (tx: %s)\n", token.Hex()[:10], signedTx.Hash().Hex()[:20])
				return nil
			}
			return fmt.Errorf("approve reverted")
		}
		select {
		case <-ctxTO.Done():
			return fmt.Errorf("approve timeout")
		case <-time.After(time.Second):
		}
	}
}

// ============ 1h Gain (from K-lines) ============

func get1hGain(symbol string) float64 {
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Get(
		fmt.Sprintf("https://api.binance.com/api/v3/klines?symbol=%s&interval=1h&limit=2", symbol))
	if err != nil {
		return 0
	}
	defer resp.Body.Close()

	var raw [][]json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil || len(raw) < 2 {
		return 0
	}

	var prevClose, curClose string
	json.Unmarshal(raw[0][4], &prevClose)
	json.Unmarshal(raw[1][4], &curClose)

	prev := parseFloat(prevClose)
	cur := parseFloat(curClose)
	if prev == 0 {
		return 0
	}
	return (cur - prev) / prev * 100
}

func countPositiveSpread(tokens []hotToken) int {
	n := 0
	for _, t := range tokens {
		if t.spread > 0 {
			n++
		}
	}
	return n
}

// ============ DEX Swap (same as cexdex-loop) ============

func doSwapFee(client *ethclient.Client, privKey *ecdsa.PrivateKey, keeper common.Address,
	tokenIn, mid, tokenOut common.Address, amtIn float64, inDec, outDec int,
	fee1, fee2 int64, expectedOut float64) (string, error) {

	quotedOut := quoteMultiHopFee(client, tokenIn, mid, tokenOut, amtIn, inDec, outDec, fee1, fee2)
	if quotedOut <= 0 {
		return "", fmt.Errorf("quote=0")
	}

	exactInputABI := `[{"inputs":[{"components":[{"name":"path","type":"bytes"},{"name":"recipient","type":"address"},{"name":"deadline","type":"uint256"},{"name":"amountIn","type":"uint256"},{"name":"amountOutMinimum","type":"uint256"}],"name":"params","type":"tuple"}],"name":"exactInput","outputs":[{"name":"amountOut","type":"uint256"}],"stateMutability":"payable","type":"function"}]`
	parsed, _ := abi.JSON(strings.NewReader(exactInputABI))

	path := encodePath(tokenIn, fee1, mid, fee2, tokenOut)
	amountIn := floatToWei(amtIn, inDec)
	minOut := floatToWei(quotedOut*0.995, outDec)
	dl := new(big.Int).SetInt64(time.Now().Add(5 * time.Minute).Unix())

	type P struct {
		Path             []byte
		Recipient        common.Address
		Deadline         *big.Int
		AmountIn         *big.Int
		AmountOutMinimum *big.Int
	}
	callData, err := parsed.Pack("exactInput", P{
		Path: path, Recipient: keeper, Deadline: dl,
		AmountIn: amountIn, AmountOutMinimum: minOut,
	})
	if err != nil {
		return "", fmt.Errorf("pack: %w", err)
	}

	if _, err = client.CallContract(context.Background(), ethereum.CallMsg{
		From: keeper, To: &V3Router, Data: callData,
	}, nil); err != nil {
		return "", fmt.Errorf("sim: %w", err)
	}

	nonce, _ := client.PendingNonceAt(context.Background(), keeper)
	head, err2 := client.HeaderByNumber(context.Background(), nil)
	tip := big.NewInt(100000000)
	baseFee := big.NewInt(100000000)
	if err2 == nil && head != nil && head.BaseFee != nil {
		baseFee = head.BaseFee
	}
	maxFee := new(big.Int).Add(new(big.Int).Mul(baseFee, big.NewInt(2)), tip)
	gasLimit, err := client.EstimateGas(context.Background(), ethereum.CallMsg{
		From: keeper, To: &V3Router, Data: callData,
	})
	if err != nil {
		gasLimit = 500000
	} else {
		gasLimit = gasLimit * 130 / 100
	}

	tx := types.NewTx(&types.DynamicFeeTx{
		ChainID: chainID, Nonce: nonce, GasTipCap: tip, GasFeeCap: maxFee,
		Gas: gasLimit, To: &V3Router, Value: big.NewInt(0), Data: callData,
	})
	signer := types.LatestSignerForChainID(chainID)
	signedTx, _ := types.SignTx(tx, signer, privKey)
	if err := client.SendTransaction(context.Background(), signedTx); err != nil {
		return "", fmt.Errorf("send: %w", err)
	}

	txHash := signedTx.Hash().Hex()
	ctxTO, cancelTO := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancelTO()
	for {
		receipt, err := client.TransactionReceipt(ctxTO, signedTx.Hash())
		if err == nil {
			if receipt.Status == 1 {
				return txHash, nil
			}
			return txHash, fmt.Errorf("reverted(gas=%d)", receipt.GasUsed)
		}
		select {
		case <-ctxTO.Done():
			return txHash, fmt.Errorf("timeout")
		case <-time.After(time.Second):
		}
	}
}

func encodePath(tokenIn common.Address, fee1 int64, mid common.Address, fee2 int64, tokenOut common.Address) []byte {
	path := make([]byte, 0, 66)
	path = append(path, tokenIn.Bytes()...)
	path = append(path, feeToBytes(fee1)...)
	path = append(path, mid.Bytes()...)
	path = append(path, feeToBytes(fee2)...)
	path = append(path, tokenOut.Bytes()...)
	return path
}

func feeToBytes(fee int64) []byte {
	b := make([]byte, 3)
	b[0] = byte(fee >> 16)
	b[1] = byte(fee >> 8)
	b[2] = byte(fee)
	return b
}

// ============ Quote ============

func quoteMultiHopFee(client *ethclient.Client, tokenIn, mid, tokenOut common.Address, amtIn float64, inDec, outDec int, fee1, fee2 int64) float64 {
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

// ============ Utils ============

func ts() string { return time.Now().Format("15:04:05") }

func parseFloat(s string) float64 {
	v := 0.0
	fmt.Sscanf(s, "%f", &v)
	return v
}

func formatQty(qty float64, minStep string) string {
	switch minStep {
	case "0.001":
		return fmt.Sprintf("%.3f", qty)
	case "0.01":
		return fmt.Sprintf("%.2f", qty)
	case "0.1":
		return fmt.Sprintf("%.1f", qty)
	case "1":
		return fmt.Sprintf("%.0f", qty)
	default:
		return fmt.Sprintf("%.2f", qty)
	}
}

func getBalance(client *ethclient.Client, token, owner common.Address, dec int) float64 {
	sig, _ := hex.DecodeString("70a08231")
	data := append(sig, common.LeftPadBytes(owner.Bytes(), 32)...)
	for i := 0; i < 3; i++ {
		result, err := client.CallContract(context.Background(), ethereum.CallMsg{To: &token, Data: data}, nil)
		if err == nil && len(result) >= 32 {
			return weiToFloat(new(big.Int).SetBytes(result[:32]), dec)
		}
		if i < 2 {
			time.Sleep(500 * time.Millisecond)
		}
	}
	return 0
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

// unused import guard
var _ = math.Abs
