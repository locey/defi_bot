# 03 - 数据采集模块测试 (internal/collector)

## 模块概述

数据采集模块负责从链上和 CEX 获取实时数据。核心组件：

- **Collector**: 统一的数据采集器，协调 DEX 和 CEX 数据采集
- **CexCollector**: Binance 等 CEX 数据采集 (REST API + WebSocket)
- **CollectorConcurrent**: 并发采集多个交易对数据
- **BlockSubscriber**: 新区块订阅
- **GasCollector**: Gas 价格采集
- **WSPriceFeed**: WebSocket 实时价格推送
- **DepthCollector**: Uniswap V3 深度数据采集

## 关键文件

| 文件 | 说明 |
|------|------|
| `internal/collector/collector.go` | 采集器主逻辑 |
| `internal/collector/collector_concurrent.go` | 并发采集 |
| `internal/collector/cex_collector.go` | CEX 数据采集 |
| `internal/collector/fast_collector.go` | 快速采集模式 |
| `internal/collector/gas_collector.go` | Gas 价格采集 |
| `internal/collector/block_subscriber.go` | 区块订阅 |
| `internal/collector/depth_collector.go` | V3 深度采集 |
| `internal/collector/ws_price_feed.go` | WebSocket 价格推送 |
| `cmd/test-collector/main.go` | 测试入口 |

## 测试命令

```bash
go run cmd/test-collector/main.go -config configs/config.yaml
```

## 测试用例

### 1. 环境初始化
- **验证**: 配置加载、数据库连接、RPC 连接、Redis 连接
- **预期**: 全部成功

### 2. 获取区块号
- **验证**: 能否获取最新 Arbitrum 区块号
- **预期**: 返回有效区块号

### 3. 交易对采集 (CollectTradingPairs)
- **验证**: 从链上 DEX Factory 合约发现交易对
- **流程**:
  1. 查询数据库中活跃的 DEX (exchanges 表)
  2. 查询数据库中活跃的 Token (tokens 表)
  3. 对每对 Token 组合，调用 Factory.getPool() 或 Factory.getPair()
  4. 新发现的交易对写入 trading_pairs 表
- **预期**: 
  - 发现多个交易对 (数量取决于 DEX 和 Token 配置)
  - 交易对写入数据库
  - 耗时较长 (大量 RPC 调用，可能触发速率限制)

### 4. 价格数据采集 (CollectPriceOnly)
- **验证**: 并发采集所有交易对的最新价格
- **流程**:
  1. 从数据库加载所有活跃交易对
  2. 对每个交易对，根据协议类型 (V2/V3) 获取储备量或 sqrtPriceX96
  3. 计算价格
  4. 批量写入 pair_reserves 和 price_records 表
- **预期**:
  - 成功采集到价格记录 (数量取决于交易对数和 RPC 成功率)
  - pair_reserves 表有新记录
  - price_records 表有新记录

### 5. Gas 价格采集 (CollectGasData)
- **验证**: 获取链上 Gas 价格并存储
- **预期**: 成功获取 Gas 价格，写入 gas_price_history 表

## 已知限制

- **公共 RPC 速率限制**: 大量并发 RPC 调用会触发 429 错误
- **binance_spot 协议不支持**: CEX 交易对 (如 ETHUSDT) 不支持 DEX 价格采集，这是预期行为

## 验证标准

| 检查项 | 通过条件 |
|--------|----------|
| 区块号获取 | > 0 |
| 交易对采集 | trading_pairs 表有记录 |
| 价格采集 | price_records 表有新记录 |
| Gas 采集 | gas_price_history 有记录 (可能因 RPC 限流失败) |
| 并发安全 | 无 panic、无数据竞争 |
