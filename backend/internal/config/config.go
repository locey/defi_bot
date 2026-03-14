package config

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
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

	// 清算机器人配置
	Liquidation LiquidationConfig `mapstructure:"liquidation"`

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
	Vault                string `mapstructure:"vault"`
	SpotArbitrage        string `mapstructure:"spot_arbitrage"`
	FlashLoanRouter      string `mapstructure:"flash_loan_router"`
	FlashLoanArbitrage   string `mapstructure:"flash_loan_arbitrage"` // FlashLoanArbitrage 合约地址

	// 集成合约
	DoubleRouterIntegration string `mapstructure:"double_router_integration"`
	UniswapV2Integration    string `mapstructure:"uniswap_v2_integration"`

	// 工具合约
	Multicall string `mapstructure:"multicall"`

	// 外部合约
	AaveLendingPool string `mapstructure:"aave_lending_pool"`

	// 清算合约
	FlashLoanLiquidator  string `mapstructure:"flash_loan_liquidator"`   // FlashLoanLiquidator 合约地址（Aave 闪电贷，0.05% 费率）
	BalancerLiquidator   string `mapstructure:"balancer_liquidator"`     // BalancerLiquidator 合约地址（Balancer 闪电贷，0% 费率）
	BalancerVault        string `mapstructure:"balancer_vault"`          // Balancer V2 Vault 地址
	AaveDataProvider     string `mapstructure:"aave_data_provider"`      // Aave V3 PoolDataProvider
	AaveOracle           string `mapstructure:"aave_oracle"`             // Aave V3 Oracle（用于查询 Chainlink feed 地址）
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

// LiquidationConfig 清算机器人配置
type LiquidationConfig struct {
	Enabled          bool    `mapstructure:"enabled"`            // 是否启用清算机器人
	DryRun           bool    `mapstructure:"dry_run"`            // 干运行模式
	WatchThreshold   float64 `mapstructure:"watch_threshold"`    // 健康因子监控阈值（如 1.05）
	MinDebtUSD       float64 `mapstructure:"min_debt_usd"`       // 最小债务金额 (USD)
	ScanIntervalSec  int     `mapstructure:"scan_interval_sec"`  // 扫描间隔（秒）
	EventScanBlocks  int     `mapstructure:"event_scan_blocks"`  // 事件扫描区块数
	SwapRouter       string  `mapstructure:"swap_router"`        // collateral→debt DEX router
	SwapFeeTier      int     `mapstructure:"swap_fee_tier"`      // V3 fee tier (0=V2)
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

// loadDotEnv 加载 .env 文件中的环境变量（如果存在）
// 业界标准做法：.env 文件不提交到 Git，仅在本地/部署环境使用
func loadDotEnv(configPath string) {
	// 尝试在 config 文件所在目录的上级（backend/）查找 .env
	configDir := filepath.Dir(configPath)
	envPaths := []string{
		filepath.Join(configDir, "..", ".env"),   // backend/.env
		filepath.Join(configDir, ".env"),          // backend/configs/.env
		".env",                                    // 当前目录
	}

	for _, envPath := range envPaths {
		data, err := os.ReadFile(envPath)
		if err != nil {
			continue
		}
		// 逐行解析 KEY=VALUE 格式
		for _, line := range strings.Split(string(data), "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			parts := strings.SplitN(line, "=", 2)
			if len(parts) != 2 {
				continue
			}
			key := strings.TrimSpace(parts[0])
			value := strings.TrimSpace(parts[1])
			// 不覆盖已有的环境变量（真实环境变量优先级最高）
			if os.Getenv(key) == "" {
				os.Setenv(key, value)
			}
		}
		log.Printf("已加载环境变量: %s", envPath)
		return
	}
}

// overrideSensitiveFromEnv 强制从环境变量覆盖敏感配置
// 即使 config.yaml 中有值，环境变量优先级更高
func overrideSensitiveFromEnv(config *Config) {
	// Keeper 私钥（最关键）
	if pk := os.Getenv("KEEPER_PRIVATE_KEY"); pk != "" {
		config.Keeper.PrivateKey = pk
		log.Printf("Keeper 私钥已从环境变量加载")
	}

	// 数据库密码
	if dbPass := os.Getenv("DATABASE_PASSWORD"); dbPass != "" {
		config.Database.Password = dbPass
	}

	// Binance API
	if apiKey := os.Getenv("BINANCE_API_KEY"); apiKey != "" {
		config.Cex.Binance.APIKey = apiKey
	}
	if apiSecret := os.Getenv("BINANCE_API_SECRET"); apiSecret != "" {
		config.Cex.Binance.APISecret = apiSecret
	}

	// RPC URL 覆盖
	if rpcURL := os.Getenv("BLOCKCHAIN_RPC_URL"); rpcURL != "" {
		config.Blockchain.RPCURL = rpcURL
	}
	if wsURL := os.Getenv("BLOCKCHAIN_WS_URL"); wsURL != "" {
		config.Blockchain.WSURL = wsURL
	}
}

// LoadConfig 加载配置文件
// 加载优先级（从低到高）：config.yaml < .env 文件 < 真实环境变量
func LoadConfig(configPath string) (*Config, error) {
	// 1. 加载 .env 文件（如果存在）— 设置环境变量
	loadDotEnv(configPath)

	// 2. 加载 YAML 配置文件
	viper.SetConfigFile(configPath)
	viper.SetConfigType("yaml")

	// 3. 自动读取环境变量（Viper 层面，KEY_SUBKEY 映射到 key.subkey）
	viper.AutomaticEnv()
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

	if err := viper.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("读取配置文件失败: %w", err)
	}

	var config Config
	if err := viper.Unmarshal(&config); err != nil {
		return nil, fmt.Errorf("解析配置文件失败: %w", err)
	}

	// 4. 强制从环境变量覆盖敏感字段（最高优先级）
	overrideSensitiveFromEnv(&config)

	// 5. 安全检查：确保私钥不在日志中泄露
	if config.Keeper.PrivateKey != "" {
		maskedKey := config.Keeper.PrivateKey[:6] + "..." + config.Keeper.PrivateKey[len(config.Keeper.PrivateKey)-4:]
		log.Printf("Keeper 私钥已配置: %s", maskedKey)
	} else {
		log.Printf("⚠️ Keeper 私钥未配置，系统将以只读模式运行（不执行交易）")
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
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=%s TimeZone=%s connect_timeout=10",
		c.Host, c.Port, c.User, c.Password, c.DBName, c.SSLMode, c.Timezone,
	)
}
