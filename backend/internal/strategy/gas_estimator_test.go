package strategy

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/assert"
)

// We test the pure computation methods that don't need a web3 client.

func TestGetSwapGas_UniswapV2(t *testing.T) {
	ge := &GasEstimator{baseGasOverhead: 50000, baseGasPerSwap: 150000}
	assert.Equal(t, uint64(200000), ge.getSwapGas("uniswap_v2"))
}

func TestGetSwapGas_UniswapV3(t *testing.T) {
	ge := &GasEstimator{baseGasOverhead: 50000, baseGasPerSwap: 150000}
	assert.Equal(t, uint64(450000), ge.getSwapGas("uniswap_v3"))
}

func TestGetSwapGas_Curve(t *testing.T) {
	ge := &GasEstimator{baseGasOverhead: 50000, baseGasPerSwap: 150000}
	assert.Equal(t, uint64(350000), ge.getSwapGas("curve"))
}

func TestGetSwapGas_SushiSwap(t *testing.T) {
	ge := &GasEstimator{baseGasOverhead: 50000, baseGasPerSwap: 150000}
	assert.Equal(t, uint64(200000), ge.getSwapGas("sushiswap"))
}

func TestGetSwapGas_Unknown(t *testing.T) {
	ge := &GasEstimator{baseGasOverhead: 50000, baseGasPerSwap: 150000}
	assert.Equal(t, uint64(350000), ge.getSwapGas("unknown_dex"))
}

func TestEstimateGasUsage_TwoSwaps(t *testing.T) {
	ge := &GasEstimator{baseGasOverhead: 50000, baseGasPerSwap: 150000}
	path := []PathNode{
		{DexName: "uniswap_v2", Token: common.Address{}},
		{DexName: "uniswap_v3", Token: common.Address{}},
		{DexName: "", Token: common.Address{}}, // end node
	}

	gas := ge.estimateGasUsage(path)
	// 50000 (overhead) + 200000 (v2) + 450000 (v3) = 700000 * 1.2 = 840000
	expected := uint64((50000 + 200000 + 450000) * 120 / 100)
	assert.Equal(t, expected, gas)
}

func TestCalculateGasCost(t *testing.T) {
	ge := &GasEstimator{}
	gasEstimate := uint64(500000)
	gasPrice := big.NewInt(100_000_000) // 0.1 gwei

	cost := ge.CalculateGasCost(gasEstimate, gasPrice)
	expected := new(big.Int).Mul(big.NewInt(500000), big.NewInt(100_000_000))
	assert.Equal(t, expected.String(), cost.String())
}

func TestCalculateMinProfit(t *testing.T) {
	ge := &GasEstimator{}
	gasEstimate := uint64(500000)
	gasPrice := big.NewInt(100_000_000) // 0.1 gwei

	minProfit := ge.CalculateMinProfit(gasEstimate, gasPrice)
	gasCost := new(big.Int).Mul(big.NewInt(500000), big.NewInt(100_000_000))
	expected := new(big.Int).Mul(gasCost, big.NewInt(2))
	assert.Equal(t, expected.String(), minProfit.String(), "MinProfit should be 2 * GasCost")
}
