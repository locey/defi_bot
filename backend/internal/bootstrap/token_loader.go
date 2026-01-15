// Package bootstrap 提供应用启动时的初始化功能
package bootstrap

import (
	"context"
	"fmt"

	"github.com/defi-bot/backend/internal/config"
	"github.com/defi-bot/backend/pkg/log"
	"github.com/defi-bot/backend/pkg/token"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
)

// TokenLoader 代币加载器
type TokenLoader struct {
	config   *config.Config
	registry *token.Registry
	client   *ethclient.Client
}

// NewTokenLoader 创建代币加载器
func NewTokenLoader(cfg *config.Config, client *ethclient.Client) *TokenLoader {
	chainID := uint64(cfg.Blockchain.ChainID)
	registry := token.NewRegistry(chainID)

	return &TokenLoader{
		config:   cfg,
		registry: registry,
		client:   client,
	}
}

// LoadAndVerify 加载并验证代币配置
// 这是业界标准做法：启动时验证配置与链上一致
func (tl *TokenLoader) LoadAndVerify(ctx context.Context) (*token.Registry, error) {
	log.Main().Info().Msg("=== 初始化代币注册表 ===")

	// 1. 从配置加载代币
	if err := tl.loadFromConfig(); err != nil {
		return nil, fmt.Errorf("加载配置失败: %w", err)
	}

	log.Main().Info().Int("count", tl.registry.Count()).Msg("✅ 从配置加载代币")

	// 2. 验证链上精度（可选，生产环境建议启用）
	if tl.config.Blockchain.VerifyTokenDecimals {
		if err := tl.verifyOnChain(ctx); err != nil {
			return nil, fmt.Errorf("链上验证失败: %w", err)
		}
	} else {
		log.Main().Warn().Msg("⚠️  跳过链上验证（配置已禁用）")
	}

	// 3. 打印统计信息
	tl.printStats()

	log.Main().Info().Msg("=== 代币注册表初始化完成 ===")
	return tl.registry, nil
}

// loadFromConfig 从配置加载代币
func (tl *TokenLoader) loadFromConfig() error {
	tokens := tl.config.Tokens

	for _, tokenCfg := range tokens {
		metadata := &token.Metadata{
			Address:  common.HexToAddress(tokenCfg.Address),
			Symbol:   tokenCfg.Symbol,
			Decimals: uint8(tokenCfg.Decimals),
		}

		// 判断是否为稳定币
		metadata.IsStable = isStablecoin(tokenCfg.Symbol)

		if err := tl.registry.Register(metadata); err != nil {
			return fmt.Errorf("注册代币 %s 失败: %w", tokenCfg.Symbol, err)
		}
	}

	return nil
}

// verifyOnChain 验证链上精度
func (tl *TokenLoader) verifyOnChain(ctx context.Context) error {
	log.Main().Info().Msg("🔍 开始验证链上精度...")

	validator := token.NewValidator(tl.client, tl.registry)

	// 快速验证关键代币
	criticalTokens := []string{"WETH", "USDT", "USDC", "DAI"}
	if err := validator.QuickVerify(ctx, criticalTokens); err != nil {
		return fmt.Errorf("关键代币验证失败: %w", err)
	}

	// 全量验证（可选）
	// if err := validator.VerifyAll(ctx); err != nil {
	//     return fmt.Errorf("全量验证失败: %w", err)
	// }

	return nil
}

// printStats 打印统计信息
func (tl *TokenLoader) printStats() {
	tokens := tl.registry.GetAll()

	var stableCount, verifiedCount int
	decimalsMap := make(map[uint8]int)

	for _, t := range tokens {
		if t.IsStable {
			stableCount++
		}
		if t.IsVerified {
			verifiedCount++
		}
		decimalsMap[t.Decimals]++
	}

	log.Main().Info().Msg("📊 代币统计:")
	log.Main().Info().Int("total", len(tokens)).Msg("   总数")
	log.Main().Info().Int("stable", stableCount).Msg("   稳定币")
	log.Main().Info().Int("verified", verifiedCount).Msg("   已验证")
	log.Main().Info().Msg("   精度分布:")
	for decimals, count := range decimalsMap {
		log.Main().Info().Int("decimals", int(decimals)).Int("count", count).Msg("     代币精度")
	}
}

// isStablecoin 判断是否为稳定币
func isStablecoin(symbol string) bool {
	stablecoins := map[string]bool{
		"USDT": true,
		"USDC": true,
		"DAI":  true,
		"BUSD": true,
		"TUSD": true,
		"USDD": true,
		"FRAX": true,
	}
	return stablecoins[symbol]
}

// GetRegistry 获取注册表
func (tl *TokenLoader) GetRegistry() *token.Registry {
	return tl.registry
}

// LoadQuick 快速加载（不验证链上）
// 用于开发/测试环境
func (tl *TokenLoader) LoadQuick() (*token.Registry, error) {
	if err := tl.loadFromConfig(); err != nil {
		return nil, err
	}
	log.Main().Info().Int("count", tl.registry.Count()).Msg("✅ 快速加载代币（未验证链上）")
	return tl.registry, nil
}
