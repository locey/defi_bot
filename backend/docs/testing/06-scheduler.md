# 调度器模块测试

<div align="center">

**📦 模块路径**: `internal/scheduler/`

</div>

---

## 📋 模块概述

调度器模块负责任务调度和执行，包含以下核心组件：

| 文件 | 组件 | 功能 |
|------|------|------|
| `scheduler.go` | Scheduler | 基础任务调度器 |
| `high_performance_scheduler.go` | HPScheduler | 高性能调度器 |

---

## 🧪 测试用例

### 1. Scheduler 基础测试

#### 1.1 初始化测试

**目的**: 验证调度器正确初始化

**测试步骤**:
```bash
go run cmd/test-scheduler/main.go
```

**验证点**:
- [ ] cron 调度器创建成功
- [ ] 任务列表初始化

**预期输出**:
```
✅ Scheduler 初始化成功
   已注册任务: 5
```

#### 1.2 任务注册测试

**目的**: 验证能正确注册定时任务

**测试步骤**:
```go
scheduler := scheduler.NewScheduler(cfg)
err := scheduler.AddTask("collect_prices", "*/5 * * * *", collectPricesFunc)
```

**验证点**:
- [ ] 任务注册成功
- [ ] cron 表达式解析正确
- [ ] 任务函数绑定

**测试用例**:

| 任务名 | cron 表达式 | 执行频率 |
|--------|-------------|----------|
| collect_prices | `*/5 * * * *` | 每 5 分钟 |
| analyze | `*/10 * * * *` | 每 10 分钟 |
| cleanup | `0 0 * * *` | 每天凌晨 |
| gas_update | `*/1 * * * *` | 每分钟 |

#### 1.3 启动/停止测试

**测试步骤**:
```go
err := scheduler.Start(ctx)
// 等待执行几次任务
time.Sleep(5 * time.Minute)
scheduler.Stop()
```

**验证点**:
- [ ] Start 返回 nil
- [ ] 任务按时执行
- [ ] Stop 正确停止所有任务

#### 1.4 任务执行测试

**目的**: 验证任务按预期执行

**测试步骤**:
```go
var executionCount int
scheduler.AddTask("test", "* * * * *", func() {
    executionCount++
    log.Printf("执行次数: %d", executionCount)
})
scheduler.Start(ctx)
time.Sleep(3 * time.Minute)
scheduler.Stop()
// executionCount 应该 >= 2
```

**验证点**:
- [ ] 任务按时触发
- [ ] 执行间隔正确
- [ ] 执行次数正确

---

### 2. High Performance Scheduler 测试

#### 2.1 初始化测试

**目的**: 验证高性能调度器正确初始化

**测试步骤**:
```bash
go run cmd/test-hp-scheduler/main.go
```

**验证点**:
- [ ] 工作池创建成功
- [ ] 任务队列初始化
- [ ] 配置加载正确

**预期输出**:
```
✅ HPScheduler 初始化成功
   工作协程数: 10
   队列容量: 1000
```

#### 2.2 任务提交测试

**目的**: 验证能正确提交任务

**测试步骤**:
```go
hpScheduler := scheduler.NewHPScheduler(cfg)
err := hpScheduler.Submit(ctx, task)
```

**验证点**:
- [ ] 任务入队成功
- [ ] 队列不阻塞（非满时）
- [ ] 返回任务 ID

#### 2.3 并发执行测试

**目的**: 验证多任务并发执行

**测试步骤**:
```go
var wg sync.WaitGroup
for i := 0; i < 100; i++ {
    wg.Add(1)
    hpScheduler.Submit(ctx, Task{
        ID: fmt.Sprintf("task-%d", i),
        Execute: func() error {
            defer wg.Done()
            time.Sleep(100 * time.Millisecond)
            return nil
        },
    })
}
wg.Wait()
```

**验证点**:
- [ ] 所有任务都执行
- [ ] 并发度符合配置
- [ ] 无死锁

**性能指标**:

| 指标 | 目标 |
|------|------|
| 100 任务完成时间 | < 2s（10 并发） |
| 任务吞吐量 | > 50 任务/秒 |

#### 2.4 优先级测试

**目的**: 验证高优先级任务优先执行

**测试步骤**:
```go
// 提交低优先级任务
hpScheduler.Submit(ctx, Task{Priority: 1, ...})
// 提交高优先级任务
hpScheduler.Submit(ctx, Task{Priority: 10, ...})
```

**验证点**:
- [ ] 高优先级任务先执行
- [ ] 低优先级任务后执行

#### 2.5 队列满载测试

**目的**: 验证队列满时的行为

**测试步骤**:
```go
// 填满队列
for i := 0; i < queueCapacity + 10; i++ {
    err := hpScheduler.Submit(ctx, longRunningTask)
    if err != nil {
        log.Printf("任务 %d 提交失败: %v", i, err)
    }
}
```

**验证点**:
- [ ] 队列满时返回错误或阻塞
- [ ] 不丢失任务
- [ ] 背压处理正确

#### 2.6 错误处理测试

**目的**: 验证任务执行错误的处理

**测试步骤**:
```go
hpScheduler.Submit(ctx, Task{
    Execute: func() error {
        return fmt.Errorf("模拟错误")
    },
    OnError: func(err error) {
        log.Printf("任务失败: %v", err)
    },
})
```

**验证点**:
- [ ] 错误被捕获
- [ ] OnError 回调执行
- [ ] 不影响其他任务

#### 2.7 优雅关闭测试

**目的**: 验证调度器优雅关闭

**测试步骤**:
```go
// 提交一些长时间任务
for i := 0; i < 10; i++ {
    hpScheduler.Submit(ctx, longTask)
}
// 立即关闭
hpScheduler.Shutdown(ctx)
```

**验证点**:
- [ ] 等待正在执行的任务完成
- [ ] 拒绝新任务
- [ ] 超时后强制关闭

---

### 3. 集成测试

#### 3.1 数据采集任务测试

**目的**: 验证数据采集任务的调度

**测试步骤**:
```go
scheduler.AddTask("collect", "*/5 * * * *", func() {
    collector.CollectAll(ctx)
})
scheduler.Start(ctx)
```

**验证点**:
- [ ] 每 5 分钟执行一次
- [ ] 数据库有新数据
- [ ] 日志记录正确

#### 3.2 套利分析任务测试

**目的**: 验证套利分析任务的调度

**测试步骤**:
```go
scheduler.AddTask("analyze", "*/10 * * * *", func() {
    engine.FindOpportunities(ctx)
})
```

**验证点**:
- [ ] 每 10 分钟执行一次
- [ ] 分析结果保存
- [ ] 触发执行（如果有机会）

---

## 🔧 测试命令汇总

```bash
# 基础调度器测试
go run cmd/test-scheduler/main.go

# 高性能调度器测试
go run cmd/test-hp-scheduler/main.go

# 完整调度测试（长时间运行）
go run cmd/test-scheduler/main.go -duration 30m

# 压力测试
go run cmd/test-hp-scheduler/main.go -tasks 10000
```

---

## 📊 测试检查清单

| 测试项 | 状态 | 备注 |
|--------|:----:|------|
| Scheduler 初始化 | ⬜ | |
| 任务注册 | ⬜ | |
| 启动/停止 | ⬜ | |
| cron 执行 | ⬜ | 需要等待 |
| HPScheduler 初始化 | ⬜ | |
| 任务提交 | ⬜ | |
| 并发执行 | ⬜ | |
| 优先级 | ⬜ | |
| 队列满载 | ⬜ | |
| 错误处理 | ⬜ | |
| 优雅关闭 | ⬜ | |

---

## ⚠️ 注意事项

1. cron 任务测试需要等待触发时间
2. 高性能调度器测试注意资源消耗
3. 长时间运行测试注意内存泄漏
4. 并发测试注意数据竞争
