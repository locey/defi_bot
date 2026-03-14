// pkg/trading/momentum.go
// 动量扫描 — Binance 涨幅榜 + Arbitrum DEX 价格对比
package trading

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
)

// ArbToken Arbitrum 上可交易的代币
type ArbToken struct {
	Symbol   string         // e.g., "LINK"
	BinPair  string         // e.g., "LINKUSDT"
	Address  common.Address // Arbitrum ERC20 address
	Decimals int
	FeeTier  int64  // WETH pool fee tier
	MinQty   string // Binance minimum sell qty
}

// HotToken 热门代币扫描结果
type HotToken struct {
	Token      ArbToken
	Gain24h    float64 // 24h 涨幅 %
	Volume24h  float64 // 24h 成交量 USD
	Gain1h     float64 // 1h 涨幅 %
	HasArbPool bool    // Arbitrum 上有 V3 流动性
	DexPrice   float64 // DEX 实际价格
	CexPrice   float64 // CEX 价格
	Spread     float64 // CEX-DEX 价差 %
}

// DefaultTokenRegistry 默认 Arbitrum 代币注册表
// 只包含同时在 Binance 和 Arbitrum Uniswap V3 上有流动性的代币
var DefaultTokenRegistry = []ArbToken{
	// Blue chips — 高流动性
	{"LINK", "LINKUSDT", common.HexToAddress("0xf97f4df75117a78c1A5a0DBb814Af92458539FB4"), 18, 500, "0.01"},
	{"AAVE", "AAVEUSDT", common.HexToAddress("0xba5DdD1f9d7F570dc94a51479a000E3BCE967196"), 18, 3000, "0.001"},
	{"UNI", "UNIUSDT", common.HexToAddress("0xFa7F8980b0f1E64A2062791cc3b0871572f1F7f0"), 18, 3000, "0.01"},
	{"ARB", "ARBUSDT", common.HexToAddress("0x912CE59144191C1204E64559FE8253a0e49E6548"), 18, 500, "0.1"},
	{"GMX", "GMXUSDT", common.HexToAddress("0xfc5A1A6EB076a2C7aD06eD22C90d7E710E35ad0a"), 18, 3000, "0.001"},
	{"PENDLE", "PENDLEUSDT", common.HexToAddress("0x0c880f6761F1af8d9Aa9C466984b80DAb9a8c9e8"), 18, 3000, "0.01"},
	{"GRT", "GRTUSDT", common.HexToAddress("0x9623063377AD1B27544C965cCd7342f7EA7e88C7"), 18, 3000, "1"},
	{"COMP", "COMPUSDT", common.HexToAddress("0x354A6dA3fcde098F8389cad84b0182725c6C91dE"), 18, 3000, "0.001"},
	{"CRV", "CRVUSDT", common.HexToAddress("0x11cDb42B0EB46D95f990BeDD4695A6e3fA034978"), 18, 3000, "0.1"},
	{"LDO", "LDOUSDT", common.HexToAddress("0x13Ad51ed4F1B7e9Dc168d8a00cB3f4dDD85EfA60"), 18, 3000, "0.01"},
	// Mid caps — 中等流动性
	{"MAGIC", "MAGICUSDT", common.HexToAddress("0x539bdE0d7Dbd336b79148AA742883198BBF60342"), 18, 3000, "0.1"},
	{"RDNT", "RDNTUSDT", common.HexToAddress("0x3082CC23568eA640225c2467653dB90e9250AaA0"), 18, 3000, "1"},
	{"SUSHI", "SUSHIUSDT", common.HexToAddress("0xd4d42F0b6DEF4CE0383636770eF773390d85c61A"), 18, 3000, "0.1"},
	{"WOO", "WOOUSDT", common.HexToAddress("0xcAFcD85D8ca7Ad1e1C6F82F651fA15E33AEfD07b"), 18, 3000, "1"},
	{"DODO", "DODOUSDT", common.HexToAddress("0x69Eb4FA4a2fbd498C257C57Ea8b7655a2559A581"), 18, 3000, "1"},
	// 新增 — Arbitrum 上有 V3 流动性
	{"STG", "STGUSDT", common.HexToAddress("0x6694340fc020c5E6B96567843da2df01b2CE1eb6"), 18, 3000, "0.1"},
	// 注: WLD/GRAIL/DPX/BAL 在 Arbitrum V3 无 WETH 池, 已移除
}

// BinanceTicker Binance 24h 行情
type BinanceTicker struct {
	Symbol             string `json:"symbol"`
	PriceChangePercent string `json:"priceChangePercent"`
	LastPrice          string `json:"lastPrice"`
	QuoteVolume        string `json:"quoteVolume"`
}

// ScanMomentum 扫描 Binance 涨幅榜，返回在 Arbitrum 上有流动性的热门代币
func ScanMomentum(client *ethclient.Client, minGain24h, minVolume float64, topN int) []HotToken {
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Get(
		"https://api.binance.com/api/v3/ticker/24hr")
	if err != nil {
		return nil
	}
	defer resp.Body.Close()

	var tickers []BinanceTicker
	if err := json.NewDecoder(resp.Body).Decode(&tickers); err != nil {
		return nil
	}

	// 建索引
	tokenIndex := make(map[string]*ArbToken)
	for i := range DefaultTokenRegistry {
		tokenIndex[DefaultTokenRegistry[i].BinPair] = &DefaultTokenRegistry[i]
	}

	// 过滤 USDT 交易对
	var usdtPairs []BinanceTicker
	for _, t := range tickers {
		if !strings.HasSuffix(t.Symbol, "USDT") {
			continue
		}
		base := strings.TrimSuffix(t.Symbol, "USDT")
		if base == "USDC" || base == "BUSD" || base == "DAI" || base == "TUSD" || base == "FDUSD" {
			continue
		}
		if strings.Contains(base, "UP") || strings.Contains(base, "DOWN") ||
			strings.Contains(base, "BEAR") || strings.Contains(base, "BULL") {
			continue
		}
		usdtPairs = append(usdtPairs, t)
	}

	// 按涨幅排序
	sort.Slice(usdtPairs, func(i, j int) bool {
		return ParseFloat(usdtPairs[i].PriceChangePercent) > ParseFloat(usdtPairs[j].PriceChangePercent)
	})

	var result []HotToken
	found := 0

	for _, t := range usdtPairs {
		if found >= topN {
			break
		}

		gain := ParseFloat(t.PriceChangePercent)
		vol := ParseFloat(t.QuoteVolume)
		price := ParseFloat(t.LastPrice)

		if gain < minGain24h || vol < minVolume {
			continue
		}

		tok, ok := tokenIndex[t.Symbol]
		if !ok {
			continue // 不在 Arbitrum 注册表中，跳过不计数
		}

		// 用 $10 模拟 DEX 报价
		dexOut := QuoteMultiHop(client, USDT, WETH, tok.Address, 10.0, 6, tok.Decimals, 500, tok.FeeTier)
		dexPrice := 0.0
		if dexOut > 0 {
			dexPrice = 10.0 / dexOut
		}

		spread := 0.0
		if dexPrice > 0 {
			spread = (price - dexPrice) / dexPrice * 100
		}

		ht := HotToken{
			Token:      *tok,
			Gain24h:    gain,
			Volume24h:  vol,
			HasArbPool: dexOut > 0,
			DexPrice:   dexPrice,
			CexPrice:   price,
			Spread:     spread,
		}

		ht.Gain1h = Get1hGain(t.Symbol)
		result = append(result, ht)
		found++
	}

	// 按 spread 排序
	sort.Slice(result, func(i, j int) bool {
		return result[i].Spread > result[j].Spread
	})

	return result
}

// ScanAllSpreads 扫描所有注册代币的 CEX-DEX 价差（不看涨幅/成交量）
// 返回所有有效价差的代币，按 spread 排序
func ScanAllSpreads(client *ethclient.Client, minSpread float64) []HotToken {
	var result []HotToken

	for i := range DefaultTokenRegistry {
		tok := &DefaultTokenRegistry[i]

		// 获取 CEX 价格
		cexPrice := GetBidPrice(tok.BinPair)
		if cexPrice == 0 {
			continue
		}

		// DEX 报价: $10 USDT → TOKEN
		dexOut := QuoteMultiHop(client, USDT, WETH, tok.Address, 10.0, 6, tok.Decimals, 500, tok.FeeTier)
		dexPrice := 0.0
		if dexOut > 0 {
			dexPrice = 10.0 / dexOut
		}
		if dexPrice == 0 {
			continue
		}

		spread := (cexPrice - dexPrice) / dexPrice * 100

		if spread < minSpread {
			continue
		}

		result = append(result, HotToken{
			Token:      *tok,
			HasArbPool: true,
			DexPrice:   dexPrice,
			CexPrice:   cexPrice,
			Spread:     spread,
		})
	}

	// 按 spread 排序
	sort.Slice(result, func(i, j int) bool {
		return result[i].Spread > result[j].Spread
	})

	return result
}

// Get1hGain 获取最近 1 小时涨幅
func Get1hGain(symbol string) float64 {
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

	prev := ParseFloat(prevClose)
	cur := ParseFloat(curClose)
	if prev == 0 {
		return 0
	}
	return (cur - prev) / prev * 100
}
