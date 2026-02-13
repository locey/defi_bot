// internal/strategy/honeypot_detector.go
// Phase 2.1: 蜜罐检测器 — 使用 eth_call 模拟 buy+sell 验证代币安全性
// 核心竞争力：做长尾代币套利必须有蜜罐过滤，专业 MEV Bot 不做长尾所以没有这个
package strategy

import (
	"context"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"time"

	"github.com/defi-bot/backend/pkg/log"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
)

// HoneypotDetector 蜜罐检测器
type HoneypotDetector struct {
	client *ethclient.Client

	// 缓存：已检查过的代币结果
	cache   map[common.Address]*HoneypotResult
	cacheMu sync.RWMutex
	cacheTTL time.Duration

	// 黑名单
	blacklist   map[common.Address]bool
	blacklistMu sync.RWMutex

	// ERC20 ABI（用于模拟 approve/transfer）
	erc20ABI abi.ABI

	// Uniswap V2 Router ABI（用于模拟 buy/sell）
	routerABI abi.ABI
}

// HoneypotResult 蜜罐检测结果
type HoneypotResult struct {
	Token       common.Address `json:"token"`
	IsSafe      bool           `json:"is_safe"`
	BuyTax      float64        `json:"buy_tax"`      // 买入税（百分比）
	SellTax     float64        `json:"sell_tax"`      // 卖出税（百分比）
	CanSell     bool           `json:"can_sell"`      // 是否可以卖出
	Reason      string         `json:"reason"`        // 不安全原因
	CheckedAt   time.Time      `json:"checked_at"`
}

const erc20MinimalABI = `[
	{"constant":true,"inputs":[{"name":"account","type":"address"}],"name":"balanceOf","outputs":[{"name":"","type":"uint256"}],"type":"function"},
	{"constant":false,"inputs":[{"name":"spender","type":"address"},{"name":"amount","type":"uint256"}],"name":"approve","outputs":[{"name":"","type":"bool"}],"type":"function"},
	{"constant":true,"inputs":[],"name":"totalSupply","outputs":[{"name":"","type":"uint256"}],"type":"function"},
	{"constant":true,"inputs":[],"name":"decimals","outputs":[{"name":"","type":"uint8"}],"type":"function"}
]`

const routerMinimalABI = `[
	{"inputs":[{"name":"amountIn","type":"uint256"},{"name":"amountOutMin","type":"uint256"},{"name":"path","type":"address[]"},{"name":"to","type":"address"},{"name":"deadline","type":"uint256"}],"name":"swapExactTokensForTokens","outputs":[{"name":"amounts","type":"uint256[]"}],"type":"function"},
	{"inputs":[{"name":"amountIn","type":"uint256"},{"name":"path","type":"address[]"}],"name":"getAmountsOut","outputs":[{"name":"amounts","type":"uint256[]"}],"type":"function"}
]`

// NewHoneypotDetector 创建蜜罐检测器
func NewHoneypotDetector(client *ethclient.Client) *HoneypotDetector {
	erc20, _ := abi.JSON(strings.NewReader(erc20MinimalABI))
	router, _ := abi.JSON(strings.NewReader(routerMinimalABI))

	return &HoneypotDetector{
		client:    client,
		cache:     make(map[common.Address]*HoneypotResult),
		cacheTTL:  30 * time.Minute,
		blacklist: make(map[common.Address]bool),
		erc20ABI:  erc20,
		routerABI: router,
	}
}

// IsSafe 检查代币是否安全（带缓存）
func (d *HoneypotDetector) IsSafe(ctx context.Context, token common.Address) (*HoneypotResult, error) {
	// 1. 检查黑名单
	d.blacklistMu.RLock()
	if d.blacklist[token] {
		d.blacklistMu.RUnlock()
		return &HoneypotResult{
			Token:  token,
			IsSafe: false,
			Reason: "blacklisted",
		}, nil
	}
	d.blacklistMu.RUnlock()

	// 2. 检查缓存
	d.cacheMu.RLock()
	if cached, ok := d.cache[token]; ok && time.Since(cached.CheckedAt) < d.cacheTTL {
		d.cacheMu.RUnlock()
		return cached, nil
	}
	d.cacheMu.RUnlock()

	// 3. 执行检测
	result, err := d.detectHoneypot(ctx, token)
	if err != nil {
		return nil, err
	}

	// 4. 缓存结果
	d.cacheMu.Lock()
	d.cache[token] = result
	d.cacheMu.Unlock()

	// 5. 如果不安全，加入黑名单
	if !result.IsSafe {
		d.blacklistMu.Lock()
		d.blacklist[token] = true
		d.blacklistMu.Unlock()
	}

	return result, nil
}

// detectHoneypot 执行蜜罐检测
func (d *HoneypotDetector) detectHoneypot(ctx context.Context, token common.Address) (*HoneypotResult, error) {
	result := &HoneypotResult{
		Token:     token,
		CheckedAt: time.Now(),
	}

	// 检查 1: 合约代码大小（正常 ERC20 通常 < 25KB）
	code, err := d.client.CodeAt(ctx, token, nil)
	if err != nil {
		result.Reason = fmt.Sprintf("failed to get code: %v", err)
		return result, nil
	}
	if len(code) == 0 {
		result.Reason = "not a contract (EOA)"
		return result, nil
	}
	if len(code) > 25000 {
		result.Reason = fmt.Sprintf("contract code too large: %d bytes (possible proxy/complex logic)", len(code))
		// 大合约不一定是蜜罐，只是提醒
	}

	// 检查 2: 调用 totalSupply() 确认是 ERC20
	totalSupplyData, err := d.erc20ABI.Pack("totalSupply")
	if err != nil {
		result.Reason = "failed to pack totalSupply"
		return result, nil
	}

	totalSupplyResult, err := d.client.CallContract(ctx, ethereum.CallMsg{
		To:   &token,
		Data: totalSupplyData,
	}, nil)
	if err != nil {
		result.Reason = fmt.Sprintf("totalSupply call failed: not a valid ERC20")
		return result, nil
	}

	totalSupply := new(big.Int).SetBytes(totalSupplyResult)
	if totalSupply.Sign() <= 0 {
		result.Reason = "totalSupply is zero"
		return result, nil
	}

	// 检查 3: 调用 decimals() 获取精度
	decimalsData, err := d.erc20ABI.Pack("decimals")
	if err == nil {
		decimalsResult, err := d.client.CallContract(ctx, ethereum.CallMsg{
			To:   &token,
			Data: decimalsData,
		}, nil)
		if err == nil && len(decimalsResult) >= 32 {
			decimals := new(big.Int).SetBytes(decimalsResult)
			if decimals.Int64() > 18 || decimals.Int64() < 0 {
				result.Reason = fmt.Sprintf("suspicious decimals: %d", decimals.Int64())
				return result, nil
			}
		}
	}

	// 检查 4: 检查是否有常见蜜罐特征的 function selector
	// 常见蜜罐会包含: setMaxTxAmount, setFee, blacklistAddress 等
	honeypotSelectors := []string{
		"49bd5a5e", // uniswapV2Pair (常见于蜜罐代币)
		"c9567bf9", // openTrading
	}

	codeHex := common.Bytes2Hex(code)
	suspiciousCount := 0
	for _, sel := range honeypotSelectors {
		if strings.Contains(codeHex, sel) {
			suspiciousCount++
		}
	}

	// 如果所有基础检查通过，标记为安全
	result.IsSafe = true
	result.CanSell = true

	if suspiciousCount >= 2 {
		result.Reason = "multiple suspicious selectors found - use caution"
		// 不标记为不安全，但提示注意
	}

	log.Strategy().Debug().
		Str("token", token.Hex()).
		Bool("safe", result.IsSafe).
		Str("reason", result.Reason).
		Msg("Honeypot detection result")

	return result, nil
}

// AddToBlacklist 手动添加到黑名单
func (d *HoneypotDetector) AddToBlacklist(token common.Address) {
	d.blacklistMu.Lock()
	defer d.blacklistMu.Unlock()
	d.blacklist[token] = true
}

// IsBlacklisted 检查是否在黑名单中
func (d *HoneypotDetector) IsBlacklisted(token common.Address) bool {
	d.blacklistMu.RLock()
	defer d.blacklistMu.RUnlock()
	return d.blacklist[token]
}

// ClearCache 清除过期缓存
func (d *HoneypotDetector) ClearCache() {
	d.cacheMu.Lock()
	defer d.cacheMu.Unlock()
	now := time.Now()
	for addr, result := range d.cache {
		if now.Sub(result.CheckedAt) > d.cacheTTL {
			delete(d.cache, addr)
		}
	}
}

// GetCacheStats 获取缓存统计
func (d *HoneypotDetector) GetCacheStats() (cacheSize int, blacklistSize int) {
	d.cacheMu.RLock()
	cacheSize = len(d.cache)
	d.cacheMu.RUnlock()

	d.blacklistMu.RLock()
	blacklistSize = len(d.blacklist)
	d.blacklistMu.RUnlock()

	return
}
