// Package token - 链上验证器
package token

import (
	"context"
	"fmt"
	"math/big"
	"strings"

	"github.com/defi-bot/backend/pkg/log"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/accounts/abi/bind"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
)

// Validator 代币元数据验证器
type Validator struct {
	client   *ethclient.Client
	registry *Registry
}

// NewValidator 创建验证器
func NewValidator(client *ethclient.Client, registry *Registry) *Validator {
	return &Validator{
		client:   client,
		registry: registry,
	}
}

// VerifyAll 验证所有代币的链上数据
// 这是业界标准做法：启动时验证配置与链上一致
func (v *Validator) VerifyAll(ctx context.Context) error {
	tokens := v.registry.GetAll()
	
	log.Main().Info().Int("count", len(tokens)).Msg("🔍 验证代币的链上精度...")
	
	var errors []string
	successCount := 0
	
	for _, token := range tokens {
		if err := v.VerifyToken(ctx, token); err != nil {
			errors = append(errors, fmt.Sprintf("%s: %v", token.Symbol, err))
			log.Main().Error().Str("symbol", token.Symbol).Str("address", token.Address.Hex()).Err(err).Msg("  ❌ 验证失败")
		} else {
			successCount++
			log.Main().Info().Str("symbol", token.Symbol).Uint8("decimals", token.Decimals).Msg("  ✅ 验证成功")
		}
	}
	
	log.Main().Info().Int("success", successCount).Int("total", len(tokens)).Msg("✅ 验证完成")
	
	if len(errors) > 0 {
		return fmt.Errorf("验证失败:\n%v", errors)
	}
	
	return nil
}

// VerifyToken 验证单个代币
func (v *Validator) VerifyToken(ctx context.Context, token *Metadata) error {
	// 查询链上 decimals
	decimalsOnChain, err := v.queryDecimals(ctx, token.Address)
	if err != nil {
		return fmt.Errorf("查询链上精度失败: %w", err)
	}
	
	// 比对配置与链上数据
	if decimalsOnChain != token.Decimals {
		return fmt.Errorf(
			"精度不匹配: 配置=%d, 链上=%d",
			token.Decimals,
			decimalsOnChain,
		)
	}
	
	// 标记为已验证
	token.IsVerified = true
	
	return nil
}

// queryDecimals 查询链上 decimals（兼容多种实现）
func (v *Validator) queryDecimals(ctx context.Context, tokenAddr common.Address) (uint8, error) {
	// 尝试标准 ERC20 decimals() 方法
	// 方法选择器: 0x313ce567
	
	// 简化实现：直接调用
	// 实际项目应该使用生成的 ABI binding
	parsedABI, err := abi.JSON(strings.NewReader(erc20ABI()))
	if err != nil {
		return 0, fmt.Errorf("解析 ABI 失败: %w", err)
	}
	caller := bind.NewBoundContract(tokenAddr, parsedABI, v.client, v.client, v.client)
	
	var result []interface{}
	err = caller.Call(&bind.CallOpts{Context: ctx}, &result, "decimals")
	if err != nil {
		return 0, err
	}
	
	if len(result) == 0 {
		return 0, fmt.Errorf("empty result")
	}
	
	// 处理不同的返回类型
	switch v := result[0].(type) {
	case uint8:
		return v, nil
	case *big.Int:
		return uint8(v.Uint64()), nil
	case uint64:
		return uint8(v), nil
	default:
		return 0, fmt.Errorf("unexpected decimals type: %T", v)
	}
}

// erc20ABI 返回 ERC20 的 ABI（仅 decimals 方法）
func erc20ABI() string {
	return `[{
		"constant": true,
		"inputs": [],
		"name": "decimals",
		"outputs": [{"name": "", "type": "uint8"}],
		"type": "function"
	}]`
}

// VerifyWithRetry 带重试的验证
func (v *Validator) VerifyWithRetry(ctx context.Context, token *Metadata, maxRetries int) error {
	var lastErr error
	
	for i := 0; i < maxRetries; i++ {
		if err := v.VerifyToken(ctx, token); err == nil {
			return nil
		} else {
			lastErr = err
			log.Main().Warn().Int("retry", i+1).Int("max", maxRetries).Err(err).Msg("  重试")
		}
	}
	
	return fmt.Errorf("验证失败（已重试%d次）: %w", maxRetries, lastErr)
}

// QuickVerify 快速验证（仅验证关键代币）
func (v *Validator) QuickVerify(ctx context.Context, symbols []string) error {
	log.Main().Info().Interface("symbols", symbols).Msg("🔍 快速验证关键代币")
	
	for _, symbol := range symbols {
		token, ok := v.registry.GetBySymbol(symbol)
		if !ok {
			return fmt.Errorf("代币未注册: %s", symbol)
		}
		
		if err := v.VerifyToken(ctx, token); err != nil {
			return fmt.Errorf("%s 验证失败: %w", symbol, err)
		}
		
		log.Main().Info().Str("symbol", symbol).Uint8("decimals", token.Decimals).Msg("  ✅ 快速验证")
	}
	
	return nil
}

// VerifyPair 验证交易对的两个代币
func (v *Validator) VerifyPair(ctx context.Context, token0, token1 common.Address) error {
	t0, ok := v.registry.Get(token0)
	if !ok {
		return fmt.Errorf("token0 未注册: %s", token0.Hex())
	}
	
	t1, ok := v.registry.Get(token1)
	if !ok {
		return fmt.Errorf("token1 未注册: %s", token1.Hex())
	}
	
	if err := v.VerifyToken(ctx, t0); err != nil {
		return err
	}
	
	if err := v.VerifyToken(ctx, t1); err != nil {
		return err
	}
	
	return nil
}
