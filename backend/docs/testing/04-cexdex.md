# 04 - CEX-DEX 套利模块测试 (internal/cexdex)

## 模块概述

CEX-DEX 模块检测中心化交易所 (CEX) 和去中心化交易所 (DEX) 之间的价差机会。核心组件：

- **PriceMonitor**: 实时监控 Binance 价格 (WebSocket + REST 降级)
- **DEXQuoter**: 实时从链上获取 DEX 报价 (Uniswap V3 Quoter)
- **Detector**: 对比 CEX/DEX 价格，检测套利机会
- **Executor**: 执行 CEX-DEX 套利交易

## 关键文件

| 文件 | 说明 |
|------|------|
| `internal/cexdex/price_monitor.go` | CEX 价格监控 (Binance WebSocket/REST) |
| `internal/cexdex/dex_quoter.go` | DEX 链上报价 (Uniswap V3 Quoter) |
| `internal/cexdex/detector.go` | 价差检测器 |
| `internal/cexdex/executor.go` | CEX-DEX 执行器 |
| `cmd/test-cexdex/main.go` | 测试入口 |

## 测试命令

```bash
go run cmd/test-cexdex/main.go
```

**注意**: 此测试为持续运行程序 (每 10 秒打印一次价格对比)，使用 Ctrl+C 退出。

## 测试用例

### 1. 数据库初始化
- **验证**: 能否连接到 PostgreSQL
- **预期**: 连接成功

### 2. Web3 客户端连接
- **验证**: 能否连接 Arbitrum RPC
- **预期**: ChainID = 42161

### 3. Binance 价格监控
- **验证**: 能否获取 Binance 实时价格
- **模式**: 
  - 首选 WebSocket 模式 (低延迟)
  - 降级 REST API 模式 (更稳定)
- **预期**:
  - WebSocket 连接或 REST API 轮询成功
  - 获取到 11 个交易对的价格

### 4. DEX 报价器
- **验证**: 能否从链上获取 DEX 实时价格
- **流程**:
  1. 根据 ChainID 获取 Quoter 配置 (Uniswap V3 Quoter 合约地址、交易对列表)
  2. 调用 QuoterV2.quoteExactInputSingle() 获取报价
  3. 定时刷新价格
- **预期**: DEX 报价器启动，监控 6 个交易对

### 5. 价差检测
- **验证**: 能否对比 CEX 和 DEX 价格
- **配置**:
  - MinProfitRate: 0.2% (Arbitrum 低 Gas 环境)
  - CheckInterval: 从配置读取
- **预期**:
  - 每 10 秒输出价格对比表 (Symbol | CEX Price | DEX Price | Spread)
  - 如果价差超过阈值，触发机会通知

### 6. 套利机会发现
- **验证**: 当价差足够大时，能否正确识别机会
- **预期**:
  - 通常活跃机会数 = 0 (市场效率高)
  - 如果有机会，输出 symbol、direction、profit_rate 等

## 测试结果解读

- **"active_opportunities=0"**: 正常。市场效率高时无套利机会
- **"WebSocket 连接失败"**: 正常。网络环境可能不支持 Binance WebSocket，会自动切换 REST API
- **"N/A" DEX Price**: 部分交易对在链上无对应池子

## 验证标准

| 检查项 | 通过条件 |
|--------|----------|
| 数据库连接 | 无错误 |
| Web3 连接 | ChainID = 42161 |
| CEX 价格 | 至少获取到部分交易对价格 |
| DEX 报价器 | 启动成功，监控 >= 1 个交易对 |
| 价差检测 | 能输出对比表，不 panic |
