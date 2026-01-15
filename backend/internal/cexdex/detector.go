// Package cexdex 提供 CEX-DEX 套利功能
package cexdex

import (
	"context"
	"fmt"
	"math/big"
	"sync"
	"time"

	"github.com/defi-bot/backend/pkg/log"
	"github.com/ethereum/go-ethereum/common"
)

// DetectorConfig 检测器配置
type DetectorConfig struct {
	// 利润阈值
	MinProfitRate   float64 // 最小利润率 (如 0.003 = 0.3%)
	MinProfitAmount float64 // 最小利润金额 (USD)

	// 交易配置
	MaxTradeAmount float64 // 单笔最大交易金额 (USD)
	MinTradeAmount float64 // 单笔最小交易金额 (USD)

	// 滑点容忍
	MaxSlippage float64 // 最大滑点

	// 检测间隔
	CheckInterval time.Duration

	// 机会有效期
	OpportunityTTL time.Duration
}

// DefaultDetectorConfig 默认配置
func DefaultDetectorConfig() *DetectorConfig {
	return &DetectorConfig{
		MinProfitRate:   0.003,     // 0.3%
		MinProfitAmount: 10,        // $10
		MaxTradeAmount:  10000,     // $10,000
		MinTradeAmount:  100,       // $100
		MaxSlippage:     0.005,     // 0.5%
		CheckInterval:   100 * time.Millisecond,
		OpportunityTTL:  5 * time.Second,
	}
}

// CEXDEXOpportunity CEX-DEX 套利机会
type CEXDEXOpportunity struct {
	ID           string         `json:"id"`
	Symbol       string         `json:"symbol"`        // 交易对符号 (如 "ETHUSDT")
	Direction    string         `json:"direction"`     // "cex_to_dex" 或 "dex_to_cex"
	CEXPrice     float64        `json:"cex_price"`     // CEX 价格
	DEXPrice     float64        `json:"dex_price"`     // DEX 价格
	PriceSpread  float64        `json:"price_spread"`  // 价差
	ProfitRate   float64        `json:"profit_rate"`   // 利润率
	TradeAmount  float64        `json:"trade_amount"`  // 建议交易金额 (USD)
	ExpectProfit float64        `json:"expect_profit"` // 预期利润 (USD)
	GasCost      float64        `json:"gas_cost"`      // 预估 Gas 成本 (USD)
	NetProfit    float64        `json:"net_profit"`    // 净利润 (USD)
	DEXPool      common.Address `json:"dex_pool"`      // DEX 池子地址
	DEXRouter    common.Address `json:"dex_router"`    // DEX 路由地址
	ValidUntil   time.Time      `json:"valid_until"`   // 有效期
	Confidence   float64        `json:"confidence"`    // 置信度 (0-1)
	CreatedAt    time.Time      `json:"created_at"`
}

// DEXPriceProvider DEX 价格提供者接口
type DEXPriceProvider interface {
	GetPrice(symbol string) (float64, error)
	GetPoolAddress(symbol string) common.Address
	GetRouterAddress(symbol string) common.Address
}

// Detector CEX-DEX 套利机会检测器
type Detector struct {
	config          *DetectorConfig
	cexMonitor      *PriceMonitor
	dexProvider     DEXPriceProvider
	opportunities   map[string]*CEXDEXOpportunity
	opportunitiesMu sync.RWMutex
	opportunityCh   chan *CEXDEXOpportunity
	running         bool
	runningMu       sync.RWMutex
	cancelFunc      context.CancelFunc
}

// NewDetector 创建检测器
func NewDetector(config *DetectorConfig, cexMonitor *PriceMonitor, dexProvider DEXPriceProvider) *Detector {
	if config == nil {
		config = DefaultDetectorConfig()
	}

	return &Detector{
		config:        config,
		cexMonitor:    cexMonitor,
		dexProvider:   dexProvider,
		opportunities: make(map[string]*CEXDEXOpportunity),
		opportunityCh: make(chan *CEXDEXOpportunity, 100),
	}
}

// Start 启动检测器
func (d *Detector) Start(ctx context.Context) error {
	d.runningMu.Lock()
	if d.running {
		d.runningMu.Unlock()
		return nil
	}
	d.running = true
	d.runningMu.Unlock()

	ctx, cancel := context.WithCancel(ctx)
	d.cancelFunc = cancel

	// 启动检测循环
	go d.detectLoop(ctx)

	// 启动清理循环
	go d.cleanupLoop(ctx)

	log.Info("CEX-DEX 套利检测器已启动")
	return nil
}

// Stop 停止检测器
func (d *Detector) Stop() {
	d.runningMu.Lock()
	defer d.runningMu.Unlock()

	if !d.running {
		return
	}

	d.running = false
	if d.cancelFunc != nil {
		d.cancelFunc()
	}

	log.Info("CEX-DEX 套利检测器已停止")
}

// detectLoop 检测循环
func (d *Detector) detectLoop(ctx context.Context) {
	// 监听 CEX 价格更新
	priceCh := d.cexMonitor.GetPriceChan()

	for {
		select {
		case <-ctx.Done():
			return

		case cexPrice := <-priceCh:
			if cexPrice == nil {
				continue
			}
			d.checkOpportunity(cexPrice)
		}
	}
}

// checkOpportunity 检查套利机会
func (d *Detector) checkOpportunity(cexPrice *CEXPrice) {
	if d.dexProvider == nil {
		return
	}

	// 获取 DEX 价格
	dexPrice, err := d.dexProvider.GetPrice(cexPrice.Symbol)
	if err != nil {
		return
	}

	// 计算价差
	// CEX 买价 vs DEX 卖价：如果 CEX 买价 > DEX 卖价，可以在 DEX 买然后在 CEX 卖
	// CEX 卖价 vs DEX 买价：如果 CEX 卖价 < DEX 买价，可以在 CEX 买然后在 DEX 卖

	// 方向1: DEX -> CEX (在 DEX 买，在 CEX 卖)
	spread1 := (cexPrice.BidPrice - dexPrice) / dexPrice
	if spread1 > d.config.MinProfitRate {
		d.createOpportunity(cexPrice.Symbol, "dex_to_cex", dexPrice, cexPrice.BidPrice, spread1)
	}

	// 方向2: CEX -> DEX (在 CEX 买，在 DEX 卖)
	spread2 := (dexPrice - cexPrice.AskPrice) / cexPrice.AskPrice
	if spread2 > d.config.MinProfitRate {
		d.createOpportunity(cexPrice.Symbol, "cex_to_dex", cexPrice.AskPrice, dexPrice, spread2)
	}
}

// createOpportunity 创建套利机会
func (d *Detector) createOpportunity(symbol, direction string, buyPrice, sellPrice, profitRate float64) {
	// 计算建议交易金额
	tradeAmount := d.calculateTradeAmount(profitRate)

	// 计算预期利润
	expectProfit := tradeAmount * profitRate

	// 估算 Gas 成本（简化）
	gasCost := 5.0 // 假设 $5 Gas 成本

	// 计算净利润
	netProfit := expectProfit - gasCost

	// 检查是否满足最小利润要求
	if netProfit < d.config.MinProfitAmount {
		return
	}

	// 计算置信度
	confidence := d.calculateConfidence(profitRate, netProfit)

	oppID := fmt.Sprintf("%s_%s_%d", symbol, direction, time.Now().UnixNano())

	opp := &CEXDEXOpportunity{
		ID:           oppID,
		Symbol:       symbol,
		Direction:    direction,
		CEXPrice:     buyPrice,
		DEXPrice:     sellPrice,
		PriceSpread:  sellPrice - buyPrice,
		ProfitRate:   profitRate,
		TradeAmount:  tradeAmount,
		ExpectProfit: expectProfit,
		GasCost:      gasCost,
		NetProfit:    netProfit,
		DEXPool:      d.dexProvider.GetPoolAddress(symbol),
		DEXRouter:    d.dexProvider.GetRouterAddress(symbol),
		ValidUntil:   time.Now().Add(d.config.OpportunityTTL),
		Confidence:   confidence,
		CreatedAt:    time.Now(),
	}

	// 存储机会
	d.opportunitiesMu.Lock()
	d.opportunities[oppID] = opp
	d.opportunitiesMu.Unlock()

	// 发送到通道
	select {
	case d.opportunityCh <- opp:
		log.Info("发现 CEX-DEX 套利机会: %s %s, 利润率: %.2f%%, 净利润: $%.2f",
			symbol, direction, profitRate*100, netProfit)
	default:
		log.Warn("机会通道已满，丢弃机会")
	}
}

// calculateTradeAmount 计算建议交易金额
func (d *Detector) calculateTradeAmount(profitRate float64) float64 {
	// 利润率越高，交易金额越大（但不超过最大值）
	baseAmount := d.config.MinTradeAmount

	// 根据利润率调整
	if profitRate > 0.01 { // 1%
		baseAmount = d.config.MaxTradeAmount
	} else if profitRate > 0.005 { // 0.5%
		baseAmount = d.config.MaxTradeAmount * 0.5
	} else {
		baseAmount = d.config.MinTradeAmount
	}

	return baseAmount
}

// calculateConfidence 计算置信度
func (d *Detector) calculateConfidence(profitRate, netProfit float64) float64 {
	// 基于利润率和净利润计算置信度
	confidence := 0.5

	// 利润率越高，置信度越高
	if profitRate > 0.01 {
		confidence += 0.3
	} else if profitRate > 0.005 {
		confidence += 0.2
	}

	// 净利润越高，置信度越高
	if netProfit > 100 {
		confidence += 0.2
	} else if netProfit > 50 {
		confidence += 0.1
	}

	if confidence > 1.0 {
		confidence = 1.0
	}

	return confidence
}

// cleanupLoop 清理过期机会
func (d *Detector) cleanupLoop(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			d.cleanupExpired()
		}
	}
}

// cleanupExpired 清理过期机会
func (d *Detector) cleanupExpired() {
	now := time.Now()

	d.opportunitiesMu.Lock()
	defer d.opportunitiesMu.Unlock()

	for id, opp := range d.opportunities {
		if now.After(opp.ValidUntil) {
			delete(d.opportunities, id)
		}
	}
}

// GetOpportunities 获取当前所有机会
func (d *Detector) GetOpportunities() []*CEXDEXOpportunity {
	d.opportunitiesMu.RLock()
	defer d.opportunitiesMu.RUnlock()

	result := make([]*CEXDEXOpportunity, 0, len(d.opportunities))
	for _, opp := range d.opportunities {
		result = append(result, opp)
	}
	return result
}

// GetOpportunityChan 获取机会通道
func (d *Detector) GetOpportunityChan() <-chan *CEXDEXOpportunity {
	return d.opportunityCh
}

// IsRunning 是否正在运行
func (d *Detector) IsRunning() bool {
	d.runningMu.RLock()
	defer d.runningMu.RUnlock()
	return d.running
}

// SimpleDEXPriceProvider 简单的 DEX 价格提供者实现
type SimpleDEXPriceProvider struct {
	prices      map[string]float64
	pools       map[string]common.Address
	routers     map[string]common.Address
	mu          sync.RWMutex
}

// NewSimpleDEXPriceProvider 创建简单的 DEX 价格提供者
func NewSimpleDEXPriceProvider() *SimpleDEXPriceProvider {
	return &SimpleDEXPriceProvider{
		prices:  make(map[string]float64),
		pools:   make(map[string]common.Address),
		routers: make(map[string]common.Address),
	}
}

// UpdatePrice 更新价格
func (p *SimpleDEXPriceProvider) UpdatePrice(symbol string, price float64) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.prices[symbol] = price
}

// SetPool 设置池子地址
func (p *SimpleDEXPriceProvider) SetPool(symbol string, pool common.Address) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.pools[symbol] = pool
}

// SetRouter 设置路由地址
func (p *SimpleDEXPriceProvider) SetRouter(symbol string, router common.Address) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.routers[symbol] = router
}

// GetPrice 获取价格
func (p *SimpleDEXPriceProvider) GetPrice(symbol string) (float64, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	price, ok := p.prices[symbol]
	if !ok {
		return 0, fmt.Errorf("未找到 %s 的价格", symbol)
	}
	return price, nil
}

// GetPoolAddress 获取池子地址
func (p *SimpleDEXPriceProvider) GetPoolAddress(symbol string) common.Address {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.pools[symbol]
}

// GetRouterAddress 获取路由地址
func (p *SimpleDEXPriceProvider) GetRouterAddress(symbol string) common.Address {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.routers[symbol]
}

// ConvertToBigInt 将浮点数金额转换为 BigInt（考虑 decimals）
func ConvertToBigInt(amount float64, decimals int) *big.Int {
	// amount * 10^decimals
	multiplier := new(big.Float).SetFloat64(amount)
	scale := new(big.Float).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil))
	multiplier.Mul(multiplier, scale)

	result, _ := multiplier.Int(nil)
	return result
}
