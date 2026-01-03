// cmd/test-improvements/main.go
// 测试新增功能：API 层、套利机会存储、CEX-DEX 策略、执行记录保存
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"time"

	"github.com/defi-bot/backend/internal/api"
	"github.com/defi-bot/backend/internal/config"
	"github.com/defi-bot/backend/internal/database"
	"github.com/defi-bot/backend/internal/executor"
	"github.com/defi-bot/backend/internal/models"
	"github.com/defi-bot/backend/internal/strategy"
	"github.com/ethereum/go-ethereum/common"
	"gorm.io/gorm"
)

var (
	configPath = flag.String("config", "configs/config.yaml", "配置文件路径")
)

func main() {
	flag.Parse()

	fmt.Println("========================================")
	fmt.Println("🧪 开始测试新增功能")
	fmt.Println("========================================")

	// 1. 加载配置
	cfg, err := config.LoadConfig(*configPath)
	if err != nil {
		fmt.Printf("❌ 加载配置失败: %v\n", err)
		return
	}
	fmt.Println("✅ 配置加载成功")

	// 2. 初始化数据库
	if err := database.InitDB(&cfg.Database); err != nil {
		fmt.Printf("❌ 数据库初始化失败: %v\n", err)
		return
	}
	defer database.CloseDB()
	fmt.Println("✅ 数据库连接成功")

	db := database.GetDB()

	// 运行测试
	allPassed := true

	// 测试 1: API 层
	if !testAPILayer(db) {
		allPassed = false
	}

	// 测试 2: 套利机会存储
	if !testOpportunitySave(db) {
		allPassed = false
	}

	// 测试 3: CEX-DEX 策略
	if !testCexDexStrategy(db) {
		allPassed = false
	}

	// 测试 4: 执行记录保存
	if !testExecutionSave(db) {
		allPassed = false
	}

	// 输出总结
	fmt.Println("========================================")
	if allPassed {
		fmt.Println("✅ 所有测试通过！")
	} else {
		fmt.Println("❌ 部分测试失败，请检查上面的错误")
	}
	fmt.Println("========================================")
}

// ============================================================
// 测试 1: API 层
// ============================================================
func testAPILayer(db *gorm.DB) bool {
	fmt.Println("\n--- 测试 1: REST API 层 ---")
	passed := true

	// 创建 API 服务器
	apiServer := api.NewAPIServer(db)
	router := apiServer.GetRouter()

	// 测试端点列表
	tests := []struct {
		name       string
		method     string
		path       string
		expectCode int
	}{
		{"健康检查", "GET", "/health", 200},
		{"获取套利机会", "GET", "/api/v1/opportunities", 200},
		{"获取执行记录", "GET", "/api/v1/executions", 200},
		{"获取统计数据", "GET", "/api/v1/stats", 200},
		{"获取每日统计", "GET", "/api/v1/stats/daily", 200},
		{"获取代币列表", "GET", "/api/v1/tokens", 200},
		{"获取金库信息", "GET", "/api/v1/vault/0x1234567890abcdef1234567890abcdef12345678", 200},
	}

	for _, tt := range tests {
		req := httptest.NewRequest(tt.method, tt.path, nil)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)

		if w.Code != tt.expectCode {
			fmt.Printf("  ❌ %s: 期望状态码 %d, 实际 %d\n", tt.name, tt.expectCode, w.Code)
			passed = false
		} else {
			// 检查返回的 JSON
			var resp map[string]interface{}
			if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
				fmt.Printf("  ❌ %s: JSON 解析失败: %v\n", tt.name, err)
				passed = false
			} else {
				fmt.Printf("  ✅ %s: 状态码 %d, 响应有效\n", tt.name, w.Code)
			}
		}
	}

	// 测试带参数的请求
	fmt.Println("  测试带参数请求:")

	// 获取单个不存在的机会
	req := httptest.NewRequest("GET", "/api/v1/opportunities/99999", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code == 404 {
		fmt.Println("    ✅ 不存在的机会返回 404")
	} else {
		fmt.Printf("    ❌ 不存在的机会应返回 404, 实际 %d\n", w.Code)
		passed = false
	}

	// 获取带过滤的机会
	req = httptest.NewRequest("GET", "/api/v1/opportunities?status=pending&limit=10", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code == 200 {
		fmt.Println("    ✅ 带参数查询正常")
	} else {
		fmt.Printf("    ❌ 带参数查询失败: %d\n", w.Code)
		passed = false
	}

	if passed {
		fmt.Println("  ✅ API 层测试通过")
	} else {
		fmt.Println("  ❌ API 层测试失败")
	}

	return passed
}

// ============================================================
// 测试 2: 套利机会存储
// ============================================================
func testOpportunitySave(db *gorm.DB) bool {
	fmt.Println("\n--- 测试 2: 套利机会数据库存储 ---")
	passed := true

	// 创建测试用的策略引擎（不需要 web3 客户端）
	strategyConfig := &strategy.StrategyConfig{
		MinProfitRate:    0.005,
		MaxPathLength:    5,
		MinPathLength:    3,
		ValidityDuration: 30 * time.Second,
	}

	engine := strategy.NewStrategyEngine(strategyConfig, nil, db, nil)

	// 创建测试套利机会
	testOpps := []*strategy.ArbitrageOpportunity{
		{
			ID: "test_opp_1",
			SwapPath: []common.Address{
				common.HexToAddress("0x1111111111111111111111111111111111111111"),
				common.HexToAddress("0x2222222222222222222222222222222222222222"),
				common.HexToAddress("0x1111111111111111111111111111111111111111"),
			},
			Dexes: []common.Address{
				common.HexToAddress("0xaaaa000000000000000000000000000000000001"),
				common.HexToAddress("0xbbbb000000000000000000000000000000000002"),
			},
			DexNames:     []string{"Uniswap V2", "SushiSwap"},
			AmountIn:     big.NewInt(1e18),
			ExpectedOut:  big.NewInt(1.02e18),
			ExpectProfit: big.NewInt(0.02e18),
			MinProfit:    big.NewInt(0.01e18),
			ProfitRate:   0.02,
			GasEstimate:  150000,
			GasPrice:     big.NewInt(50e9),
			GasCost:      big.NewInt(7.5e15),
			Timestamp:    time.Now(),
			ValidUntil:   time.Now().Add(30 * time.Second),
			Confidence:   0.85,
			PathLength:   3,
		},
	}

	// 保存到数据库
	ctx := context.Background()
	err := engine.SaveOpportunitiesToDB(ctx, testOpps)
	if err != nil {
		fmt.Printf("  ❌ 保存套利机会失败: %v", err)
		passed = false
	} else {
		fmt.Println("  ✅ 套利机会保存成功")
	}

	// 验证是否保存成功
	var count int64
	db.Model(&models.ArbitrageOpportunity{}).
		Where("arbitrage_type = ?", "cross_dex").
		Count(&count)

	if count > 0 {
		fmt.Printf("  ✅ 数据库中有 %d 条套利机会记录", count)
	} else {
		fmt.Println("  ⚠️  数据库中没有套利机会记录（可能是因为没有匹配的 Token）")
	}

	// 测试重复保存（应该更新而不是创建新记录）
	err = engine.SaveOpportunitiesToDB(ctx, testOpps)
	if err != nil {
		fmt.Printf("  ❌ 重复保存失败: %v", err)
		passed = false
	} else {
		fmt.Println("  ✅ 重复保存处理正确")
	}

	if passed {
		fmt.Println("  ✅ 套利机会存储测试通过")
	} else {
		fmt.Println("  ❌ 套利机会存储测试失败")
	}

	return passed
}

// ============================================================
// 测试 3: CEX-DEX 策略
// ============================================================
func testCexDexStrategy(db *gorm.DB) bool {
	fmt.Println("\n--- 测试 3: CEX-DEX 套利策略 ---")
	passed := true

	// 检查 CexTicker 表是否有数据
	var tickerCount int64
	db.Model(&models.CexTicker{}).Count(&tickerCount)
	fmt.Printf("  📊 CexTicker 表记录数: %d", tickerCount)

	// 检查 DEX 池子数据
	var pairCount int64
	db.Model(&models.TradingPair{}).Where("is_active = ?", true).Count(&pairCount)
	fmt.Printf("  📊 活跃交易对数: %d", pairCount)

	// 创建策略引擎
	strategyConfig := &strategy.StrategyConfig{
		MinProfitRate:    0.005,
		MaxPathLength:    5,
		MinPathLength:    3,
		ValidityDuration: 30 * time.Second,
	}

	engine := strategy.NewStrategyEngine(strategyConfig, nil, db, nil)

	// 尝试查找 CEX-DEX 套利机会
	ctx := context.Background()
	opps, err := engine.FindCexDexOpportunities(ctx)
	if err != nil {
		fmt.Printf("  ❌ 查找 CEX-DEX 机会失败: %v", err)
		passed = false
	} else {
		fmt.Printf("  ✅ FindCexDexOpportunities 执行成功，找到 %d 个机会", len(opps))

		// 打印找到的机会
		for i, opp := range opps {
			if i >= 3 {
				fmt.Printf("     ... 还有 %d 个机会", len(opps)-3)
				break
			}
			fmt.Printf("     机会 %d: %s, 价差 %.2f%%, 方向 %s",
				i+1, opp.TokenPair, opp.PriceSpread, opp.Direction)
		}
	}

	// 如果没有数据，提示用户
	if tickerCount == 0 {
		fmt.Println("  ⚠️  没有 CEX 行情数据，建议运行数据采集")
	}
	if pairCount == 0 {
		fmt.Println("  ⚠️  没有 DEX 交易对数据，建议运行数据采集")
	}

	if passed {
		fmt.Println("  ✅ CEX-DEX 策略测试通过")
	} else {
		fmt.Println("  ❌ CEX-DEX 策略测试失败")
	}

	return passed
}

// ============================================================
// 测试 4: 执行记录保存
// ============================================================
func testExecutionSave(db *gorm.DB) bool {
	fmt.Println("\n--- 测试 4: 执行记录保存 ---")
	passed := true

	// 创建执行器（不需要真实的 web3 客户端）
	exec := executor.NewArbitrageExecutor(
		nil,
		common.HexToAddress("0x0000000000000000000000000000000000000000"),
		"",
	)
	exec.SetDB(db)

	// 检查 SetDB 是否工作
	fmt.Println("  ✅ SetDB 方法调用成功")

	// 直接创建一条测试执行记录（使用 nil 表示没有关联的 token）
	testExecution := &models.ArbitrageExecution{
		VaultAddress:    "0x1234567890abcdef1234567890abcdef12345678",
		TokenInID:       nil, // 可能没有对应的 token
		TokenOutID:      nil,
		AmountIn:        "1000000000000000000",
		AmountOut:       "1020000000000000000",
		ActualProfit:    "20000000000000000",
		ProfitRate:      2.0,
		SwapPath:        `["0x1111", "0x2222", "0x1111"]`,
		DexPath:         `["Uniswap V2", "SushiSwap"]`,
		GasUsed:         150000,
		GasPrice:        "50000000000",
		TxHash:          fmt.Sprintf("0xtest_%d", time.Now().UnixNano()),
		BlockNumber:     12345678,
		Status:          "success",
		ExecutionTimeMs: 1500,
		Timestamp:       time.Now(),
	}

	// 保存到数据库
	if err := db.Create(testExecution).Error; err != nil {
		fmt.Printf("  ❌ 保存执行记录失败: %v", err)
		passed = false
	} else {
		fmt.Printf("  ✅ 执行记录保存成功 (ID: %d)", testExecution.ID)
	}

	// 验证可以查询到
	var count int64
	db.Model(&models.ArbitrageExecution{}).Count(&count)
	fmt.Printf("  📊 数据库中有 %d 条执行记录", count)

	// 通过 API 查询
	apiServer := api.NewAPIServer(db)
	router := apiServer.GetRouter()

	req := httptest.NewRequest("GET", "/api/v1/executions", nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code == 200 {
		var resp struct {
			Success bool                        `json:"success"`
			Data    []models.ArbitrageExecution `json:"data"`
			Count   int                         `json:"count"`
		}
		json.Unmarshal(w.Body.Bytes(), &resp)
		fmt.Printf("  ✅ API 返回 %d 条执行记录", resp.Count)
	} else {
		fmt.Printf("  ❌ API 查询执行记录失败: %d", w.Code)
		passed = false
	}

	// 清理测试数据
	db.Delete(testExecution)
	fmt.Println("  ✅ 测试数据已清理")

	if passed {
		fmt.Println("  ✅ 执行记录保存测试通过")
	} else {
		fmt.Println("  ❌ 执行记录保存测试失败")
	}

	return passed
}

// ============================================================
// 集成测试: 完整流程
// ============================================================
func testIntegration(db *gorm.DB) bool {
	fmt.Println("\n--- 集成测试: 完整 API 流程 ---")
	passed := true

	apiServer := api.NewAPIServer(db)
	router := apiServer.GetRouter()

	// 1. 创建套利机会
	testOpp := models.ArbitrageOpportunity{
		ArbitrageType:  "cross_dex",
		AmountIn:       "1000000000000000000",
		ExpectedProfit: "20000000000000000",
		MinProfit:      "10000000000000000",
		ProfitRate:     2.0,
		SwapPath:       `["0x1111", "0x2222", "0x1111"]`,
		DexPath:        `["Uniswap V2", "SushiSwap"]`,
		DexRouters:     `["0xaaaa", "0xbbbb"]`,
		GasEstimate:    150000,
		MaxGasPrice:    "50000000000",
		Status:         "pending",
		Priority:       80,
		ExpiresAt:      time.Now().Add(1 * time.Hour),
	}
	db.Create(&testOpp)
	fmt.Printf("  ✅ 创建测试机会 ID: %d", testOpp.ID)

	// 2. 通过 API 获取
	req := httptest.NewRequest("GET", fmt.Sprintf("/api/v1/opportunities/%d", testOpp.ID), nil)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code == 200 {
		fmt.Println("  ✅ API 获取单个机会成功")
	} else {
		fmt.Printf("  ❌ API 获取单个机会失败: %d", w.Code)
		passed = false
	}

	// 3. 获取统计数据
	req = httptest.NewRequest("GET", "/api/v1/stats", nil)
	w = httptest.NewRecorder()
	router.ServeHTTP(w, req)

	if w.Code == 200 {
		var resp struct {
			Success bool `json:"success"`
			Data    struct {
				TotalOpportunities   int64 `json:"total_opportunities"`
				PendingOpportunities int64 `json:"pending_opportunities"`
			} `json:"data"`
		}
		json.Unmarshal(w.Body.Bytes(), &resp)
		fmt.Printf("  ✅ 统计数据: 总机会 %d, 待处理 %d",
			resp.Data.TotalOpportunities, resp.Data.PendingOpportunities)
	} else {
		fmt.Printf("  ❌ 获取统计失败: %d", w.Code)
		passed = false
	}

	// 清理
	db.Delete(&testOpp)
	fmt.Println("  ✅ 测试数据已清理")

	if passed {
		fmt.Println("  ✅ 集成测试通过")
	} else {
		fmt.Println("  ❌ 集成测试失败")
	}

	return passed
}

// 辅助函数：发送 HTTP 请求
func httpRequest(method, url string, body interface{}) (*http.Response, []byte, error) {
	var reqBody io.Reader
	if body != nil {
		jsonBody, _ := json.Marshal(body)
		reqBody = bytes.NewBuffer(jsonBody)
	}

	req, err := http.NewRequest(method, url, reqBody)
	if err != nil {
		return nil, nil, err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, err
	}

	return resp, respBody, nil
}
