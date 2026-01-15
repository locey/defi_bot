// Package chain 提供多链管理功能
package chain

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/defi-bot/backend/internal/config"
	"github.com/spf13/viper"
)

// LoadChainConfigs 从指定目录加载所有链配置
// 目录下每个 .yaml 文件代表一个链的配置
func LoadChainConfigs(chainsDir string) (map[string]config.ChainConfig, error) {
	chains := make(map[string]config.ChainConfig)

	// 检查目录是否存在
	if _, err := os.Stat(chainsDir); os.IsNotExist(err) {
		return chains, nil // 目录不存在，返回空 map
	}

	// 遍历目录下的所有 YAML 文件
	err := filepath.Walk(chainsDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// 跳过目录
		if info.IsDir() {
			return nil
		}

		// 只处理 .yaml 文件
		if !strings.HasSuffix(info.Name(), ".yaml") && !strings.HasSuffix(info.Name(), ".yml") {
			return nil
		}

		// 获取链名称（文件名去掉扩展名）
		chainName := strings.TrimSuffix(info.Name(), filepath.Ext(info.Name()))

		// 加载配置
		chainCfg, err := loadChainConfig(path)
		if err != nil {
			return fmt.Errorf("加载链配置 %s 失败: %w", chainName, err)
		}

		chains[chainName] = *chainCfg
		return nil
	})

	if err != nil {
		return nil, err
	}

	return chains, nil
}

// loadChainConfig 加载单个链配置文件
func loadChainConfig(configPath string) (*config.ChainConfig, error) {
	v := viper.New()
	v.SetConfigFile(configPath)
	v.SetConfigType("yaml")

	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("读取配置文件失败: %w", err)
	}

	var chainCfg config.ChainConfig
	if err := v.Unmarshal(&chainCfg); err != nil {
		return nil, fmt.Errorf("解析配置文件失败: %w", err)
	}

	return &chainCfg, nil
}

// LoadConfigWithChains 加载主配置文件并自动加载链配置
// 链配置目录默认为主配置文件同级的 chains/ 目录
func LoadConfigWithChains(configPath string) (*config.Config, error) {
	// 1. 加载主配置
	cfg, err := config.LoadConfig(configPath)
	if err != nil {
		return nil, err
	}

	// 2. 确定链配置目录
	configDir := filepath.Dir(configPath)
	chainsDir := filepath.Join(configDir, "chains")

	// 3. 加载链配置
	chains, err := LoadChainConfigs(chainsDir)
	if err != nil {
		return nil, fmt.Errorf("加载链配置失败: %w", err)
	}

	// 4. 合并到主配置
	if len(chains) > 0 {
		cfg.Chains = chains
	}

	return cfg, nil
}

// ChainInfo 链信息摘要
type ChainInfo struct {
	Name        string  `json:"name"`
	ChainID     int64   `json:"chain_id"`
	NativeToken string  `json:"native_token"`
	BlockTime   float64 `json:"block_time"`
	IsL2        bool    `json:"is_l2"`
	DexCount    int     `json:"dex_count"`
	TokenCount  int     `json:"token_count"`
}

// GetChainInfo 获取链信息摘要
func GetChainInfo(chainCfg *config.ChainConfig) *ChainInfo {
	return &ChainInfo{
		Name:        chainCfg.Name,
		ChainID:     chainCfg.ChainID,
		NativeToken: chainCfg.NativeToken,
		BlockTime:   chainCfg.BlockTime,
		IsL2:        chainCfg.IsL2,
		DexCount:    len(chainCfg.Dexes),
		TokenCount:  len(chainCfg.Tokens),
	}
}

// GetAllChainInfos 获取所有链的信息摘要
func GetAllChainInfos(chains map[string]config.ChainConfig) map[string]*ChainInfo {
	infos := make(map[string]*ChainInfo)
	for name, chainCfg := range chains {
		cfg := chainCfg // 避免闭包问题
		infos[name] = GetChainInfo(&cfg)
	}
	return infos
}
