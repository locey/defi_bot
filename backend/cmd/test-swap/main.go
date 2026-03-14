package main

import (
	"context"
	"encoding/hex"
	"fmt"
	"math/big"
	"os"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
)

var (
	LINK     = common.HexToAddress("0xf97f4df75117a78c1A5a0DBb814Af92458539FB4")
	USDT     = common.HexToAddress("0xFd086bC7CD5C481DCC9C85ebE478A1C0b69FCbb9")
	WETH     = common.HexToAddress("0x82aF49447D8a07e3bd95BD0d56f35241523fBab1")
	V3Router = common.HexToAddress("0xE592427A0AEce92De3Edee1F18E0157C05861564")
	QuoterV2 = common.HexToAddress("0x61fFE014bA17989E743c5F6cB21bF9697530B21e")
	chainID  = big.NewInt(42161)
)

func main() {
	pk := os.Getenv("KEEPER_PRIVATE_KEY")
	if strings.HasPrefix(pk, "0x") {
		pk = pk[2:]
	}
	privKey, _ := crypto.HexToECDSA(pk)
	keeper := crypto.PubkeyToAddress(privKey.PublicKey)

	client, _ := ethclient.Dial("https://warmhearted-wild-road.arbitrum-mainnet.quiknode.pro/1ed461039e0b8a3008160cbc130479deb34798c9/")
	defer client.Close()

	// Step 1: 查询余额
	linkBal := getBalance(client, LINK, keeper, 18)
	usdtBal := getBalance(client, USDT, keeper, 6)
	nonce, _ := client.PendingNonceAt(context.Background(), keeper)
	fmt.Printf("Before: LINK=%.4f USDT=%.4f nonce=%d\n", linkBal, usdtBal, nonce)

	// Step 2: QuoterV2 报价 — 卖 1 LINK → WETH → USDT
	testAmt := 3.0 // 卖 3 LINK → 得到 ~$27 USDT
	midOut := quoteSingle(client, LINK, WETH, testAmt, 18, 18)
	fmt.Printf("Quote hop1: 1 LINK → %.8f WETH\n", midOut)
	if midOut <= 0 {
		fmt.Println("Quote hop1 failed!")
		return
	}
	finalOut := quoteSingle(client, WETH, USDT, midOut, 18, 6)
	fmt.Printf("Quote hop2: %.8f WETH → %.4f USDT\n", midOut, finalOut)
	if finalOut <= 0 {
		fmt.Println("Quote hop2 failed!")
		return
	}

	// Step 3: 构建 exactInput 交易
	exactInputABI := `[{"inputs":[{"components":[{"name":"path","type":"bytes"},{"name":"recipient","type":"address"},{"name":"deadline","type":"uint256"},{"name":"amountIn","type":"uint256"},{"name":"amountOutMinimum","type":"uint256"}],"name":"params","type":"tuple"}],"name":"exactInput","outputs":[{"name":"amountOut","type":"uint256"}],"stateMutability":"payable","type":"function"}]`
	parsed, _ := abi.JSON(strings.NewReader(exactInputABI))

	// Path: LINK → (500) → WETH → (500) → USDT
	fee500 := []byte{0x00, 0x01, 0xF4}
	path := make([]byte, 0, 66)
	path = append(path, LINK.Bytes()...)
	path = append(path, fee500...)
	path = append(path, WETH.Bytes()...)
	path = append(path, fee500...)
	path = append(path, USDT.Bytes()...)

	amountIn := floatToWei(testAmt, 18)
	minOut := floatToWei(finalOut*0.99, 6) // 1% slippage
	deadline := new(big.Int).SetInt64(time.Now().Add(5 * time.Minute).Unix())

	type ExactInputParams struct {
		Path             []byte
		Recipient        common.Address
		Deadline         *big.Int
		AmountIn         *big.Int
		AmountOutMinimum *big.Int
	}

	callData, err := parsed.Pack("exactInput", ExactInputParams{
		Path: path, Recipient: keeper, Deadline: deadline,
		AmountIn: amountIn, AmountOutMinimum: minOut,
	})
	if err != nil {
		fmt.Printf("Pack error: %v\n", err)
		return
	}
	fmt.Printf("Calldata length: %d bytes\n", len(callData))

	// Step 4: eth_call 模拟
	simResult, err := client.CallContract(context.Background(), ethereum.CallMsg{
		From: keeper, To: &V3Router, Data: callData,
	}, nil)
	if err != nil {
		fmt.Printf("Simulation FAILED: %v\n", err)
		return
	}
	simOut := new(big.Int).SetBytes(simResult[:32])
	simOutF, _ := new(big.Float).Quo(new(big.Float).SetInt(simOut), big.NewFloat(1e6)).Float64()
	fmt.Printf("Simulation OK: output = %.4f USDT\n", simOutF)

	// Step 5: 发送真实交易
	head, _ := client.HeaderByNumber(context.Background(), nil)
	tip := big.NewInt(100000000)
	maxFee := new(big.Int).Add(new(big.Int).Mul(head.BaseFee, big.NewInt(2)), tip)
	gasLimit, err := client.EstimateGas(context.Background(), ethereum.CallMsg{
		From: keeper, To: &V3Router, Data: callData,
	})
	if err != nil {
		fmt.Printf("EstimateGas error: %v (using 400k)\n", err)
		gasLimit = 400000
	} else {
		fmt.Printf("EstimateGas: %d (using %d)\n", gasLimit, gasLimit*130/100)
		gasLimit = gasLimit * 130 / 100
	}

	tx := types.NewTx(&types.DynamicFeeTx{
		ChainID: chainID, Nonce: nonce, GasTipCap: tip, GasFeeCap: maxFee,
		Gas: gasLimit, To: &V3Router, Value: big.NewInt(0), Data: callData,
	})
	signer := types.LatestSignerForChainID(chainID)
	signedTx, _ := types.SignTx(tx, signer, privKey)

	fmt.Printf("Sending tx with nonce=%d hash=%s\n", nonce, signedTx.Hash().Hex())
	if err := client.SendTransaction(context.Background(), signedTx); err != nil {
		fmt.Printf("Send error: %v\n", err)
		return
	}

	// Step 6: 等待确认
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	for {
		receipt, err := client.TransactionReceipt(ctx, signedTx.Hash())
		if err == nil {
			fmt.Printf("Receipt: status=%d gasUsed=%d\n", receipt.Status, receipt.GasUsed)
			if receipt.Status == 0 {
				fmt.Println("TX REVERTED!")
			} else {
				fmt.Println("TX SUCCESS!")
			}
			break
		}
		select {
		case <-ctx.Done():
			fmt.Println("Timeout waiting for receipt")
			break
		case <-time.After(time.Second):
		}
	}

	// Step 7: 查询最终余额
	time.Sleep(2 * time.Second)
	linkBal2 := getBalance(client, LINK, keeper, 18)
	usdtBal2 := getBalance(client, USDT, keeper, 6)
	fmt.Printf("After: LINK=%.4f USDT=%.4f\n", linkBal2, usdtBal2)
	fmt.Printf("Delta: LINK=%+.4f USDT=%+.4f\n", linkBal2-linkBal, usdtBal2-usdtBal)
}

func getBalance(client *ethclient.Client, token, owner common.Address, dec int) float64 {
	sig, _ := hex.DecodeString("70a08231")
	data := append(sig, common.LeftPadBytes(owner.Bytes(), 32)...)
	result, err := client.CallContract(context.Background(), ethereum.CallMsg{To: &token, Data: data}, nil)
	if err != nil || len(result) < 32 {
		return 0
	}
	return weiToFloat(new(big.Int).SetBytes(result[:32]), dec)
}

func quoteSingle(client *ethclient.Client, tokenIn, tokenOut common.Address, amtIn float64, inDec, outDec int) float64 {
	abiJSON := `[{"inputs":[{"components":[{"name":"tokenIn","type":"address"},{"name":"tokenOut","type":"address"},{"name":"amountIn","type":"uint256"},{"name":"fee","type":"uint24"},{"name":"sqrtPriceLimitX96","type":"uint160"}],"name":"params","type":"tuple"}],"name":"quoteExactInputSingle","outputs":[{"name":"amountOut","type":"uint256"},{"name":"sqrtPriceX96After","type":"uint160"},{"name":"initializedTicksCrossed","type":"uint32"},{"name":"gasEstimate","type":"uint256"}],"stateMutability":"nonpayable","type":"function"}]`
	parsedABI, _ := abi.JSON(strings.NewReader(abiJSON))
	type P struct {
		TokenIn, TokenOut                common.Address
		AmountIn, Fee, SqrtPriceLimitX96 *big.Int
	}
	data, _ := parsedABI.Pack("quoteExactInputSingle", P{
		tokenIn, tokenOut, floatToWei(amtIn, inDec), big.NewInt(500), big.NewInt(0),
	})
	result, err := client.CallContract(context.Background(), ethereum.CallMsg{To: &QuoterV2, Data: data}, nil)
	if err != nil || len(result) < 32 {
		fmt.Printf("  quoteSingle error: %v\n", err)
		return 0
	}
	return weiToFloat(new(big.Int).SetBytes(result[:32]), outDec)
}

func weiToFloat(wei *big.Int, dec int) float64 {
	if wei == nil {
		return 0
	}
	f, _ := new(big.Float).Quo(
		new(big.Float).SetInt(wei),
		new(big.Float).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(dec)), nil)),
	).Float64()
	return f
}

func floatToWei(amt float64, dec int) *big.Int {
	r, _ := new(big.Float).Mul(
		new(big.Float).SetFloat64(amt),
		new(big.Float).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(dec)), nil)),
	).Int(nil)
	return r
}
