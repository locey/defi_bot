// internal/liquidation/collector.go
// Aave V3 用户仓位采集器 — 通过 Multicall 批量查询健康因子
package liquidation

import (
	"context"
	"fmt"
	"math/big"
	"strings"
	"sync"
	"time"

	"github.com/defi-bot/backend/pkg/log"
	"github.com/defi-bot/backend/pkg/web3"
	"github.com/ethereum/go-ethereum"
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
)

// AaveCollector 采集 Aave V3 用户仓位
type AaveCollector struct {
	web3Client   *web3.Client
	poolAddress  common.Address
	dataProvider common.Address

	// 解析后的 ABI
	poolABI         abi.ABI
	dataProviderABI abi.ABI
	multicallABI    abi.ABI
	erc20ABI        abi.ABI

	// 已知的储备资产列表
	reserves   []ReserveInfo
	reservesMu sync.RWMutex

	// 已知的借款人地址（通过事件扫描或外部来源）
	borrowers   map[common.Address]bool
	borrowersMu sync.RWMutex

	// 缓存：用户健康因子
	positions   map[common.Address]*UserPosition
	positionsMu sync.RWMutex
}

// NewAaveCollector 创建采集器
func NewAaveCollector(
	web3Client *web3.Client,
	poolAddress common.Address,
	dataProvider common.Address,
) (*AaveCollector, error) {
	poolABI, err := abi.JSON(strings.NewReader(AaveV3PoolABI))
	if err != nil {
		return nil, fmt.Errorf("parse pool ABI: %w", err)
	}

	dpABI, err := abi.JSON(strings.NewReader(AaveV3DataProviderABI))
	if err != nil {
		return nil, fmt.Errorf("parse data provider ABI: %w", err)
	}

	// Multicall3 ABI
	mcABI, err := abi.JSON(strings.NewReader(`[{"inputs":[{"components":[{"name":"target","type":"address"},{"name":"allowFailure","type":"bool"},{"name":"callData","type":"bytes"}],"name":"calls","type":"tuple[]"}],"name":"aggregate3","outputs":[{"components":[{"name":"success","type":"bool"},{"name":"returnData","type":"bytes"}],"name":"returnData","type":"tuple[]"}],"stateMutability":"payable","type":"function"}]`))
	if err != nil {
		return nil, fmt.Errorf("parse multicall ABI: %w", err)
	}

	erc20ABI, err := abi.JSON(strings.NewReader(ERC20BalanceOfABI))
	if err != nil {
		return nil, fmt.Errorf("parse erc20 ABI: %w", err)
	}

	return &AaveCollector{
		web3Client:      web3Client,
		poolAddress:     poolAddress,
		dataProvider:    dataProvider,
		poolABI:         poolABI,
		dataProviderABI: dpABI,
		multicallABI:    mcABI,
		erc20ABI:        erc20ABI,
		borrowers:       make(map[common.Address]bool),
		positions:       make(map[common.Address]*UserPosition),
	}, nil
}

// FetchReserves 获取 Aave V3 所有储备资产列表
func (c *AaveCollector) FetchReserves(ctx context.Context) ([]ReserveInfo, error) {
	// 调用 getAllReservesTokens()
	callData, err := c.dataProviderABI.Pack("getAllReservesTokens")
	if err != nil {
		return nil, fmt.Errorf("pack getAllReservesTokens: %w", err)
	}

	result, err := c.web3Client.GetClient().CallContract(ctx, ethereum.CallMsg{
		To:   &c.dataProvider,
		Data: callData,
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("call getAllReservesTokens: %w", err)
	}

	// 解码结果
	type TokenData struct {
		Symbol       string
		TokenAddress common.Address
	}
	outputs, err := c.dataProviderABI.Unpack("getAllReservesTokens", result)
	if err != nil {
		return nil, fmt.Errorf("unpack getAllReservesTokens: %w", err)
	}

	// outputs[0] 是 []struct{Symbol string; TokenAddress common.Address}
	tokenDataSlice, ok := outputs[0].([]struct {
		Symbol       string         `json:"symbol"`
		TokenAddress common.Address `json:"tokenAddress"`
	})
	if !ok {
		return nil, fmt.Errorf("unexpected type for getAllReservesTokens result")
	}

	reserves := make([]ReserveInfo, 0, len(tokenDataSlice))
	for _, td := range tokenDataSlice {
		reserves = append(reserves, ReserveInfo{
			Asset:  td.TokenAddress,
			Symbol: td.Symbol,
		})
	}

	// 批量获取每个储备的配置（清算奖励等）
	if err := c.enrichReserveConfigs(ctx, reserves); err != nil {
		log.Strategy().Warn().Err(err).Msg("清算：获取储备配置失败（将使用默认值）")
	}

	c.reservesMu.Lock()
	c.reserves = reserves
	c.reservesMu.Unlock()

	log.Strategy().Info().Int("reserves", len(reserves)).Msg("清算：Aave V3 储备资产列表已更新")
	return reserves, nil
}

// enrichReserveConfigs 通过 Multicall 批量获取储备配置
func (c *AaveCollector) enrichReserveConfigs(ctx context.Context, reserves []ReserveInfo) error {
	if len(reserves) == 0 {
		return nil
	}

	multicallAddr := common.HexToAddress(web3.Multicall3Address)

	// 构建 multicall 批量调用：getReserveConfigurationData + getReserveTokensAddresses
	type Call3 struct {
		Target       common.Address
		AllowFailure bool
		CallData     []byte
	}

	calls := make([]Call3, 0, len(reserves)*2)
	for _, r := range reserves {
		// getReserveConfigurationData(asset)
		configData, _ := c.dataProviderABI.Pack("getReserveConfigurationData", r.Asset)
		calls = append(calls, Call3{Target: c.dataProvider, AllowFailure: true, CallData: configData})

		// getReserveTokensAddresses(asset)
		tokenData, _ := c.dataProviderABI.Pack("getReserveTokensAddresses", r.Asset)
		calls = append(calls, Call3{Target: c.dataProvider, AllowFailure: true, CallData: tokenData})
	}

	// ABI encode the multicall
	mcCallData, err := c.multicallABI.Pack("aggregate3", calls)
	if err != nil {
		return fmt.Errorf("pack multicall: %w", err)
	}

	result, err := c.web3Client.GetClient().CallContract(ctx, ethereum.CallMsg{
		To:   &multicallAddr,
		Data: mcCallData,
	}, nil)
	if err != nil {
		return fmt.Errorf("multicall reserves config: %w", err)
	}

	// 解码 multicall 结果
	mcOutputs, err := c.multicallABI.Unpack("aggregate3", result)
	if err != nil {
		return fmt.Errorf("unpack multicall: %w", err)
	}

	type Result struct {
		Success    bool
		ReturnData []byte
	}
	results, ok := mcOutputs[0].([]struct {
		Success    bool   `json:"success"`
		ReturnData []byte `json:"returnData"`
	})
	if !ok {
		return fmt.Errorf("unexpected multicall result type")
	}

	for i := range reserves {
		configIdx := i * 2
		tokenIdx := i*2 + 1

		if configIdx < len(results) && results[configIdx].Success {
			out, err := c.dataProviderABI.Unpack("getReserveConfigurationData", results[configIdx].ReturnData)
			if err == nil && len(out) >= 6 {
				decimals := out[0].(*big.Int)
				liqThreshold := out[2].(*big.Int)
				liqBonus := out[3].(*big.Int)
				isActive := out[8].(bool)

				reserves[i].Decimals = uint8(decimals.Uint64())
				reserves[i].LiquidationThreshold = uint16(liqThreshold.Uint64())
				reserves[i].LiquidationBonus = uint16(liqBonus.Uint64())
				reserves[i].IsActive = isActive
			}
		}

		if tokenIdx < len(results) && results[tokenIdx].Success {
			out, err := c.dataProviderABI.Unpack("getReserveTokensAddresses", results[tokenIdx].ReturnData)
			if err == nil && len(out) >= 3 {
				reserves[i].ATokenAddress = out[0].(common.Address)
				reserves[i].VariableDebtToken = out[2].(common.Address)
			}
		}
	}

	return nil
}

// AddBorrowers 添加借款人地址（从事件日志或外部来源）
func (c *AaveCollector) AddBorrowers(addrs []common.Address) {
	c.borrowersMu.Lock()
	defer c.borrowersMu.Unlock()
	for _, addr := range addrs {
		c.borrowers[addr] = true
	}
	log.Strategy().Info().Int("total_borrowers", len(c.borrowers)).Msg("清算：借款人列表已更新")
}

// ScanBorrowEvents 扫描 Aave V3 Borrow 事件，发现借款人
// Aave V3 Borrow event: event Borrow(address indexed reserve, address user, address indexed onBehalfOf, uint256 amount, ...)
func (c *AaveCollector) ScanBorrowEvents(ctx context.Context, fromBlock, toBlock uint64) ([]common.Address, error) {
	// Borrow event topic0 = keccak256("Borrow(address,address,address,uint256,uint8,uint256,uint16)")
	borrowTopic := common.HexToHash("0xb3d084820fb1a9decffb176436bd02558d15fac9b0ddfed8c465bc7359d7dce0")

	query := ethereum.FilterQuery{
		FromBlock: new(big.Int).SetUint64(fromBlock),
		ToBlock:   new(big.Int).SetUint64(toBlock),
		Addresses: []common.Address{c.poolAddress},
		Topics:    [][]common.Hash{{borrowTopic}},
	}

	logs, err := c.web3Client.GetClient().FilterLogs(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("filter borrow events: %w", err)
	}

	seen := make(map[common.Address]bool)
	var newBorrowers []common.Address

	c.borrowersMu.Lock()
	for _, l := range logs {
		// onBehalfOf is topics[2] (indexed parameter)
		if len(l.Topics) >= 3 {
			borrower := common.HexToAddress(l.Topics[2].Hex())
			if !c.borrowers[borrower] && !seen[borrower] {
				seen[borrower] = true
				newBorrowers = append(newBorrowers, borrower)
				c.borrowers[borrower] = true
			}
		}
	}
	c.borrowersMu.Unlock()

	log.Strategy().Info().
		Int("new_borrowers", len(newBorrowers)).
		Int("total_borrowers", len(c.borrowers)).
		Uint64("from_block", fromBlock).
		Uint64("to_block", toBlock).
		Msg("清算：Borrow 事件扫描完成")

	return newBorrowers, nil
}

// BatchCheckHealthFactors 批量查询用户健康因子（Multicall）
// 优先查上次发现的高风险用户，然后查其余用户
func (c *AaveCollector) BatchCheckHealthFactors(ctx context.Context) ([]*UserPosition, error) {
	c.borrowersMu.RLock()
	borrowers := make([]common.Address, 0, len(c.borrowers))
	// 优先查上次已知高风险的用户（HF < 1.15），放队列前面
	c.positionsMu.RLock()
	prioritySet := make(map[common.Address]bool)
	for addr, pos := range c.positions {
		if pos.TotalDebtBase.Sign() > 0 && pos.HealthFactorFloat() < 1.15 {
			borrowers = append(borrowers, addr)
			prioritySet[addr] = true
		}
	}
	c.positionsMu.RUnlock()
	// 其余用户追加到后面
	for addr := range c.borrowers {
		if !prioritySet[addr] {
			borrowers = append(borrowers, addr)
		}
	}
	c.borrowersMu.RUnlock()

	if len(borrowers) == 0 {
		return nil, nil
	}

	multicallAddr := common.HexToAddress(web3.Multicall3Address)

	// 分批处理（每批最多 100 个用户）
	const batchSize = 100
	var allPositions []*UserPosition

	for start := 0; start < len(borrowers); start += batchSize {
		end := start + batchSize
		if end > len(borrowers) {
			end = len(borrowers)
		}
		batch := borrowers[start:end]

		positions, err := c.batchCheckHealthFactorsBatch(ctx, multicallAddr, batch)
		if err != nil {
			log.Strategy().Warn().Err(err).
				Int("batch_start", start).
				Msg("清算：批量查询健康因子失败")
			continue
		}
		allPositions = append(allPositions, positions...)
	}

	// 更新缓存 + 清理零债务用户
	c.positionsMu.Lock()
	for _, p := range allPositions {
		c.positions[p.User] = p
	}
	c.positionsMu.Unlock()

	// 清理已还清债务的借款人（每轮检查后移除零债务用户）
	c.cleanupZeroDebtBorrowers(allPositions)

	return allPositions, nil
}

// cleanupZeroDebtBorrowers 移除已还清债务的用户，防止列表无限膨胀
func (c *AaveCollector) cleanupZeroDebtBorrowers(currentPositions []*UserPosition) {
	// 构建本轮有债务的用户集合
	hasDebt := make(map[common.Address]bool)
	for _, p := range currentPositions {
		hasDebt[p.User] = true
	}

	// 查上次缓存中有、但本轮已无债务的用户
	c.positionsMu.Lock()
	var removed int
	for addr, pos := range c.positions {
		// 如果在本轮查询范围内但没出现在有债务结果中，且上次检查超过 5 分钟，则移除
		if !hasDebt[addr] && time.Since(pos.LastChecked) > 5*time.Minute {
			delete(c.positions, addr)
			removed++
		}
	}
	c.positionsMu.Unlock()

	if removed > 0 {
		log.Strategy().Debug().Int("removed", removed).Msg("清算：清理零债务用户缓存")
	}
}

func (c *AaveCollector) batchCheckHealthFactorsBatch(
	ctx context.Context,
	multicallAddr common.Address,
	users []common.Address,
) ([]*UserPosition, error) {
	type Call3 struct {
		Target       common.Address
		AllowFailure bool
		CallData     []byte
	}

	calls := make([]Call3, 0, len(users))
	for _, user := range users {
		callData, _ := c.poolABI.Pack("getUserAccountData", user)
		calls = append(calls, Call3{
			Target:       c.poolAddress,
			AllowFailure: true,
			CallData:     callData,
		})
	}

	mcCallData, err := c.multicallABI.Pack("aggregate3", calls)
	if err != nil {
		return nil, fmt.Errorf("pack multicall: %w", err)
	}

	result, err := c.web3Client.GetClient().CallContract(ctx, ethereum.CallMsg{
		To:   &multicallAddr,
		Data: mcCallData,
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("multicall health factors: %w", err)
	}

	mcOutputs, err := c.multicallABI.Unpack("aggregate3", result)
	if err != nil {
		return nil, fmt.Errorf("unpack multicall: %w", err)
	}

	results, ok := mcOutputs[0].([]struct {
		Success    bool   `json:"success"`
		ReturnData []byte `json:"returnData"`
	})
	if !ok {
		return nil, fmt.Errorf("unexpected multicall result type")
	}

	now := time.Now()
	var positions []*UserPosition

	for i, r := range results {
		if !r.Success || len(r.ReturnData) == 0 {
			continue
		}

		out, err := c.poolABI.Unpack("getUserAccountData", r.ReturnData)
		if err != nil || len(out) < 6 {
			continue
		}

		pos := &UserPosition{
			User:                        users[i],
			TotalCollateralBase:         out[0].(*big.Int),
			TotalDebtBase:               out[1].(*big.Int),
			AvailableBorrowsBase:        out[2].(*big.Int),
			CurrentLiquidationThreshold: out[3].(*big.Int),
			Ltv:                         out[4].(*big.Int),
			HealthFactor:                out[5].(*big.Int),
			LastChecked:                 now,
		}

		// 只保留有债务的用户
		if pos.TotalDebtBase.Sign() > 0 {
			positions = append(positions, pos)
		}
	}

	return positions, nil
}

// GetLiquidatablePositions 返回可清算的仓位（healthFactor < 1）
func (c *AaveCollector) GetLiquidatablePositions() []*UserPosition {
	c.positionsMu.RLock()
	defer c.positionsMu.RUnlock()

	var liquidatable []*UserPosition
	for _, p := range c.positions {
		if p.IsLiquidatable() {
			liquidatable = append(liquidatable, p)
		}
	}
	return liquidatable
}

// GetAtRiskPositions 返回高风险仓位（healthFactor < threshold）
func (c *AaveCollector) GetAtRiskPositions(threshold float64) []*UserPosition {
	c.positionsMu.RLock()
	defer c.positionsMu.RUnlock()

	var atRisk []*UserPosition
	for _, p := range c.positions {
		if p.TotalDebtBase.Sign() > 0 && p.HealthFactorFloat() < threshold {
			atRisk = append(atRisk, p)
		}
	}
	return atRisk
}

// GetReserves 获取已缓存的储备资产列表
func (c *AaveCollector) GetReserves() []ReserveInfo {
	c.reservesMu.RLock()
	defer c.reservesMu.RUnlock()
	result := make([]ReserveInfo, len(c.reserves))
	copy(result, c.reserves)
	return result
}

// GetBorrowerCount 返回已知借款人数量
func (c *AaveCollector) GetBorrowerCount() int {
	c.borrowersMu.RLock()
	defer c.borrowersMu.RUnlock()
	return len(c.borrowers)
}

// FetchUserReserveData 获取用户在特定资产上的仓位详情
func (c *AaveCollector) FetchUserReserveData(ctx context.Context, user common.Address, assets []common.Address) ([]UserReserveData, error) {
	multicallAddr := common.HexToAddress(web3.Multicall3Address)

	type Call3 struct {
		Target       common.Address
		AllowFailure bool
		CallData     []byte
	}

	calls := make([]Call3, 0, len(assets))
	for _, asset := range assets {
		callData, _ := c.dataProviderABI.Pack("getUserReserveData", asset, user)
		calls = append(calls, Call3{
			Target:       c.dataProvider,
			AllowFailure: true,
			CallData:     callData,
		})
	}

	mcCallData, err := c.multicallABI.Pack("aggregate3", calls)
	if err != nil {
		return nil, fmt.Errorf("pack multicall: %w", err)
	}

	result, err := c.web3Client.GetClient().CallContract(ctx, ethereum.CallMsg{
		To:   &multicallAddr,
		Data: mcCallData,
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("multicall user reserve data: %w", err)
	}

	mcOutputs, err := c.multicallABI.Unpack("aggregate3", result)
	if err != nil {
		return nil, fmt.Errorf("unpack multicall: %w", err)
	}

	results, ok := mcOutputs[0].([]struct {
		Success    bool   `json:"success"`
		ReturnData []byte `json:"returnData"`
	})
	if !ok {
		return nil, fmt.Errorf("unexpected multicall result type")
	}

	var userData []UserReserveData
	for i, r := range results {
		if !r.Success || len(r.ReturnData) == 0 {
			continue
		}

		out, err := c.dataProviderABI.Unpack("getUserReserveData", r.ReturnData)
		if err != nil || len(out) < 3 {
			continue
		}

		aTokenBal := out[0].(*big.Int)
		stableDebt := out[1].(*big.Int)
		variableDebt := out[2].(*big.Int)

		// 只保留有余额或有债务的记录
		if aTokenBal.Sign() > 0 || stableDebt.Sign() > 0 || variableDebt.Sign() > 0 {
			userData = append(userData, UserReserveData{
				User:                user,
				Asset:               assets[i],
				ATokenBalance:       aTokenBal,
				StableDebtBalance:   stableDebt,
				VariableDebtBalance: variableDebt,
			})
		}
	}

	return userData, nil
}
