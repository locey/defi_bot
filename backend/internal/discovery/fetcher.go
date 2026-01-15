// Package discovery 提供代币自动发现功能
package discovery

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/defi-bot/backend/pkg/log"
)

// TokenFetcherConfig 代币获取器配置
type TokenFetcherConfig struct {
	// 筛选条件
	MinTVL       float64 // 最小 TVL（美元）
	MinVolume24h float64 // 最小 24h 交易量（美元）
	MaxTokens    int     // 最大代币数量

	// API 配置
	Timeout    time.Duration
	RetryCount int
	RetryDelay time.Duration
}

// DefaultTokenFetcherConfig 默认配置
func DefaultTokenFetcherConfig() *TokenFetcherConfig {
	return &TokenFetcherConfig{
		MinTVL:       100000,  // $100k TVL
		MinVolume24h: 50000,   // $50k 日交易量
		MaxTokens:    100,
		Timeout:      30 * time.Second,
		RetryCount:   3,
		RetryDelay:   time.Second,
	}
}

// TokenInfo 代币信息
type TokenInfo struct {
	Symbol      string  `json:"symbol"`
	Name        string  `json:"name"`
	Address     string  `json:"address"`
	Decimals    int     `json:"decimals"`
	ChainID     int64   `json:"chain_id"`
	TVL         float64 `json:"tvl"`
	Volume24h   float64 `json:"volume_24h"`
	PriceUSD    float64 `json:"price_usd"`
	LogoURL     string  `json:"logo_url"`
	IsVerified  bool    `json:"is_verified"`
	LastUpdated time.Time `json:"last_updated"`
}

// TokenFetcher 代币获取器
// 从 DeFiLlama 等 API 获取代币列表
type TokenFetcher struct {
	config     *TokenFetcherConfig
	httpClient *http.Client
	cache      map[string][]*TokenInfo // chainID -> tokens
	cacheMu    sync.RWMutex
	cacheTime  map[string]time.Time
}

// NewTokenFetcher 创建代币获取器
func NewTokenFetcher(config *TokenFetcherConfig) *TokenFetcher {
	if config == nil {
		config = DefaultTokenFetcherConfig()
	}

	return &TokenFetcher{
		config: config,
		httpClient: &http.Client{
			Timeout: config.Timeout,
		},
		cache:     make(map[string][]*TokenInfo),
		cacheTime: make(map[string]time.Time),
	}
}

// DeFiLlama API 响应结构
type defiLlamaProtocolsResponse struct {
	Protocols []struct {
		Name    string  `json:"name"`
		Symbol  string  `json:"symbol"`
		TVL     float64 `json:"tvl"`
		Chain   string  `json:"chain"`
		Chains  []string `json:"chains"`
	} `json:"protocols"`
}

type defiLlamaCoinsResponse map[string]struct {
	Decimals  int     `json:"decimals"`
	Symbol    string  `json:"symbol"`
	Price     float64 `json:"price"`
	Timestamp int64   `json:"timestamp"`
	Confidence float64 `json:"confidence"`
}

// FetchTokensByChain 按链获取代币列表
func (f *TokenFetcher) FetchTokensByChain(ctx context.Context, chainName string) ([]*TokenInfo, error) {
	// 检查缓存
	f.cacheMu.RLock()
	if tokens, ok := f.cache[chainName]; ok {
		if time.Since(f.cacheTime[chainName]) < 5*time.Minute {
			f.cacheMu.RUnlock()
			return tokens, nil
		}
	}
	f.cacheMu.RUnlock()

	// 从 DeFiLlama 获取
	tokens, err := f.fetchFromDeFiLlama(ctx, chainName)
	if err != nil {
		return nil, err
	}

	// 更新缓存
	f.cacheMu.Lock()
	f.cache[chainName] = tokens
	f.cacheTime[chainName] = time.Now()
	f.cacheMu.Unlock()

	return tokens, nil
}

// fetchFromDeFiLlama 从 DeFiLlama API 获取代币
func (f *TokenFetcher) fetchFromDeFiLlama(ctx context.Context, chainName string) ([]*TokenInfo, error) {
	// DeFiLlama 链名称映射
	chainMap := map[string]string{
		"ethereum": "ethereum",
		"arbitrum": "arbitrum",
		"optimism": "optimism",
		"polygon":  "polygon",
	}

	llamaChain, ok := chainMap[strings.ToLower(chainName)]
	if !ok {
		return nil, fmt.Errorf("不支持的链: %s", chainName)
	}

	// 获取链上代币列表
	url := fmt.Sprintf("https://coins.llama.fi/prices/current/%s:", llamaChain)
	
	// 获取热门代币地址列表（从 stablecoins API）
	stablecoinsURL := fmt.Sprintf("https://stablecoins.llama.fi/stablecoins?includePrices=true")
	
	// 并行请求
	var tokens []*TokenInfo
	var mu sync.Mutex
	var wg sync.WaitGroup

	// 1. 获取稳定币
	wg.Add(1)
	go func() {
		defer wg.Done()
		stableTokens, err := f.fetchStablecoins(ctx, llamaChain)
		if err != nil {
			log.Warn("获取稳定币失败: %v", err)
			return
		}
		mu.Lock()
		tokens = append(tokens, stableTokens...)
		mu.Unlock()
	}()

	// 2. 获取 Top TVL 代币
	wg.Add(1)
	go func() {
		defer wg.Done()
		tvlTokens, err := f.fetchTopTVLTokens(ctx, llamaChain)
		if err != nil {
			log.Warn("获取 TVL 代币失败: %v", err)
			return
		}
		mu.Lock()
		tokens = append(tokens, tvlTokens...)
		mu.Unlock()
	}()

	wg.Wait()

	// 去重和排序
	tokens = f.deduplicateAndSort(tokens)

	// 限制数量
	if len(tokens) > f.config.MaxTokens {
		tokens = tokens[:f.config.MaxTokens]
	}

	log.Info("从 DeFiLlama 获取到 %d 个代币 (链: %s)", len(tokens), chainName)
	_ = url // 标记使用
	_ = stablecoinsURL

	return tokens, nil
}

// fetchStablecoins 获取稳定币列表
func (f *TokenFetcher) fetchStablecoins(ctx context.Context, chain string) ([]*TokenInfo, error) {
	url := "https://stablecoins.llama.fi/stablecoins?includePrices=true"

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := f.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API 返回状态码: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var result struct {
		PeggedAssets []struct {
			Name    string `json:"name"`
			Symbol  string `json:"symbol"`
			ChainCirculating map[string]struct {
				Current float64 `json:"current"`
			} `json:"chainCirculating"`
		} `json:"peggedAssets"`
	}

	if err := json.Unmarshal(body, &result); err != nil {
		return nil, err
	}

	var tokens []*TokenInfo
	for _, asset := range result.PeggedAssets {
		if chainData, ok := asset.ChainCirculating[chain]; ok {
			if chainData.Current >= f.config.MinTVL {
				tokens = append(tokens, &TokenInfo{
					Symbol:      asset.Symbol,
					Name:        asset.Name,
					TVL:         chainData.Current,
					IsVerified:  true,
					LastUpdated: time.Now(),
				})
			}
		}
	}

	return tokens, nil
}

// fetchTopTVLTokens 获取 TVL 排名靠前的代币
func (f *TokenFetcher) fetchTopTVLTokens(ctx context.Context, chain string) ([]*TokenInfo, error) {
	url := "https://api.llama.fi/protocols"

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := f.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API 返回状态码: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var protocols []struct {
		Name    string   `json:"name"`
		Symbol  string   `json:"symbol"`
		TVL     float64  `json:"tvl"`
		Chain   string   `json:"chain"`
		Chains  []string `json:"chains"`
	}

	if err := json.Unmarshal(body, &protocols); err != nil {
		return nil, err
	}

	var tokens []*TokenInfo
	for _, protocol := range protocols {
		// 检查是否在目标链上
		onChain := protocol.Chain == chain
		if !onChain {
			for _, c := range protocol.Chains {
				if strings.EqualFold(c, chain) {
					onChain = true
					break
				}
			}
		}

		if onChain && protocol.TVL >= f.config.MinTVL && protocol.Symbol != "" {
			tokens = append(tokens, &TokenInfo{
				Symbol:      protocol.Symbol,
				Name:        protocol.Name,
				TVL:         protocol.TVL,
				IsVerified:  true,
				LastUpdated: time.Now(),
			})
		}
	}

	return tokens, nil
}

// deduplicateAndSort 去重并按 TVL 排序
func (f *TokenFetcher) deduplicateAndSort(tokens []*TokenInfo) []*TokenInfo {
	seen := make(map[string]bool)
	var unique []*TokenInfo

	for _, t := range tokens {
		key := strings.ToLower(t.Symbol)
		if t.Address != "" {
			key = strings.ToLower(t.Address)
		}
		if !seen[key] {
			seen[key] = true
			unique = append(unique, t)
		}
	}

	// 按 TVL 降序排序
	sort.Slice(unique, func(i, j int) bool {
		return unique[i].TVL > unique[j].TVL
	})

	return unique
}

// GetCachedTokens 获取缓存的代币列表
func (f *TokenFetcher) GetCachedTokens(chainName string) []*TokenInfo {
	f.cacheMu.RLock()
	defer f.cacheMu.RUnlock()
	return f.cache[chainName]
}

// ClearCache 清除缓存
func (f *TokenFetcher) ClearCache() {
	f.cacheMu.Lock()
	defer f.cacheMu.Unlock()
	f.cache = make(map[string][]*TokenInfo)
	f.cacheTime = make(map[string]time.Time)
}

// SetConfig 更新配置
func (f *TokenFetcher) SetConfig(config *TokenFetcherConfig) {
	f.config = config
}
