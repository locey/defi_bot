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
	Keeper     KeeperConfig     `mapstructure:"keeper"` // ← 新增: Keeper 配置
	Dexes      []DexConfig      `mapstructure:"dexes"`
	Cex        CexConfig        `mapstructure:"cex"` // CEX 配置
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

// KeeperConfig Keeper 配置
type KeeperConfig struct {
	PrivateKey string `mapstructure:"private_key"` // Keeper 私钥（用于签名交易）
	Address    string `mapstructure:"address"`     // Keeper 地址
}

// DexConfig DEX 配置
type DexConfig struct {
	Name             string `mapstructure:"name"`
	DexType          string `mapstructure:"dex_type"`           // DEX类型：amm, aggregator, orderbook, hybrid
	Protocol         string `mapstructure:"protocol"`           // 协议类型：uniswap_v2, uniswap_v3, sushiswap, curve, 1inch 等
	Router           string `mapstructure:"router"`             // 路由合约地址
	Factory          string `mapstructure:"factory"`            // 工厂合约地址（聚合器可为空）
	Quoter           string `mapstructure:"quoter"`             // Quoter合约地址（V3专用）
	Fee              int    `mapstructure:"fee"`                // 手续费（基点）
	FeeTier          uint32 `mapstructure:"fee_tier"`           // V3 费率层级
	DynamicFee       bool   `mapstructure:"dynamic_fee"`        // 是否为动态费率
	Version          string `mapstructure:"version"`            // 版本
	ChainID          int64  `mapstructure:"chain_id"`           // 链 ID
	SupportFlashLoan bool   `mapstructure:"support_flash_loan"` // 是否支持闪电贷
	SupportMultiHop  bool   `mapstructure:"support_multi_hop"`  // 是否支持多跳路由
	SupportV3Ticks   bool   `mapstructure:"support_v3_ticks"`   // 是否支持V3 tick数据
	Priority         int    `mapstructure:"priority"`           // 优先级（数值越小越优先）
}

// TokenConfig 代币配置
type TokenConfig struct {
	Symbol   string `mapstructure:"symbol"`
	Address  string `mapstructure:"address"`
	Decimals int    `mapstructure:"decimals"`
}

// SchedulerConfig 定时任务配置
type SchedulerConfig struct {
	CollectInterval int     `mapstructure:"collect_interval"`
	AnalyzeInterval int     `mapstructure:"analyze_interval"`
	CleanupInterval int     `mapstructure:"cleanup_interval"`
	MinProfitRate   float64 `mapstructure:"min_profit_rate"` // ← 新增: 最小利润率阈值
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
	Port    int    `mapstructure:"port"`
	Mode    string `mapstructure:"mode"`
	APIPort int    `mapstructure:"api_port"` // API 服务端口
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

// CexConfig CEX 配置
type CexConfig struct {
	Enabled bool          `mapstructure:"enabled"` // 是否启用 CEX 采集
	Binance BinanceConfig `mapstructure:"binance"` // 币安配置
}

// BinanceConfig 币安配置
type BinanceConfig struct {
	Enabled     bool     `mapstructure:"enabled"`       // 是否启用币安
	APIEndpoint string   `mapstructure:"api_endpoint"`  // API 地址
	WSEndpoint  string   `mapstructure:"ws_endpoint"`   // WebSocket 地址
	APIKey      string   `mapstructure:"api_key"`       // API Key（可选，读取公开数据不需要）
	APISecret   string   `mapstructure:"api_secret"`    // API Secret（可选）
	RateLimit   int      `mapstructure:"rate_limit"`    // 每分钟请求限制
	Symbols     []string `mapstructure:"symbols"`       // 要采集的交易对符号列表
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
