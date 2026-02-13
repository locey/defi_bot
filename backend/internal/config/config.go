package config

import (
	"fmt"
	"log"
	"strings"

	"github.com/spf13/viper"
)

// Config 全局配置结构
type Config struct {
	Database    DatabaseConfig            `mapstructure:"database"`
	Blockchain  BlockchainConfig          `mapstructure:"blockchain"`
	Contracts   ContractsConfig           `mapstructure:"contracts"`
	Keeper      KeeperConfig              `mapstructure:"keeper"`       // Keeper 配置
	Dexes       []DexConfig               `mapstructure:"dexes"`
	Cex         CexConfig                 `mapstructure:"cex"`          // CEX 配置
	CEXDEX      CEXDEXConfig              `mapstructure:"cexdex"`       // CEX-DEX 套利配置
	Tokens      []TokenConfig             `mapstructure:"tokens"`
	Scheduler   SchedulerConfig           `mapstructure:"scheduler"`
	Arbitrage   ArbitrageConfig           `mapstructure:"arbitrage"`
	Log         LogConfig                 `mapstructure:"log"`
	Server      ServerConfig              `mapstructure:"server"`
	Redis       RedisConfig               `mapstructure:"redis"`
	Chains      map[string]ChainConfig    `mapstructure:"chains"`       // 多链配置
	ActiveChain string                    `mapstructure:"active_chain"` // 当前活跃链名称

	// Phase 4: 可观测性配置
	Metrics  MetricsConfig  `mapstructure:"metrics"`  // Prometheus 指标
	Telegram TelegramConfig `mapstructure:"telegram"` // Telegram 告警
}

// MetricsConfig Prometheus 指标配置
type MetricsConfig struct {
	Enabled bool   `mapstructure:"enabled"` // 是否启用
	Port    int    `mapstructure:"port"`    // 指标端口（默认 9090）
}

// TelegramConfig Telegram 告警配置
type TelegramConfig struct {
	Enabled   bool   `mapstructure:"enabled"`    // 是否启用
	BotToken  string `mapstructure:"bot_token"`  // Bot Token
	ChatID    string `mapstructure:"chat_id"`    // 目标聊天 ID
	RateLimit int    `mapstructure:"rate_limit"` // 每分钟最大消息数
}

// ChainConfig 单链配置
type ChainConfig struct {
	Name        string          `mapstructure:"name"`         // 链名称
	ChainID     int64           `mapstructure:"chain_id"`     // 链 ID
	NativeToken string          `mapstructure:"native_token"` // 原生代币符号
	BlockTime   float64         `mapstructure:"block_time"`   // 平均出块时间（秒）
	IsL2        bool            `mapstructure:"is_l2"`        // 是否为 L2 链
	RPCURL      string          `mapstructure:"rpc_url"`      // 主 RPC URL
	RPCURLs     []string        `mapstructure:"rpc_urls"`     // 多个 RPC URL
	WSURL       string          `mapstructure:"ws_url"`       // WebSocket URL
	Timeout     int             `mapstructure:"timeout"`      // 请求超时（秒）
	Contracts   ContractsConfig `mapstructure:"contracts"`    // 合约地址
	Dexes       []DexConfig     `mapstructure:"dexes"`        // DEX 配置
	Tokens      []TokenConfig   `mapstructure:"tokens"`       // 代币配置
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
	RPCURL              string   `mapstructure:"rpc_url"`                // 主 RPC URL（向后兼容）
	RPCURLs             []string `mapstructure:"rpc_urls"`               // 多个 RPC URL（用于负载均衡）
	WSURL               string   `mapstructure:"ws_url"`                 // WebSocket URL（用于实时订阅）
	ChainID             int64    `mapstructure:"chain_id"`
	Timeout             int      `mapstructure:"timeout"`
	Retry               int      `mapstructure:"retry"`
	UsePool             bool     `mapstructure:"use_pool"`               // 是否使用 RPC 池
	VerifyTokenDecimals bool     `mapstructure:"verify_token_decimals"`  // 是否验证代币精度
}

// ContractsConfig 合约配置
type ContractsConfig struct {
	// 核心合约
	ArbitrageCore string `mapstructure:"arbitrage_core"`
	ConfigManager string `mapstructure:"config_manager"`

	// 套利执行合约
	Vault           string `mapstructure:"vault"`
	SpotArbitrage   string `mapstructure:"spot_arbitrage"`
	FlashLoanRouter string `mapstructure:"flash_loan_router"`

	// 集成合约
	DoubleRouterIntegration string `mapstructure:"double_router_integration"`
	UniswapV2Integration    string `mapstructure:"uniswap_v2_integration"`

	// 工具合约
	Multicall string `mapstructure:"multicall"`

	// 外部合约
	AaveLendingPool string `mapstructure:"aave_lending_pool"`
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
	Symbol    string `mapstructure:"symbol"`
	Address   string `mapstructure:"address"`
	Decimals  int    `mapstructure:"decimals"`
	CEXSymbol string `mapstructure:"cex_symbol"` // CEX 交易对符号（如 ETHUSDT）
}

// SchedulerConfig 定时任务配置
type SchedulerConfig struct {
	CollectInterval int     `mapstructure:"collect_interval"`
	AnalyzeInterval int     `mapstructure:"analyze_interval"`
	CleanupInterval int     `mapstructure:"cleanup_interval"`
	MinProfitRate   float64 `mapstructure:"min_profit_rate"`

	// Phase 1.1: 高性能模式配置
	Mode               string  `mapstructure:"mode"`                  // "standard" 或 "high_performance"（默认 high_performance）
	EnableExecution    bool    `mapstructure:"enable_execution"`      // 是否启用自动执行
	DryRun             bool    `mapstructure:"dry_run"`               // 干运行模式（只检测不执行）
	MinConfidence      float64 `mapstructure:"min_confidence"`        // 最小置信度阈值
	MaxConcurrentExec  int     `mapstructure:"max_concurrent_exec"`   // 最大并发执行数
}

// ArbitrageConfig 套利配置
type ArbitrageConfig struct {
	MinProfitRate float64 `mapstructure:"min_profit_rate"`
	MaxSlippage   float64 `mapstructure:"max_slippage"`
	MaxGasPrice   int64   `mapstructure:"max_gas_price"`
}

// LogConfig 日志配置
type LogConfig struct {
	Level      string `mapstructure:"level"`       // 日志级别: debug, info, warn, error
	Dir        string `mapstructure:"dir"`         // 日志根目录（按日期+小时自动创建子目录）
	File       string `mapstructure:"file"`        // 向后兼容：单文件模式
	MaxSize    int    `mapstructure:"max_size"`    // 单个文件最大大小 (MB)
	MaxBackups int    `mapstructure:"max_backups"` // 最大备份数量
	MaxAge     int    `mapstructure:"max_age"`     // 最大保存天数
	Compress   bool   `mapstructure:"compress"`    // 是否压缩旧日志
	Console    bool   `mapstructure:"console"`     // 是否输出到控制台
	JSONFormat bool   `mapstructure:"json_format"` // 控制台是否使用 JSON 格式
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

// CEXDEXConfig CEX-DEX 套利配置
type CEXDEXConfig struct {
	Enabled          bool               `mapstructure:"enabled"`            // 是否启用 CEX-DEX 套利
	MinProfitRate    float64            `mapstructure:"min_profit_rate"`    // 最小利润率 (如 0.002 = 0.2%)
	MinProfitAmount  float64            `mapstructure:"min_profit_amount"`  // 最小利润金额 (USD)
	MaxTradeAmount   float64            `mapstructure:"max_trade_amount"`   // 单笔最大交易金额 (USD)
	MinTradeAmount   float64            `mapstructure:"min_trade_amount"`   // 单笔最小交易金额 (USD)
	MaxSlippage      float64            `mapstructure:"max_slippage"`       // 最大滑点
	EstimatedGasCost float64            `mapstructure:"estimated_gas_cost"` // 预估 Gas 成本 (USD)
	CheckInterval    int                `mapstructure:"check_interval"`     // 检测间隔 (ms)
	OpportunityTTL   int                `mapstructure:"opportunity_ttl"`    // 机会有效期 (秒)
	PairMapping      map[string]string  `mapstructure:"pair_mapping"`       // DEX Token -> CEX Symbol 映射
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
