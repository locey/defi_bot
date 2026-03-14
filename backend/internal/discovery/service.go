// internal/discovery/service.go
// Pool Discovery Service — 连接工厂事件监听器到 FastCollector 自动扩展监控池
package discovery

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/defi-bot/backend/internal/collector"
	"github.com/defi-bot/backend/pkg/log"
	"github.com/ethereum/go-ethereum/common"
)

// ServiceConfig 池发现服务配置
type ServiceConfig struct {
	WSURL     string
	Factories []FactoryInfo
	// 核心代币（两个都是核心代币 → Tier1，一个核心 → Tier2）
	CoreTokens map[common.Address]bool
}

// FactoryInfo 工厂合约信息
type FactoryInfo struct {
	Address  common.Address
	DexName  string
	Protocol string // "uniswap_v3", "sushiswap", etc.
}

// DiscoveryService 池发现服务
type DiscoveryService struct {
	config   *ServiceConfig
	listener *EventListener
	fetcher  *TokenFetcher

	// 目标：FastCollector.AddPool()
	fastCollector *collector.FastCollector

	// 统计
	poolsFound int
	mu         sync.Mutex

	running   bool
	runningMu sync.RWMutex
	cancelFn  context.CancelFunc
}

// NewDiscoveryService 创建池发现服务
func NewDiscoveryService(
	config *ServiceConfig,
	fastCollector *collector.FastCollector,
) *DiscoveryService {
	listenerConfig := DefaultEventListenerConfig()
	listenerConfig.Factories = make([]common.Address, len(config.Factories))
	for i, f := range config.Factories {
		listenerConfig.Factories[i] = f.Address
	}

	listener := NewEventListener(config.WSURL, listenerConfig)
	for _, f := range config.Factories {
		listener.AddFactory(f.Address)
	}

	return &DiscoveryService{
		config:        config,
		listener:      listener,
		fetcher:       NewTokenFetcher(DefaultTokenFetcherConfig()),
		fastCollector: fastCollector,
	}
}

// Start 启动池发现服务
func (s *DiscoveryService) Start(ctx context.Context) error {
	s.runningMu.Lock()
	if s.running {
		s.runningMu.Unlock()
		return nil
	}
	s.running = true
	s.runningMu.Unlock()

	ctx, cancel := context.WithCancel(ctx)
	s.cancelFn = cancel

	// 启动工厂事件监听
	if err := s.listener.Start(ctx); err != nil {
		log.Main().Warn().Err(err).Msg("池发现: EventListener 启动失败")
		// 不阻塞，继续运行（可能 WebSocket 不可用）
	} else {
		log.Info("池发现: EventListener 已启动，监听 %d 个工厂", len(s.config.Factories))
	}

	// 消费新池事件
	go s.consumeEvents(ctx)

	// 首次 backfill：从 DeFiLlama 获取热门代币
	go s.backfillTokens(ctx)

	log.Info("✅ 池发现服务已启动")
	return nil
}

// Stop 停止
func (s *DiscoveryService) Stop() {
	s.runningMu.Lock()
	defer s.runningMu.Unlock()

	if !s.running {
		return
	}
	s.running = false
	if s.cancelFn != nil {
		s.cancelFn()
	}
	s.listener.Stop()
	log.Info("池发现服务已停止")
}

// consumeEvents 消费工厂事件并添加到 FastCollector
func (s *DiscoveryService) consumeEvents(ctx context.Context) {
	eventCh := s.listener.GetEventChan()

	for {
		select {
		case <-ctx.Done():
			return
		case event, ok := <-eventCh:
			if !ok {
				return
			}
			s.handleNewPair(event)
		}
	}
}

// handleNewPair 处理新发现的交易对
func (s *DiscoveryService) handleNewPair(event *NewPairEvent) {
	if event == nil || s.fastCollector == nil {
		return
	}

	// 确定 DEX 信息
	dexName := "Unknown"
	protocol := "unknown"
	for _, f := range s.config.Factories {
		if f.Address == event.Factory {
			dexName = f.DexName
			protocol = f.Protocol
			break
		}
	}

	// 分层：两个核心代币 → Tier1，一个核心 → Tier2，其他 → Tier3
	tier := 3
	token0Core := s.config.CoreTokens[event.Token0]
	token1Core := s.config.CoreTokens[event.Token1]
	if token0Core && token1Core {
		tier = 1
	} else if token0Core || token1Core {
		tier = 2
	}

	pool := &collector.PoolTier{
		PoolAddress: event.PairAddr.Hex(),
		Token0:      event.Token0,
		Token1:      event.Token1,
		DexName:     dexName,
		Protocol:    protocol,
		Fee:         uint64(event.Fee),
		Tier:        tier,
	}

	s.fastCollector.AddPool(pool)

	s.mu.Lock()
	s.poolsFound++
	count := s.poolsFound
	s.mu.Unlock()

	log.Info("池发现: 新池 %s (%s) token0=%s token1=%s tier=%d (总发现: %d)",
		event.PairAddr.Hex()[:14], dexName,
		event.Token0.Hex()[:14], event.Token1.Hex()[:14],
		tier, count)
}

// backfillTokens 从 DeFiLlama 获取热门代币
func (s *DiscoveryService) backfillTokens(ctx context.Context) {
	// 等待 5 秒让其他组件启动
	select {
	case <-ctx.Done():
		return
	case <-time.After(5 * time.Second):
	}

	// 获取 Arbitrum 热门代币
	tokens, err := s.fetcher.FetchTokensByChain(ctx, "arbitrum")
	if err != nil {
		log.Main().Debug().Err(err).Msg("池发现: DeFiLlama 获取代币失败")
		return
	}

	log.Info("池发现: DeFiLlama 返回 %d 个热门代币", len(tokens))

	// 将高 TVL 代币加入核心代币列表
	added := 0
	for _, t := range tokens {
		if t.Address == "" || t.TVL < 100000 { // $100k 最小 TVL
			continue
		}
		addr := common.HexToAddress(t.Address)
		if !s.config.CoreTokens[addr] {
			s.config.CoreTokens[addr] = true
			added++
		}
	}

	if added > 0 {
		log.Info("池发现: 从 DeFiLlama 补充 %d 个核心代币（总 %d）", added, len(s.config.CoreTokens))
	}
}

// Stats 统计
func (s *DiscoveryService) Stats() (poolsFound int, listening bool) {
	s.mu.Lock()
	poolsFound = s.poolsFound
	s.mu.Unlock()
	return poolsFound, s.listener.IsRunning()
}

// BuildCoreTokens 从配置构建核心代币映射
func BuildCoreTokens(tokenAddresses []string) map[common.Address]bool {
	m := make(map[common.Address]bool)
	for _, addr := range tokenAddresses {
		addr = strings.TrimSpace(addr)
		if addr != "" {
			m[common.HexToAddress(addr)] = true
		}
	}
	return m
}

// BuildFactories 从 DEX 配置构建工厂列表
func BuildFactories(dexes []struct {
	Name     string
	Factory  string
	Protocol string
}) []FactoryInfo {
	var factories []FactoryInfo
	seen := make(map[string]bool)
	for _, d := range dexes {
		if d.Factory == "" || seen[d.Factory] {
			continue
		}
		seen[d.Factory] = true
		factories = append(factories, FactoryInfo{
			Address:  common.HexToAddress(d.Factory),
			DexName:  d.Name,
			Protocol: d.Protocol,
		})
	}
	return factories
}

// FormatStats 格式化统计输出
func (s *DiscoveryService) FormatStats() string {
	pools, listening := s.Stats()
	return fmt.Sprintf("pools_found=%d listening=%v", pools, listening)
}
