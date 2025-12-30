package collector

// BinanceConfig 币安配置（在 collector 包中重新定义，避免循环依赖）
type BinanceConfig struct {
	APIEndpoint string
	WSEndpoint  string
	APIKey      string
	APISecret   string
	RateLimit   int
}

