// internal/notify/telegram.go
// Phase 4.2: Telegram 告警 Bot
// 实时推送套利执行结果、系统异常和关键事件到 Telegram
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/defi-bot/backend/pkg/log"
)

// TelegramConfig Telegram 配置
type TelegramConfig struct {
	Enabled   bool   `mapstructure:"enabled"`
	BotToken  string `mapstructure:"bot_token"`  // Telegram Bot Token
	ChatID    string `mapstructure:"chat_id"`    // 目标聊天 ID
	RateLimit int    `mapstructure:"rate_limit"` // 每分钟最大消息数（防止刷屏）
}

// TelegramNotifier Telegram 通知器
type TelegramNotifier struct {
	config  *TelegramConfig
	client  *http.Client
	msgCh   chan *TelegramMessage
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup

	// 限频
	lastSendTime time.Time
	sendCount    int
	rateMu       sync.Mutex
}

// TelegramMessage 消息结构
type TelegramMessage struct {
	Level   AlertLevel
	Title   string
	Content string
	Fields  map[string]string
}

// AlertLevel 告警级别
type AlertLevel int

const (
	AlertInfo    AlertLevel = iota // 信息
	AlertSuccess                   // 成功
	AlertWarning                   // 警告
	AlertError                     // 错误
)

// NewTelegramNotifier 创建通知器
func NewTelegramNotifier(config *TelegramConfig) *TelegramNotifier {
	ctx, cancel := context.WithCancel(context.Background())
	n := &TelegramNotifier{
		config: config,
		client: &http.Client{Timeout: 10 * time.Second},
		msgCh:  make(chan *TelegramMessage, 100),
		ctx:    ctx,
		cancel: cancel,
	}

	if config.Enabled && config.BotToken != "" && config.ChatID != "" {
		n.wg.Add(1)
		go n.processMessages()
		log.Main().Info().Msg("Telegram notifier started")
	}

	return n
}

// NotifyExecutionSuccess 通知执行成功
func (n *TelegramNotifier) NotifyExecutionSuccess(
	txHash string,
	profit string,
	gasUsed uint64,
	executionTime time.Duration,
) {
	n.send(&TelegramMessage{
		Level: AlertSuccess,
		Title: "Arbitrage Executed Successfully",
		Fields: map[string]string{
			"TX Hash":   txHash,
			"Profit":    profit,
			"Gas Used":  fmt.Sprintf("%d", gasUsed),
			"Exec Time": executionTime.String(),
		},
	})
}

// NotifyExecutionFailed 通知执行失败
func (n *TelegramNotifier) NotifyExecutionFailed(
	reason string,
	pathInfo string,
) {
	n.send(&TelegramMessage{
		Level: AlertWarning,
		Title: "Arbitrage Execution Failed",
		Fields: map[string]string{
			"Reason": reason,
			"Path":   pathInfo,
		},
	})
}

// NotifySystemError 通知系统错误
func (n *TelegramNotifier) NotifySystemError(component string, err error) {
	n.send(&TelegramMessage{
		Level: AlertError,
		Title: "System Error",
		Fields: map[string]string{
			"Component": component,
			"Error":     err.Error(),
			"Time":      time.Now().Format("2006-01-02 15:04:05"),
		},
	})
}

// NotifyOpportunityFound 通知发现高价值机会
func (n *TelegramNotifier) NotifyOpportunityFound(
	pathLength int,
	estimatedProfit string,
	confidence float64,
) {
	n.send(&TelegramMessage{
		Level: AlertInfo,
		Title: "High-Value Opportunity Found",
		Fields: map[string]string{
			"Path Length": fmt.Sprintf("%d hops", pathLength),
			"Est. Profit": estimatedProfit,
			"Confidence":  fmt.Sprintf("%.1f%%", confidence*100),
		},
	})
}

// NotifyNewPool 通知发现新池子
func (n *TelegramNotifier) NotifyNewPool(
	poolAddress string,
	token0 string,
	token1 string,
	protocol string,
) {
	n.send(&TelegramMessage{
		Level: AlertInfo,
		Title: "New Pool Detected",
		Fields: map[string]string{
			"Pool":     poolAddress,
			"Token0":   token0,
			"Token1":   token1,
			"Protocol": protocol,
		},
	})
}

// send 入队消息
func (n *TelegramNotifier) send(msg *TelegramMessage) {
	if !n.config.Enabled {
		return
	}
	select {
	case n.msgCh <- msg:
	default:
		// channel 满了，丢弃消息
	}
}

// processMessages 后台发送循环
func (n *TelegramNotifier) processMessages() {
	defer n.wg.Done()

	for {
		select {
		case <-n.ctx.Done():
			return
		case msg := <-n.msgCh:
			if n.checkRateLimit() {
				n.sendToTelegram(msg)
			}
		}
	}
}

// checkRateLimit 限频检查
func (n *TelegramNotifier) checkRateLimit() bool {
	n.rateMu.Lock()
	defer n.rateMu.Unlock()

	limit := n.config.RateLimit
	if limit <= 0 {
		limit = 30 // 默认每分钟30条
	}

	now := time.Now()
	if now.Sub(n.lastSendTime) > time.Minute {
		n.sendCount = 0
		n.lastSendTime = now
	}

	if n.sendCount >= limit {
		return false
	}

	n.sendCount++
	return true
}

// sendToTelegram 发送到 Telegram API
func (n *TelegramNotifier) sendToTelegram(msg *TelegramMessage) {
	// 构建消息文本（Markdown 格式）
	var text string

	// 级别图标
	levelIcon := map[AlertLevel]string{
		AlertInfo:    "ℹ️",
		AlertSuccess: "✅",
		AlertWarning: "⚠️",
		AlertError:   "🚨",
	}

	icon := levelIcon[msg.Level]
	text = fmt.Sprintf("%s *%s*\n", icon, escapeMarkdown(msg.Title))

	if msg.Content != "" {
		text += fmt.Sprintf("\n%s\n", escapeMarkdown(msg.Content))
	}

	for key, value := range msg.Fields {
		text += fmt.Sprintf("• *%s:* `%s`\n", escapeMarkdown(key), escapeMarkdown(value))
	}

	text += fmt.Sprintf("\n_Time: %s_", time.Now().Format("15:04:05 MST"))

	// 发送请求
	url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", n.config.BotToken)
	body := map[string]interface{}{
		"chat_id":    n.config.ChatID,
		"text":       text,
		"parse_mode": "MarkdownV2",
	}

	jsonBody, err := json.Marshal(body)
	if err != nil {
		log.Main().Warn().Err(err).Msg("Telegram: failed to marshal message")
		return
	}

	resp, err := n.client.Post(url, "application/json", bytes.NewReader(jsonBody))
	if err != nil {
		log.Main().Warn().Err(err).Msg("Telegram: failed to send message")
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		log.Main().Warn().
			Int("status", resp.StatusCode).
			Str("body", string(respBody)).
			Msg("Telegram API error")
	}
}

// escapeMarkdown 转义 Telegram MarkdownV2 特殊字符
func escapeMarkdown(s string) string {
	specialChars := []string{"_", "*", "[", "]", "(", ")", "~", "`", ">", "#", "+", "-", "=", "|", "{", "}", ".", "!"}
	result := s
	for _, ch := range specialChars {
		result = replaceAll(result, ch, "\\"+ch)
	}
	return result
}

func replaceAll(s, old, new string) string {
	for {
		i := indexOf(s, old)
		if i == -1 {
			break
		}
		s = s[:i] + new + s[i+len(old):]
	}
	return s
}

func indexOf(s, substr string) int {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return i
		}
	}
	return -1
}

// Stop 停止通知器
func (n *TelegramNotifier) Stop() {
	n.cancel()
	n.wg.Wait()
	log.Main().Info().Msg("Telegram notifier stopped")
}
