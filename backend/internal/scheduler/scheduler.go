package scheduler

import (
	"fmt"
	"log"
	"time"

	"github.com/defi-bot/backend/internal/collector"
	"github.com/defi-bot/backend/internal/config"
	"github.com/robfig/cron/v3"
)

// Scheduler 定时任务调度器
type Scheduler struct {
	cron      *cron.Cron
	collector *collector.Collector
	config    *config.SchedulerConfig
}

// NewScheduler 创建新的调度器
func NewScheduler(collector *collector.Collector, cfg *config.SchedulerConfig) *Scheduler {
	return &Scheduler{
		cron:      cron.New(cron.WithSeconds()),
		collector: collector,
		config:    cfg,
	}
}

// Start 启动调度器
func (s *Scheduler) Start() error {
	log.Println("启动定时任务调度器...")

	// 1. 采集价格数据任务
	collectInterval := s.config.CollectInterval
	if collectInterval <= 0 {
		collectInterval = 30 // 默认 30 秒
	}

	collectSpec := fmt.Sprintf("@every %ds", collectInterval)
	_, err := s.cron.AddFunc(collectSpec, func() {
		log.Println("执行定时任务: 采集价格数据")
		if err := s.collector.CollectAllData(); err != nil {
			log.Printf("采集数据失败: %v", err)
		}
	})
	if err != nil {
		return fmt.Errorf("添加采集任务失败: %w", err)
	}
	log.Printf("已添加采集任务: 每 %d 秒执行一次", collectInterval)

	// 2. 分析套利机会任务
	analyzeInterval := s.config.AnalyzeInterval
	if analyzeInterval <= 0 {
		analyzeInterval = 10 // 默认 10 秒
	}

	analyzeSpec := fmt.Sprintf("@every %ds", analyzeInterval)
	_, err = s.cron.AddFunc(analyzeSpec, func() {
		log.Println("执行定时任务: 分析套利机会")
		// TODO: 实现套利分析逻辑
		// if err := s.analyzer.AnalyzeOpportunities(); err != nil {
		//     log.Printf("分析套利机会失败: %v", err)
		// }
	})
	if err != nil {
		return fmt.Errorf("添加分析任务失败: %w", err)
	}
	log.Printf("已添加分析任务: 每 %d 秒执行一次", analyzeInterval)

	// 3. ✅ Gas 价格采集任务（业界标准：每30秒）
	gasSpec := "@every 30s"
	_, err = s.cron.AddFunc(gasSpec, func() {
		log.Println("执行定时任务: 采集 Gas 价格")
		if err := s.collector.CollectGasData(); err != nil {
			log.Printf("采集 Gas 价格失败: %v", err)
		}
	})
	if err != nil {
		return fmt.Errorf("添加 Gas 采集任务失败: %w", err)
	}
	log.Printf("已添加 Gas 采集任务: 每 30 秒执行一次")

	// 4. 清理过期数据任务
	cleanupInterval := s.config.CleanupInterval
	if cleanupInterval <= 0 {
		cleanupInterval = 24 // 默认 24 小时
	}

	cleanupSpec := fmt.Sprintf("@every %dh", cleanupInterval)
	_, err = s.cron.AddFunc(cleanupSpec, func() {
		log.Println("执行定时任务: 清理过期数据")
		// 保留最近 7 天的数据
		if err := s.collector.CleanupOldData(7); err != nil {
			log.Printf("清理过期数据失败: %v", err)
		}
	})
	if err != nil {
		return fmt.Errorf("添加清理任务失败: %w", err)
	}
	log.Printf("已添加清理任务: 每 %d 小时执行一次", cleanupInterval)

	// 启动 cron
	s.cron.Start()
	log.Println("定时任务调度器已启动")

	return nil
}

// Stop 停止调度器
func (s *Scheduler) Stop() {
	if s.cron != nil {
		log.Println("停止定时任务调度器...")
		ctx := s.cron.Stop()
		// 等待所有任务完成
		select {
		case <-ctx.Done():
			log.Println("定时任务调度器已停止")
		case <-time.After(30 * time.Second):
			log.Println("定时任务调度器停止超时")
		}
	}
}
