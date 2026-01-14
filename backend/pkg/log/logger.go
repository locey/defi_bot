package log

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"

	"github.com/defi-bot/backend/internal/config"
	"github.com/natefinch/lumberjack"
)

// Logger 是日志实例
type Logger struct {
	*log.Logger
}

// Global logger instance
var globalLogger *Logger

// Init 初始化日志
func Init(cfg *config.LogConfig) error {
	// 创建日志目录（如果不存在）
	logDir := filepath.Dir(cfg.File)
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return fmt.Errorf("创建日志目录失败: %w", err)
	}

	// 配置日志文件写入器
	fileWriter := &lumberjack.Logger{
		Filename:   cfg.File,
		MaxSize:    cfg.MaxSize,    // MB
		MaxBackups: cfg.MaxBackups, // 备份数量
		MaxAge:     cfg.MaxAge,     // 保存天数
		Compress:   true,           // 压缩旧日志
	}

	// 同时输出到控制台和文件
	multiWriter := io.MultiWriter(os.Stdout, fileWriter)

	// 创建日志实例
	logger := &Logger{
		Logger: log.New(multiWriter, "", log.Ldate|log.Ltime|log.Lshortfile),
	}

	globalLogger = logger
	return nil
}

// GetLogger 获取全局日志实例
func GetLogger() *Logger {
	if globalLogger == nil {
		// 如果未初始化，返回默认日志实例
		return &Logger{
			Logger: log.New(os.Stdout, "", log.Ldate|log.Ltime|log.Lshortfile),
		}
	}
	return globalLogger
}

// Debug 打印调试日志
func Debug(format string, v ...interface{}) {
	GetLogger().Printf("[DEBUG] "+format, v...)
}

// Info 打印信息日志
func Info(format string, v ...interface{}) {
	GetLogger().Printf("[INFO] "+format, v...)
}

// Warn 打印警告日志
func Warn(format string, v ...interface{}) {
	GetLogger().Printf("[WARN] "+format, v...)
}

// Error 打印错误日志
func Error(format string, v ...interface{}) {
	GetLogger().Printf("[ERROR] "+format, v...)
}

// Fatal 打印致命错误日志并退出
func Fatal(format string, v ...interface{}) {
	GetLogger().Fatalf("[FATAL] "+format, v...)
}
