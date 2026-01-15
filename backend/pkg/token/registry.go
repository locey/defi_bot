// Package token 提供代币元数据管理
// 参考: Flashbots, 1inch, Uniswap SDK
package token

import (
	"fmt"
	"math/big"
	"sync"

	"github.com/ethereum/go-ethereum/common"
)

// Metadata 代币元数据（业界标准）
type Metadata struct {
	Address  common.Address `json:"address"`
	Symbol   string         `json:"symbol"`
	Decimals uint8          `json:"decimals"` // 核心：精度信息
	ChainID  uint64         `json:"chain_id"`

	// 性能优化：预计算的缩放因子
	// PriceScale = 10^decimals
	PriceScale *big.Int `json:"-"`

	// 风险控制
	IsStable   bool     `json:"is_stable"`    // 是否稳定币
	MinAmount  *big.Int `json:"-"`            // 最小交易金额（Wei单位）
	MaxAmount  *big.Int `json:"-"`            // 最大交易金额（Wei单位）
	IsVerified bool     `json:"is_verified"`  // 是否已验证链上数据
}

// Registry 代币注册表（线程安全）
type Registry struct {
	tokens   map[common.Address]*Metadata
	bySymbol map[string]*Metadata
	mu       sync.RWMutex
	chainID  uint64
}

// NewRegistry 创建代币注册表
func NewRegistry(chainID uint64) *Registry {
	return &Registry{
		tokens:   make(map[common.Address]*Metadata),
		bySymbol: make(map[string]*Metadata),
		chainID:  chainID,
	}
}

// Register 注册代币元数据
func (r *Registry) Register(metadata *Metadata) error {
	if metadata.Address == (common.Address{}) {
		return fmt.Errorf("invalid token address")
	}

	if metadata.Decimals > 77 {
		return fmt.Errorf("invalid decimals: %d (max 77)", metadata.Decimals)
	}

	// 预计算缩放因子
	metadata.PriceScale = calculatePriceScale(metadata.Decimals)
	metadata.ChainID = r.chainID

	// 设置默认最小/最大金额
	if metadata.MinAmount == nil {
		metadata.MinAmount = calculateMinAmount(metadata.Decimals)
	}
	if metadata.MaxAmount == nil {
		metadata.MaxAmount = calculateMaxAmount(metadata.Decimals)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.tokens[metadata.Address] = metadata
	if metadata.Symbol != "" {
		r.bySymbol[metadata.Symbol] = metadata
	}

	return nil
}

// Get 通过地址获取代币元数据
func (r *Registry) Get(address common.Address) (*Metadata, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	metadata, ok := r.tokens[address]
	return metadata, ok
}

// GetBySymbol 通过符号获取代币元数据
func (r *Registry) GetBySymbol(symbol string) (*Metadata, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	metadata, ok := r.bySymbol[symbol]
	return metadata, ok
}

// MustGet 获取代币元数据（不存在则panic）
func (r *Registry) MustGet(address common.Address) *Metadata {
	metadata, ok := r.Get(address)
	if !ok {
		panic(fmt.Sprintf("token not found: %s", address.Hex()))
	}
	return metadata
}

// GetAll 获取所有代币
func (r *Registry) GetAll() []*Metadata {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]*Metadata, 0, len(r.tokens))
	for _, metadata := range r.tokens {
		result = append(result, metadata)
	}
	return result
}

// Count 获取注册的代币数量
func (r *Registry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.tokens)
}

// ============================================================
// 精度相关的工具函数（业界标准）
// ============================================================

// calculatePriceScale 计算精度缩放因子
// 返回 10^decimals
func calculatePriceScale(decimals uint8) *big.Int {
	return new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil)
}

// calculateMinAmount 计算最小交易金额
// 返回 0.01 token (Wei单位)
func calculateMinAmount(decimals uint8) *big.Int {
	if decimals >= 2 {
		// 0.01 token = 10^(decimals-2)
		return new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals-2)), nil)
	}
	return big.NewInt(1)
}

// calculateMaxAmount 计算默认最大交易金额
// 返回 1,000,000 token (Wei单位)
func calculateMaxAmount(decimals uint8) *big.Int {
	// 1,000,000 token = 10^6 * 10^decimals
	base := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil)
	return new(big.Int).Mul(base, big.NewInt(1000000))
}

// CalculatePrecision 计算二分搜索精度
// 返回 0.01 token (Wei单位) - 用于金额优化
func CalculatePrecision(decimals uint8) *big.Int {
	return calculateMinAmount(decimals)
}

// CalculateTestAmount 计算测试金额
// 返回 1.0 token (Wei单位) - 用于路径模拟
func CalculateTestAmount(decimals uint8) *big.Int {
	return calculatePriceScale(decimals)
}

// ============================================================
// 格式化工具（用于日志和显示）
// ============================================================

// FormatAmount 格式化金额（Wei -> 人类可读）
func (m *Metadata) FormatAmount(weiAmount *big.Int) string {
	if weiAmount == nil {
		return "0"
	}

	divisor := m.PriceScale
	whole := new(big.Int).Div(weiAmount, divisor)
	remainder := new(big.Int).Mod(weiAmount, divisor)

	if remainder.Sign() == 0 {
		return whole.String()
	}

	// 格式化小数部分
	fractionalStr := remainder.String()
	// 补齐前导零
	for len(fractionalStr) < int(m.Decimals) {
		fractionalStr = "0" + fractionalStr
	}
	// 移除尾部零
	fractionalStr = trimTrailingZeros(fractionalStr)

	if fractionalStr == "" {
		return whole.String()
	}

	return fmt.Sprintf("%s.%s", whole.String(), fractionalStr)
}

// ParseAmount 解析金额（人类可读 -> Wei）
func (m *Metadata) ParseAmount(humanAmount string) (*big.Int, error) {
	// 简化实现，实际应该处理小数点
	amount := new(big.Int)
	_, ok := amount.SetString(humanAmount, 10)
	if !ok {
		return nil, fmt.Errorf("invalid amount: %s", humanAmount)
	}
	return new(big.Int).Mul(amount, m.PriceScale), nil
}

// trimTrailingZeros 移除尾部零
func trimTrailingZeros(s string) string {
	for len(s) > 0 && s[len(s)-1] == '0' {
		s = s[:len(s)-1]
	}
	return s
}

// ============================================================
// 批量注册辅助函数
// ============================================================

// RegisterBatch 批量注册代币
func (r *Registry) RegisterBatch(metadatas []*Metadata) error {
	for _, metadata := range metadatas {
		if err := r.Register(metadata); err != nil {
			return fmt.Errorf("failed to register %s: %w", metadata.Symbol, err)
		}
	}
	return nil
}

// LoadFromConfig 从配置加载代币
type TokenConfig struct {
	Symbol   string `mapstructure:"symbol"`
	Address  string `mapstructure:"address"`
	Decimals uint8  `mapstructure:"decimals"`
}

func (r *Registry) LoadFromConfig(configs []TokenConfig) error {
	for _, cfg := range configs {
		metadata := &Metadata{
			Address:  common.HexToAddress(cfg.Address),
			Symbol:   cfg.Symbol,
			Decimals: cfg.Decimals,
		}
		if err := r.Register(metadata); err != nil {
			return err
		}
	}
	return nil
}
