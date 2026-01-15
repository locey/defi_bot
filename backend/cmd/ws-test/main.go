// WebSocket 连接测试工具
// 用于验证 Alchemy/Infura WebSocket 连接和实时价格订阅
package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/defi-bot/backend/internal/config"
	"github.com/defi-bot/backend/internal/database"
	"github.com/defi-bot/backend/internal/models"
	"github.com/defi-bot/backend/pkg/web3"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
)

var (
	configPath = flag.String("config", "configs/config.mainnet.yaml", "配置文件路径")
)

func main() {
	flag.Parse()
	log.SetOutput(os.Stdout)

	log.Println("========================================")
	log.Println("WebSocket 连接测试工具")
	log.Println("========================================")

	// 加载配置
	cfg, err := config.LoadConfig(*configPath)
	if err != nil {
		log.Fatalf("加载配置失败: %v", err)
	}

	// 检查 WebSocket URL
	if cfg.Blockchain.WSURL == "" {
		log.Println("❌ 未配置 WebSocket URL (ws_url)")
		log.Println("")
		log.Println("请在配置文件中设置 ws_url，例如：")
		log.Println("  Alchemy: wss://eth-mainnet.g.alchemy.com/v2/YOUR_API_KEY")
		log.Println("  Infura:  wss://mainnet.infura.io/ws/v3/YOUR_PROJECT_ID")
		os.Exit(1)
	}

	log.Printf("WebSocket URL: %s", maskAPIKey(cfg.Blockchain.WSURL))

	// 创建 WebSocket 客户端
	wsClient, err := web3.NewWSClient(cfg.Blockchain.WSURL, cfg.Blockchain.ChainID)
	if err != nil {
		log.Fatalf("创建 WebSocket 客户端失败: %v", err)
	}
	defer wsClient.Close()

	// 连接
	if err := wsClient.Connect(); err != nil {
		log.Fatalf("连接失败: %v", err)
	}

	// 注册新区块回调
	blockCount := 0
	wsClient.OnNewBlock(func(header *types.Header) {
		blockCount++
		log.Printf("📦 新区块 #%d (Hash: %s, Time: %s)",
			header.Number.Uint64(),
			header.Hash().Hex()[:16]+"...",
			time.Unix(int64(header.Time), 0).Format("15:04:05"))
	})

	// 订阅新区块
	if err := wsClient.SubscribeNewBlocks(); err != nil {
		log.Fatalf("订阅新区块失败: %v", err)
	}

	// 尝试从数据库加载池地址（可选）
	if err := database.InitDB(&cfg.Database); err == nil {
		defer database.CloseDB()
		loadAndWatchPools(wsClient, cfg.Blockchain.ChainID)
	} else {
		log.Printf("⚠️ 数据库连接失败，跳过池地址加载: %v", err)
	}

	log.Println("")
	log.Println("✅ WebSocket 订阅已启动，等待事件...")
	log.Println("   按 Ctrl+C 退出")
	log.Println("")

	// 等待中断信号
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	// 定期打印统计
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-sigCh:
			log.Println("\n收到退出信号，正在关闭...")
			return
		case <-ticker.C:
			log.Printf("📊 统计: 收到 %d 个新区块, 监控 %d 个池",
				blockCount, wsClient.GetWatchedPoolCount())
		}
	}
}

// loadAndWatchPools 从数据库加载池地址并订阅
func loadAndWatchPools(wsClient *web3.WSClient, chainID int64) {
	db := database.GetDB()

	// 查询活跃的交易对
	var pairs []models.TradingPair
	if err := db.Where("is_active = ?", true).
		Preload("Dex").
		Find(&pairs).Error; err != nil {
		log.Printf("⚠️ 查询交易对失败: %v", err)
		return
	}

	// 过滤当前链的交易对
	poolAddresses := make([]common.Address, 0)
	for _, pair := range pairs {
		if pair.Dex.ChainID == chainID && pair.PairAddress != "" {
			poolAddresses = append(poolAddresses, common.HexToAddress(pair.PairAddress))
		}
	}

	if len(poolAddresses) == 0 {
		log.Println("⚠️ 没有找到可监控的池地址")
		return
	}

	log.Printf("从数据库加载 %d 个池地址", len(poolAddresses))

	// 添加到监控列表
	wsClient.AddWatchedPools(poolAddresses)

	// 注册价格更新回调
	syncCount := 0
	wsClient.OnPriceUpdate(func(event *web3.SyncEvent) {
		syncCount++
		log.Printf("💱 Sync 事件 - 池: %s, Reserve0: %s, Reserve1: %s, 区块: %d",
			event.PairAddress.Hex()[:16]+"...",
			event.Reserve0.String()[:min(10, len(event.Reserve0.String()))]+"...",
			event.Reserve1.String()[:min(10, len(event.Reserve1.String()))]+"...",
			event.BlockNumber)
	})

	// 订阅 Sync 事件
	if err := wsClient.SubscribeSyncEvents(); err != nil {
		log.Printf("⚠️ 订阅 Sync 事件失败: %v", err)
	}
}

// maskAPIKey 隐藏 API Key 中间部分
func maskAPIKey(url string) string {
	if len(url) < 50 {
		return url
	}
	// 只显示前30和后10个字符
	return url[:30] + "..." + url[len(url)-10:]
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
