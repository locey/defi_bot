// internal/metrics/metrics.go
// Phase 4.1: Prometheus 可观测性指标
// 提供套利系统的核心运行指标，用于监控、告警和性能分析
package metrics

import (
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/defi-bot/backend/pkg/log"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// ArbMetrics 套利系统指标集合
type ArbMetrics struct {
	// 套利机会发现
	OpportunitiesFound *prometheus.CounterVec
	OpportunitiesValue prometheus.Histogram

	// 执行指标
	ExecutionsTotal    *prometheus.CounterVec
	ExecutionDuration  prometheus.Histogram
	ExecutionGasUsed   prometheus.Histogram

	// 利润指标
	ProfitTotal        *prometheus.CounterVec
	ProfitPerExecution prometheus.Histogram
	GasSpentTotal      prometheus.Counter

	// 延迟指标
	DetectionLatency   prometheus.Histogram // 从价格变化到发现机会的延迟
	ExecutionLatency   prometheus.Histogram // 从发现机会到交易上链的延迟
	EndToEndLatency    prometheus.Histogram // 端到端延迟

	// 系统健康指标
	RPCLatency         *prometheus.HistogramVec
	RPCErrors          *prometheus.CounterVec
	ActivePools        prometheus.Gauge
	PriceUpdatesPerSec prometheus.Gauge

	// 蜜罐检测
	HoneypotChecks     *prometheus.CounterVec
	HoneypotDetected   prometheus.Counter

	// 新池子
	NewPoolsDetected   prometheus.Counter

	// eth_call 模拟：过滤数 / 通过数（用于计算真实机会通过率）
	OpportunitiesSimFiltered prometheus.Counter
	OpportunitiesSimPassed   prometheus.Counter
}

var (
	instance *ArbMetrics
	once     sync.Once
)

// GetMetrics 获取全局指标实例（单例）
func GetMetrics() *ArbMetrics {
	once.Do(func() {
		instance = newArbMetrics()
	})
	return instance
}

// newArbMetrics 创建指标集合
func newArbMetrics() *ArbMetrics {
	return &ArbMetrics{
		// 套利机会发现
		OpportunitiesFound: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "arb_opportunities_found_total",
				Help: "Total number of arbitrage opportunities found",
			},
			[]string{"path_length", "protocol"},
		),
		OpportunitiesValue: promauto.NewHistogram(
			prometheus.HistogramOpts{
				Name:    "arb_opportunity_value_usd",
				Help:    "Estimated value of discovered opportunities in USD",
				Buckets: []float64{0.1, 0.5, 1, 5, 10, 50, 100, 500, 1000},
			},
		),

		// 执行指标
		ExecutionsTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "arb_executions_total",
				Help: "Total number of arbitrage executions",
			},
			[]string{"status", "strategy_type"}, // status: success/failed/simulated
		),
		ExecutionDuration: promauto.NewHistogram(
			prometheus.HistogramOpts{
				Name:    "arb_execution_duration_seconds",
				Help:    "Duration of arbitrage execution in seconds",
				Buckets: []float64{0.1, 0.5, 1, 2, 5, 10, 30, 60},
			},
		),
		ExecutionGasUsed: promauto.NewHistogram(
			prometheus.HistogramOpts{
				Name:    "arb_execution_gas_used",
				Help:    "Gas used per arbitrage execution",
				Buckets: []float64{100000, 200000, 300000, 500000, 800000, 1000000},
			},
		),

		// 利润指标
		ProfitTotal: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "arb_profit_total_wei",
				Help: "Total profit earned in wei",
			},
			[]string{"token"},
		),
		ProfitPerExecution: promauto.NewHistogram(
			prometheus.HistogramOpts{
				Name:    "arb_profit_per_execution_usd",
				Help:    "Profit per execution in USD",
				Buckets: []float64{0.01, 0.1, 0.5, 1, 5, 10, 50, 100},
			},
		),
		GasSpentTotal: promauto.NewCounter(
			prometheus.CounterOpts{
				Name: "arb_gas_spent_total_wei",
				Help: "Total gas spent in wei",
			},
		),

		// 延迟指标
		DetectionLatency: promauto.NewHistogram(
			prometheus.HistogramOpts{
				Name:    "arb_detection_latency_seconds",
				Help:    "Latency from price change to opportunity detection",
				Buckets: []float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1, 5},
			},
		),
		ExecutionLatency: promauto.NewHistogram(
			prometheus.HistogramOpts{
				Name:    "arb_execution_latency_seconds",
				Help:    "Latency from detection to transaction submission",
				Buckets: []float64{0.01, 0.05, 0.1, 0.5, 1, 2, 5, 10},
			},
		),
		EndToEndLatency: promauto.NewHistogram(
			prometheus.HistogramOpts{
				Name:    "arb_end_to_end_latency_seconds",
				Help:    "End-to-end latency from price change to confirmation",
				Buckets: []float64{0.1, 0.5, 1, 2, 5, 10, 30},
			},
		),

		// 系统健康指标
		RPCLatency: promauto.NewHistogramVec(
			prometheus.HistogramOpts{
				Name:    "arb_rpc_latency_seconds",
				Help:    "RPC call latency in seconds",
				Buckets: []float64{0.01, 0.05, 0.1, 0.5, 1, 5},
			},
			[]string{"method"},
		),
		RPCErrors: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "arb_rpc_errors_total",
				Help: "Total RPC errors",
			},
			[]string{"method", "error_type"},
		),
		ActivePools: promauto.NewGauge(
			prometheus.GaugeOpts{
				Name: "arb_active_pools",
				Help: "Number of actively monitored pools",
			},
		),
		PriceUpdatesPerSec: promauto.NewGauge(
			prometheus.GaugeOpts{
				Name: "arb_price_updates_per_second",
				Help: "Rate of price updates received per second",
			},
		),

		// 蜜罐检测
		HoneypotChecks: promauto.NewCounterVec(
			prometheus.CounterOpts{
				Name: "arb_honeypot_checks_total",
				Help: "Total honeypot detection checks",
			},
			[]string{"result"}, // safe/honeypot/error
		),
		HoneypotDetected: promauto.NewCounter(
			prometheus.CounterOpts{
				Name: "arb_honeypot_detected_total",
				Help: "Total honeypot tokens detected",
			},
		),

		// 新池子
		NewPoolsDetected: promauto.NewCounter(
			prometheus.CounterOpts{
				Name: "arb_new_pools_detected_total",
				Help: "Total new pools detected",
			},
		),

		// eth_call 模拟：假机会过滤数 / 真实机会通过数
		OpportunitiesSimFiltered: promauto.NewCounter(
			prometheus.CounterOpts{
				Name: "arb_opportunities_sim_filtered_total",
				Help: "Opportunities filtered by eth_call simulation (reverted or unprofitable)",
			},
		),
		OpportunitiesSimPassed: promauto.NewCounter(
			prometheus.CounterOpts{
				Name: "arb_opportunities_sim_passed_total",
				Help: "Opportunities passed eth_call simulation (real opportunity)",
			},
		),
	}
}

// RecordSimFiltered 记录一次被 eth_call 模拟过滤掉的机会
func (m *ArbMetrics) RecordSimFiltered() {
	m.OpportunitiesSimFiltered.Inc()
}

// RecordSimPassed 记录一次通过 eth_call 模拟的机会
func (m *ArbMetrics) RecordSimPassed() {
	m.OpportunitiesSimPassed.Inc()
}

// RecordExecution 记录一次执行
func (m *ArbMetrics) RecordExecution(success bool, strategyType string, duration time.Duration, gasUsed uint64) {
	status := "failed"
	if success {
		status = "success"
	}
	m.ExecutionsTotal.WithLabelValues(status, strategyType).Inc()
	m.ExecutionDuration.Observe(duration.Seconds())
	if gasUsed > 0 {
		m.ExecutionGasUsed.Observe(float64(gasUsed))
	}
}

// RecordProfit 记录利润
func (m *ArbMetrics) RecordProfit(token string, profitWei float64, profitUSD float64) {
	m.ProfitTotal.WithLabelValues(token).Add(profitWei)
	m.ProfitPerExecution.Observe(profitUSD)
}

// RecordOpportunity 记录发现的套利机会
func (m *ArbMetrics) RecordOpportunity(pathLength int, protocol string, estimatedValueUSD float64) {
	m.OpportunitiesFound.WithLabelValues(
		fmt.Sprintf("%d", pathLength),
		protocol,
	).Inc()
	m.OpportunitiesValue.Observe(estimatedValueUSD)
}

// StartMetricsServer 启动 Prometheus 指标 HTTP 服务器
func StartMetricsServer(addr string) {
	http.Handle("/metrics", promhttp.Handler())
	go func() {
		log.Main().Info().Str("addr", addr).Msg("Prometheus metrics server started")
		if err := http.ListenAndServe(addr, nil); err != nil {
			log.Main().Error().Err(err).Msg("Metrics server failed")
		}
	}()
}

