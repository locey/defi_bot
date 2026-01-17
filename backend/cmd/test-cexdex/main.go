// cmd/test-cexdex/main.go
// CEX-DEX 套利测试程序（Arbitrum）
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/defi-bot/backend/internal/cexdex"
	"github.com/defi-bot/backend/internal/config"
	"github.com/defi-bot/backend/internal/database"
	"github.com/defi-bot/backend/pkg/log"
	"github.com/defi-bot/backend/pkg/web3"
)

func main() {
	fmt.Println("========================================")
	fmt.Println("CEX-DEX Arbitrage Test (Arbitrum)")
	fmt.Println("========================================")

	// 1. 加载配置
	cfg, err := config.LoadConfig("configs/config.yaml")
	if err != nil {
		fmt.Printf("加载配置失败: %v\n", err)
		os.Exit(1)
	}

	// 2. 初始化日志
	if err := log.Init(&cfg.Log); err != nil {
		fmt.Printf("日志初始化失败: %v\n", err)
		os.Exit(1)
	}

	log.Main().Info().Msg("========================================")
	log.Main().Info().Msg("CEX-DEX Arbitrage Test (Arbitrum)")
	log.Main().Info().Int64("chain_id", cfg.Blockchain.ChainID).Msg("Chain")
	log.Main().Info().Msg("========================================")

	// 3. 初始化数据库
	log.Main().Info().Msg("初始化数据库...")
	if err := database.InitDB(&cfg.Database); err != nil {
		log.Main().Fatal().Err(err).Msg("数据库初始化失败")
	}
	defer database.CloseDB()

	// 4. 初始化 Web3 客户端
	log.Main().Info().Str("rpc", cfg.Blockchain.RPCURL).Msg("初始化 Web3 客户端...")
	web3Client, err := web3.NewClient(
		cfg.Blockchain.RPCURL,
		cfg.Blockchain.ChainID,
		cfg.Blockchain.Timeout,
	)
	if err != nil {
		log.Main().Fatal().Err(err).Msg("Web3 客户端初始化失败")
	}
	defer web3Client.Close()

	// 验证链 ID
	chainID := web3Client.GetChainID()
	log.Main().Info().Str("chain_id", chainID.String()).Msg("✅ 连接到 Arbitrum")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 5. 初始化 CEX 价格监控器（使用 REST API 轮询模式，更稳定）
	if !cfg.Cex.Enabled || !cfg.Cex.Binance.Enabled {
		log.Main().Fatal().Msg("请在配置中启用 CEX (Binance)")
	}

	log.Main().Info().Int("symbols", len(cfg.Cex.Binance.Symbols)).Msg("初始化 Binance 价格监控（REST API 模式）...")

	// 使用 REST API 轮询模式，避免 WebSocket 连接问题
	priceMonitorConfig := &cexdex.PriceMonitorConfig{
		BinanceWSURL:   cfg.Cex.Binance.WSEndpoint + "/ws",
		Symbols:        toLowerSymbols(cfg.Cex.Binance.Symbols),
		ReconnectDelay: 5 * time.Second,
		PingInterval:   30 * time.Second,
		BufferSize:     1000,
	}

	priceMonitor := cexdex.NewPriceMonitor(priceMonitorConfig)

	// 尝试 WebSocket 连接，如果失败则使用 REST API 模式
	wsErr := priceMonitor.Start(ctx)
	if wsErr != nil {
		log.Main().Warn().Err(wsErr).Msg("WebSocket 连接失败，切换到 REST API 模式")
		// 启动 REST API 轮询
		go startRESTPolling(ctx, priceMonitor, cfg.Cex.Binance.APIEndpoint, cfg.Cex.Binance.Symbols)
	} else {
		defer priceMonitor.Stop()
		log.Main().Info().Msg("✅ Binance WebSocket 已连接")
	}

	// 6. 初始化 DEX 实时报价器（直接从链上获取价格，无需数据库）
	log.Main().Info().Msg("初始化 DEX 实时报价器...")
	
	// 根据链 ID 获取配置
	quoterConfig := cexdex.GetQuoterConfigByChainID(cfg.Blockchain.ChainID)
	if quoterConfig == nil {
		log.Main().Fatal().Int64("chain_id", cfg.Blockchain.ChainID).Msg("不支持的链")
	}
	
	dexQuoter, err := cexdex.NewDEXQuoter(web3Client.GetClient(), quoterConfig)
	if err != nil {
		log.Main().Fatal().Err(err).Msg("创建 DEX 报价器失败")
	}
	
	// 启动 DEX 报价器
	if err := dexQuoter.Start(ctx); err != nil {
		log.Main().Fatal().Err(err).Msg("启动 DEX 报价器失败")
	}
	defer dexQuoter.Stop()
	
	log.Main().Info().Int("pairs", len(quoterConfig.Pairs)).Msg("✅ DEX 报价器已启动")
	
	// 使用 DEX 报价器作为价格提供者
	dexPriceProvider := dexQuoter

	// 8. 初始化 CEX-DEX 检测器
	detectorConfig := &cexdex.DetectorConfig{
		MinProfitRate:   cfg.CEXDEX.MinProfitRate,
		MinProfitAmount: cfg.CEXDEX.MinProfitAmount,
		MaxTradeAmount:  cfg.CEXDEX.MaxTradeAmount,
		MinTradeAmount:  cfg.CEXDEX.MinTradeAmount,
		MaxSlippage:     cfg.CEXDEX.MaxSlippage,
		CheckInterval:   time.Duration(cfg.CEXDEX.CheckInterval) * time.Millisecond,
		OpportunityTTL:  time.Duration(cfg.CEXDEX.OpportunityTTL) * time.Second,
	}

	if detectorConfig.MinProfitRate == 0 {
		detectorConfig = cexdex.DefaultDetectorConfig()
		detectorConfig.MinProfitRate = 0.002 // Arbitrum 上降低阈值
	}

	detector := cexdex.NewDetector(detectorConfig, priceMonitor, dexPriceProvider)

	// 9. 启动检测器
	log.Main().Info().Msg("启动 CEX-DEX 套利检测器...")
	if err := detector.Start(ctx); err != nil {
		log.Main().Fatal().Err(err).Msg("检测器启动失败")
	}
	defer detector.Stop()

	// 10. 监控循环
	go monitorLoop(ctx, priceMonitor, dexQuoter, detector)

	// 12. 等待退出信号
	log.Main().Info().Msg("========================================")
	log.Main().Info().Msg("CEX-DEX 套利检测器运行中，按 Ctrl+C 退出")
	log.Main().Info().Float64("min_profit_rate", detectorConfig.MinProfitRate*100).Msg("最小利润率")
	log.Main().Info().Msg("========================================")

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Main().Info().Msg("正在关闭...")
	log.Close()
}

// startRESTPolling 使用 REST API 轮询获取价格
func startRESTPolling(ctx context.Context, monitor *cexdex.PriceMonitor, apiEndpoint string, symbols []string) {
	// 使用支持系统代理的 HTTP 客户端
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment, // 自动使用系统代理 (HTTP_PROXY/HTTPS_PROXY)
	}
	client := &http.Client{
		Timeout:   15 * time.Second,
		Transport: transport,
	}

	// 立即获取一次
	for _, symbol := range symbols {
		fetchBinancePrice(client, apiEndpoint, symbol, monitor)
	}
	log.Main().Info().Int("symbols", len(symbols)).Msg("✅ Binance REST API 初始价格已获取")

	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for _, symbol := range symbols {
				go fetchBinancePrice(client, apiEndpoint, symbol, monitor)
			}
		}
	}
}

// fetchBinancePrice 从 Binance REST API 获取价格
func fetchBinancePrice(client *http.Client, apiEndpoint, symbol string, monitor *cexdex.PriceMonitor) {
	url := fmt.Sprintf("%s/api/v3/ticker/bookTicker?symbol=%s", apiEndpoint, symbol)
	resp, err := client.Get(url)
	if err != nil {
		log.Main().Debug().Err(err).Str("symbol", symbol).Msg("获取 Binance 价格失败")
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		log.Main().Debug().Int("status", resp.StatusCode).Str("symbol", symbol).Msg("Binance API 返回非 200")
		return
	}

	var data struct {
		Symbol   string `json:"symbol"`
		BidPrice string `json:"bidPrice"`
		BidQty   string `json:"bidQty"`
		AskPrice string `json:"askPrice"`
		AskQty   string `json:"askQty"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		log.Main().Debug().Err(err).Msg("解析 Binance 响应失败")
		return
	}
	
	log.Main().Debug().Str("symbol", data.Symbol).Str("bid", data.BidPrice).Str("ask", data.AskPrice).Msg("获取到 Binance 价格")

	bidPrice, _ := parseFloat(data.BidPrice)
	askPrice, _ := parseFloat(data.AskPrice)
	bidQty, _ := parseFloat(data.BidQty)
	askQty, _ := parseFloat(data.AskQty)

	price := &cexdex.CEXPrice{
		Symbol:    data.Symbol,
		BidPrice:  bidPrice,
		BidQty:    bidQty,
		AskPrice:  askPrice,
		AskQty:    askQty,
		LastPrice: (bidPrice + askPrice) / 2,
		Exchange:  "binance",
		Timestamp: time.Now(),
	}

	// 更新到监控器
	monitor.UpdatePrice(price)
}

func parseFloat(s string) (float64, error) {
	var f float64
	_, err := fmt.Sscanf(s, "%f", &f)
	return f, err
}

// toLowerSymbols 转换为小写
func toLowerSymbols(symbols []string) []string {
	result := make([]string, len(symbols))
	for i, s := range symbols {
		result[i] = toLower(s)
	}
	return result
}

func toLower(s string) string {
	result := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 32
		}
		result[i] = c
	}
	return string(result)
}

// monitorLoop 监控循环
func monitorLoop(ctx context.Context, cexMonitor *cexdex.PriceMonitor, dexQuoter *cexdex.DEXQuoter, detector *cexdex.Detector) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	oppCh := detector.GetOpportunityChan()

	for {
		select {
		case <-ctx.Done():
			return

		case opp := <-oppCh:
			if opp != nil {
				log.Main().Info().Msg("🎯 ========== 发现套利机会 ==========")
				log.Main().Info().Str("symbol", opp.Symbol).Str("direction", opp.Direction).Msg("交易对")
				log.Main().Info().Float64("cex_price", opp.CEXPrice).Float64("dex_price", opp.DEXPrice).Msg("价格")
				log.Main().Info().Float64("profit_rate", opp.ProfitRate*100).Float64("net_profit", opp.NetProfit).Msg("利润")
				log.Main().Info().Msg("=====================================")
			}

		case <-ticker.C:
			// 获取 CEX 和 DEX 价格
			cexPrices := cexMonitor.GetAllPrices()
			dexPrices := dexQuoter.GetAllPrices()

			log.Main().Info().Msg("========== 价格对比 ==========")
			log.Main().Info().Msgf("%-12s | %12s | %12s | %8s", "Symbol", "CEX Price", "DEX Price", "Spread")
			log.Main().Info().Msg("-------------------------------------------")

			for symbol, cexPrice := range cexPrices {
				dexPrice, hasDex := dexPrices[symbol]
				if hasDex && dexPrice > 0 && cexPrice.LastPrice > 0 {
					spread := (cexPrice.LastPrice - dexPrice) / dexPrice * 100
					log.Main().Info().Msgf("%-12s | %12.4f | %12.4f | %+7.3f%%", symbol, cexPrice.LastPrice, dexPrice, spread)
				} else if cexPrice.LastPrice > 0 {
					log.Main().Info().Msgf("%-12s | %12.4f | %12s | %8s", symbol, cexPrice.LastPrice, "N/A", "-")
				}
			}

			log.Main().Info().Msg("================================")

			// 打印机会统计
			opps := detector.GetOpportunities()
			log.Main().Info().Int("active_opportunities", len(opps)).Msg("当前活跃机会数")
		}
	}
}
