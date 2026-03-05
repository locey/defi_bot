package executor

import (
	"math/big"
	"testing"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestExecutor() *ArbitrageExecutor {
	return &ArbitrageExecutor{
		pendingTx:    make(map[string]*types.Transaction),
		totalProfit:  big.NewInt(0),
		totalGasSpent: big.NewInt(0),
		dailyGasLoss: big.NewInt(0),
		dailyGasDate: time.Now().Format("2006-01-02"),
	}
}

func TestDailyGasLossExceeded_BelowLimit(t *testing.T) {
	e := newTestExecutor()
	e.dailyGasLoss = big.NewInt(1_000_000_000_000_000) // 1e15 < 5e15 limit
	assert.False(t, e.DailyGasLossExceeded())
}

func TestDailyGasLossExceeded_AtLimit(t *testing.T) {
	e := newTestExecutor()
	e.dailyGasLoss = new(big.Int).SetUint64(5_000_000_000_000_000) // exactly 5e15
	assert.True(t, e.DailyGasLossExceeded())
}

func TestDailyGasLossExceeded_AboveLimit(t *testing.T) {
	e := newTestExecutor()
	e.dailyGasLoss = new(big.Int).SetUint64(6_000_000_000_000_000) // 6e15 > 5e15
	assert.True(t, e.DailyGasLossExceeded())
}

func TestDailyGasLossExceeded_DayReset(t *testing.T) {
	e := newTestExecutor()
	e.dailyGasLoss = new(big.Int).SetUint64(5_000_000_000_000_000)
	e.dailyGasDate = "2020-01-01" // old date → triggers reset

	assert.False(t, e.DailyGasLossExceeded(), "should reset on new day")
	assert.Equal(t, int64(0), e.dailyGasLoss.Int64(), "gas loss should be reset to 0")
}

func TestRecordGasLoss(t *testing.T) {
	e := newTestExecutor()
	e.recordGasLoss(big.NewInt(1e15))
	e.recordGasLoss(big.NewInt(2e15))
	assert.Equal(t, big.NewInt(3e15).String(), e.dailyGasLoss.String())
}

func TestRecordGasLoss_NilSafe(t *testing.T) {
	e := newTestExecutor()
	assert.NotPanics(t, func() {
		e.recordGasLoss(nil)
		e.recordGasLoss(big.NewInt(0))
		e.recordGasLoss(big.NewInt(-1))
	})
	assert.Equal(t, int64(0), e.dailyGasLoss.Int64())
}

func TestParseActualProfit_WithVaultEvent(t *testing.T) {
	e := newTestExecutor()

	// Build a VaultArbitrageExecuted event log
	vaultSig := crypto.Keccak256Hash([]byte("VaultArbitrageExecuted(address,address,uint256,uint256,uint256,uint256,uint256)"))
	vaultAddr := common.HexToAddress("0x1111111111111111111111111111111111111111")
	assetAddr := common.HexToAddress("0x2222222222222222222222222222222222222222")

	// Data: amountIn(0:32), profit(32:64), platFormFee(64:96), netProfitToVault(96:128), timestamp(128:160)
	data := make([]byte, 160)
	amountIn := big.NewInt(1e18)
	profit := big.NewInt(5e16)      // 0.05 ETH total profit
	platformFee := big.NewInt(5e15)  // 0.005 ETH fee
	netProfit := big.NewInt(45e15)   // 0.045 ETH net profit

	copy(data[0:32], common.LeftPadBytes(amountIn.Bytes(), 32))
	copy(data[32:64], common.LeftPadBytes(profit.Bytes(), 32))
	copy(data[64:96], common.LeftPadBytes(platformFee.Bytes(), 32))
	copy(data[96:128], common.LeftPadBytes(netProfit.Bytes(), 32))

	receipt := &types.Receipt{
		Logs: []*types.Log{
			{
				Topics: []common.Hash{
					vaultSig,
					common.BytesToHash(vaultAddr.Bytes()),
					common.BytesToHash(assetAddr.Bytes()),
				},
				Data: data,
			},
		},
	}

	actualProfit := e.parseActualProfit(receipt)
	require.NotNil(t, actualProfit)
	assert.Equal(t, netProfit.String(), actualProfit.String(), "should parse netProfitToVault from event data")
}

func TestParseActualProfit_NoMatchingEvent(t *testing.T) {
	e := newTestExecutor()

	receipt := &types.Receipt{
		Logs: []*types.Log{
			{
				Topics: []common.Hash{
					common.HexToHash("0xdeadbeef"), // wrong event signature
				},
				Data: make([]byte, 160),
			},
		},
	}

	actualProfit := e.parseActualProfit(receipt)
	assert.Equal(t, int64(0), actualProfit.Int64(), "no matching event should return 0")
}

func TestParseActualProfit_EmptyLogs(t *testing.T) {
	e := newTestExecutor()
	receipt := &types.Receipt{Logs: []*types.Log{}}
	actualProfit := e.parseActualProfit(receipt)
	assert.Equal(t, int64(0), actualProfit.Int64())
}
