// internal/executor/contract_caller.go
package executor

import (
	"context"
	"fmt"
	"math/big"
	"strings"
	"sync"

	"github.com/defi-bot/backend/pkg/log"
	"github.com/defi-bot/backend/pkg/web3"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/ethclient"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

// FlashLoanArbitrage ABI（最小化：executeFlashLoan）
// platform 参数是 FlashLoanRouter.LendingPlatForm 枚举: 0=Aave_V2, 1=Aave_V3, 2=DYDX, 3=UNISWAP_V3
const FlashLoanArbitrageABI = `[
    {
        "inputs": [
            {"internalType":"uint8","name":"platform","type":"uint8"},
            {"internalType":"address","name":"tokenIn","type":"address"},
            {"internalType":"uint256","name":"amountIn","type":"uint256"},
            {"internalType":"address[]","name":"swapPath","type":"address[]"},
            {"internalType":"address[]","name":"dexes","type":"address[]"},
            {"internalType":"uint24[]","name":"feeTiers","type":"uint24[]"},
            {"internalType":"uint256","name":"expectProfit","type":"uint256"},
            {"internalType":"uint256","name":"minProfit","type":"uint256"}
        ],
        "name": "executeFlashLoan",
        "outputs": [],
        "stateMutability": "nonpayable",
        "type": "function"
    }
]`

// ArbitrageCore ABI（最小化：executeStrategy + getVaultInfo + 相关事件）
const ArbitrageCoreABI = `[
    {
        "inputs": [
            {
                "components": [
                    {"internalType":"address","name":"asset","type":"address"},
                    {"internalType":"address","name":"tokenOut","type":"address"},
                    {"internalType":"uint256","name":"amountIn","type":"uint256"},
                    {"internalType":"address[]","name":"swapPath","type":"address[]"},
                    {"internalType":"address[]","name":"dexes","type":"address[]"},
                    {"internalType":"uint24[]","name":"feeTiers","type":"uint24[]"},
                    {"internalType":"uint256","name":"expectProfit","type":"uint256"},
                    {"internalType":"uint256","name":"minProfit","type":"uint256"},
                    {"internalType":"bool","name":"isCex","type":"bool"}
                ],
                "internalType":"struct IArbitrage.ArbitrageParams",
                "name":"params",
                "type":"tuple"
            }
        ],
        "name": "executeStrategy",
        "outputs": [],
        "stateMutability": "nonpayable",
        "type": "function"
    },
    {
        "inputs": [{"internalType":"address","name":"asset","type":"address"}],
        "name": "getVaultInfo",
        "outputs": [
            {"internalType":"address","name":"vaultAddress","type":"address"},
            {"internalType":"uint256","name":"totalAssets","type":"uint256"},
            {"internalType":"uint256","name":"availableAssets","type":"uint256"}
        ],
        "stateMutability": "view",
        "type": "function"
    },
    {
        "anonymous": false,
        "inputs": [
            {"indexed": true, "internalType":"address", "name":"vault", "type":"address"},
            {"indexed": true, "internalType":"address", "name":"asset", "type":"address"},
            {"indexed": false, "internalType":"uint256", "name":"amountIn", "type":"uint256"},
            {"indexed": false, "internalType":"uint256", "name":"profit", "type":"uint256"},
            {"indexed": false, "internalType":"uint256", "name":"platFormFee", "type":"uint256"},
            {"indexed": false, "internalType":"uint256", "name":"netProfitToVault", "type":"uint256"},
            {"indexed": false, "internalType":"uint256", "name":"timestamp", "type":"uint256"}
        ],
        "name": "VaultArbitrageExecuted",
        "type": "event"
    }
]`

// ContractCaller 合约调用器
type ContractCaller struct {
	web3Client      *web3.Client
	contractAddress common.Address
	contractABI     abi.ABI
	execRPCClient   *ethclient.Client // 独立的执行 RPC（公共节点，避免与数据采集竞争）
	keeperAddress   common.Address    // Keeper 地址（用于 eth_call 的 From 字段）
	privateTxSender *PrivateTxSender  // 私有交易发送器（防 MEV 抢跑，Arbitrum FCFS 下可选）
	nonceTracker    *NonceTracker     // 本地 Nonce 追踪器（替代 PendingNonceAt，消除并发竞态）
	sendMu          sync.Mutex        // 发送锁：序列化 Reserve→Send→Confirm，防止并发 nonce 冲突

	// Flash Loan 相关
	flashLoanAddress common.Address // FlashLoanArbitrage 合约地址
	flashLoanABI     abi.ABI        // FlashLoanArbitrage ABI
}

// NewContractCaller 创建合约调用器
func NewContractCaller(
	web3Client *web3.Client,
	contractAddress common.Address,
) *ContractCaller {

	parsedABI, err := abi.JSON(strings.NewReader(ArbitrageCoreABI))
	if err != nil {
		panic(fmt.Sprintf("failed to parse ABI: %v", err))
	}

	cc := &ContractCaller{
		web3Client:      web3Client,
		contractAddress: contractAddress,
		contractABI:     parsedABI,
		nonceTracker:    NewNonceTracker(),
	}

	// 解析 FlashLoanArbitrage ABI
	flABI, flErr := abi.JSON(strings.NewReader(FlashLoanArbitrageABI))
	if flErr == nil {
		cc.flashLoanABI = flABI
	}

	// 独立的公共 RPC 用于交易提交（nonce 查询 + 发送）
	execClient, dialErr := ethclient.Dial("https://arbitrum-one.publicnode.com")
	if dialErr == nil {
		cc.execRPCClient = execClient
		log.Executor().Info().Msg("Using publicnode RPC for tx submission")
	}

	// 初始化私有交易发送器（防 MEV 抢跑，Arbitrum FCFS 场景下自动降级到公共 RPC）
	privateSender, privErr := NewPrivateTxSender(DefaultPrivateTxConfig())
	if privErr == nil {
		cc.privateTxSender = privateSender
		log.Executor().Info().Msg("PrivateTxSender initialized (Arbitrum FCFS mode)")
	}

	return cc
}

// GetVaultAvailable 查询指定 asset 在 ArbitrageCore 的 Vault 可用余额
// 用于在执行前判断 amountIn 是否超过 Vault 余额（避免 `amountIn too much` revert）
func (cc *ContractCaller) GetVaultAvailable(ctx context.Context, asset common.Address) (*big.Int, error) {
	callData, err := cc.contractABI.Pack("getVaultInfo", asset)
	if err != nil {
		return nil, fmt.Errorf("pack getVaultInfo: %w", err)
	}

	result, err := cc.web3Client.GetClient().CallContract(ctx, ethereum.CallMsg{
		From: cc.keeperAddress,
		To:   &cc.contractAddress,
		Data: callData,
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("getVaultInfo call: %w", err)
	}
	if len(result) < 96 {
		return nil, fmt.Errorf("getVaultInfo: unexpected result length %d", len(result))
	}

	// 返回值：(vaultAddress, totalAssets, availableAssets)
	// availableAssets 在 result[64:96]
	available := new(big.Int).SetBytes(result[64:96])
	return available, nil
}

// ExecuteArbitrage 执行套利
func (cc *ContractCaller) ExecuteArbitrage(
	ctx context.Context,
	keeperPrivateKey string,
	params *ArbitrageParams,
) (*types.Transaction, error) {

	if keeperPrivateKey == "" {
		return nil, fmt.Errorf("keeper private key is empty")
	}

	callData, err := cc.buildCallData(params)
	if err != nil {
		return nil, err
	}

	pk, err := crypto.HexToECDSA(strings.TrimPrefix(keeperPrivateKey, "0x"))
	if err != nil {
		return nil, fmt.Errorf("invalid keeper private key: %w", err)
	}

	from := crypto.PubkeyToAddress(pk.PublicKey)

	// 使用独立的公共 RPC 做交易提交，避免与 Multicall 数据采集竞争 Alchemy 带宽
	execClient := cc.web3Client.GetClient()
	if cc.execRPCClient != nil {
		execClient = cc.execRPCClient
	}

	// 序列化交易发送：防止并发 goroutine 拿到相同 nonce
	cc.sendMu.Lock()
	defer cc.sendMu.Unlock()

	// 使用本地 NonceTracker 替代 PendingNonceAt，消除并发 nonce 冲突
	if err := cc.nonceTracker.InitIfNeeded(ctx, execClient, from); err != nil {
		return nil, fmt.Errorf("init nonce failed: %w", err)
	}
	// 安全模式：先预留 nonce，发送成功后再确认递增
	nonce := cc.nonceTracker.Reserve(from)

	// Gas 使用固定值（Arbitrum 上 Gas 估算开销大且容易超时）
	gasLimit := uint64(1_000_000) // 1M Gas（保守值）

	// EIP-1559 交易
	header, hErr := execClient.HeaderByNumber(ctx, nil)
	if hErr == nil && header != nil && header.BaseFee != nil {
		tipCap, tipErr := execClient.SuggestGasTipCap(ctx)
		if tipErr != nil {
			tipCap = big.NewInt(100_000_000) // 0.1 gwei (Arbitrum 便宜)
		}
		feeCap := new(big.Int).Add(new(big.Int).Mul(header.BaseFee, big.NewInt(2)), tipCap)

		tx := types.NewTx(&types.DynamicFeeTx{
			ChainID:   cc.web3Client.GetChainID(),
			Nonce:     nonce,
			To:        &cc.contractAddress,
			Gas:       gasLimit,
			GasTipCap: tipCap,
			GasFeeCap: feeCap,
			Value:     big.NewInt(0),
			Data:      callData,
		})

		signer := types.LatestSignerForChainID(cc.web3Client.GetChainID())
		signed, err := types.SignTx(tx, signer, pk)
		if err != nil {
			return nil, fmt.Errorf("sign tx failed: %w", err)
		}
		// 优先走私有 RPC（防 MEV），Arbitrum FCFS 下 fallback 到公共 RPC
		if cc.privateTxSender != nil {
			if err := cc.privateTxSender.SendTransaction(ctx, signed, execClient); err != nil {
				return nil, fmt.Errorf("send tx failed: %w", err)
			}
		} else if err := execClient.SendTransaction(ctx, signed); err != nil {
			return nil, fmt.Errorf("send tx failed: %w", err)
		}
		// 发送成功后才确认 nonce 递增
		cc.nonceTracker.Confirm(from, nonce)
		return signed, nil
	}

	// Legacy fallback
	gasPrice, err := execClient.SuggestGasPrice(ctx)
	if err != nil {
		gasPrice = big.NewInt(100_000_000) // 0.1 gwei fallback
	}

	tx := types.NewTx(&types.LegacyTx{
		Nonce:    nonce,
		To:       &cc.contractAddress,
		Gas:      gasLimit,
		GasPrice: gasPrice,
		Value:    big.NewInt(0),
		Data:     callData,
	})

	signer := types.LatestSignerForChainID(cc.web3Client.GetChainID())
	signed, err := types.SignTx(tx, signer, pk)
	if err != nil {
		return nil, fmt.Errorf("sign tx failed: %w", err)
	}

	if cc.privateTxSender != nil {
		if err := cc.privateTxSender.SendTransaction(ctx, signed, execClient); err != nil {
			return nil, fmt.Errorf("send tx failed: %w", err)
		}
	} else if err := execClient.SendTransaction(ctx, signed); err != nil {
		return nil, fmt.Errorf("send tx failed: %w", err)
	}
	cc.nonceTracker.Confirm(from, nonce)

	return signed, nil
}

// buildCallData 构建 executeStrategy(ArbitrageParams) 调用数据
func (cc *ContractCaller) buildCallData(params *ArbitrageParams) ([]byte, error) {

	// 将 []uint32 转换为 []*big.Int（go-ethereum ABI 编码 uint24[] 需要 []*big.Int）
	feeTiersBig := make([]*big.Int, len(params.FeeTiers))
	for i, ft := range params.FeeTiers {
		feeTiersBig[i] = new(big.Int).SetUint64(uint64(ft))
	}

	// 构建参数结构体（与合约 IArbitrage.ArbitrageParams 一一对应）
	paramsStruct := struct {
		Asset        common.Address
		TokenOut     common.Address
		AmountIn     *big.Int
		SwapPath     []common.Address
		Dexes        []common.Address
		FeeTiers     []*big.Int
		ExpectProfit *big.Int
		MinProfit    *big.Int
		IsCex        bool
	}{
		Asset:        params.Asset,
		TokenOut:     params.TokenOut,
		AmountIn:     params.AmountIn,
		SwapPath:     params.SwapPath,
		Dexes:        params.Dexes,
		FeeTiers:     feeTiersBig,
		ExpectProfit: params.ExpectProfit,
		MinProfit:    params.MinProfit,
		IsCex:        params.IsCex,
	}

	// 编码调用数据
	callData, err := cc.contractABI.Pack("executeStrategy", paramsStruct)
	if err != nil {
		return nil, err
	}

	return callData, nil
}

// estimateGas 估算Gas
func (cc *ContractCaller) estimateGas(
	ctx context.Context,
	callData []byte,
) (uint64, error) {
	// 使用默认 Gas 限制
	// TODO: 实现精确的 Gas 估算
	return 800000, nil
}

// sendTransaction 发送交易
func (cc *ContractCaller) sendTransaction(
	ctx context.Context,
	auth interface{},
	callData []byte,
) (*types.Transaction, error) {
	// TODO: 实现交易发送逻辑
	return nil, fmt.Errorf("transaction sending not implemented")
}

// SimulateArbitrage 模拟套利（不发送交易）
// SetKeeperAddress 设置 Keeper 地址（用于 eth_call 的 From 字段，必须与合约 backCaller 一致）
func (cc *ContractCaller) SetKeeperAddress(addr common.Address) {
	cc.keeperAddress = addr
}

// 使用 eth_call 模拟 executeStrategy，检测交易是否会 revert
// 返回预估 Gas；如果 revert 则返回 error
func (cc *ContractCaller) SimulateArbitrage(
	ctx context.Context,
	params *ArbitrageParams,
) (*big.Int, error) {
	callData, err := cc.buildCallData(params)
	if err != nil {
		return nil, fmt.Errorf("build calldata: %w", err)
	}

	// 使用 EstimateGas 作为模拟：如果交易会 revert，EstimateGas 也会失败
	// From 字段必须是 backCaller（合约权限检查用），否则永远 revert
	callMsg := ethereum.CallMsg{
		From:  cc.keeperAddress,
		To:    &cc.contractAddress,
		Data:  callData,
		Value: big.NewInt(0),
	}
	if cc.keeperAddress == (common.Address{}) {
		log.Executor().Warn().Msg("SimulateArbitrage: keeperAddress not set, eth_call From=0x0 will fail permission check")
	}

	gasEstimate, err := cc.web3Client.GetClient().EstimateGas(ctx, callMsg)
	if err != nil {
		return nil, fmt.Errorf("simulation reverted: %w", err)
	}

	return big.NewInt(int64(gasEstimate)), nil
}

// DebugCallData 返回 executeStrategy 的 calldata（便于排查/打印）
func (cc *ContractCaller) DebugCallData(params *ArbitrageParams) (string, error) {
	data, err := cc.buildCallData(params)
	if err != nil {
		return "", err
	}
	return hexutil.Encode(data), nil
}

// SetFlashLoanAddress 设置 FlashLoanArbitrage 合约地址
func (cc *ContractCaller) SetFlashLoanAddress(addr common.Address) {
	cc.flashLoanAddress = addr
	log.Executor().Info().Str("address", addr.Hex()).Msg("FlashLoanArbitrage address configured")
}

// FlashLoanParams Flash Loan 执行参数
type FlashLoanParams struct {
	Platform     uint8 // LendingPlatForm 枚举: 0=Aave_V2, 1=Aave_V3
	TokenIn      common.Address
	AmountIn     *big.Int
	SwapPath     []common.Address
	Dexes        []common.Address
	FeeTiers     []uint32 // 每步 V3 fee tier
	ExpectProfit *big.Int
	MinProfit    *big.Int
}

// SimulateFlashLoan eth_call 预检 FlashLoan 交易是否会 revert
func (cc *ContractCaller) SimulateFlashLoan(
	ctx context.Context,
	params *FlashLoanParams,
) (*big.Int, error) {
	if cc.flashLoanAddress == (common.Address{}) {
		return nil, fmt.Errorf("flash loan contract address not configured")
	}

	callData, err := cc.buildFlashLoanCallData(params)
	if err != nil {
		return nil, fmt.Errorf("build flash loan calldata: %w", err)
	}

	callMsg := ethereum.CallMsg{
		From:  cc.keeperAddress,
		To:    &cc.flashLoanAddress,
		Data:  callData,
		Value: big.NewInt(0),
	}

	gasEstimate, err := cc.web3Client.GetClient().EstimateGas(ctx, callMsg)
	if err != nil {
		return nil, fmt.Errorf("flash loan simulation reverted: %w", err)
	}

	return big.NewInt(int64(gasEstimate)), nil
}

// ExecuteFlashLoanArbitrage 通过 FlashLoanArbitrage 合约执行闪电贷套利
func (cc *ContractCaller) ExecuteFlashLoanArbitrage(
	ctx context.Context,
	keeperPrivateKey string,
	params *FlashLoanParams,
) (*types.Transaction, error) {
	if keeperPrivateKey == "" {
		return nil, fmt.Errorf("keeper private key is empty")
	}
	if cc.flashLoanAddress == (common.Address{}) {
		return nil, fmt.Errorf("flash loan contract address not configured")
	}

	// 构建 executeFlashLoan calldata
	callData, err := cc.buildFlashLoanCallData(params)
	if err != nil {
		return nil, fmt.Errorf("build flash loan calldata: %w", err)
	}

	pk, err := crypto.HexToECDSA(strings.TrimPrefix(keeperPrivateKey, "0x"))
	if err != nil {
		return nil, fmt.Errorf("invalid keeper private key: %w", err)
	}
	from := crypto.PubkeyToAddress(pk.PublicKey)

	execClient := cc.web3Client.GetClient()
	if cc.execRPCClient != nil {
		execClient = cc.execRPCClient
	}

	// 序列化交易发送：防止并发 goroutine 拿到相同 nonce
	cc.sendMu.Lock()
	defer cc.sendMu.Unlock()

	// 使用本地 NonceTracker（安全模式：发送成功后再确认递增）
	if err := cc.nonceTracker.InitIfNeeded(ctx, execClient, from); err != nil {
		return nil, fmt.Errorf("init nonce failed: %w", err)
	}
	nonce := cc.nonceTracker.Reserve(from)

	gasLimit := uint64(1_500_000) // Flash Loan 交易 Gas 更高（含回调）

	// EIP-1559 交易
	header, hErr := execClient.HeaderByNumber(ctx, nil)
	if hErr == nil && header != nil && header.BaseFee != nil {
		tipCap, tipErr := execClient.SuggestGasTipCap(ctx)
		if tipErr != nil {
			tipCap = big.NewInt(100_000_000) // 0.1 gwei
		}
		feeCap := new(big.Int).Add(new(big.Int).Mul(header.BaseFee, big.NewInt(2)), tipCap)

		tx := types.NewTx(&types.DynamicFeeTx{
			ChainID:   cc.web3Client.GetChainID(),
			Nonce:     nonce,
			To:        &cc.flashLoanAddress,
			Gas:       gasLimit,
			GasTipCap: tipCap,
			GasFeeCap: feeCap,
			Value:     big.NewInt(0),
			Data:      callData,
		})

		signer := types.LatestSignerForChainID(cc.web3Client.GetChainID())
		signed, err := types.SignTx(tx, signer, pk)
		if err != nil {
			return nil, fmt.Errorf("sign tx failed: %w", err)
		}

		if cc.privateTxSender != nil {
			if err := cc.privateTxSender.SendTransaction(ctx, signed, execClient); err != nil {
				return nil, fmt.Errorf("send flash loan tx failed: %w", err)
			}
		} else if err := execClient.SendTransaction(ctx, signed); err != nil {
			return nil, fmt.Errorf("send flash loan tx failed: %w", err)
		}
		cc.nonceTracker.Confirm(from, nonce)
		return signed, nil
	}

	// Legacy fallback
	gasPrice, err := execClient.SuggestGasPrice(ctx)
	if err != nil {
		gasPrice = big.NewInt(100_000_000)
	}

	tx := types.NewTx(&types.LegacyTx{
		Nonce:    nonce,
		To:       &cc.flashLoanAddress,
		Gas:      gasLimit,
		GasPrice: gasPrice,
		Value:    big.NewInt(0),
		Data:     callData,
	})

	signer := types.LatestSignerForChainID(cc.web3Client.GetChainID())
	signed, err := types.SignTx(tx, signer, pk)
	if err != nil {
		return nil, fmt.Errorf("sign tx failed: %w", err)
	}

	if cc.privateTxSender != nil {
		if err := cc.privateTxSender.SendTransaction(ctx, signed, execClient); err != nil {
			return nil, fmt.Errorf("send flash loan tx failed: %w", err)
		}
	} else if err := execClient.SendTransaction(ctx, signed); err != nil {
		return nil, fmt.Errorf("send flash loan tx failed: %w", err)
	}
	cc.nonceTracker.Confirm(from, nonce)

	return signed, nil
}

// buildFlashLoanCallData 构建 executeFlashLoan 调用数据
func (cc *ContractCaller) buildFlashLoanCallData(params *FlashLoanParams) ([]byte, error) {
	// 将 []uint32 转换为 []*big.Int
	feeTiersBig := make([]*big.Int, len(params.FeeTiers))
	for i, ft := range params.FeeTiers {
		feeTiersBig[i] = new(big.Int).SetUint64(uint64(ft))
	}

	return cc.flashLoanABI.Pack(
		"executeFlashLoan",
		params.Platform,
		params.TokenIn,
		params.AmountIn,
		params.SwapPath,
		params.Dexes,
		feeTiersBig,
		params.ExpectProfit,
		params.MinProfit,
	)
}

// RefreshNonce 刷新指定地址的 Nonce（交易失败后调用）
func (cc *ContractCaller) RefreshNonce(ctx context.Context, address common.Address) error {
	execClient := cc.web3Client.GetClient()
	if cc.execRPCClient != nil {
		execClient = cc.execRPCClient
	}
	return cc.nonceTracker.Refresh(ctx, execClient, address)
}
