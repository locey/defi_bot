package main

import (
	"context"
	"encoding/hex"
	"fmt"
	"math/big"
	"os"
	"strings"

	"github.com/defi-bot/backend/pkg/cex"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/ethclient"
)

func main() {
	pk := os.Getenv("KEEPER_PRIVATE_KEY")
	if strings.HasPrefix(pk, "0x") {
		pk = pk[2:]
	}
	key, _ := crypto.HexToECDSA(pk)
	keeper := crypto.PubkeyToAddress(key.PublicKey)
	client, _ := ethclient.Dial("https://warmhearted-wild-road.arbitrum-mainnet.quiknode.pro/1ed461039e0b8a3008160cbc130479deb34798c9/")
	defer client.Close()

	USDT := common.HexToAddress("0xFd086bC7CD5C481DCC9C85ebE478A1C0b69FCbb9")
	LINK := common.HexToAddress("0xf97f4df75117a78c1A5a0DBb814Af92458539FB4")

	ethBal, _ := client.BalanceAt(context.Background(), keeper, nil)
	nonce, _ := client.PendingNonceAt(context.Background(), keeper)

	sig, _ := hex.DecodeString("70a08231")
	data := append(sig, common.LeftPadBytes(keeper.Bytes(), 32)...)
	usdtR, _ := client.CallContract(context.Background(), ethereum.CallMsg{To: &USDT, Data: data}, nil)
	linkR, _ := client.CallContract(context.Background(), ethereum.CallMsg{To: &LINK, Data: data}, nil)

	usdtBal := new(big.Int).SetBytes(usdtR[:32])
	linkBal := new(big.Int).SetBytes(linkR[:32])

	usdtF, _ := new(big.Float).Quo(new(big.Float).SetInt(usdtBal), big.NewFloat(1e6)).Float64()
	linkF, _ := new(big.Float).Quo(new(big.Float).SetInt(linkBal), big.NewFloat(1e18)).Float64()
	ethF, _ := new(big.Float).Quo(new(big.Float).SetInt(ethBal), big.NewFloat(1e18)).Float64()

	fmt.Printf("Keeper: %s\n", keeper.Hex())
	fmt.Printf("ETH:  %.6f\n", ethF)
	fmt.Printf("USDT: %.6f\n", usdtF)
	fmt.Printf("LINK: %.6f\n", linkF)
	fmt.Printf("Nonce: %d\n", nonce)

	trader := cex.NewBinanceTrader(&cex.BinanceConfig{
		APIEndpoint: "https://api.binance.com",
		APIKey:      os.Getenv("BINANCE_API_KEY"),
		APISecret:   os.Getenv("BINANCE_API_SECRET"),
		RateLimit:   1200,
	})
	binUSDT, _, _ := trader.GetBalance("USDT")
	binLINK, _, _ := trader.GetBalance("LINK")
	fmt.Printf("Binance USDT: %.4f\n", binUSDT)
	fmt.Printf("Binance LINK: %.4f\n", binLINK)
}
