# 套利系统修复上下文文档

**日期**: 2026-02-11  
**分支**: backend  
**状态**: 已完成测试，待推送远程

---

## 一、项目概述

这是一个 DeFi 套利机器人后端系统，运行在 Arbitrum 主网上，支持三种套利模式：
1. **CEX-DEX 套利**: 在 Binance (CEX) 和 Uniswap/Sushiswap (DEX) 之间套利
2. **跨 DEX 套利**: 在不同 DEX 之间的同一交易对套利
3. **单 DEX 套利 (V2-V3)**: 在同一 DEX 的 V2 和 V3 池子之间套利

---

## 二、本次修复内容

### 2.1 核心问题：UniswapV3 价格查询错误

**原因分析**：
1. `Multicall` 只支持 V2 的 `getReserves`，不支持 V3 的 `slot0`/`liquidity`
2. V3 价格计算中 decimals 调整方向错误
3. 数据库存储的 token 顺序与 V3 合约实际顺序不一致

**修复文件**：

#### `backend/pkg/web3/multicall.go`
- 新增 `uniswapV3PoolABI` 常量（`slot0`, `liquidity`, `token0`, `token1` 方法）
- 新增 `V3SlotResult` 结构体
- 新增 `GetV3SlotBatch` 和 `executeV3SlotBatch` 方法
- 新增 `SqrtPriceX96ToPrice` 辅助函数

#### `backend/pkg/cache/price_cache.go`
- `PoolPrice` 结构体新增 `SqrtPriceX96`, `Liquidity`, `IsV3` 字段
- 新增 `UpdateV3` 方法处理 V3 池子更新
- 修复 `calculatePriceFromSqrtX96WithDecimals` 函数
- **关键修复**: 处理数据库 token 顺序与合约顺序不一致的问题
  ```go
  // V3 合约按地址排序：小地址是 token0，大地址是 token1
  dbOrderMatchesContract := dbToken0Hex < dbToken1Hex
  if !dbOrderMatchesContract {
      // 数据库顺序与合约相反，交换 decimals
      contractDec0, contractDec1 = oldPrice.Decimals1, oldPrice.Decimals0
  }
  ```

#### `backend/internal/collector/fast_collector.go`
- `PoolTier` 结构体新增 `Decimals0`, `Decimals1` 字段
- `LoadAndClassifyPools` 从数据库加载 token decimals
- `pollPools` 区分 V2 和 V3 池子，分别调用对应的 Multicall 方法
- 新增 `isV3Protocol` 辅助函数

#### `backend/internal/cexdex/dex_price_adapter.go` (新文件)
- 实现 `DEXPriceProvider` 接口，将 `PriceCache` 适配给 CEX-DEX 检测器
- 池子选择策略：V3 优先 (score=1000) + 流动性评分
- **关键修复**: 价格转换逻辑
  ```go
  // poolPrice = db_token0/db_token1
  // CEX 价格 = quoteToken/baseToken (如 ETHUSDT=1978 表示 1 ETH = 1978 USDT)
  if baseIsToken0 {
      // baseToken 是 db_token0，需要 quoteToken/baseToken = 1/poolPrice
      finalPrice = 1.0 / poolPrice
  } else {
      // baseToken 是 db_token1，poolPrice 就是 quoteToken/baseToken
      finalPrice = poolPrice
  }
  ```

---

## 三、测试结果

### 3.1 CEX-DEX 套利 ✅
```
ETHUSDT: CEX=1979.87, DEX=1979.14, spread=0.037% (低于阈值)
BTCUSDT: CEX=68366.43, DEX=68213.20, spread=0.22% ✓ (触发套利)
```

### 3.2 跨 DEX 套利 ✅
- 高性能调度器正常运行
- 预计算路径数：2,239,432 条
- 利润阈值：3 跳 0.3%，4 跳 0.5%
- 当前市场无超阈值机会（正常）

### 3.3 单 DEX 套利 ✅
- V2-V3 策略代码已存在 (`v2v3_strategy.go`)
- 未在 main.go 中启用（可按需集成）

### 3.4 执行器 ✅
- 初始化成功，无 ABI panic 问题

---

## 四、关键数据库结构

### 交易对表 (trading_pairs)
```sql
SELECT tp.pair_address, t0.symbol as token0, t1.symbol as token1, 
       t0.decimals as token0_decimals, t1.decimals as token1_decimals,
       ex.protocol 
FROM trading_pairs tp 
JOIN tokens t0 ON t0.id = tp.token0_id 
JOIN tokens t1 ON t1.id = tp.token1_id 
JOIN exchanges ex ON ex.id = tp.exchange_id;
```

### 重要池子地址
| Symbol | Pool Address | Protocol | token0 | token1 |
|--------|--------------|----------|--------|--------|
| ETHUSDT | 0x641C00A822e8b671738d32a431a4Fb6074E5c79d | uniswap_v3 | WETH | USDT |
| BTCUSDT | 0x5969EFddE3cF5C0D9a88aE51E47d721096A97203 | uniswap_v3 | USDT | WBTC |

**注意**: BTCUSDT 池子的数据库顺序 (token0=USDT, token1=WBTC) 与合约顺序 (token0=WBTC, token1=USDT) 相反！

---

## 五、配置文件

### `backend/configs/config.yaml`
```yaml
cexdex:
  enabled: true
  min_profit_rate: 0.002  # 0.2%
  min_profit_amount: 10   # $10
  max_trade_amount: 10000 # $10,000
  min_trade_amount: 100   # $100

scheduler:
  mode: high_performance
  
cex:
  enabled: true
  binance:
    enabled: true
    symbols:
      - ETHUSDT
      - BTCUSDT
      - ARBUSDT
      # ... 更多交易对
```

### 环境变量 (`backend/.env`)
```bash
BINANCE_API_KEY=xxx
BINANCE_API_SECRET=xxx
KEEPER_PRIVATE_KEY=xxx  # 用于执行交易
```

---

## 六、架构组件

```
┌─────────────────────────────────────────────────────────────┐
│                    main.go (入口)                            │
├─────────────────────────────────────────────────────────────┤
│                                                             │
│  ┌─────────────────┐    ┌─────────────────────────────────┐│
│  │ FastCollector   │    │ HighPerformanceScheduler        ││
│  │ - V2 Multicall  │───>│ - ArbitrageDetector (跨DEX)     ││
│  │ - V3 Multicall  │    │ - 2.2M+ paths precomputed       ││
│  └────────┬────────┘    └─────────────────────────────────┘│
│           │                                                 │
│           v                                                 │
│  ┌─────────────────┐    ┌─────────────────────────────────┐│
│  │ PriceCache      │    │ CEX-DEX Detector                ││
│  │ - Update (V2)   │───>│ - DEXPriceAdapter               ││
│  │ - UpdateV3 (V3) │    │ - Binance WebSocket             ││
│  │ - IsV3 flag     │    │ - spread1/spread2 calculation   ││
│  └─────────────────┘    └─────────────────────────────────┘│
│                                                             │
│  ┌─────────────────────────────────────────────────────────┐│
│  │ ArbitrageExecutor                                       ││
│  │ - Contract: ArbitrageCore                               ││
│  │ - Keeper private key                                    ││
│  └─────────────────────────────────────────────────────────┘│
└─────────────────────────────────────────────────────────────┘
```

---

## 七、Git 状态

### 已提交 (本地)
```
commit 65a4435
feat: fix UniswapV3 price calculation and CEX-DEX arbitrage detection

- Add V3 slot0/liquidity batch query support in multicall
- Fix V3 price calculation with correct decimal adjustment
- Handle database token order vs contract token order mismatch
- Add DEX price adapter for CEX-DEX arbitrage integration
- Fix finalPrice conversion logic for ETHUSDT/BTCUSDT pairs
- Enable proper V3 pool selection with liquidity-based scoring
```

### 待执行
```bash
git push origin backend  # SSH 连接问题，需手动执行
```

---

## 八、后续工作建议

1. **推送代码**: `git push origin backend`
2. **启用 CEX-DEX 执行**: 取消 `main.go` 中 CEX-DEX 执行的注释
3. **集成单 DEX 套利**: 将 `v2v3_strategy.go` 集成到 main.go
4. **监控**: 添加更多性能监控和告警
5. **优化**: 根据实际运行情况调整利润阈值

---

## 九、常用命令

```bash
# 编译
cd backend && go build ./...

# 运行服务
go run cmd/server/main.go

# 测试 CEX-DEX
go run cmd/test-cexdex/main.go

# 数据库查询
docker exec defi_bot_db_dev psql -U defi_user -d defi_arbitrage -c "SELECT ..."

# 查看日志中的套利机会
grep -E "CEX-DEX 价格比较|套利机会|spread" <log_file>
```

---

## 十、关键代码路径

| 功能 | 文件路径 |
|------|----------|
| V3 Multicall | `backend/pkg/web3/multicall.go` |
| 价格缓存 | `backend/pkg/cache/price_cache.go` |
| 数据采集 | `backend/internal/collector/fast_collector.go` |
| DEX 价格适配器 | `backend/internal/cexdex/dex_price_adapter.go` |
| CEX-DEX 检测器 | `backend/internal/cexdex/detector.go` |
| 跨 DEX 检测器 | `backend/internal/strategy/fast_detector.go` |
| 高性能调度器 | `backend/internal/scheduler/high_performance_scheduler.go` |
| 执行器 | `backend/internal/executor/executor.go` |
| 主程序 | `backend/cmd/server/main.go` |

---

**文档生成时间**: 2026-02-11 14:30
