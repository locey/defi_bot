package main

import (
	"context"
	"fmt"
	"math/big"
	"strings"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
)

func main() {
	client, _ := ethclient.Dial("https://warmhearted-wild-road.arbitrum-mainnet.quiknode.pro/1ed461039e0b8a3008160cbc130479deb34798c9/")
	defer client.Close()

	LDO := common.HexToAddress("0x13Ad51ed4F1B7e9Dc168d8a00cB3f4dDD85EfA60")
	WETH := common.HexToAddress("0x82aF49447D8a07e3bd95BD0d56f35241523fBab1")
	USDT := common.HexToAddress("0xFd086bC7CD5C481DCC9C85ebE478A1C0b69FCbb9")
	QuoterV2 := common.HexToAddress("0x61fFE014bA17989E743c5F6cB21bF9697530B21e")

	abiJSON := `[{"inputs":[{"components":[{"name":"tokenIn","type":"address"},{"name":"tokenOut","type":"address"},{"name":"amountIn","type":"uint256"},{"name":"fee","type":"uint24"},{"name":"sqrtPriceLimitX96","type":"uint160"}],"name":"params","type":"tuple"}],"name":"quoteExactInputSingle","outputs":[{"name":"amountOut","type":"uint256"},{"name":"sqrtPriceX96After","type":"uint160"},{"name":"initializedTicksCrossed","type":"uint32"},{"name":"gasEstimate","type":"uint256"}],"stateMutability":"nonpayable","type":"function"}]`
	parsedABI, _ := abi.JSON(strings.NewReader(abiJSON))

	type P struct {
		TokenIn, TokenOut                common.Address
		AmountIn, Fee, SqrtPriceLimitX96 *big.Int
	}

	// Test LDO→WETH with different fee tiers and amounts
	for _, ldoAmt := range []int64{1, 10, 54} {
		amt := new(big.Int).Mul(big.NewInt(ldoAmt), new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil))
		for _, fee := range []int64{500, 3000, 10000} {
			data, _ := parsedABI.Pack("quoteExactInputSingle", P{LDO, WETH, amt, big.NewInt(fee), big.NewInt(0)})
			result, err := client.CallContract(context.Background(), ethereum.CallMsg{To: &QuoterV2, Data: data}, nil)
			if err != nil {
				fmt.Printf("LDO(%d)→WETH fee=%d: ERROR\n", ldoAmt, fee)
			} else if len(result) >= 32 {
				out := new(big.Int).SetBytes(result[:32])
				f, _ := new(big.Float).Quo(new(big.Float).SetInt(out), big.NewFloat(1e18)).Float64()
				fmt.Printf("LDO(%d)→WETH fee=%d: %.8f WETH ($%.2f)\n", ldoAmt, fee, f, f*2120)
			}
		}
	}

	// Also try LDO→USDT direct
	fmt.Println("\n--- Direct LDO→USDT ---")
	for _, fee := range []int64{500, 3000, 10000} {
		amt := new(big.Int).Mul(big.NewInt(10), new(big.Int).Exp(big.NewInt(10), big.NewInt(18), nil))
		data, _ := parsedABI.Pack("quoteExactInputSingle", P{LDO, USDT, amt, big.NewInt(fee), big.NewInt(0)})
		result, err := client.CallContract(context.Background(), ethereum.CallMsg{To: &QuoterV2, Data: data}, nil)
		if err != nil {
			fmt.Printf("LDO(10)→USDT fee=%d: ERROR\n", fee)
		} else if len(result) >= 32 {
			out := new(big.Int).SetBytes(result[:32])
			f, _ := new(big.Float).Quo(new(big.Float).SetInt(out), big.NewFloat(1e6)).Float64()
			fmt.Printf("LDO(10)→USDT fee=%d: $%.4f\n", fee, f)
		}
	}
}
