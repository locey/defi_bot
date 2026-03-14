// cmd/rebalance/main.go
// 资金再平衡: 卖 Arb LINK→USDT, deposit 到 Binance, 实现 50/50
package main

import (
	"context"
	"crypto/ecdsa"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/defi-bot/backend/pkg/cex"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
)

var (
	USDT     = common.HexToAddress("0xFd086bC7CD5C481DCC9C85ebE478A1C0b69FCbb9")
	WETH     = common.HexToAddress("0x82aF49447D8a07e3bd95BD0d56f35241523fBab1")
	LINK     = common.HexToAddress("0xf97f4df75117a78c1A5a0DBb814Af92458539FB4")
	AAVE     = common.HexToAddress("0xba5DdD1f9d7F570dc94a51479a000E3BCE967196")
	V3Router = common.HexToAddress("0xE592427A0AEce92De3Edee1F18E0157C05861564")
	QuoterV2 = common.HexToAddress("0x61fFE014bA17989E743c5F6cB21bF9697530B21e")
	chainID  = big.NewInt(42161)
)

func main() {
	pk := os.Getenv("KEEPER_PRIVATE_KEY")
	if pk == "" {
		fmt.Println("ERROR: KEEPER_PRIVATE_KEY not set")
		return
	}
	if strings.HasPrefix(pk, "0x") {
		pk = pk[2:]
	}
	privKey, _ := crypto.HexToECDSA(pk)
	keeper := crypto.PubkeyToAddress(privKey.PublicKey)

	client, _ := ethclient.Dial("https://warmhearted-wild-road.arbitrum-mainnet.quiknode.pro/1ed461039e0b8a3008160cbc130479deb34798c9/")
	defer client.Close()

	trader := cex.NewBinanceTrader(&cex.BinanceConfig{
		APIEndpoint: "https://api.binance.com",
		APIKey:      os.Getenv("BINANCE_API_KEY"),
		APISecret:   os.Getenv("BINANCE_API_SECRET"),
		RateLimit:   1200,
	})

	// === 1. 当前余额 ===
	fmt.Println("=== Current Balances ===")
	arbUSDT := getBalance(client, USDT, keeper, 6)
	arbLINK := getBalance(client, LINK, keeper, 18)
	arbAAVE := getBalance(client, AAVE, keeper, 18)
	ethBal, _ := client.BalanceAt(context.Background(), keeper, nil)
	arbETH := weiToFloat(ethBal, 18)

	binUSDT, _, _ := trader.GetBalance("USDT")
	binLINK, _, _ := trader.GetBalance("LINK")
	binAAVE, _, _ := trader.GetBalance("AAVE")

	linkP := getBidPrice("LINKUSDT")
	aaveP := getBidPrice("AAVEUSDT")
	ethP := getBidPrice("ETHUSDT")

	arbTotal := arbUSDT + arbLINK*linkP + arbAAVE*aaveP + arbETH*ethP
	binTotal := binUSDT + binLINK*linkP + binAAVE*aaveP
	total := arbTotal + binTotal
	target := total / 2

	fmt.Printf("  Arb: $%.2f (%.2f USDT + %.4f LINK@$%.2f + %.6f ETH@$%.0f)\n",
		arbTotal, arbUSDT, arbLINK, linkP, arbETH, ethP)
	fmt.Printf("  Bin: $%.2f (%.2f USDT + %.4f LINK@$%.2f + %.4f AAVE@$%.0f)\n",
		binTotal, binUSDT, binLINK, linkP, binAAVE, aaveP)
	fmt.Printf("  Total: $%.2f | Target each: $%.2f\n", total, target)
	fmt.Println()

	// === 2. 计算需要卖多少 LINK ===
	// Arb 需要降到 ~target, 多余部分通过卖 LINK 变成 USDT, 然后 deposit
	// 目标: Arb 保留 ~$target (含 ETH), Bin 得到 ~$target
	// Arb 的 ETH ($49) 不动, 需要用 USDT + LINK 凑 $target - $49 = ~$60
	// 当前 Arb 可用价值 (不含 ETH) = arbUSDT + arbLINK*linkP = ~$143
	// 需要留在 Arb 的 (不含 ETH): target - arbETH*ethP = ~$60
	// 需要 deposit 到 Bin 的 USDT: arbUSDT + arbLINK*linkP - 60 = ~$83
	// 但我们需要保留一些 LINK 在 Arb 做 DEX 端的 token receive
	// 实际上 CEX-DEX 模式: Arb 只需 USDT 买 token, Bin 需要 token 卖
	// 所以 Arb 尽量留 USDT, Bin 留 USDT + token

	// Arb 目标: $target = ETH + 少量LINK + USDT
	// Arb 保留 1 LINK 做缓冲, 其余全卖成 USDT
	// 然后 deposit 多余 USDT 到 Binance
	keepLINK := 1.0 // 保留在 Arb 的 LINK
	sellLINK := arbLINK - keepLINK
	if sellLINK < 0.1 {
		sellLINK = 0
	}
	// Arb 卖完后: ETH($49) + 1 LINK($9) + USDT(原来 + 卖LINK所得)
	// 需要 deposit 的 USDT = 卖完后总USDT - (target - ETH价值 - 保留LINK价值)
	arbKeepUSDT := target - arbETH*ethP - keepLINK*linkP
	if arbKeepUSDT < 10 {
		arbKeepUSDT = 10
	}

	fmt.Printf("=== Plan ===\n")
	fmt.Printf("  Arb keep: $%.2f USDT + %.1f LINK($%.0f) + ETH($%.0f) = $%.2f\n",
		arbKeepUSDT, keepLINK, keepLINK*linkP, arbETH*ethP, arbKeepUSDT+keepLINK*linkP+arbETH*ethP)
	fmt.Printf("  Sell %.4f LINK on DEX (≈$%.2f)\n", sellLINK, sellLINK*linkP)

	// 估算卖 LINK 获得的 USDT
	expectedUSDT := quoteMultiHop(client, LINK, WETH, USDT, sellLINK, 18, 6, 500, 500)
	fmt.Printf("  DEX quote: %.4f LINK → $%.2f USDT\n", sellLINK, expectedUSDT)

	afterSellUSDT := arbUSDT + expectedUSDT
	depositAmt := afterSellUSDT - arbKeepUSDT
	if depositAmt < 10 {
		depositAmt = 0
	}
	fmt.Printf("  After sell: $%.2f USDT on Arb\n", afterSellUSDT)
	fmt.Printf("  Deposit to Binance: $%.2f\n", depositAmt)
	fmt.Printf("  Final Arb USDT: ~$%.2f | Final Bin USDT: ~$%.2f\n",
		afterSellUSDT-depositAmt, binUSDT+depositAmt)
	fmt.Println()

	if sellLINK < 0.1 {
		fmt.Println("Nothing to sell, already balanced enough")
		return
	}

	// === 3. 执行: 卖 LINK → USDT ===
	fmt.Printf("=== Step 1: Sell %.4f LINK on DEX ===\n", sellLINK)

	// 先 approve LINK to V3Router (如果需要)
	approveIfNeeded(client, privKey, keeper, LINK, V3Router, sellLINK, 18)

	txHash, err := doSwapReverse(client, privKey, keeper, LINK, WETH, USDT, sellLINK, 18, 6, 500, 500, expectedUSDT)
	if err != nil {
		fmt.Printf("  SELL FAIL: %v\n", err)
		return
	}
	fmt.Printf("  SELL OK: %s\n", txHash)

	// 等几秒让余额更新
	time.Sleep(3 * time.Second)

	newArbUSDT := getBalance(client, USDT, keeper, 6)
	fmt.Printf("  Arb USDT now: $%.2f\n", newArbUSDT)

	// === 4. Deposit USDT to Binance ===
	depositAmt = newArbUSDT - arbKeepUSDT
	if depositAmt < 10 {
		fmt.Println("Not enough to deposit (< $10)")
		// 还是打印最终余额
		goto summary
	}

	// 截断到 6 位小数
	depositAmt = float64(int(depositAmt*1e6)) / 1e6

	fmt.Printf("\n=== Step 2: Deposit $%.2f USDT → Binance ===\n", depositAmt)
	{
		txH, err := sendUSDT(client, privKey, keeper, trader, depositAmt)
		if err != nil {
			fmt.Printf("  DEPOSIT FAIL: %v\n", err)
			goto summary
		}
		fmt.Printf("  DEPOSIT OK: %s\n", txH)
		fmt.Println("  Binance will credit in ~10-15 min")
	}

summary:
	fmt.Println("\n=== Post-Rebalance Balances ===")
	time.Sleep(2 * time.Second)
	arbUSDT = getBalance(client, USDT, keeper, 6)
	arbLINK = getBalance(client, LINK, keeper, 18)
	ethBal, _ = client.BalanceAt(context.Background(), keeper, nil)
	arbETH = weiToFloat(ethBal, 18)
	binUSDT, _, _ = trader.GetBalance("USDT")
	binLINK, _, _ = trader.GetBalance("LINK")
	binAAVE, _, _ = trader.GetBalance("AAVE")

	arbTotal = arbUSDT + arbLINK*linkP + arbAAVE*aaveP + arbETH*ethP
	binTotal = binUSDT + binLINK*linkP + binAAVE*aaveP
	fmt.Printf("  Arb: $%.2f (%.2f USDT + %.4f LINK + %.6f ETH)\n", arbTotal, arbUSDT, arbLINK, arbETH)
	fmt.Printf("  Bin: $%.2f (%.2f USDT + %.4f LINK + %.4f AAVE)\n", binTotal, binUSDT, binLINK, binAAVE)
	fmt.Printf("  (Bin USDT will increase by ~$%.0f after deposit confirms)\n", depositAmt)
}

// ============ Approve ERC20 ============

func approveIfNeeded(client *ethclient.Client, privKey *ecdsa.PrivateKey, keeper common.Address, token, spender common.Address, amount float64, dec int) {
	// Check allowance
	sig, _ := hex.DecodeString("dd62ed3e") // allowance(address,address)
	data := make([]byte, 0, 68)
	data = append(data, sig...)
	data = append(data, common.LeftPadBytes(keeper.Bytes(), 32)...)
	data = append(data, common.LeftPadBytes(spender.Bytes(), 32)...)

	result, err := client.CallContract(context.Background(), ethereum.CallMsg{To: &token, Data: data}, nil)
	if err == nil && len(result) >= 32 {
		allowance := new(big.Int).SetBytes(result[:32])
		needed := floatToWei(amount, dec)
		if allowance.Cmp(needed) >= 0 {
			fmt.Println("  Allowance sufficient, skip approve")
			return
		}
	}

	// Approve max uint256
	approveSig, _ := hex.DecodeString("095ea7b3") // approve(address,uint256)
	approveData := make([]byte, 0, 68)
	approveData = append(approveData, approveSig...)
	approveData = append(approveData, common.LeftPadBytes(spender.Bytes(), 32)...)
	maxUint := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 256), big.NewInt(1))
	approveData = append(approveData, common.LeftPadBytes(maxUint.Bytes(), 32)...)

	nonce, _ := client.PendingNonceAt(context.Background(), keeper)
	head, _ := client.HeaderByNumber(context.Background(), nil)
	tip := big.NewInt(100000000)
	maxFee := new(big.Int).Add(new(big.Int).Mul(head.BaseFee, big.NewInt(2)), tip)

	tx := types.NewTx(&types.DynamicFeeTx{
		ChainID: chainID, Nonce: nonce, GasTipCap: tip, GasFeeCap: maxFee,
		Gas: 100000, To: &token, Value: big.NewInt(0), Data: approveData,
	})
	signer := types.LatestSignerForChainID(chainID)
	signedTx, _ := types.SignTx(tx, signer, privKey)
	if err := client.SendTransaction(context.Background(), signedTx); err != nil {
		fmt.Printf("  Approve FAIL: %v\n", err)
		return
	}
	fmt.Printf("  Approve TX: %s\n", signedTx.Hash().Hex())

	// Wait for receipt
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for {
		receipt, err := client.TransactionReceipt(ctx, signedTx.Hash())
		if err == nil {
			if receipt.Status == 1 {
				fmt.Println("  Approve OK")
				return
			}
			fmt.Println("  Approve REVERTED")
			return
		}
		select {
		case <-ctx.Done():
			fmt.Println("  Approve timeout")
			return
		case <-time.After(time.Second):
		}
	}
}

// ============ Swap LINK → WETH → USDT ============

func doSwapReverse(client *ethclient.Client, privKey *ecdsa.PrivateKey, keeper common.Address,
	tokenIn, mid, tokenOut common.Address, amtIn float64, inDec, outDec int,
	fee1, fee2 int64, expectedOut float64) (string, error) {

	exactInputABI := `[{"inputs":[{"components":[{"name":"path","type":"bytes"},{"name":"recipient","type":"address"},{"name":"deadline","type":"uint256"},{"name":"amountIn","type":"uint256"},{"name":"amountOutMinimum","type":"uint256"}],"name":"params","type":"tuple"}],"name":"exactInput","outputs":[{"name":"amountOut","type":"uint256"}],"stateMutability":"payable","type":"function"}]`
	parsed, _ := abi.JSON(strings.NewReader(exactInputABI))

	path := encodePath(tokenIn, fee1, mid, fee2, tokenOut)
	amountIn := floatToWei(amtIn, inDec)
	minOut := floatToWei(expectedOut*0.99, outDec) // 1% slippage
	dl := new(big.Int).SetInt64(time.Now().Add(5 * time.Minute).Unix())

	type P struct {
		Path             []byte
		Recipient        common.Address
		Deadline         *big.Int
		AmountIn         *big.Int
		AmountOutMinimum *big.Int
	}
	callData, err := parsed.Pack("exactInput", P{
		Path: path, Recipient: keeper, Deadline: dl,
		AmountIn: amountIn, AmountOutMinimum: minOut,
	})
	if err != nil {
		return "", fmt.Errorf("pack: %w", err)
	}

	// Simulate
	if _, err = client.CallContract(context.Background(), ethereum.CallMsg{
		From: keeper, To: &V3Router, Data: callData,
	}, nil); err != nil {
		return "", fmt.Errorf("sim: %w", err)
	}

	nonce, _ := client.PendingNonceAt(context.Background(), keeper)
	head, _ := client.HeaderByNumber(context.Background(), nil)
	tip := big.NewInt(100000000)
	maxFee := new(big.Int).Add(new(big.Int).Mul(head.BaseFee, big.NewInt(2)), tip)
	gasLimit, err := client.EstimateGas(context.Background(), ethereum.CallMsg{
		From: keeper, To: &V3Router, Data: callData,
	})
	if err != nil {
		gasLimit = 500000
	} else {
		gasLimit = gasLimit * 130 / 100
	}

	tx := types.NewTx(&types.DynamicFeeTx{
		ChainID: chainID, Nonce: nonce, GasTipCap: tip, GasFeeCap: maxFee,
		Gas: gasLimit, To: &V3Router, Value: big.NewInt(0), Data: callData,
	})
	signer := types.LatestSignerForChainID(chainID)
	signedTx, _ := types.SignTx(tx, signer, privKey)
	if err := client.SendTransaction(context.Background(), signedTx); err != nil {
		return "", fmt.Errorf("send: %w", err)
	}

	txHash := signedTx.Hash().Hex()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	for {
		receipt, err := client.TransactionReceipt(ctx, signedTx.Hash())
		if err == nil {
			if receipt.Status == 1 {
				return txHash, nil
			}
			return txHash, fmt.Errorf("reverted(gas=%d)", receipt.GasUsed)
		}
		select {
		case <-ctx.Done():
			return txHash, fmt.Errorf("timeout")
		case <-time.After(time.Second):
		}
	}
}

// ============ Deposit USDT to Binance ============

func sendUSDT(client *ethclient.Client, privKey *ecdsa.PrivateKey, keeper common.Address, trader *cex.BinanceTrader, amount float64) (string, error) {
	addr, err := trader.GetDepositAddress("USDT", "ARBITRUM")
	if err != nil {
		return "", fmt.Errorf("get deposit addr: %w", err)
	}
	depositAddr := common.HexToAddress(addr.Address)

	sig, _ := hex.DecodeString("a9059cbb")
	data := make([]byte, 0, 68)
	data = append(data, sig...)
	data = append(data, common.LeftPadBytes(depositAddr.Bytes(), 32)...)
	amt := new(big.Int).SetUint64(uint64(amount * 1e6))
	data = append(data, common.LeftPadBytes(amt.Bytes(), 32)...)

	if _, err = client.CallContract(context.Background(), ethereum.CallMsg{
		From: keeper, To: &USDT, Data: data,
	}, nil); err != nil {
		return "", fmt.Errorf("sim: %w", err)
	}

	nonce, _ := client.PendingNonceAt(context.Background(), keeper)
	head, _ := client.HeaderByNumber(context.Background(), nil)
	tip := big.NewInt(100000000)
	maxFee := new(big.Int).Add(new(big.Int).Mul(head.BaseFee, big.NewInt(2)), tip)
	gasLimit, err := client.EstimateGas(context.Background(), ethereum.CallMsg{
		From: keeper, To: &USDT, Data: data,
	})
	if err != nil {
		gasLimit = 100000
	} else {
		gasLimit = gasLimit * 130 / 100
	}

	tx := types.NewTx(&types.DynamicFeeTx{
		ChainID: chainID, Nonce: nonce, GasTipCap: tip, GasFeeCap: maxFee,
		Gas: gasLimit, To: &USDT, Value: big.NewInt(0), Data: data,
	})
	signer := types.LatestSignerForChainID(chainID)
	signedTx, _ := types.SignTx(tx, signer, privKey)
	if err := client.SendTransaction(context.Background(), signedTx); err != nil {
		return "", fmt.Errorf("send: %w", err)
	}

	txHash := signedTx.Hash().Hex()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	for {
		receipt, err := client.TransactionReceipt(ctx, signedTx.Hash())
		if err == nil {
			if receipt.Status == 1 {
				return txHash, nil
			}
			return txHash, fmt.Errorf("reverted")
		}
		select {
		case <-ctx.Done():
			return txHash, fmt.Errorf("timeout")
		case <-time.After(time.Second):
		}
	}
}

// ============ Quote ============

func quoteMultiHop(client *ethclient.Client, tokenIn, mid, tokenOut common.Address, amtIn float64, inDec, outDec int, fee1, fee2 int64) float64 {
	midOut := quoteSingle(client, tokenIn, mid, amtIn, inDec, 18, fee1)
	if midOut <= 0 {
		return 0
	}
	return quoteSingle(client, mid, tokenOut, midOut, 18, outDec, fee2)
}

func quoteSingle(client *ethclient.Client, tokenIn, tokenOut common.Address, amtIn float64, inDec, outDec int, fee int64) float64 {
	abiJSON := `[{"inputs":[{"components":[{"name":"tokenIn","type":"address"},{"name":"tokenOut","type":"address"},{"name":"amountIn","type":"uint256"},{"name":"fee","type":"uint24"},{"name":"sqrtPriceLimitX96","type":"uint160"}],"name":"params","type":"tuple"}],"name":"quoteExactInputSingle","outputs":[{"name":"amountOut","type":"uint256"},{"name":"sqrtPriceX96After","type":"uint160"},{"name":"initializedTicksCrossed","type":"uint32"},{"name":"gasEstimate","type":"uint256"}],"stateMutability":"nonpayable","type":"function"}]`
	parsedABI, _ := abi.JSON(strings.NewReader(abiJSON))
	type P struct {
		TokenIn, TokenOut                common.Address
		AmountIn, Fee, SqrtPriceLimitX96 *big.Int
	}
	data, _ := parsedABI.Pack("quoteExactInputSingle", P{
		tokenIn, tokenOut, floatToWei(amtIn, inDec), big.NewInt(fee), big.NewInt(0),
	})
	result, err := client.CallContract(context.Background(), ethereum.CallMsg{To: &QuoterV2, Data: data}, nil)
	if err != nil || len(result) < 32 {
		return 0
	}
	return weiToFloat(new(big.Int).SetBytes(result[:32]), outDec)
}

// ============ Utils ============

func encodePath(tokenIn common.Address, fee1 int64, mid common.Address, fee2 int64, tokenOut common.Address) []byte {
	path := make([]byte, 0, 66)
	path = append(path, tokenIn.Bytes()...)
	path = append(path, feeToBytes(fee1)...)
	path = append(path, mid.Bytes()...)
	path = append(path, feeToBytes(fee2)...)
	path = append(path, tokenOut.Bytes()...)
	return path
}

func feeToBytes(fee int64) []byte {
	b := make([]byte, 3)
	b[0] = byte(fee >> 16)
	b[1] = byte(fee >> 8)
	b[2] = byte(fee)
	return b
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

func getBidPrice(symbol string) float64 {
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Get(
		"https://api.binance.com/api/v3/ticker/bookTicker?symbol=" + symbol)
	if err != nil {
		return 0
	}
	defer resp.Body.Close()
	var d struct{ BidPrice string `json:"bidPrice"` }
	json.NewDecoder(resp.Body).Decode(&d)
	v := 0.0
	fmt.Sscanf(d.BidPrice, "%f", &v)
	return v
}
