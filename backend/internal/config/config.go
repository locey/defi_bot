package config

import (
	"fmt"
	"log"
	"strings"

	"github.com/spf13/viper"
)

// Config 全局配置结构
type Config struct {
	Database   DatabaseConfig   `mapstructure:"database"`
	Blockchain BlockchainConfig `mapstructure:"blockchain"`
	Contracts  ContractsConfig  `mapstructure:"contracts"`
	Dexes      []DexConfig      `mapstructure:"dexes"`
	Tokens     []TokenConfig    `mapstructure:"tokens"`
	Scheduler  SchedulerConfig  `mapstructure:"scheduler"`
	Arbitrage  ArbitrageConfig  `mapstructure:"arbitrage"`
	Log        LogConfig        `mapstructure:"log"`
	Server     ServerConfig     `mapstructure:"server"`
	Redis      RedisConfig      `mapstructure:"redis"`
}

// DatabaseConfig 数据库配置
type DatabaseConfig struct {
	Host            string `mapstructure:"host"`
	Port            int    `mapstructure:"port"`
	User            string `mapstructure:"user"`
	Password        string `mapstructure:"password"`
	DBName          string `mapstructure:"dbname"`
	SSLMode         string `mapstructure:"sslmode"`
	Timezone        string `mapstructure:"timezone"`
	MaxIdleConns    int    `mapstructure:"max_idle_conns"`
	MaxOpenConns    int    `mapstructure:"max_open_conns"`
	ConnMaxLifetime int    `mapstructure:"conn_max_lifetime"`
}

// BlockchainConfig 区块链配置
type BlockchainConfig struct {
	RPCURL  string   `mapstructure:"rpc_url"`  // 主 RPC URL（向后兼容）
	RPCURLs []string `mapstructure:"rpc_urls"` // 多个 RPC URL（用于负载均衡）
	ChainID int64    `mapstructure:"chain_id"`
	Timeout int      `mapstructure:"timeout"`
	Retry   int      `mapstructure:"retry"`
	UsePool bool     `mapstructure:"use_pool"` // 是否使用 RPC 池
}

// ContractsConfig 合约配置
type ContractsConfig struct {
	ArbitrageCore string `mapstructure:"arbitrage_core"`
	ConfigManager string `mapstructure:"config_manager"`
}

// DexConfig DEX 配置
type DexConfig struct {
	Name     string `mapstructure:"name"`
	Protocol string `mapstructure:"protocol"` // 协议类型：uniswap_v2, uniswap_v3, sushiswap 等
	Router   string `mapstructure:"router"`
	Factory  string `mapstructure:"factory"`
	Fee      int    `mapstructure:"fee"`
	FeeTier  uint32 `mapstructure:"fee_tier"` // V3 费率层级（如 500, 3000, 10000），V2 为 0
	Version  string `mapstructure:"version"`  // 版本，如 v2, v3
	ChainID  int64  `mapstructure:"chain_id"` // 链 ID
}

// TokenConfig 代币配置
type TokenConfig struct {
	Symbol   string `mapstructure:"symbol"`
	Address  string `mapstructure:"address"`
	Decimals int    `mapstructure:"decimals"`
}

// SchedulerConfig 定时任务配置
type SchedulerConfig struct {
	CollectInterval int `mapstructure:"collect_interval"`
	AnalyzeInterval int `mapstructure:"analyze_interval"`
	CleanupInterval int `mapstructure:"cleanup_interval"`
}

// ArbitrageConfig 套利配置
type ArbitrageConfig struct {
	MinProfitRate float64 `mapstructure:"min_profit_rate"`
	MaxSlippage   float64 `mapstructure:"max_slippage"`
	MaxGasPrice   int64   `mapstructure:"max_gas_price"`
}

// LogConfig 日志配置
type LogConfig struct {
	Level      string `mapstructure:"level"`
	File       string `mapstructure:"file"`
	MaxSize    int    `mapstructure:"max_size"`
	MaxBackups int    `mapstructure:"max_backups"`
	MaxAge     int    `mapstructure:"max_age"`
}

// ServerConfig 服务器配置
type ServerConfig struct {
	Port int    `mapstructure:"port"`
	Mode string `mapstructure:"mode"`
}

// RedisConfig Redis 配置
type RedisConfig struct {
	Enabled  bool   `mapstructure:"enabled"` // 是否启用 Redis
	Host     string `mapstructure:"host"`
	Port     int    `mapstructure:"port"`
	Password string `mapstructure:"password"`
	DB       int    `mapstructure:"db"`
	TTL      int    `mapstructure:"ttl"` // 默认过期时间（秒）
}

var globalConfig *Config

// LoadConfig 加载配置文件
func LoadConfig(configPath string) (*Config, error) {
	viper.SetConfigFile(configPath)
	viper.SetConfigType("yaml")

	// 自动读取环境变量
	viper.AutomaticEnv()
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

	if err := viper.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("读取配置文件失败: %w", err)
	}

	var config Config
	if err := viper.Unmarshal(&config); err != nil {
		return nil, fmt.Errorf("解析配置文件失败: %w", err)
	}

	globalConfig = &config
	log.Printf("配置加载成功: %s", configPath)
	return &config, nil
}

// GetConfig 获取全局配置
func GetConfig() *Config {
	if globalConfig == nil {
		log.Fatal("配置未初始化，请先调用 LoadConfig")
	}
	return globalConfig
}

// GetDSN 获取数据库连接字符串
func (c *DatabaseConfig) GetDSN() string {
	return fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=%s TimeZone=%s",
		c.Host, c.Port, c.User, c.Password, c.DBName, c.SSLMode, c.Timezone,
	)
}
