package log

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/defi-bot/backend/internal/config"
	"github.com/natefinch/lumberjack"
	"github.com/rs/zerolog"
)

// =====================================================================
//                       DeFi Bot 日志系统
// =====================================================================
// 基于 zerolog 的高性能结构化日志系统
// 特性：
//   - 按日期+小时自动创建日志目录
//   - 不同模块分开记录（collector, strategy, executor, api 等）
//   - 同时输出到控制台（美化）和文件（JSON）
//   - 支持日志级别控制
//   - 支持上下文字段
// =====================================================================

// LogModule 日志模块定义
type LogModule string

const (
	ModuleMain      LogModule = "main"      // 主程序
	ModuleAPI       LogModule = "api"       // API 服务
	ModuleCollector LogModule = "collector" // 数据采集
	ModuleStrategy  LogModule = "strategy"  // 策略引擎
	ModuleExecutor  LogModule = "executor"  // 交易执行
	ModuleScheduler LogModule = "scheduler" // 调度器
	ModuleDatabase  LogModule = "database"  // 数据库
	ModuleWeb3      LogModule = "web3"      // Web3 交互
	ModuleCache     LogModule = "cache"     // 缓存
	ModuleCEX       LogModule = "cex"       // CEX 采集
)

// LogConfig 日志配置
type LogConfig struct {
	Level      string `mapstructure:"level"`       // 日志级别: debug, info, warn, error
	Dir        string `mapstructure:"dir"`         // 日志根目录
	MaxSize    int    `mapstructure:"max_size"`    // 单个文件最大大小 (MB)
	MaxBackups int    `mapstructure:"max_backups"` // 最大备份数量
	MaxAge     int    `mapstructure:"max_age"`     // 最大保存天数
	Compress   bool   `mapstructure:"compress"`    // 是否压缩旧日志
	Console    bool   `mapstructure:"console"`     // 是否输出到控制台
	JSONFormat bool   `mapstructure:"json_format"` // 控制台是否使用 JSON 格式
}

// DefaultConfig 默认配置
func DefaultConfig() *LogConfig {
	return &LogConfig{
		Level:      "info",
		Dir:        "logs",
		MaxSize:    100,
		MaxBackups: 10,
		MaxAge:     30,
		Compress:   true,
		Console:    true,
		JSONFormat: false,
	}
}

// Logger 模块日志器
type Logger struct {
	zerolog.Logger
	module LogModule
}

// LogManager 日志管理器
type LogManager struct {
	config      *LogConfig
	loggers     map[LogModule]*Logger
	fileWriters map[LogModule]*lumberjack.Logger
	currentHour string
	mu          sync.RWMutex
	baseDir     string
}

var (
	manager *LogManager
	once    sync.Once
)

// Init 初始化日志系统（接受 config.LogConfig 类型）
func Init(cfgPtr *config.LogConfig) error {
	var initErr error
	once.Do(func() {
		// 转换为内部配置
		var cfg *LogConfig
		if cfgPtr == nil {
			cfg = DefaultConfig()
		} else {
			cfg = &LogConfig{
				Level:      cfgPtr.Level,
				Dir:        cfgPtr.Dir,
				MaxSize:    cfgPtr.MaxSize,
				MaxBackups: cfgPtr.MaxBackups,
				MaxAge:     cfgPtr.MaxAge,
				Compress:   cfgPtr.Compress,
				Console:    cfgPtr.Console,
				JSONFormat: cfgPtr.JSONFormat,
			}
			// 向后兼容：如果没有设置 Dir，使用 File 的目录
			if cfg.Dir == "" && cfgPtr.File != "" {
				cfg.Dir = filepath.Dir(cfgPtr.File)
			}
			if cfg.Dir == "" {
				cfg.Dir = "logs"
			}
		}

		manager = &LogManager{
			config:      cfg,
			loggers:     make(map[LogModule]*Logger),
			fileWriters: make(map[LogModule]*lumberjack.Logger),
		}

		// 设置全局日志级别
		level, err := zerolog.ParseLevel(cfg.Level)
		if err != nil {
			level = zerolog.InfoLevel
		}
		zerolog.SetGlobalLevel(level)

		// 设置时间格式
		zerolog.TimeFieldFormat = "2006-01-02 15:04:05.000"

		// 创建初始日志目录
		if err := manager.updateLogDir(); err != nil {
			initErr = err
			return
		}

		// 启动定时检查，每小时更新日志目录
		go manager.hourlyRotation()

		fmt.Printf("✅ 日志系统初始化成功: %s\n", cfg.Dir)
	})
	return initErr
}

// updateLogDir 更新日志目录（按日期+小时）
func (m *LogManager) updateLogDir() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	hourStr := now.Format("2006-01-02/15")

	// 如果小时没变，不需要更新
	if hourStr == m.currentHour {
		return nil
	}

	// 创建新的日志目录: logs/2026-01-16/14/
	m.baseDir = filepath.Join(m.config.Dir, hourStr)
	if err := os.MkdirAll(m.baseDir, 0755); err != nil {
		return fmt.Errorf("创建日志目录失败: %w", err)
	}

	// 关闭旧的文件写入器
	for _, fw := range m.fileWriters {
		fw.Close()
	}
	m.fileWriters = make(map[LogModule]*lumberjack.Logger)
	m.loggers = make(map[LogModule]*Logger)
	m.currentHour = hourStr

	return nil
}

// hourlyRotation 每小时检查并轮转日志目录
func (m *LogManager) hourlyRotation() {
	ticker := time.NewTicker(time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		if err := m.updateLogDir(); err != nil {
			fmt.Printf("⚠️ 日志目录更新失败: %v\n", err)
		}
	}
}

// getOrCreateLogger 获取或创建模块日志器
func (m *LogManager) getOrCreateLogger(module LogModule) *Logger {
	m.mu.RLock()
	logger, exists := m.loggers[module]
	m.mu.RUnlock()

	if exists {
		return logger
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// 双重检查
	if logger, exists = m.loggers[module]; exists {
		return logger
	}

	// 创建文件写入器
	logFile := filepath.Join(m.baseDir, fmt.Sprintf("%s.log", module))
	fileWriter := &lumberjack.Logger{
		Filename:   logFile,
		MaxSize:    m.config.MaxSize,
		MaxBackups: m.config.MaxBackups,
		MaxAge:     m.config.MaxAge,
		Compress:   m.config.Compress,
	}
	m.fileWriters[module] = fileWriter

	// 构建输出
	var writers []io.Writer

	// 文件输出（始终 JSON 格式）
	writers = append(writers, fileWriter)

	// 控制台输出
	if m.config.Console {
		if m.config.JSONFormat {
			writers = append(writers, os.Stdout)
		} else {
			// 美化控制台输出
			consoleWriter := zerolog.ConsoleWriter{
				Out:        os.Stdout,
				TimeFormat: "15:04:05",
				NoColor:    false,
			}
			writers = append(writers, consoleWriter)
		}
	}

	multi := zerolog.MultiLevelWriter(writers...)
	zl := zerolog.New(multi).With().
		Timestamp().
		Str("module", string(module)).
		Logger()

	logger = &Logger{
		Logger: zl,
		module: module,
	}
	m.loggers[module] = logger

	return logger
}

// =====================================================================
//                          获取模块日志器
// =====================================================================

// Get 获取指定模块的日志器
func Get(module LogModule) *Logger {
	if manager == nil {
		// 未初始化时使用默认配置
		initWithDefaultConfig()
	}
	return manager.getOrCreateLogger(module)
}

// initWithDefaultConfig 使用默认配置初始化（内部使用）
func initWithDefaultConfig() {
	Init(nil) // 传入 nil 会使用默认配置
}

// Main 获取主程序日志器
func Main() *Logger { return Get(ModuleMain) }

// API 获取 API 模块日志器
func API() *Logger { return Get(ModuleAPI) }

// Collector 获取采集器日志器
func Collector() *Logger { return Get(ModuleCollector) }

// Strategy 获取策略引擎日志器
func Strategy() *Logger { return Get(ModuleStrategy) }

// Executor 获取执行器日志器
func Executor() *Logger { return Get(ModuleExecutor) }

// Scheduler 获取调度器日志器
func Scheduler() *Logger { return Get(ModuleScheduler) }

// Database 获取数据库日志器
func Database() *Logger { return Get(ModuleDatabase) }

// Web3 获取 Web3 日志器
func Web3() *Logger { return Get(ModuleWeb3) }

// Cache 获取缓存日志器
func Cache() *Logger { return Get(ModuleCache) }

// CEX 获取 CEX 日志器
func CEX() *Logger { return Get(ModuleCEX) }

// =====================================================================
//                          便捷方法
// =====================================================================

// WithContext 添加上下文字段
func (l *Logger) WithContext(key string, value interface{}) *Logger {
	return &Logger{
		Logger: l.Logger.With().Interface(key, value).Logger(),
		module: l.module,
	}
}

// WithStr 添加字符串字段
func (l *Logger) WithStr(key, value string) *Logger {
	return &Logger{
		Logger: l.Logger.With().Str(key, value).Logger(),
		module: l.module,
	}
}

// WithError 添加错误字段
func (l *Logger) WithError(err error) *Logger {
	return &Logger{
		Logger: l.Logger.With().Err(err).Logger(),
		module: l.module,
	}
}

// =====================================================================
//                       全局便捷函数（兼容旧代码）
// =====================================================================

// Debug 打印调试日志
func Debug(format string, v ...interface{}) {
	Main().Debug().Msgf(format, v...)
}

// Info 打印信息日志
func Info(format string, v ...interface{}) {
	Main().Info().Msgf(format, v...)
}

// Warn 打印警告日志
func Warn(format string, v ...interface{}) {
	Main().Warn().Msgf(format, v...)
}

// Error 打印错误日志
func Error(format string, v ...interface{}) {
	Main().Error().Msgf(format, v...)
}

// Fatal 打印致命错误日志并退出
func Fatal(format string, v ...interface{}) {
	Main().Fatal().Msgf(format, v...)
}

// =====================================================================
//                          工具函数
// =====================================================================

// GetLogDir 获取当前日志目录
func GetLogDir() string {
	if manager == nil {
		return ""
	}
	manager.mu.RLock()
	defer manager.mu.RUnlock()
	return manager.baseDir
}

// SetLevel 动态设置日志级别
func SetLevel(level string) error {
	l, err := zerolog.ParseLevel(level)
	if err != nil {
		return err
	}
	zerolog.SetGlobalLevel(l)
	return nil
}

// Close 关闭日志系统
func Close() {
	if manager == nil {
		return
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()

	for _, fw := range manager.fileWriters {
		fw.Close()
	}
}
