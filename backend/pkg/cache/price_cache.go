// pkg/cache/price_cache.go
// 高性能内存价格缓存，用于实时套利检测
package cache

import (
	"math"
	"math/big"
	"strings"
	"sync"
	"time"

	"github.com/defi-bot/backend/pkg/log"
	"github.com/ethereum/go-ethereum/common"
)

// PoolPrice 池子价格信息
type PoolPrice struct {
	PoolAddress string         `json:"pool_address"`
	Token0      common.Address `json:"token0"`
	Token1      common.Address `json:"token1"`
	Reserve0    *big.Int       `json:"reserve0"`
	Reserve1    *big.Int       `json:"reserve1"`
	
	// 精度信息（业界标准：必须包含）
	Decimals0   uint8          `json:"decimals0"` // Token0 精度
	Decimals1   uint8          `json:"decimals1"` // Token1 精度
	
	// V3 特有字段
	SqrtPriceX96 *big.Int      `json:"sqrt_price_x96,omitempty"` // V3: sqrt(price) * 2^96
	Liquidity    *big.Int      `json:"liquidity,omitempty"`      // V3: 当前活跃流动性
	IsV3         bool          `json:"is_v3"`                    // 是否为 V3 池子
	
	Price       float64        `json:"price"` // Token1/Token0
	DexName     string         `json:"dex_name"`
	Protocol    string         `json:"protocol"`
	Fee         uint64         `json:"fee"` // basis points
	UpdatedAt   time.Time      `json:"updated_at"`
	BlockNumber uint64         `json:"block_number"`
}

// PriceChangeEvent 价格变化事件
type PriceChangeEvent struct {
	PoolAddress string
	Token0      common.Address
	Token1      common.Address
	OldPrice    float64
	NewPrice    float64
	OldReserve0 *big.Int
	OldReserve1 *big.Int
	NewReserve0 *big.Int
	NewReserve1 *big.Int
	ChangeRate  float64 // 价格变化率 (newPrice - oldPrice) / oldPrice
	BlockNumber uint64
	Timestamp   time.Time
}

// PriceCache 高性能内存价格缓存
type PriceCache struct {
	// 核心存储 (使用sync.Map实现无锁读取)
	prices sync.Map // poolAddr -> *PoolPrice

	// 索引 (需要锁保护，但只在写入时)
	indexMu     sync.RWMutex
	byToken     map[string][]string // tokenAddr -> []poolAddr
	byTokenPair map[string][]string // "token0-token1" -> []poolAddr (sorted)

	// 事件订阅
	subMu       sync.RWMutex
	subscribers []chan PriceChangeEvent

	// 配置
	changeThreshold float64 // 价格变化阈值，超过才触发事件 (如0.0001 = 0.01%)
	bufferSize      int     // subscriber channel buffer size

	// 统计
	updateCount   uint64
	eventCount    uint64
	lastUpdateAt  time.Time
	statsMu       sync.RWMutex
}

// NewPriceCache 创建新的价格缓存
func NewPriceCache(changeThreshold float64, bufferSize int) *PriceCache {
	if changeThreshold <= 0 {
		changeThreshold = 0.0001 // 默认0.01%
	}
	if bufferSize <= 0 {
		bufferSize = 1000
	}

	return &PriceCache{
		byToken:         make(map[string][]string),
		byTokenPair:     make(map[string][]string),
		subscribers:     make([]chan PriceChangeEvent, 0),
		changeThreshold: changeThreshold,
		bufferSize:      bufferSize,
	}
}

// Update 更新池子价格（核心方法，高频调用需高性能）
func (c *PriceCache) Update(poolAddr string, reserve0, reserve1 *big.Int, blockNumber uint64) {
	// 获取旧价格
	var oldPrice *PoolPrice
	if old, ok := c.prices.Load(poolAddr); ok {
		oldPrice = old.(*PoolPrice)
	}

	// 计算原始价格 (reserve1/reserve0)
	rawPrice := calculatePrice(reserve0, reserve1)

	// 创建新价格对象
	now := time.Now()
	newPrice := &PoolPrice{
		PoolAddress: poolAddr,
		Reserve0:    new(big.Int).Set(reserve0),
		Reserve1:    new(big.Int).Set(reserve1),
		Price:       rawPrice,
		UpdatedAt:   now,
		BlockNumber: blockNumber,
	}

	// 如果有旧数据，保留token信息和decimals
	if oldPrice != nil {
		newPrice.Token0 = oldPrice.Token0
		newPrice.Token1 = oldPrice.Token1
		newPrice.DexName = oldPrice.DexName
		newPrice.Protocol = oldPrice.Protocol
		newPrice.Fee = oldPrice.Fee
		newPrice.Decimals0 = oldPrice.Decimals0
		newPrice.Decimals1 = oldPrice.Decimals1

		// 如果有 decimals 信息，调整价格
		// V2 价格 = reserve1/reserve0（原始单位）
		// 真实价格 = rawPrice * 10^(decimals0 - decimals1)
		if oldPrice.Decimals0 > 0 || oldPrice.Decimals1 > 0 {
			newPrice.Price = adjustPriceByDecimals(rawPrice, oldPrice.Decimals0, oldPrice.Decimals1)
		}
	}

	newPriceFloat := newPrice.Price

	// 存储新价格 (无锁写入)
	c.prices.Store(poolAddr, newPrice)

	// 更新统计
	c.statsMu.Lock()
	c.updateCount++
	c.lastUpdateAt = now
	c.statsMu.Unlock()

	// 计算价格变化率并决定是否触发事件
	if oldPrice != nil && oldPrice.Price > 0 {
		changeRate := (newPriceFloat - oldPrice.Price) / oldPrice.Price

		// 只有变化超过阈值才触发事件
		if math.Abs(changeRate) >= c.changeThreshold {
			event := PriceChangeEvent{
				PoolAddress: poolAddr,
				Token0:      newPrice.Token0,
				Token1:      newPrice.Token1,
				OldPrice:    oldPrice.Price,
				NewPrice:    newPriceFloat,
				OldReserve0: oldPrice.Reserve0,
				OldReserve1: oldPrice.Reserve1,
				NewReserve0: reserve0,
				NewReserve1: reserve1,
				ChangeRate:  changeRate,
				BlockNumber: blockNumber,
				Timestamp:   now,
			}
			c.notifySubscribers(event)
		}
	}
}

// UpdateV3 更新 V3 池子价格（使用 sqrtPriceX96）
func (c *PriceCache) UpdateV3(poolAddr string, sqrtPriceX96, liquidity *big.Int, blockNumber uint64) {
	// 获取旧价格
	var oldPrice *PoolPrice
	if old, ok := c.prices.Load(poolAddr); ok {
		oldPrice = old.(*PoolPrice)
	}
	
	// 计算价格 (从 sqrtPriceX96)
	newPriceFloat := calculatePriceFromSqrtX96(sqrtPriceX96)
	

	// 创建新价格对象
	now := time.Now()
	newPrice := &PoolPrice{
		PoolAddress:  poolAddr,
		SqrtPriceX96: new(big.Int).Set(sqrtPriceX96),
		Liquidity:    liquidity,
		IsV3:         true,
		Price:        newPriceFloat,
		UpdatedAt:    now,
		BlockNumber:  blockNumber,
	}

	// 如果有旧数据，保留 token 信息和 decimals
	if oldPrice != nil {
		newPrice.Token0 = oldPrice.Token0
		newPrice.Token1 = oldPrice.Token1
		newPrice.DexName = oldPrice.DexName
		newPrice.Protocol = oldPrice.Protocol
		newPrice.Fee = oldPrice.Fee
		newPrice.Decimals0 = oldPrice.Decimals0
		newPrice.Decimals1 = oldPrice.Decimals1

		// 如果有 decimals 信息，重新计算价格
		if oldPrice.Decimals0 > 0 || oldPrice.Decimals1 > 0 {
			// V3 合约按地址排序：小地址是 token0，大地址是 token1
			// sqrtPriceX96 给出的是 (token1_contract / token0_contract)
			// 数据库的 token 顺序可能与合约不同
			dbToken0Hex := strings.ToLower(oldPrice.Token0.Hex())
			dbToken1Hex := strings.ToLower(oldPrice.Token1.Hex())
			
			// 确定合约实际的 decimals（按地址排序）
			contractDec0, contractDec1 := oldPrice.Decimals0, oldPrice.Decimals1
			dbOrderMatchesContract := dbToken0Hex < dbToken1Hex
			
			if !dbOrderMatchesContract {
				// 数据库顺序与合约相反，交换 decimals
				contractDec0, contractDec1 = oldPrice.Decimals1, oldPrice.Decimals0
			}
			
			// 计算合约顺序的价格：token1_contract_real / token0_contract_real
			contractPrice := calculatePriceFromSqrtX96WithDecimals(sqrtPriceX96, contractDec0, contractDec1)
			
			// 转换为数据库顺序的价格：db_token0_real / db_token1_real
			// 与 V2 的 adjustPriceByDecimals 保持一致
			if dbOrderMatchesContract {
				// 数据库顺序与合约相同
				// contract_token1/contract_token0 = db_token1/db_token0
				// 需要 db_token0/db_token1 = 1 / contractPrice
				if contractPrice > 0 {
					newPriceFloat = 1.0 / contractPrice
				}
			} else {
				// 数据库顺序与合约相反
				// contract_token1/contract_token0 = db_token0/db_token1
				// 这正是我们需要的
				newPriceFloat = contractPrice
			}
			
			newPrice.Price = newPriceFloat
		}
	}

	// 存储新价格
	c.prices.Store(poolAddr, newPrice)

	// 更新统计
	c.statsMu.Lock()
	c.updateCount++
	c.lastUpdateAt = now
	c.statsMu.Unlock()

	// 计算价格变化率并决定是否触发事件
	if oldPrice != nil && oldPrice.Price > 0 {
		changeRate := (newPriceFloat - oldPrice.Price) / oldPrice.Price

		if math.Abs(changeRate) >= c.changeThreshold {
			event := PriceChangeEvent{
				PoolAddress: poolAddr,
				Token0:      newPrice.Token0,
				Token1:      newPrice.Token1,
				OldPrice:    oldPrice.Price,
				NewPrice:    newPriceFloat,
				OldReserve0: oldPrice.Reserve0,
				OldReserve1: oldPrice.Reserve1,
				NewReserve0: nil,
				NewReserve1: nil,
				ChangeRate:  changeRate,
				BlockNumber: blockNumber,
				Timestamp:   now,
			}
			c.notifySubscribers(event)
		}
	}
}

// UpdateWithMetadata 更新池子价格（包含完整元数据）
func (c *PriceCache) UpdateWithMetadata(price *PoolPrice) {
	poolAddr := price.PoolAddress

	// 获取旧价格
	var oldPrice *PoolPrice
	if old, ok := c.prices.Load(poolAddr); ok {
		oldPrice = old.(*PoolPrice)
	}

	// 计算价格
	if price.Price == 0 && price.Reserve0 != nil && price.Reserve1 != nil {
		price.Price = calculatePrice(price.Reserve0, price.Reserve1)
	}

	price.UpdatedAt = time.Now()

	// 存储
	c.prices.Store(poolAddr, price)

	// 更新索引
	c.updateIndex(price)

	// 更新统计
	c.statsMu.Lock()
	c.updateCount++
	c.lastUpdateAt = price.UpdatedAt
	c.statsMu.Unlock()

	// 触发事件
	if oldPrice != nil && oldPrice.Price > 0 {
		changeRate := (price.Price - oldPrice.Price) / oldPrice.Price
		if math.Abs(changeRate) >= c.changeThreshold {
			event := PriceChangeEvent{
				PoolAddress: poolAddr,
				Token0:      price.Token0,
				Token1:      price.Token1,
				OldPrice:    oldPrice.Price,
				NewPrice:    price.Price,
				OldReserve0: oldPrice.Reserve0,
				OldReserve1: oldPrice.Reserve1,
				NewReserve0: price.Reserve0,
				NewReserve1: price.Reserve1,
				ChangeRate:  changeRate,
				BlockNumber: price.BlockNumber,
				Timestamp:   price.UpdatedAt,
			}
			c.notifySubscribers(event)
		}
	}
}

// Get 获取单个池子价格
func (c *PriceCache) Get(poolAddr string) (*PoolPrice, bool) {
	if v, ok := c.prices.Load(poolAddr); ok {
		return v.(*PoolPrice), true
	}
	return nil, false
}

// GetByToken 获取包含某Token的所有池子
func (c *PriceCache) GetByToken(tokenAddr string) []*PoolPrice {
	c.indexMu.RLock()
	poolAddrs := c.byToken[tokenAddr]
	c.indexMu.RUnlock()

	result := make([]*PoolPrice, 0, len(poolAddrs))
	for _, addr := range poolAddrs {
		if price, ok := c.Get(addr); ok {
			result = append(result, price)
		}
	}
	return result
}

// GetByTokenPair 获取某交易对的所有池子（跨DEX）
func (c *PriceCache) GetByTokenPair(token0, token1 string) []*PoolPrice {
	// 标准化key (按字母顺序)
	key := makeTokenPairKey(token0, token1)

	c.indexMu.RLock()
	poolAddrs := c.byTokenPair[key]
	c.indexMu.RUnlock()

	result := make([]*PoolPrice, 0, len(poolAddrs))
	for _, addr := range poolAddrs {
		if price, ok := c.Get(addr); ok {
			result = append(result, price)
		}
	}
	return result
}

// GetAll 获取所有价格
func (c *PriceCache) GetAll() []*PoolPrice {
	result := make([]*PoolPrice, 0)
	c.prices.Range(func(key, value interface{}) bool {
		result = append(result, value.(*PoolPrice))
		return true
	})
	return result
}

// Subscribe 订阅价格变化事件
func (c *PriceCache) Subscribe() <-chan PriceChangeEvent {
	c.subMu.Lock()
	defer c.subMu.Unlock()

	ch := make(chan PriceChangeEvent, c.bufferSize)
	c.subscribers = append(c.subscribers, ch)

	log.Cache().Debug().Int("total", len(c.subscribers)).Msg("PriceCache: New subscriber added")
	return ch
}

// Unsubscribe 取消订阅
func (c *PriceCache) Unsubscribe(ch <-chan PriceChangeEvent) {
	c.subMu.Lock()
	defer c.subMu.Unlock()

	for i, sub := range c.subscribers {
		if sub == ch {
			c.subscribers = append(c.subscribers[:i], c.subscribers[i+1:]...)
			close(sub)
			break
		}
	}
}

// notifySubscribers 通知所有订阅者
func (c *PriceCache) notifySubscribers(event PriceChangeEvent) {
	c.subMu.RLock()
	defer c.subMu.RUnlock()

	c.statsMu.Lock()
	c.eventCount++
	c.statsMu.Unlock()

	for _, ch := range c.subscribers {
		select {
		case ch <- event:
			// 成功发送
		default:
			// 通道满，跳过（避免阻塞）
			log.Cache().Warn().Str("pool", event.PoolAddress).Msg("PriceCache: Subscriber channel full, skipping event")
		}
	}
}

// BuildIndex 构建索引（初始化时调用）
func (c *PriceCache) BuildIndex(pools []*PoolPrice) {
	c.indexMu.Lock()
	defer c.indexMu.Unlock()

	// 清空旧索引
	c.byToken = make(map[string][]string)
	c.byTokenPair = make(map[string][]string)

	for _, pool := range pools {
		// 存储价格
		c.prices.Store(pool.PoolAddress, pool)

		// 更新索引
		c.addToIndex(pool)
	}

	log.Cache().Info().Int("pools", len(pools)).Int("tokens", len(c.byToken)).Int("pairs", len(c.byTokenPair)).Msg("PriceCache: Built index")
}

// updateIndex 更新单个池子的索引
func (c *PriceCache) updateIndex(pool *PoolPrice) {
	c.indexMu.Lock()
	defer c.indexMu.Unlock()
	c.addToIndex(pool)
}

// addToIndex 添加到索引（需要持有锁）
func (c *PriceCache) addToIndex(pool *PoolPrice) {
	token0 := pool.Token0.Hex()
	token1 := pool.Token1.Hex()
	poolAddr := pool.PoolAddress

	// byToken索引
	if !containsString(c.byToken[token0], poolAddr) {
		c.byToken[token0] = append(c.byToken[token0], poolAddr)
	}
	if !containsString(c.byToken[token1], poolAddr) {
		c.byToken[token1] = append(c.byToken[token1], poolAddr)
	}

	// byTokenPair索引
	key := makeTokenPairKey(token0, token1)
	if !containsString(c.byTokenPair[key], poolAddr) {
		c.byTokenPair[key] = append(c.byTokenPair[key], poolAddr)
	}
}

// Stats 获取统计信息
func (c *PriceCache) Stats() (updateCount, eventCount uint64, poolCount int, lastUpdate time.Time) {
	c.statsMu.RLock()
	updateCount = c.updateCount
	eventCount = c.eventCount
	lastUpdate = c.lastUpdateAt
	c.statsMu.RUnlock()

	// 统计池子数量
	c.prices.Range(func(key, value interface{}) bool {
		poolCount++
		return true
	})

	return
}

// ============================================================
// 辅助函数
// ============================================================

// calculatePrice 计算价格 (Token1/Token0) - 原始单位，不考虑 decimals
func calculatePrice(reserve0, reserve1 *big.Int) float64 {
	if reserve0 == nil || reserve1 == nil || reserve0.Sign() == 0 {
		return 0
	}

	// 使用big.Float提高精度
	r0 := new(big.Float).SetInt(reserve0)
	r1 := new(big.Float).SetInt(reserve1)

	price := new(big.Float).Quo(r1, r0)
	result, _ := price.Float64()

	return result
}

// adjustPriceByDecimals 根据 decimals 调整价格
// 原始价格是 token1_raw / token0_raw
// 真实价格 = rawPrice * 10^(decimals0 - decimals1)
func adjustPriceByDecimals(rawPrice float64, decimals0, decimals1 uint8) float64 {
	if rawPrice == 0 {
		return 0
	}

	decimalDiff := int(decimals0) - int(decimals1)

	if decimalDiff > 0 {
		for i := 0; i < decimalDiff; i++ {
			rawPrice *= 10
		}
	} else if decimalDiff < 0 {
		for i := 0; i < -decimalDiff; i++ {
			rawPrice /= 10
		}
	}

	return rawPrice
}

// makeTokenPairKey 生成交易对key（按字母顺序，统一使用小写以避免大小写不一致问题）
func makeTokenPairKey(token0, token1 string) string {
	t0 := strings.ToLower(token0)
	t1 := strings.ToLower(token1)
	if t0 < t1 {
		return t0 + "-" + t1
	}
	return t1 + "-" + t0
}

// containsString 检查slice是否包含字符串
func containsString(slice []string, s string) bool {
	for _, v := range slice {
		if v == s {
			return true
		}
	}
	return false
}

// calculatePriceFromSqrtX96 从 sqrtPriceX96 计算价格
// V3 价格公式: price = (sqrtPriceX96 / 2^96)^2 = sqrtPriceX96^2 / 2^192
func calculatePriceFromSqrtX96(sqrtPriceX96 *big.Int) float64 {
	if sqrtPriceX96 == nil || sqrtPriceX96.Sign() == 0 {
		return 0
	}

	// sqrtPriceX96^2
	sqrtPrice2 := new(big.Int).Mul(sqrtPriceX96, sqrtPriceX96)

	// 2^192
	q192 := new(big.Int).Exp(big.NewInt(2), big.NewInt(192), nil)

	// 为了保持精度，使用 big.Float
	price := new(big.Float).SetInt(sqrtPrice2)
	divisor := new(big.Float).SetInt(q192)
	price.Quo(price, divisor)

	result, _ := price.Float64()
	return result
}

// calculatePriceFromSqrtX96WithDecimals 从 sqrtPriceX96 计算价格（带 decimals 调整）
func calculatePriceFromSqrtX96WithDecimals(sqrtPriceX96 *big.Int, decimals0, decimals1 uint8) float64 {
	basePrice := calculatePriceFromSqrtX96(sqrtPriceX96)
	if basePrice == 0 {
		return 0
	}

	// 调整 decimals
	// V3 原始价格是 token1/token0，需要根据 decimals 调整
	// 如果 token0 有 18 decimals，token1 有 6 decimals
	// 实际价格 = rawPrice * 10^(decimals0 - decimals1)
	decimalDiff := int(decimals0) - int(decimals1)
	
	if decimalDiff > 0 {
		for i := 0; i < decimalDiff; i++ {
			basePrice *= 10
		}
	} else if decimalDiff < 0 {
		for i := 0; i < -decimalDiff; i++ {
			basePrice /= 10
		}
	}

	return basePrice
}
