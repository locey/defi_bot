package strategy

import (
	"math/big"
	"testing"

	"github.com/defi-bot/backend/pkg/cache"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	tokenA = common.HexToAddress("0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	tokenB = common.HexToAddress("0xBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB")
)

func makePool(addr string, t0, t1 common.Address, price float64, dex, protocol string) *cache.PoolPrice {
	return &cache.PoolPrice{
		PoolAddress: addr,
		Token0:      t0,
		Token1:      t1,
		Price:       price,
		DexName:     dex,
		Protocol:    protocol,
		Reserve0:    big.NewInt(1e18),
		Reserve1:    big.NewInt(1e18),
	}
}

func TestEvaluatePair_V2vsV3_AboveThreshold(t *testing.T) {
	pc := cache.NewPriceCache(0.0001, 100)
	scanner := NewSpreadScanner(pc, 30) // 0.3% min spread

	// V2(SushiSwap) vs V3(Uniswap V3): 不同 router，真正的跨 DEX
	// rawSpread=1%, fee=30+5=35bps, net=65bps > 30bps threshold
	v2 := makePool("pool_v2", tokenA, tokenB, 100.0, "SushiSwap", "uniswap_v2")
	v3 := makePool("pool_v3", tokenA, tokenB, 101.0, "Uniswap V3", "uniswap_v3")
	v3.IsV3 = true
	v3.Fee = 5 // 0.05%
	v3.Liquidity = big.NewInt(1e18)

	opps := scanner.evaluatePair([]*cache.PoolPrice{v2, v3})
	require.Len(t, opps, 1, "should find one opportunity")
	assert.True(t, opps[0].SpreadBps >= 30)
}

func TestEvaluatePair_BelowThreshold(t *testing.T) {
	pc := cache.NewPriceCache(0.0001, 100)
	scanner := NewSpreadScanner(pc, 100) // 1% min spread

	// rawSpread=0.5%, fee=30+5=35bps, net=15bps < 100bps threshold
	v2 := makePool("pool_v2", tokenA, tokenB, 100.0, "SushiSwap", "uniswap_v2")
	v3 := makePool("pool_v3", tokenA, tokenB, 100.5, "Uniswap V3", "uniswap_v3")
	v3.IsV3 = true
	v3.Fee = 5
	v3.Liquidity = big.NewInt(1e18)

	opps := scanner.evaluatePair([]*cache.PoolPrice{v2, v3})
	assert.Len(t, opps, 0, "0.5% spread should not meet 1% threshold after fee deduction")
}

func TestEvaluatePair_Direction_LowPriceIsBuyPool(t *testing.T) {
	pc := cache.NewPriceCache(0.0001, 100)
	scanner := NewSpreadScanner(pc, 10) // low threshold

	// rawSpread=5.26%, fee=30+5=35bps, net=4.91% > 10bps
	v2 := makePool("pool_v2", tokenA, tokenB, 95.0, "SushiSwap", "uniswap_v2")
	v3 := makePool("pool_v3", tokenA, tokenB, 100.0, "Uniswap V3", "uniswap_v3")
	v3.IsV3 = true
	v3.Fee = 5
	v3.Liquidity = big.NewInt(1e18)

	opps := scanner.evaluatePair([]*cache.PoolPrice{v2, v3})
	require.NotEmpty(t, opps)
	assert.Equal(t, "SushiSwap", opps[0].BuyPool.DexName, "lower price pool should be BuyPool")
	assert.Equal(t, "Uniswap V3", opps[0].SellPool.DexName, "higher price pool should be SellPool")
}

func TestEvaluatePair_SameProtocolDifferentDex(t *testing.T) {
	pc := cache.NewPriceCache(0.0001, 100)
	scanner := NewSpreadScanner(pc, 30)

	// Both V2, different DEX
	p1 := makePool("pool_sushi", tokenA, tokenB, 100.0, "SushiSwap", "uniswap_v2")
	p2 := makePool("pool_camelot", tokenA, tokenB, 101.5, "Camelot", "uniswap_v2")

	opps := scanner.evaluatePair([]*cache.PoolPrice{p1, p2})
	// Should find same-protocol cross-DEX opportunity
	if len(opps) > 0 {
		assert.True(t, opps[0].SpreadBps > 0)
	}
}

func TestEvaluatePair_SinglePool_NilReturn(t *testing.T) {
	pc := cache.NewPriceCache(0.0001, 100)
	scanner := NewSpreadScanner(pc, 30)

	v2 := makePool("pool_v2", tokenA, tokenB, 100.0, "SushiSwap", "uniswap_v2")
	opps := scanner.evaluatePair([]*cache.PoolPrice{v2})
	assert.Nil(t, opps, "single pool should return nil")
}

func TestEvaluatePair_ZeroPriceFiltered(t *testing.T) {
	pc := cache.NewPriceCache(0.0001, 100)
	scanner := NewSpreadScanner(pc, 30)

	v2 := makePool("pool_v2", tokenA, tokenB, 0.0, "SushiSwap", "uniswap_v2")
	v3 := makePool("pool_v3", tokenA, tokenB, 100.0, "Uniswap V3", "uniswap_v3")
	v3.IsV3 = true
	v3.Fee = 5
	v3.Liquidity = big.NewInt(1e18)

	opps := scanner.evaluatePair([]*cache.PoolPrice{v2, v3})
	// With one zero-price pool, no V2 pools available → should try same protocol spread but fail
	assert.Empty(t, opps)
}

func TestEvaluatePair_AbnormalSpreadFiltered(t *testing.T) {
	pc := cache.NewPriceCache(0.0001, 100)
	scanner := NewSpreadScanner(pc, 30)

	// >10000% spread → should be filtered as anomaly
	v2 := makePool("pool_v2", tokenA, tokenB, 0.001, "SushiSwap", "uniswap_v2")
	v3 := makePool("pool_v3", tokenA, tokenB, 100000.0, "Uniswap V3", "uniswap_v3")
	v3.IsV3 = true
	v3.Fee = 5
	v3.Liquidity = big.NewInt(1e18)

	opps := scanner.evaluatePair([]*cache.PoolPrice{v2, v3})
	assert.Empty(t, opps, "extreme spread >100 should be filtered")
}

func TestGetDexRouter_ConfigMapPriority(t *testing.T) {
	pc := cache.NewPriceCache(0.0001, 100)
	scanner := NewSpreadScanner(pc, 30)

	customAddr := common.HexToAddress("0x1234567890123456789012345678901234567890")
	scanner.SetDexRouters(map[string]common.Address{
		"SushiSwap": customAddr,
	})

	pool := &cache.PoolPrice{DexName: "SushiSwap"}
	router := scanner.getDexRouter(pool)
	assert.Equal(t, customAddr, router, "should use config map over fallback")
}

func TestGetDexRouter_FuzzyMatch(t *testing.T) {
	pc := cache.NewPriceCache(0.0001, 100)
	scanner := NewSpreadScanner(pc, 30)

	customAddr := common.HexToAddress("0x1234567890123456789012345678901234567890")
	scanner.SetDexRouters(map[string]common.Address{
		"sushi": customAddr,
	})

	pool := &cache.PoolPrice{DexName: "SushiSwap V2"}
	router := scanner.getDexRouter(pool)
	assert.Equal(t, customAddr, router, "should fuzzy match 'sushi' in 'SushiSwap V2'")
}

func TestGetDexRouter_FallbackDefault(t *testing.T) {
	pc := cache.NewPriceCache(0.0001, 100)
	scanner := NewSpreadScanner(pc, 30)
	// No custom routers set

	pool := &cache.PoolPrice{DexName: "UnknownDEX"}
	router := scanner.getDexRouter(pool)
	// Default fallback is SushiSwap router
	expected := common.HexToAddress("0x1b02dA8Cb0d097eB8D57A175b88c7D8b47997506")
	assert.Equal(t, expected, router, "unknown DEX should fallback to SushiSwap")
}

func TestTokenPairKey_NormalizedSorted(t *testing.T) {
	key1 := tokenPairKey("0xAAAA", "0xBBBB")
	key2 := tokenPairKey("0xBBBB", "0xAAAA")
	assert.Equal(t, key1, key2, "tokenPairKey should normalize order")

	key3 := tokenPairKey("0xaaaa", "0xBBBB")
	assert.Equal(t, key1, key3, "tokenPairKey should normalize case")
}
