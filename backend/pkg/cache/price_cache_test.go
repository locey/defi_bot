package cache

import (
	"math/big"
	"sync"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPriceCache_UpdateAndGet(t *testing.T) {
	pc := NewPriceCache(0.01, 100)
	reserve0 := big.NewInt(1e18)
	reserve1 := big.NewInt(2e18)

	pc.Update("pool1", reserve0, reserve1, 100)

	got, ok := pc.Get("pool1")
	require.True(t, ok)
	assert.Equal(t, "pool1", got.PoolAddress)
	assert.InDelta(t, 2.0, got.Price, 0.001) // reserve1/reserve0 = 2
}

func TestPriceCache_Get_NotFound(t *testing.T) {
	pc := NewPriceCache(0.01, 100)
	_, ok := pc.Get("nonexistent")
	assert.False(t, ok)
}

func TestPriceCache_PriceCalculation(t *testing.T) {
	pc := NewPriceCache(0.01, 100)
	// 1000 USDC (6 dec) / 1 WETH (18 dec) → raw price = 1000e6 / 1e18 = 1e-9
	reserve0 := big.NewInt(1e18)        // 1 WETH
	reserve1 := big.NewInt(1000_000000) // 1000 USDC

	pc.Update("pool1", reserve0, reserve1, 100)

	got, ok := pc.Get("pool1")
	require.True(t, ok)
	assert.InDelta(t, 1e-9, got.Price, 1e-12, "raw price without decimals adjustment")
}

func TestPriceCache_DecimalsAdjustment(t *testing.T) {
	pc := NewPriceCache(0.01, 100)
	// First set metadata with decimals
	token0 := common.HexToAddress("0x0000000000000000000000000000000000000001")
	token1 := common.HexToAddress("0x0000000000000000000000000000000000000002")

	pc.UpdateWithMetadata(&PoolPrice{
		PoolAddress: "pool1",
		Token0:      token0,
		Token1:      token1,
		Decimals0:   18,
		Decimals1:   6,
		Reserve0:    big.NewInt(1e18),
		Reserve1:    big.NewInt(2500_000000),
	})

	// Now update with reserves — decimals should carry over
	pc.Update("pool1", big.NewInt(1e18), big.NewInt(2500_000000), 200)

	got, ok := pc.Get("pool1")
	require.True(t, ok)
	// raw = 2500e6 / 1e18 = 2.5e-9; adjusted = 2.5e-9 * 10^(18-6) = 2500
	assert.InDelta(t, 2500.0, got.Price, 1.0)
}

func TestPriceCache_EventTriggered_AboveThreshold(t *testing.T) {
	pc := NewPriceCache(0.01, 100) // 1% threshold
	ch := pc.Subscribe()

	// First update (triggers "first update" event)
	pc.Update("pool1", big.NewInt(1e18), big.NewInt(1e18), 100)
	select {
	case evt := <-ch:
		assert.Equal(t, "pool1", evt.PoolAddress)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected first update event")
	}

	// Second update with >1% price change
	pc.Update("pool1", big.NewInt(1e18), big.NewInt(1.02e18), 101) // 2% increase
	select {
	case evt := <-ch:
		assert.True(t, evt.ChangeRate > 0.01)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected price change event above threshold")
	}
}

func TestPriceCache_EventNotTriggered_BelowThreshold(t *testing.T) {
	pc := NewPriceCache(0.01, 100) // 1% threshold
	ch := pc.Subscribe()

	pc.Update("pool1", big.NewInt(1e18), big.NewInt(1e18), 100)
	<-ch // consume first update event

	// Tiny change (0.01%) — below threshold
	pc.Update("pool1", big.NewInt(1e18), big.NewInt(1.0001e18), 101)
	select {
	case <-ch:
		t.Fatal("should not trigger event for change below threshold")
	case <-time.After(50 * time.Millisecond):
		// expected: no event
	}
}

func TestPriceCache_FirstUpdateTriggersEvent(t *testing.T) {
	pc := NewPriceCache(0.01, 100)
	ch := pc.Subscribe()

	pc.Update("pool1", big.NewInt(1e18), big.NewInt(1e18), 100)
	select {
	case evt := <-ch:
		assert.Equal(t, float64(0), evt.OldPrice)
		assert.True(t, evt.NewPrice > 0)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("first update should trigger event")
	}
}

func TestPriceCache_UpdateV3(t *testing.T) {
	pc := NewPriceCache(0.01, 100)
	// sqrtPriceX96 = Q96 → price = 1.0
	sqrtPriceX96 := new(big.Int).Set(Q96)
	liquidity := big.NewInt(1e18)

	// Pre-set metadata
	token0 := common.HexToAddress("0x0000000000000000000000000000000000000001")
	token1 := common.HexToAddress("0x0000000000000000000000000000000000000002")
	pc.UpdateWithMetadata(&PoolPrice{
		PoolAddress: "v3pool",
		Token0:      token0,
		Token1:      token1,
		IsV3:        true,
	})

	pc.UpdateV3("v3pool", sqrtPriceX96, liquidity, 100)

	got, ok := pc.Get("v3pool")
	require.True(t, ok)
	assert.True(t, got.IsV3)
	assert.NotNil(t, got.SqrtPriceX96)
	assert.True(t, got.Price > 0)
}

func TestPriceCache_GetByTokenPair(t *testing.T) {
	pc := NewPriceCache(0.01, 100)
	token0 := common.HexToAddress("0xAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA")
	token1 := common.HexToAddress("0xBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB")

	pc.UpdateWithMetadata(&PoolPrice{
		PoolAddress: "pool_sushi",
		Token0:      token0,
		Token1:      token1,
		DexName:     "SushiSwap",
		Reserve0:    big.NewInt(1e18),
		Reserve1:    big.NewInt(2e18),
	})
	pc.UpdateWithMetadata(&PoolPrice{
		PoolAddress: "pool_uni",
		Token0:      token0,
		Token1:      token1,
		DexName:     "Uniswap",
		Reserve0:    big.NewInt(1e18),
		Reserve1:    big.NewInt(2.1e18),
	})

	pairs := pc.GetByTokenPair(token0.Hex(), token1.Hex())
	assert.Len(t, pairs, 2, "should return both pools for this token pair")
}

func TestPriceCache_MultipleSubscribers(t *testing.T) {
	pc := NewPriceCache(0.01, 100)
	ch1 := pc.Subscribe()
	ch2 := pc.Subscribe()

	pc.Update("pool1", big.NewInt(1e18), big.NewInt(1e18), 100)

	// Both subscribers should receive the event
	select {
	case <-ch1:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("subscriber 1 should receive event")
	}
	select {
	case <-ch2:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("subscriber 2 should receive event")
	}
}

func TestPriceCache_FullChannelNoPanic(t *testing.T) {
	pc := NewPriceCache(0.0001, 1) // buffer size = 1
	_ = pc.Subscribe()

	// Fill the channel and then send more — should not panic
	assert.NotPanics(t, func() {
		for i := 0; i < 10; i++ {
			r := big.NewInt(int64(1e18 + int64(i)*1e16))
			pc.Update("pool1", big.NewInt(1e18), r, uint64(100+i))
		}
	})
}

func TestPriceCache_Stats(t *testing.T) {
	pc := NewPriceCache(0.01, 100)
	pc.Update("pool1", big.NewInt(1e18), big.NewInt(1e18), 100)
	pc.Update("pool2", big.NewInt(1e18), big.NewInt(2e18), 101)

	updateCount, _, poolCount, lastUpdate := pc.Stats()
	assert.Equal(t, uint64(2), updateCount)
	assert.Equal(t, 2, poolCount)
	assert.False(t, lastUpdate.IsZero())
}

func TestPriceCache_ConcurrentUpdates(t *testing.T) {
	pc := NewPriceCache(0.01, 100)
	var wg sync.WaitGroup
	n := 100

	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			r := big.NewInt(int64(1e18 + int64(idx)*1e15))
			pc.Update("pool1", big.NewInt(1e18), r, uint64(idx))
		}(i)
	}

	wg.Wait()
	got, ok := pc.Get("pool1")
	assert.True(t, ok)
	assert.True(t, got.Price > 0)
}

// Q96 reference for V3 tests
var Q96 = new(big.Int).Lsh(big.NewInt(1), 96)
