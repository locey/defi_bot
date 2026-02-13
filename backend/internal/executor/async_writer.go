// internal/executor/async_writer.go
// Phase 1.5: 异步 DB 写入器 — 将执行记录通过 buffered channel 异步批量写入 DB
// 不阻塞执行热路径
package executor

import (
	"context"
	"sync"
	"time"

	"github.com/defi-bot/backend/internal/strategy"
	"github.com/defi-bot/backend/pkg/log"
	"gorm.io/gorm"
)

// AsyncDBWriter 异步数据库写入器
type AsyncDBWriter struct {
	db       *gorm.DB
	recordCh chan *asyncWriteRequest
	wg       sync.WaitGroup
	ctx      context.Context
	cancel   context.CancelFunc

	// 配置
	batchSize    int           // 批量写入大小
	flushInterval time.Duration // 定时刷新间隔
	bufferSize   int           // channel 缓冲大小
}

// asyncWriteRequest 异步写入请求
type asyncWriteRequest struct {
	opp    *strategy.ArbitrageOpportunity
	result *ExecutionResult
}

// NewAsyncDBWriter 创建异步写入器
func NewAsyncDBWriter(db *gorm.DB) *AsyncDBWriter {
	ctx, cancel := context.WithCancel(context.Background())
	w := &AsyncDBWriter{
		db:            db,
		recordCh:      make(chan *asyncWriteRequest, 1000),
		ctx:           ctx,
		cancel:        cancel,
		batchSize:     50,
		flushInterval: 5 * time.Second,
		bufferSize:    1000,
	}
	w.wg.Add(1)
	go w.processLoop()
	return w
}

// EnqueueRecord 将执行记录异步入队（非阻塞）
func (w *AsyncDBWriter) EnqueueRecord(opp *strategy.ArbitrageOpportunity, result *ExecutionResult) {
	select {
	case w.recordCh <- &asyncWriteRequest{opp: opp, result: result}:
		// 成功入队
	default:
		// channel 满了，丢弃（热路径不能阻塞）
		log.Executor().Warn().Msg("AsyncDBWriter: channel full, dropping record")
	}
}

// processLoop 后台处理循环
func (w *AsyncDBWriter) processLoop() {
	defer w.wg.Done()

	batch := make([]*asyncWriteRequest, 0, w.batchSize)
	ticker := time.NewTicker(w.flushInterval)
	defer ticker.Stop()

	for {
		select {
		case <-w.ctx.Done():
			// 关闭前刷新剩余数据
			w.flushBatch(batch)
			return
		case req := <-w.recordCh:
			batch = append(batch, req)
			if len(batch) >= w.batchSize {
				w.flushBatch(batch)
				batch = batch[:0]
			}
		case <-ticker.C:
			if len(batch) > 0 {
				w.flushBatch(batch)
				batch = batch[:0]
			}
		}
	}
}

// flushBatch 批量写入数据库
func (w *AsyncDBWriter) flushBatch(batch []*asyncWriteRequest) {
	if len(batch) == 0 || w.db == nil {
		return
	}

	for _, req := range batch {
		// 复用 ArbitrageExecutor 的保存逻辑
		// 这里简化为直接调用 executor 的 save 方法
		executor := &ArbitrageExecutor{db: w.db}
		if err := executor.saveExecutionRecord(req.opp, req.result); err != nil {
			log.Executor().Warn().Err(err).Msg("AsyncDBWriter: failed to save record")
		}
	}

	log.Executor().Debug().Int("count", len(batch)).Msg("AsyncDBWriter: flushed batch")
}

// Stop 停止异步写入器（优雅关闭）
func (w *AsyncDBWriter) Stop() {
	w.cancel()
	w.wg.Wait()
	log.Executor().Info().Msg("AsyncDBWriter: stopped")
}
