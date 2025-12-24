package scheduler

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/defi-bot/backend/internal/collector"
	"github.com/defi-bot/backend/internal/config"
	"github.com/defi-bot/backend/internal/executor" // 导入执行器
	"github.com/defi-bot/backend/internal/strategy" // 导入策略引擎
	"github.com/robfig/cron/v3"
)

// Scheduler 定时任务调度器
type Scheduler struct {
	cron           *cron.Cron
	collector      *collector.Collector
	strategyEngine *strategy.StrategyEngine // + 新增：策略引擎
	executor       *executor.ArbitrageExecutor // + 新增：执行器
	config         *config.SchedulerConfig
}

// NewScheduler 创建新的调度器
// + 修改：接收 strategyEngine 和 executor 依赖
func NewScheduler(
	collector *collector.Collector,
	strategyEngine *strategy.Engine, // 注意：这里传入的是 Engine 接口
	executor *executor.ArbitrageExecutor,
	cfg *config.SchedulerConfig,
) *Scheduler {
	return &Scheduler{
		cron:           cron.New(cron.WithSeconds(), cron.WithLocation(time.Local)),
		collector:      collector,
		strategyEngine: strategyEngine, // + 初始化
		executor:       executor,       // + 初始化
		config:         cfg,
	}
}

// Start 启动调度器
func (s *Scheduler) Start(ctx context.Context) error {
	log.Println("启动定时任务调度器...")

	// 1. 采集价格数据任务 (保持不变)
	collectInterval := s.config.CollectInterval
	if collectInterval <= 0 {
		collectInterval = 30
	}
	_, err := s.cron.AddFunc(fmt.Sprintf("@every %ds", collectInterval), func() {
		log.Println("执行定时任务: 采集价格数据")
		if err := s.collector.CollectAllData(ctx); err != nil {
			log.Printf("采集数据失败: %v", err)
		}
	})
	if err != nil {
		return fmt.Errorf("添加采集任务失败: %w", err)
	}
	log.Printf("已添加采集任务: 每 %d 秒执行一次", collectInterval)

	// 2. ✅ 分析套利机会任务 (核心修改)
	analyzeInterval := s.config.AnalyzeInterval
	if analyzeInterval <= 0 {
		analyzeInterval = 10 // 默认 10 秒
	}
	_, err = s.cron.AddFunc(fmt.Sprintf("@every %ds", analyzeInterval), func() {
		log.Println("执行定时任务: 分析套利机会")
		s.runAnalysis(ctx) // 调用独立的分析函数
	})
	if err != nil {
		return fmt.Errorf("添加分析任务失败: %w", err)
	}
	log.Printf("已添加分析任务: 每 %d 秒执行一次", analyzeInterval)

	// 3. Gas 价格采集任务 (保持不变)
	gasSpec := "@every 30s"
	_, err = s.cron.AddFunc(gasSpec, func() {
		log.Println("执行定时任务: 采集 Gas 价格")
		if err := s.collector.CollectGasData(ctx); err != nil {
			log.Printf("采集 Gas 价格失败: %v", err)
		}
	})
	if err != nil {
		return fmt.Errorf("添加 Gas 采集任务失败: %w", err)
	}
	log.Printf("已添加 Gas 采集任务: 每 30 秒执行一次")

	// 4. 清理过期数据任务 (保持不变)
	cleanupInterval := s.config.CleanupInterval
	if cleanupInterval <= 0 {
		cleanupInterval = 24
	}
	_, err = s.cron.AddFunc(fmt.Sprintf("@every %dh", cleanupInterval), func() {
		log.Println("执行定时任务: 清理过期数据")
		if err := s.collector.CleanupOldData(ctx, 7); err != nil {
			log.Printf("清理过期数据失败: %v", err)
		}
	})
	if err != nil {
		return fmt.Errorf("添加清理任务失败: %w", err)
	}
	log.Printf("已添加清理任务: 每 %d 小时执行一次", cleanupInterval)

	s.cron.Start()
	log.Println("定时任务调度器已启动")
	return nil
}

// runAnalysis 执行套利分析和执行的完整流程
func (s *Scheduler) runAnalysis(ctx context.Context) {
	// 步骤 1: 查找机会
	opportunities, err := s.strategyEngine.FindOpportunities(ctx)
	if err != nil {
		log.Printf("策略引擎查找机会失败: %v", err)
		return
	}

	if len(opportunities) == 0 {
		log.Println("未发现套利机会")
		return
	}

	log.Printf("发现 %d 个套利机会，利润率: %.4f%% - %.4f%%",
		len(opportunities),
		opportunities[0].ProfitRate*100,
		opportunities[len(opportunities)-1].ProfitRate*100,
	)

	// 步骤 2: 筛选并执行最优机会
	// 为了安全，我们只执行利润率最高的那个
	bestOpp := opportunities[0]

	// 添加一个利润率阈值，避免执行微利机会
	if bestOpp.ProfitRate < s.config.MinProfitRate { // 假设 config 中有这个配置
		log.Printf("最佳机会利润率 %.4f%% 低于阈值 %.4f%%，放弃执行", bestOpp.ProfitRate, s.config.MinProfitRate)
		return
	}

	log.Printf("准备执行最佳机会: %s -> %s, 预期利润: %s",
		bestOpp.SwapPath[0].Hex(),
		bestOpp.SwapPath[len(bestOpp.SwapPath)-1].Hex(),
		bestOpp.ExpectProfit.String(),
	)

	// 步骤 3: 执行
	// 使用带锁的单次执行，防止并发执行同一个机会
	// (更高级的实现可以用分布式锁)
	result, err := s.executor.Execute(ctx, bestOpp)
	if err != nil {
		log.Printf("执行套利失败: %v", err)
		return
	}

	if result.Success {
		log.Printf("✅ 套利执行成功! TxHash: %s, 实际利润: %s, Gas成本: %s",
			result.TxHash,
			result.ActualProfit.String(),
			result.GasCost.String(),
		)
	} else {
		log.Printf("❌ 套利执行失败: %s", result.Error)
	}
}


// Stop 停止调度器 (优化版)
func (s *Scheduler) Stop() {
	if s.cron != nil {
		log.Println("正在停止定时任务调度器...")
		s.cron.Stop()
		log.Println("定时任务调度器已停止")
	}
}