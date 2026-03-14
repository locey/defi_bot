// pkg/trading/swap.go
// 共用交易工具 — DEX swap, quote, balance, 价格查询
// 供 cexdex-loop, momentum-scanner 等多个策略共用
package trading

import (
	"context"
	"crypto/ecdsa"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/ethclient"
)

var (
	USDT     = common.HexToAddress("0xFd086bC7CD5C481DCC9C85ebE478A1C0b69FCbb9")
	WETH     = common.HexToAddress("0x82aF49447D8a07e3bd95BD0d56f35241523fBab1")
	V3Router = common.HexToAddress("0xE592427A0AEce92De3Edee1F18E0157C05861564")
	QuoterV2 = common.HexToAddress("0x61fFE014bA17989E743c5F6cB21bF9697530B21e")
	ChainID  = big.NewInt(42161)
)

// ============ Quote ============

// QuoteMultiHop 获取多跳报价: tokenIn → mid → tokenOut
func QuoteMultiHop(client *ethclient.Client, tokenIn, mid, tokenOut common.Address,
	amtIn float64, inDec, outDec int, fee1, fee2 int64) float64 {

	midOut := QuoteSingle(client, tokenIn, mid, amtIn, inDec, 18, fee1)
	if midOut <= 0 {
		return 0
	}
	return QuoteSingle(client, mid, tokenOut, midOut, 18, outDec, fee2)
}

// QuoteSingle 获取单跳报价 (QuoterV2)
func QuoteSingle(client *ethclient.Client, tokenIn, tokenOut common.Address,
	amtIn float64, inDec, outDec int, fee int64) float64 {

	abiJSON := `[{"inputs":[{"components":[{"name":"tokenIn","type":"address"},{"name":"tokenOut","type":"address"},{"name":"amountIn","type":"uint256"},{"name":"fee","type":"uint24"},{"name":"sqrtPriceLimitX96","type":"uint160"}],"name":"params","type":"tuple"}],"name":"quoteExactInputSingle","outputs":[{"name":"amountOut","type":"uint256"},{"name":"sqrtPriceX96After","type":"uint160"},{"name":"initializedTicksCrossed","type":"uint32"},{"name":"gasEstimate","type":"uint256"}],"stateMutability":"nonpayable","type":"function"}]`
	parsedABI, _ := abi.JSON(strings.NewReader(abiJSON))

	type P struct {
		TokenIn, TokenOut                common.Address
		AmountIn, Fee, SqrtPriceLimitX96 *big.Int
	}
	data, _ := parsedABI.Pack("quoteExactInputSingle", P{
		tokenIn, tokenOut, FloatToWei(amtIn, inDec), big.NewInt(fee), big.NewInt(0),
	})
	result, err := client.CallContract(context.Background(), ethereum.CallMsg{To: &QuoterV2, Data: data}, nil)
	if err != nil || len(result) < 32 {
		return 0
	}
	return WeiToFloat(new(big.Int).SetBytes(result[:32]), outDec)
}

// ============ Swap ============

// DoSwap 执行多跳 swap: tokenIn → mid → tokenOut (via Uniswap V3 exactInput)
func DoSwap(client *ethclient.Client, privKey *ecdsa.PrivateKey, keeper common.Address,
	tokenIn, mid, tokenOut common.Address, amtIn float64, inDec, outDec int,
	fee1, fee2 int64, expectedOut float64) (string, error) {

	quotedOut := QuoteMultiHop(client, tokenIn, mid, tokenOut, amtIn, inDec, outDec, fee1, fee2)
	if quotedOut <= 0 {
		return "", fmt.Errorf("quote=0")
	}

	exactInputABI := `[{"inputs":[{"components":[{"name":"path","type":"bytes"},{"name":"recipient","type":"address"},{"name":"deadline","type":"uint256"},{"name":"amountIn","type":"uint256"},{"name":"amountOutMinimum","type":"uint256"}],"name":"params","type":"tuple"}],"name":"exactInput","outputs":[{"name":"amountOut","type":"uint256"}],"stateMutability":"payable","type":"function"}]`
	parsed, _ := abi.JSON(strings.NewReader(exactInputABI))

	path := EncodePath(tokenIn, fee1, mid, fee2, tokenOut)
	amountIn := FloatToWei(amtIn, inDec)
	minOut := FloatToWei(quotedOut*0.995, outDec)
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

	// Build and send tx
	nonce, _ := client.PendingNonceAt(context.Background(), keeper)
	head, err2 := client.HeaderByNumber(context.Background(), nil)
	tip := big.NewInt(100000000)
	baseFee := big.NewInt(100000000)
	if err2 == nil && head != nil && head.BaseFee != nil {
		baseFee = head.BaseFee
	}
	maxFee := new(big.Int).Add(new(big.Int).Mul(baseFee, big.NewInt(2)), tip)

	gasLimit, err := client.EstimateGas(context.Background(), ethereum.CallMsg{
		From: keeper, To: &V3Router, Data: callData,
	})
	if err != nil {
		gasLimit = 500000
	} else {
		gasLimit = gasLimit * 130 / 100
	}

	tx := types.NewTx(&types.DynamicFeeTx{
		ChainID: ChainID, Nonce: nonce, GasTipCap: tip, GasFeeCap: maxFee,
		Gas: gasLimit, To: &V3Router, Value: big.NewInt(0), Data: callData,
	})
	signer := types.LatestSignerForChainID(ChainID)
	signedTx, _ := types.SignTx(tx, signer, privKey)
	if err := client.SendTransaction(context.Background(), signedTx); err != nil {
		return "", fmt.Errorf("send: %w", err)
	}

	return WaitReceipt(client, signedTx)
}

// EnsureApproval 确保 token 已经 approve 给 V3Router
func EnsureApproval(client *ethclient.Client, privKey *ecdsa.PrivateKey, keeper, token common.Address) error {
	allowanceSig, _ := hex.DecodeString("dd62ed3e")
	data := make([]byte, 0, 68)
	data = append(data, allowanceSig...)
	data = append(data, common.LeftPadBytes(keeper.Bytes(), 32)...)
	data = append(data, common.LeftPadBytes(V3Router.Bytes(), 32)...)

	result, err := client.CallContract(context.Background(), ethereum.CallMsg{To: &token, Data: data}, nil)
	if err != nil {
		return fmt.Errorf("check allowance: %w", err)
	}

	allowance := new(big.Int).SetBytes(result)
	threshold := new(big.Int).Exp(big.NewInt(10), big.NewInt(24), nil)
	if allowance.Cmp(threshold) >= 0 {
		return nil
	}

	// Approve max
	approveSig, _ := hex.DecodeString("095ea7b3")
	maxUint := new(big.Int).Sub(new(big.Int).Exp(big.NewInt(2), big.NewInt(256), nil), big.NewInt(1))
	approveData := make([]byte, 0, 68)
	approveData = append(approveData, approveSig...)
	approveData = append(approveData, common.LeftPadBytes(V3Router.Bytes(), 32)...)
	approveData = append(approveData, common.LeftPadBytes(maxUint.Bytes(), 32)...)

	nonce, _ := client.PendingNonceAt(context.Background(), keeper)
	head, err2 := client.HeaderByNumber(context.Background(), nil)
	tip := big.NewInt(100000000)
	baseFee := big.NewInt(100000000)
	if err2 == nil && head != nil && head.BaseFee != nil {
		baseFee = head.BaseFee
	}
	maxFee := new(big.Int).Add(new(big.Int).Mul(baseFee, big.NewInt(2)), tip)

	tx := types.NewTx(&types.DynamicFeeTx{
		ChainID: ChainID, Nonce: nonce, GasTipCap: tip, GasFeeCap: maxFee,
		Gas: 80000, To: &token, Value: big.NewInt(0), Data: approveData,
	})
	signer := types.LatestSignerForChainID(ChainID)
	signedTx, _ := types.SignTx(tx, signer, privKey)
	if err := client.SendTransaction(context.Background(), signedTx); err != nil {
		return fmt.Errorf("send approve: %w", err)
	}

	_, err = WaitReceipt(client, signedTx)
	if err != nil {
		return fmt.Errorf("approve: %w", err)
	}
	fmt.Printf("  Approved %s → V3Router\n", token.Hex()[:10])
	return nil
}

// ============ Balance ============

// GetTokenBalance 获取 ERC20 余额 (带重试)
func GetTokenBalance(client *ethclient.Client, token, owner common.Address, dec int) float64 {
	sig, _ := hex.DecodeString("70a08231")
	data := append(sig, common.LeftPadBytes(owner.Bytes(), 32)...)
	for i := 0; i < 3; i++ {
		result, err := client.CallContract(context.Background(), ethereum.CallMsg{To: &token, Data: data}, nil)
		if err == nil && len(result) >= 32 {
			return WeiToFloat(new(big.Int).SetBytes(result[:32]), dec)
		}
		if i < 2 {
			time.Sleep(500 * time.Millisecond)
		}
	}
	return 0
}

// ============ Price ============

// GetBidPrice 获取 Binance 买一价
func GetBidPrice(symbol string) float64 {
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

// ============ Path Encoding ============

func EncodePath(tokenIn common.Address, fee1 int64, mid common.Address, fee2 int64, tokenOut common.Address) []byte {
	path := make([]byte, 0, 66)
	path = append(path, tokenIn.Bytes()...)
	path = append(path, FeeToBytes(fee1)...)
	path = append(path, mid.Bytes()...)
	path = append(path, FeeToBytes(fee2)...)
	path = append(path, tokenOut.Bytes()...)
	return path
}

func FeeToBytes(fee int64) []byte {
	b := make([]byte, 3)
	b[0] = byte(fee >> 16)
	b[1] = byte(fee >> 8)
	b[2] = byte(fee)
	return b
}

// ============ Conversion ============

func WeiToFloat(wei *big.Int, dec int) float64 {
	if wei == nil {
		return 0
	}
	f, _ := new(big.Float).Quo(
		new(big.Float).SetInt(wei),
		new(big.Float).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(dec)), nil)),
	).Float64()
	return f
}

func FloatToWei(amt float64, dec int) *big.Int {
	r, _ := new(big.Float).Mul(
		new(big.Float).SetFloat64(amt),
		new(big.Float).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(dec)), nil)),
	).Int(nil)
	return r
}

// ============ Helpers ============

func WaitReceipt(client *ethclient.Client, signedTx *types.Transaction) (string, error) {
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

func Ts() string { return time.Now().Format("15:04:05") }

func FormatQty(qty float64, minStep string) string {
	switch minStep {
	case "0.001":
		return fmt.Sprintf("%.3f", qty)
	case "0.01":
		return fmt.Sprintf("%.2f", qty)
	case "0.1":
		return fmt.Sprintf("%.1f", qty)
	case "1":
		return fmt.Sprintf("%.0f", qty)
	default:
		return fmt.Sprintf("%.2f", qty)
	}
}

func ParseFloat(s string) float64 {
	v := 0.0
	fmt.Sscanf(s, "%f", &v)
	return v
}
