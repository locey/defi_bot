// cmd/test-high-performance/main.go
// 高性能组件测试程序
package main

import (
	"context"
	"fmt"
	"log"
	"math/big"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/defi-bot/backend/pkg/cache"
	"github.com/ethereum/go-ethereum/common"
)

func main() {
	log.Println("========================================")
	log.Println("High Performance Components Test")
	log.Println("========================================")

	// 运行所有测试
	tests := []struct {
		name string
		fn   func() error
	}{
		{"PriceCache Basic", testPriceCacheBasic},
		{"PriceCache Events", testPriceCacheEvents},
		{"PriceCache Concurrent", testPriceCacheConcurrent},
		{"PriceCache Index", testPriceCacheIndex},
	}

	passed := 0
	failed := 0

	for _, test := range tests {
		log.Printf("\n--- Running: %s ---", test.name)
		if err := test.fn(); err != nil {
			log.Printf("❌ FAILED: %s - %v", test.name, err)
			failed++
		} else {
			log.Printf("✅ PASSED: %s", test.name)
			passed++
		}
	}

	log.Println("\n========================================")
	log.Printf("Test Results: %d passed, %d failed", passed, failed)
	log.Println("========================================")

	if failed > 0 {
		os.Exit(1)
	}
}

// testPriceCacheBasic 测试PriceCache基本功能
func testPriceCacheBasic() error {
	pc := cache.NewPriceCache(0.0001, 100)

	// 测试更新和获取
	poolAddr := "0x1234567890abcdef1234567890abcdef12345678"
	reserve0 := big.NewInt(1000000000000000000) // 1 ETH
	reserve1 := big.NewInt(2000000000)          // 2000 USDT (6 decimals)

	pc.Update(poolAddr, reserve0, reserve1, 12345678)

	// 获取
	price, ok := pc.Get(poolAddr)
	if !ok {
		return fmt.Errorf("failed to get price after update")
	}

	if price.PoolAddress != poolAddr {
		return fmt.Errorf("pool address mismatch: got %s, want %s", price.PoolAddress, poolAddr)
	}

	if price.Reserve0.Cmp(reserve0) != 0 {
		return fmt.Errorf("reserve0 mismatch")
	}

	if price.Reserve1.Cmp(reserve1) != 0 {
		return fmt.Errorf("reserve1 mismatch")
	}

	log.Printf("  Pool: %s, Price: %.8f", price.PoolAddress, price.Price)
	return nil
}

// testPriceCacheEvents 测试PriceCache事件订阅
func testPriceCacheEvents() error {
	pc := cache.NewPriceCache(0.001, 100) // 0.1%变化阈值

	// 订阅事件
	events := pc.Subscribe()

	// 初始化池子
	poolAddr := "0xabcdef1234567890abcdef1234567890abcdef12"
	pc.UpdateWithMetadata(&cache.PoolPrice{
		PoolAddress: poolAddr,
		Token0:      common.HexToAddress("0x1111111111111111111111111111111111111111"),
		Token1:      common.HexToAddress("0x2222222222222222222222222222222222222222"),
		Reserve0:    big.NewInt(1000000000000000000),
		Reserve1:    big.NewInt(2000000000000000000),
		DexName:     "Uniswap V2",
	})

	// 使用goroutine更新价格
	go func() {
		time.Sleep(100 * time.Millisecond)
		// 更新价格（变化超过阈值）
		pc.Update(poolAddr,
			big.NewInt(1000000000000000000),
			big.NewInt(2100000000000000000), // 5%变化
			12345679)
	}()

	// 等待事件
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	select {
	case event := <-events:
		log.Printf("  Received event: pool=%s, change=%.4f%%",
			event.PoolAddress, event.ChangeRate*100)
		return nil
	case <-ctx.Done():
		return fmt.Errorf("timeout waiting for event")
	}
}

// testPriceCacheConcurrent 测试PriceCache并发安全性
func testPriceCacheConcurrent() error {
	pc := cache.NewPriceCache(0.0001, 100)

	// 并发更新
	done := make(chan bool)
	poolCount := 100
	updateCount := 100

	// 启动多个写goroutine
	for i := 0; i < poolCount; i++ {
		go func(idx int) {
			poolAddr := fmt.Sprintf("0x%040d", idx)
			for j := 0; j < updateCount; j++ {
				pc.Update(poolAddr,
					big.NewInt(int64(1000000+j)),
					big.NewInt(int64(2000000+j)),
					uint64(j))
			}
			done <- true
		}(i)
	}

	// 同时启动读goroutine
	go func() {
		for i := 0; i < poolCount*updateCount; i++ {
			poolAddr := fmt.Sprintf("0x%040d", i%poolCount)
			pc.Get(poolAddr)
		}
		done <- true
	}()

	// 等待完成
	for i := 0; i < poolCount+1; i++ {
		<-done
	}

	// 验证数据
	all := pc.GetAll()
	log.Printf("  Concurrent test completed: %d pools", len(all))

	if len(all) != poolCount {
		return fmt.Errorf("expected %d pools, got %d", poolCount, len(all))
	}

	return nil
}

// testPriceCacheIndex 测试PriceCache索引功能
func testPriceCacheIndex() error {
	pc := cache.NewPriceCache(0.0001, 100)

	// 创建测试数据
	token0 := common.HexToAddress("0x1111111111111111111111111111111111111111")
	token1 := common.HexToAddress("0x2222222222222222222222222222222222222222")
	token2 := common.HexToAddress("0x3333333333333333333333333333333333333333")

	pools := []*cache.PoolPrice{
		{
			PoolAddress: "0xpool1",
			Token0:      token0,
			Token1:      token1,
			DexName:     "Uniswap V2",
			Reserve0:    big.NewInt(1000),
			Reserve1:    big.NewInt(2000),
		},
		{
			PoolAddress: "0xpool2",
			Token0:      token0,
			Token1:      token1,
			DexName:     "SushiSwap",
			Reserve0:    big.NewInt(1100),
			Reserve1:    big.NewInt(2100),
		},
		{
			PoolAddress: "0xpool3",
			Token0:      token1,
			Token1:      token2,
			DexName:     "Uniswap V2",
			Reserve0:    big.NewInt(3000),
			Reserve1:    big.NewInt(4000),
		},
	}

	// 构建索引
	pc.BuildIndex(pools)

	// 测试GetByToken
	token0Pools := pc.GetByToken(token0.Hex())
	if len(token0Pools) != 2 {
		return fmt.Errorf("expected 2 pools for token0, got %d", len(token0Pools))
	}
	log.Printf("  Pools containing token0: %d", len(token0Pools))

	// 测试GetByTokenPair
	pairPools := pc.GetByTokenPair(token0.Hex(), token1.Hex())
	if len(pairPools) != 2 {
		return fmt.Errorf("expected 2 pools for token0-token1 pair, got %d", len(pairPools))
	}
	log.Printf("  Pools for token0-token1 pair: %d (cross-DEX arbitrage candidates!)", len(pairPools))

	return nil
}

// runInteractiveMode 交互模式（用于手动测试）
func runInteractiveMode() {
	log.Println("Interactive mode - Press Ctrl+C to exit")

	pc := cache.NewPriceCache(0.0001, 100)
	events := pc.Subscribe()

	// 监听事件
	go func() {
		for event := range events {
			log.Printf("📊 Price change: pool=%s, %.4f%% change",
				event.PoolAddress[:16]+"...", event.ChangeRate*100)
		}
	}()

	// 模拟价格更新
	go func() {
		poolAddr := "0x1234567890abcdef1234567890abcdef12345678"
		reserve0 := big.NewInt(1000000000000000000)
		reserve1 := big.NewInt(2000000000000000000)

		// 初始化
		pc.UpdateWithMetadata(&cache.PoolPrice{
			PoolAddress: poolAddr,
			Token0:      common.HexToAddress("0x1111111111111111111111111111111111111111"),
			Token1:      common.HexToAddress("0x2222222222222222222222222222222222222222"),
			Reserve0:    reserve0,
			Reserve1:    reserve1,
		})

		for i := 0; ; i++ {
			time.Sleep(time.Second)
			// 每次增加1%
			newReserve1 := new(big.Int).Add(reserve1, big.NewInt(int64(i)*20000000000000000))
			pc.Update(poolAddr, reserve0, newReserve1, uint64(12345678+i))
		}
	}()

	// 等待中断信号
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	log.Println("Shutting down...")
}
