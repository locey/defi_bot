package cexdex

import (
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestDetector(minProfitRate float64) (*Detector, *SimpleDEXPriceProvider) {
	dexProvider := NewSimpleDEXPriceProvider()
	config := &DetectorConfig{
		MinProfitRate:   minProfitRate,
		MinProfitAmount: 0,
		MaxTradeAmount:  10000,
		MinTradeAmount:  100,
		MaxSlippage:     0.005,
		CheckInterval:   100 * time.Millisecond,
		OpportunityTTL:  5 * time.Second,
	}
	// PriceMonitor is nil; we test checkOpportunity directly
	det := NewDetector(config, nil, dexProvider)
	return det, dexProvider
}

func TestCheckOpportunity_DexToCex(t *testing.T) {
	det, dexProvider := newTestDetector(0.003) // 0.3%

	// CEX bid=2600, DEX=2500 → spread1 = (2600-2500)/2500 - 0.4% = 3.6%
	dexProvider.UpdatePrice("ETHUSDT", 2500.0)
	dexProvider.SetPool("ETHUSDT", common.HexToAddress("0x1"))
	dexProvider.SetRouter("ETHUSDT", common.HexToAddress("0x2"))

	cexPrice := &CEXPrice{
		Symbol:   "ETHUSDT",
		BidPrice: 2600.0,
		AskPrice: 2610.0,
	}

	det.checkOpportunity(cexPrice)

	// Should find dex_to_cex opportunity
	select {
	case opp := <-det.opportunityCh:
		assert.Equal(t, "dex_to_cex", opp.Direction)
		assert.True(t, opp.ProfitRate > 0.003)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected dex_to_cex opportunity")
	}
}

func TestCheckOpportunity_CexToDex(t *testing.T) {
	det, dexProvider := newTestDetector(0.003)

	// CEX ask=2400, DEX=2500 → spread2 = (2500-2400)/2400 - 0.4% = 3.77%
	dexProvider.UpdatePrice("ETHUSDT", 2500.0)
	dexProvider.SetPool("ETHUSDT", common.HexToAddress("0x1"))
	dexProvider.SetRouter("ETHUSDT", common.HexToAddress("0x2"))

	cexPrice := &CEXPrice{
		Symbol:   "ETHUSDT",
		BidPrice: 2390.0,
		AskPrice: 2400.0,
	}

	det.checkOpportunity(cexPrice)

	select {
	case opp := <-det.opportunityCh:
		assert.Equal(t, "cex_to_dex", opp.Direction)
		assert.True(t, opp.ProfitRate > 0.003)
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected cex_to_dex opportunity")
	}
}

func TestCheckOpportunity_BelowThreshold(t *testing.T) {
	det, dexProvider := newTestDetector(0.01) // 1% threshold

	// CEX bid=2510, DEX=2500 → spread = (2510-2500)/2500 - 0.4% = 0.0%
	dexProvider.UpdatePrice("ETHUSDT", 2500.0)
	dexProvider.SetPool("ETHUSDT", common.HexToAddress("0x1"))
	dexProvider.SetRouter("ETHUSDT", common.HexToAddress("0x2"))

	cexPrice := &CEXPrice{
		Symbol:   "ETHUSDT",
		BidPrice: 2510.0,
		AskPrice: 2515.0,
	}

	det.checkOpportunity(cexPrice)

	select {
	case <-det.opportunityCh:
		t.Fatal("should not find opportunity below threshold")
	case <-time.After(50 * time.Millisecond):
		// expected
	}
}

func TestCheckOpportunity_InvalidDEXPrice(t *testing.T) {
	det, dexProvider := newTestDetector(0.003)

	// DEX price = 0 → should skip
	dexProvider.UpdatePrice("ETHUSDT", 0)
	dexProvider.SetPool("ETHUSDT", common.HexToAddress("0x1"))
	dexProvider.SetRouter("ETHUSDT", common.HexToAddress("0x2"))

	cexPrice := &CEXPrice{
		Symbol:   "ETHUSDT",
		BidPrice: 2500.0,
		AskPrice: 2510.0,
	}

	det.checkOpportunity(cexPrice)

	select {
	case <-det.opportunityCh:
		t.Fatal("should not create opportunity for zero DEX price")
	case <-time.After(50 * time.Millisecond):
		// expected
	}
}

func TestCheckOpportunity_ExtremelyDifferentPrices(t *testing.T) {
	det, dexProvider := newTestDetector(0.003)

	// DEX price wildly different from CEX → should be filtered
	dexProvider.UpdatePrice("ETHUSDT", 0.001) // way too low
	dexProvider.SetPool("ETHUSDT", common.HexToAddress("0x1"))
	dexProvider.SetRouter("ETHUSDT", common.HexToAddress("0x2"))

	cexPrice := &CEXPrice{
		Symbol:   "ETHUSDT",
		BidPrice: 2500.0,
		AskPrice: 2510.0,
	}

	det.checkOpportunity(cexPrice)

	select {
	case <-det.opportunityCh:
		t.Fatal("should filter extreme price divergence")
	case <-time.After(50 * time.Millisecond):
		// expected
	}
}

func TestCalculateTradeAmount(t *testing.T) {
	det, _ := newTestDetector(0.003)

	// High profit → max trade
	amount := det.calculateTradeAmount(0.02)
	assert.Equal(t, 10000.0, amount)

	// Medium profit → half max
	amount = det.calculateTradeAmount(0.006)
	assert.Equal(t, 5000.0, amount)

	// Low profit → min trade
	amount = det.calculateTradeAmount(0.004)
	assert.Equal(t, 100.0, amount)
}

func TestCalculateConfidence(t *testing.T) {
	det, _ := newTestDetector(0.003)

	// High profit + high net profit
	c := det.calculateConfidence(0.02, 200)
	assert.True(t, c > 0.8)

	// Low profit
	c2 := det.calculateConfidence(0.004, 5)
	assert.True(t, c2 < c)
	assert.True(t, c2 >= 0.5)
}

func TestCleanupExpired(t *testing.T) {
	det, _ := newTestDetector(0.003)

	det.opportunitiesMu.Lock()
	det.opportunities["opp1"] = &CEXDEXOpportunity{
		ID:         "opp1",
		ValidUntil: time.Now().Add(-1 * time.Second), // expired
	}
	det.opportunities["opp2"] = &CEXDEXOpportunity{
		ID:         "opp2",
		ValidUntil: time.Now().Add(10 * time.Second), // still valid
	}
	det.opportunitiesMu.Unlock()

	det.cleanupExpired()

	opps := det.GetOpportunities()
	require.Len(t, opps, 1)
	assert.Equal(t, "opp2", opps[0].ID)
}

func TestConvertToBigInt(t *testing.T) {
	result := ConvertToBigInt(1.5, 18)
	// 1.5 * 10^18 = 1500000000000000000
	expected := "1500000000000000000"
	assert.Equal(t, expected, result.String())

	result6 := ConvertToBigInt(100.0, 6)
	assert.Equal(t, "100000000", result6.String())
}

func TestCheckOpportunity_NilDexProvider(t *testing.T) {
	config := DefaultDetectorConfig()
	det := NewDetector(config, nil, nil) // nil dexProvider

	cexPrice := &CEXPrice{
		Symbol:   "ETHUSDT",
		BidPrice: 2500.0,
		AskPrice: 2510.0,
	}

	// Should not panic
	assert.NotPanics(t, func() {
		det.checkOpportunity(cexPrice)
	})
}
