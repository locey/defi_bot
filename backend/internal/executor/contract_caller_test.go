package executor

import (
	"math/big"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildCallData_ExecuteStrategy_Selector(t *testing.T) {
	parsedABI, err := abi.JSON(strings.NewReader(ArbitrageCoreABI))
	require.NoError(t, err)

	cc := &ContractCaller{
		contractABI: parsedABI,
	}

	params := &ArbitrageParams{
		Asset:        common.HexToAddress("0x82aF49447D8a07e3bd95BD0d56f35241523fBab1"),
		TokenOut:     common.HexToAddress("0x82aF49447D8a07e3bd95BD0d56f35241523fBab1"),
		AmountIn:     big.NewInt(1e18),
		SwapPath:     []common.Address{common.HexToAddress("0xAAAA"), common.HexToAddress("0xBBBB"), common.HexToAddress("0xAAAA")},
		Dexes:        []common.Address{common.HexToAddress("0xDDD1"), common.HexToAddress("0xDDD2")},
		FeeTiers:     []uint32{3000, 0}, // 第一步 V3 0.3%, 第二步 V2
		ExpectProfit: big.NewInt(1e16),
		MinProfit:    big.NewInt(5e15),
		IsCex:        false,
	}

	callData, err := cc.buildCallData(params)
	require.NoError(t, err)
	require.True(t, len(callData) >= 4, "calldata should have at least 4 bytes for selector")

	// Verify function selector matches executeStrategy
	expectedSelector := parsedABI.Methods["executeStrategy"].ID
	assert.Equal(t, expectedSelector, callData[:4], "first 4 bytes should be executeStrategy selector")
}

func TestBuildFlashLoanCallData_Selector(t *testing.T) {
	flABI, err := abi.JSON(strings.NewReader(FlashLoanArbitrageABI))
	require.NoError(t, err)

	cc := &ContractCaller{
		flashLoanABI: flABI,
	}

	params := &FlashLoanParams{
		Platform:     0, // Aave_V2
		TokenIn:      common.HexToAddress("0x82aF49447D8a07e3bd95BD0d56f35241523fBab1"),
		AmountIn:     big.NewInt(1e18),
		SwapPath:     []common.Address{common.HexToAddress("0xAAAA"), common.HexToAddress("0xBBBB"), common.HexToAddress("0xAAAA")},
		Dexes:        []common.Address{common.HexToAddress("0xDDD1"), common.HexToAddress("0xDDD2")},
		FeeTiers:     []uint32{500, 10000}, // V3 0.05%, V3 1%
		ExpectProfit: big.NewInt(1e16),
		MinProfit:    big.NewInt(5e15),
	}

	callData, err := cc.buildFlashLoanCallData(params)
	require.NoError(t, err)
	require.True(t, len(callData) >= 4)

	expectedSelector := flABI.Methods["executeFlashLoan"].ID
	assert.Equal(t, expectedSelector, callData[:4], "first 4 bytes should be executeFlashLoan selector")
}

func TestBuildCallData_SwapPathEncoding(t *testing.T) {
	parsedABI, err := abi.JSON(strings.NewReader(ArbitrageCoreABI))
	require.NoError(t, err)

	cc := &ContractCaller{
		contractABI: parsedABI,
	}

	// 3-element swap path
	params := &ArbitrageParams{
		Asset:        common.HexToAddress("0xAAAA"),
		TokenOut:     common.HexToAddress("0xAAAA"),
		AmountIn:     big.NewInt(1e18),
		SwapPath:     []common.Address{common.HexToAddress("0xAAAA"), common.HexToAddress("0xBBBB"), common.HexToAddress("0xAAAA")},
		Dexes:        []common.Address{common.HexToAddress("0xDDD1"), common.HexToAddress("0xDDD2")},
		FeeTiers:     []uint32{0, 3000},
		ExpectProfit: big.NewInt(1e16),
		MinProfit:    big.NewInt(5e15),
		IsCex:        false,
	}

	callData, err := cc.buildCallData(params)
	require.NoError(t, err)
	// ABI-encoded tuple with dynamic arrays should be >260 bytes
	assert.True(t, len(callData) > 260, "calldata with 3 swapPath elements should be >260 bytes, got %d", len(callData))
}

func TestDebugCallData_HexFormat(t *testing.T) {
	parsedABI, err := abi.JSON(strings.NewReader(ArbitrageCoreABI))
	require.NoError(t, err)

	cc := &ContractCaller{
		contractABI: parsedABI,
	}

	params := &ArbitrageParams{
		Asset:        common.HexToAddress("0xAAAA"),
		TokenOut:     common.HexToAddress("0xAAAA"),
		AmountIn:     big.NewInt(1e18),
		SwapPath:     []common.Address{common.HexToAddress("0xAAAA"), common.HexToAddress("0xBBBB"), common.HexToAddress("0xAAAA")},
		Dexes:        []common.Address{common.HexToAddress("0xDDD1"), common.HexToAddress("0xDDD2")},
		FeeTiers:     []uint32{0, 0},
		ExpectProfit: big.NewInt(1e16),
		MinProfit:    big.NewInt(5e15),
		IsCex:        false,
	}

	hex, err := cc.DebugCallData(params)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(hex, "0x"), "debug calldata should be 0x-prefixed hex")
	assert.True(t, len(hex) > 10, "hex should not be empty")
}
