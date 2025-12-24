package cache

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/redis/go-redis/v9"
)

// RedisCache Redis 缓存客户端
type RedisCache struct {
	client *redis.Client
	ctx    context.Context
}

// RedisConfig Redis 配置
type RedisConfig struct {
	Host     string
	Port     int
	Password string
	DB       int
	TTL      time.Duration // 默认过期时间
}

// NewRedisCache 创建 Redis 缓存客户端
func NewRedisCache(config *RedisConfig) (*RedisCache, error) {
	client := redis.NewClient(&redis.Options{
		Addr:     fmt.Sprintf("%s:%d", config.Host, config.Port),
		Password: config.Password,
		DB:       config.DB,
	})

	ctx := context.Background()

	// 测试连接
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("Redis 连接失败: %w", err)
	}

	log.Println("✅ Redis 连接成功")
	return &RedisCache{
		client: client,
		ctx:    ctx,
	}, nil
}

// Set 设置缓存
func (c *RedisCache) Set(key string, value interface{}, ttl time.Duration) error {
	// 序列化为 JSON
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Errorf("序列化失败: %w", err)
	}

	// 设置缓存
	return c.client.Set(c.ctx, key, data, ttl).Err()
}

// Get 获取缓存
func (c *RedisCache) Get(key string, dest interface{}) error {
	// 获取缓存
	data, err := c.client.Get(c.ctx, key).Bytes()
	if err != nil {
		if err == redis.Nil {
			return fmt.Errorf("缓存不存在")
		}
		return fmt.Errorf("获取缓存失败: %w", err)
	}

	// 反序列化
	if err := json.Unmarshal(data, dest); err != nil {
		return fmt.Errorf("反序列化失败: %w", err)
	}

	return nil
}

// Delete 删除缓存
func (c *RedisCache) Delete(key string) error {
	return c.client.Del(c.ctx, key).Err()
}

// Exists 检查缓存是否存在
func (c *RedisCache) Exists(key string) (bool, error) {
	result, err := c.client.Exists(c.ctx, key).Result()
	if err != nil {
		return false, err
	}
	return result > 0, nil
}

// SetNX 仅当键不存在时设置（用于分布式锁）
func (c *RedisCache) SetNX(key string, value interface{}, ttl time.Duration) (bool, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return false, fmt.Errorf("序列化失败: %w", err)
	}

	return c.client.SetNX(c.ctx, key, data, ttl).Result()
}

// Expire 设置过期时间
func (c *RedisCache) Expire(key string, ttl time.Duration) error {
	return c.client.Expire(c.ctx, key, ttl).Err()
}

// TTL 获取剩余过期时间
func (c *RedisCache) TTL(key string) (time.Duration, error) {
	return c.client.TTL(c.ctx, key).Result()
}

// GetMulti 批量获取缓存（简化版本）
func (c *RedisCache) GetMulti(keys []string) (map[string]string, error) {
	result := make(map[string]string)
	for _, key := range keys {
		val, err := c.client.Get(c.ctx, key).Result()
		if err == nil {
			result[key] = val
		}
	}
	return result, nil
}

// SetMulti 批量设置缓存（简化版本）
func (c *RedisCache) SetMulti(items map[string]string, ttl time.Duration) error {
	for key, value := range items {
		if err := c.client.Set(c.ctx, key, value, ttl).Err(); err != nil {
			return err
		}
	}
	return nil
}

// DeletePattern 根据模式删除缓存
func (c *RedisCache) DeletePattern(pattern string) error {
	iter := c.client.Scan(c.ctx, 0, pattern, 0).Iterator()

	keys := make([]string, 0)
	for iter.Next(c.ctx) {
		keys = append(keys, iter.Val())
	}

	if err := iter.Err(); err != nil {
		return fmt.Errorf("扫描失败: %w", err)
	}

	if len(keys) > 0 {
		return c.client.Del(c.ctx, keys...).Err()
	}

	return nil
}

// FlushDB 清空当前数据库
func (c *RedisCache) FlushDB() error {
	return c.client.FlushDB(c.ctx).Err()
}

// Close 关闭连接
func (c *RedisCache) Close() error {
	log.Println("关闭 Redis 连接")
	return c.client.Close()
}

// GetStats 获取统计信息
func (c *RedisCache) GetStats() (map[string]string, error) {
	info, err := c.client.Info(c.ctx, "stats").Result()
	if err != nil {
		return nil, err
	}

	stats := make(map[string]string)
	stats["info"] = info

	// 获取键数量
	dbSize, err := c.client.DBSize(c.ctx).Result()
	if err != nil {
		return nil, err
	}
	stats["keys"] = fmt.Sprintf("%d", dbSize)

	return stats, nil
}

// CachedPriceData 缓存的价格数据结构
type CachedPriceData struct {
	PairAddress  string    `json:"pair_address"`
	Token0Symbol string    `json:"token0_symbol"`
	Token1Symbol string    `json:"token1_symbol"`
	DexName      string    `json:"dex_name"`
	Reserve0     string    `json:"reserve0"`
	Reserve1     string    `json:"reserve1"`
	Price        string    `json:"price"`
	InversePrice string    `json:"inverse_price"`
	Timestamp    time.Time `json:"timestamp"`
}

// GetPriceKey 生成价格缓存键
func GetPriceKey(pairAddress string) string {
	return fmt.Sprintf("price:%s", pairAddress)
}

// GetPairsKey 生成交易对列表缓存键
func GetPairsKey(dexName string) string {
	return fmt.Sprintf("pairs:%s", dexName)
}

// GetTokenKey 生成代币信息缓存键
func GetTokenKey(tokenAddress string) string {
	return fmt.Sprintf("token:%s", tokenAddress)
}
