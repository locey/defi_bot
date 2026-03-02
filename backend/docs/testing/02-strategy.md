# 02 - 策略引擎测试 (internal/strategy)

## 模块概述

策略引擎负责发现、评估、优化套利机会。核心组件：

- **StrategyEngine**: 策略引擎主逻辑，协调所有子模块
- **PathFinder**: 在多个 DEX 间查找套利路径
- **ProfitCalculator**: 计算预期利润 (含 gas 成本扣除)
- **GasEstimator**: 估算交易 gas 消耗
- **OpportunityValidator**: 验证套利机会有效性
- **Optimizer**: 优化输入金额使利润最大化

## 关键文件

| 文件 | 说明 |
|------|------|
| `internal/strategy/strategy.go` | 策略引擎主逻辑 |
| `internal/strategy/path_finder.go` | 路径查找算法 |
| `internal/strategy/profit_calculator.go` | 利润计算 |
| `internal/strategy/gas_estimator.go` | Gas 估算 |
| `internal/strategy/opportunity_validator.go` | 机会验证 |
| `internal/strategy/optimizer.go` | 金额优化 |
| `internal/strategy/fast_detector.go` | 快速机会检测 |
| `internal/strategy/types.go` | 类型定义 |
| `cmd/test-strategy/main.go` | 测试入口 |

## 测试命令

```bash
go run cmd/test-strategy/main.go -config configs/config.yaml
```

## 测试用例

### 1. 配置加载
- **验证**: 能否正确加载 YAML 配置
- **预期**: 返回有效的配置对象，包含 Blockchain、Database、Dexes、Tokens 等

### 2. 数据库连接
- **验证**: 能否连接 PostgreSQL
- **预期**: 连接成功，无错误

### 3. RPC 连接
- **验证**: 能否连接 Arbitrum RPC
- **预期**: 连接成功，获取有效 ChainID

### 4. Redis 缓存
- **验证**: 能否连接 Redis
- **预期**: 连接成功 (可选，失败不阻塞)

### 5. 策略引擎创建
- **验证**: StrategyEngine 能否正确初始化
- **配置项**:
  - MinProfitRate: 从配置读取
  - MaxPathLength: 4
  - MinPathLength: 3
  - MaxSlippage: 从配置读取
  - GasMultiplier: 2.0
  - MaxConcurrentPaths: 50
  - BaseTokens: 从配置的 tokens 列表构建
  - SupportedDexes: 从配置的 dexes 列表构建
- **预期**: 引擎创建成功

### 6. 获取当前区块
- **验证**: 策略引擎依赖的区块号获取
- **预期**: 返回有效区块号

### 7. 查找套利机会
- **验证**: FindOpportunities 方法执行
- **流程**:
  1. 从数据库加载活跃交易对 (trading_pairs + exchanges + tokens)
  2. 获取最新储备量 (pair_reserves)
  3. 构建套利路径
  4. 并发评估路径利润
  5. 排序并返回
- **预期**: 
  - 方法正常返回 (不 panic)
  - 如果数据库中有交易对数据，可能返回 0 或多个机会
  - 如果没有交易对数据，返回 0 个机会 (正常)

### 8. 统计数据库机会
- **验证**: 能否查询 arbitrage_opportunities 表
- **预期**: 返回总数和最近 1 小时的数量

## 验证标准

| 检查项 | 通过条件 |
|--------|----------|
| 配置加载 | 无错误 |
| 数据库连接 | 无错误 |
| RPC 连接 | 区块号 > 0 |
| 引擎创建 | 非 nil |
| 套利查找 | 不 panic，返回结果 |
| 耗时 | FindOpportunities < 120s |
