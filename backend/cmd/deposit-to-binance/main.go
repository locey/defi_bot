// cmd/deposit-to-binance/main.go
// 从 Arbitrum 存入 USDT 到 Binance (存款免费, 仅付 gas)
package main

import (
	"context"
	"crypto/ecdsa"
	"encoding/hex"
	"fmt"
	"math/big"
	"os"
	"strings"
	"time"

	"github.com/defi-bot/backend/pkg/cex"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
)

var (
	USDT    = common.HexToAddress("0xFd086bC7CD5C481DCC9C85ebE478A1C0b69FCbb9")
	chainID = big.NewInt(42161)
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

	trader := cex.NewBinanceTrader(&cex.BinanceConfig{
		APIEndpoint: "https://api.binance.com",
		APIKey:      os.Getenv("BINANCE_API_KEY"),
		APISecret:   os.Getenv("BINANCE_API_SECRET"),
		RateLimit:   1200,
	})

	// 1. 获取 Binance USDT 存款地址 (Arbitrum 网络)
	addr, err := trader.GetDepositAddress("USDT", "ARBITRUM")
	if err != nil {
		fmt.Printf("Get deposit address failed: %v\n", err)
		return
	}
	fmt.Printf("Binance deposit address: %s\n", addr.Address)

	// 2. 查余额
	usdtBal := getBalance(client, USDT, keeper)
	fmt.Printf("Current USDT balance: %.6f\n", usdtBal)

	// 存入 25 USDT (保留一些在 Arb 做第一笔 DEX 买)
	depositAmt := 25.0
	if usdtBal < depositAmt+2 {
		depositAmt = usdtBal - 2 // 保留 2 USDT
	}
	if depositAmt < 10 {
		fmt.Println("Not enough USDT to deposit")
		return
	}

	fmt.Printf("Depositing %.6f USDT to Binance...\n", depositAmt)

	// 3. 发送 ERC20 transfer
	depositAddr := common.HexToAddress(addr.Address)
	txHash, err := sendUSDT(client, privKey, keeper, depositAddr, depositAmt)
	if err != nil {
		fmt.Printf("Transfer failed: %v\n", err)
		return
	}
	fmt.Printf("TX: %s\n", txHash)
	fmt.Println("Waiting for confirmation...")

	// 等待 Binance 确认 (通常 5-15 分钟)
	fmt.Println("Deposit submitted! Binance typically confirms in 5-15 min for Arbitrum.")
	fmt.Println("Check Binance balance later with check-balance tool.")
}

func sendUSDT(client *ethclient.Client, privKey *ecdsa.PrivateKey, from, to common.Address, amount float64) (string, error) {
	// ERC20 transfer(address,uint256)
	sig, _ := hex.DecodeString("a9059cbb")
	data := make([]byte, 0, 68)
	data = append(data, sig...)
	data = append(data, common.LeftPadBytes(to.Bytes(), 32)...)

	// amount in 6 decimals
	amt := new(big.Int).SetUint64(uint64(amount * 1e6))
	data = append(data, common.LeftPadBytes(amt.Bytes(), 32)...)

	// Simulate first
	if _, err := client.CallContract(context.Background(), ethereum.CallMsg{
		From: from, To: &USDT, Data: data,
	}, nil); err != nil {
		return "", fmt.Errorf("simulation failed: %w", err)
	}

	nonce, _ := client.PendingNonceAt(context.Background(), from)
	head, _ := client.HeaderByNumber(context.Background(), nil)
	tip := big.NewInt(100000000) // 0.1 gwei
	maxFee := new(big.Int).Add(new(big.Int).Mul(head.BaseFee, big.NewInt(2)), tip)

	gasLimit, err := client.EstimateGas(context.Background(), ethereum.CallMsg{
		From: from, To: &USDT, Data: data,
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
		return "", fmt.Errorf("send failed: %w", err)
	}

	txHash := signedTx.Hash().Hex()

	// Wait for receipt
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	for {
		receipt, err := client.TransactionReceipt(ctx, signedTx.Hash())
		if err == nil {
			if receipt.Status == 1 {
				return txHash, nil
			}
			return txHash, fmt.Errorf("reverted (gas=%d)", receipt.GasUsed)
		}
		select {
		case <-ctx.Done():
			return txHash, fmt.Errorf("timeout waiting for receipt")
		case <-time.After(time.Second):
		}
	}
}

func getBalance(client *ethclient.Client, token, owner common.Address) float64 {
	sig, _ := hex.DecodeString("70a08231")
	data := append(sig, common.LeftPadBytes(owner.Bytes(), 32)...)
	result, err := client.CallContract(context.Background(), ethereum.CallMsg{To: &token, Data: data}, nil)
	if err != nil || len(result) < 32 {
		return 0
	}
	bal := new(big.Int).SetBytes(result[:32])
	f, _ := new(big.Float).Quo(new(big.Float).SetInt(bal), big.NewFloat(1e6)).Float64()
	return f
}
