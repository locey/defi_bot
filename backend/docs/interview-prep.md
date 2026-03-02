# DeFi 套利机器人 — 面试准备手册

> 本文档基于项目真实代码，为面试场景设计了完整的技术叙述框架。
> 每个场景包含：背景故事、技术深度、追问预判、代码引用。

---

## 一、项目概述话术（30 秒版本）

> 我主导开发了一个部署在 Arbitrum L2 上的 DeFi 套利系统，采用事件驱动架构，覆盖从链上数据采集到自动执行的完整流水线。系统由 Go 后端 + Solidity 合约组成，支持 Uniswap V2/V3、SushiSwap、Camelot 等多个 DEX 的跨平台套利，包含闪电贷集成、蜜罐检测、私有交易提交等关键模块。整个项目经历了一次从定时轮询到事件驱动的架构重构，端到端延迟从 ~2 秒优化到 <200ms。

---

## 二、团队与角色描述

> 3 人团队，开发周期约 4 个月。
>
> - **我（后端架构 + 核心策略）：** 负责 Go 后端全部设计与实现，包括事件驱动数据引擎、策略计算、执行管道、性能优化、安全审计和可观测性。在合约工程师离开后，我也接手了合约的安全修复工作。
> - **合约工程师（1人）：** 负责初版 Solidity 合约开发，包括 ArbitrageCore、FlashLoanArbitrage、DEX 集成合约。
> - **前端工程师（1人，兼职）：** 负责 Dashboard 开发（Next.js + Tailwind CSS）。

---

## 三、核心面试场景

### 场景 1：系统架构设计

**面试官：** "请介绍你这个项目的整体架构。"

**回答框架：**

```
三层架构：数据层 → 策略层 → 执行层

数据层（事件驱动）:
  WebSocket 订阅 Sync/Swap 事件 → 内存价格引擎（map[address]*PoolState）
  Multicall3 批量查询做补充（Tier2/3 池子）
  不依赖 DB 做实时计算

策略层（图算法 + AMM 数学）:
  邻接表构建代币图 → DFS 搜索 3-5 跳环形路径
  V2: 恒定乘积公式 x * y = k
  V3: 基于 sqrtPriceX96 和 liquidity 的 tick-level 精确计算
  二分法搜索最优投入金额

执行层（模拟 + 私有提交）:
  eth_call 模拟验证 → 确认利润 > Gas → Flashbots Protect 私有提交
  异步 DB 写入不阻塞热路径
```

**关键数据点：**
- 监控 200+ 个池子，覆盖 6 个 DEX
- 端到端延迟 <200ms（从价格变化到交易提交）
- Multicall3 将 500 次 RPC 压缩为 1 次

**追问预判：**

*Q: "为什么不用数据库做实时计算？"*

> 最初版本用 PostgreSQL 存储价格数据，策略引擎从 DB 读取后计算。Profiling 发现 DB 读写占了端到端延迟的 62%。套利竞争是毫秒级的，任何 I/O 都是致命瓶颈。所以我把热路径数据全部迁入内存（Go struct + sync.RWMutex），DB 只用于异步写入历史记录和 Dashboard 展示。

*Q: "Redis 呢？用在哪里？"*

> 当前单实例部署不需要 Redis。Redis 适合多实例场景下的分布式锁（防止重复执行同一套利机会）和跨服务状态共享。我们保留了 Redis 集成代码，但在配置中默认关闭，减少不必要的延迟。

---

### 场景 2：安全漏洞发现与修复

**面试官：** "你提到做了合约安全审计，发现了什么问题？"

**回答（按严重级别排列）：**

#### 漏洞 1：闪电贷还款机制错误（致命）

> `FlashLoanArbitrage.sol` 的 `executeOperation` 回调中有三个相关 Bug：
>
> **Bug A：** `msg.sender` 校验错误。Aave LendingPool 调用回调时，`msg.sender` 是 LendingPool 地址，不是 FlashLoanRouter。原代码检查的是 Router，导致所有回调都会 revert。
>
> **Bug B：** `approve` 目标错误。Aave 还款机制是在回调返回后自动 `transferFrom`，所以需要 approve 给 LendingPool。原代码 approve 给了 FlashLoanRouter。
>
> **Bug C：** 利润先于还款转走。在 approve 之前就把 `grossProfit` 转给了平台钱包，导致余额不足供 Aave 扣款。

```solidity
// 修复后的关键代码（FlashLoanArbitrage.sol）

// 1. 校验 msg.sender 是 LendingPool
address expectedLendingPool = flashLoanRouter.getLendingPool(
    FlashLoanRouter.LendingPlatForm.Aave_V2
);
require(msg.sender == expectedLendingPool, "caller must be LendingPool");

// 2. 校验 initiator 是 FlashLoanRouter
require(initiator == address(flashLoanRouter), "initiator must be FlashLoanRouter");

// 3. approve 给 LendingPool（Aave 自动 transferFrom）
IERC20(asset).approve(msg.sender, totalDebt);

// 4. 然后才转利润（此时余额 = amountOut，approve 了 totalDebt，转出 grossProfit 后余额正好 = totalDebt）
IERC20(asset).safeTransfer(platFormWallet, grossProfit);
```

#### 漏洞 2：权限配置不匹配（致命）

> `FlashLoanRouter.requestFlashLoan` 设置了 `onlyAdmin` 修饰器，但调用者是 `FlashLoanArbitrage` 合约（不是 admin），每次调用必然 revert。

```solidity
// 修复：引入 authorizedCallers 映射
mapping(address => bool) public authorizedCallers;

modifier onlyAuthorized() {
    require(msg.sender == admin || authorizedCallers[msg.sender], "not authorized");
    _;
}
```

#### 漏洞 3：unchecked 下溢（高危）

> `ArbitrageCore.sol` 中 `unchecked { actProfit = balanceAfter - balanceBefore }` 如果套利亏损（balanceAfter < balanceBefore），在 unchecked 块中不会 revert，而是下溢产生一个巨大的正数，绕过后续的利润检查。

```solidity
// 修复：移除 unchecked，添加显式亏损保护
require(balanceAfter >= balanceBefore, "arbitrage resulted in loss");
uint256 actProfit = balanceAfter - balanceBefore;
```

#### 漏洞 4：数组未初始化（致命）

> `DoubleRouterIntegration.sol` 的 `doubleRouterArbCheck` 中声明了动态数组 `address[] memory stepPath` 但未分配内存就直接赋值，运行时会崩溃。

```solidity
// 原代码（崩溃）
address[] memory stepPath;
stepPath[0] = from; // 访问未分配的内存

// 修复
address[] memory stepPath = new address[](2);
stepPath[0] = from;
stepPath[1] = to;
```

**追问预判：**

*Q: "你怎么发现这些 Bug 的？"*

> 组合了三种方法：
> 1. **代码审计**——逐行对照 Aave 闪电贷的官方文档，追踪 `msg.sender` 和 `initiator` 在整个调用链中的值
> 2. **Foundry 单元测试**——写了 mock LendingPool 来模拟完整的闪电贷回调流程
> 3. **eth_call 模拟**——在 fork 的链状态上模拟执行，观察 revert 原因

---

### 场景 3：性能优化

**面试官：** "你说端到端延迟从 2 秒优化到 200ms，具体做了什么？"

**回答：**

> 三轮优化：
>
> **第一轮：消除 I/O 瓶颈**
> 原来的流程：RPC 采集 → 写 PostgreSQL → 策略从 DB 读 → 计算 → 写结果。每个分析周期 4 次 DB 操作。
> pprof 分析发现 DB 占了 62% 的时间。
> 解决方案：价格数据全部放内存（`map[common.Address]*PoolState`），DB 只用于异步写历史记录（通过 buffered channel + batch insert）。
>
> **第二轮：优化 RPC 调用**
> 原来逐个调用 `getReserves()`，100 个池子 = 100 次 RPC。
> 改用 Multicall3 合约批量调用，一次 RPC 获取全部数据（最多 500 个池子一批）。
>
> **第三轮：事件驱动替代轮询**
> 原来 10-30 秒轮询一次，改为 WebSocket 订阅 Sync/Swap 事件。
> 价格变化实时推送到内存引擎，只重新计算受影响的路径（增量检测）。

```go
// 异步 DB 写入（不阻塞热路径）
type AsyncDBWriter struct {
    db       *gorm.DB
    recordCh chan *asyncWriteRequest  // 缓冲 1000 条
}

func (w *AsyncDBWriter) EnqueueRecord(opp, result) {
    select {
    case w.recordCh <- &asyncWriteRequest{opp, result}:
    default:
        // channel 满了就丢弃，热路径绝不阻塞
    }
}
```

---

### 场景 4：Uniswap V3 数学计算

**面试官：** "V2 和 V3 的价格计算有什么区别？你怎么实现 V3 的？"

**回答：**

> **V2（恒定乘积）：** `x * y = k`，流动性均匀分布在 0 到无穷的价格范围内。计算简单：
> `amountOut = (amountIn * fee * reserveOut) / (reserveIn * 10000 + amountIn * fee)`
>
> **V3（集中流动性）：** 流动性分布在特定的 tick 区间内。核心变量是 `sqrtPriceX96`（Q64.96 定点数格式的价格平方根）和 `liquidity`（当前 tick 范围的活跃流动性）。
>
> 初版系统犯了一个严重错误——V3 直接调用 V2 的公式。这在 concentrated liquidity 场景下偏差可以超过 30%。

```go
// V3 精确计算核心逻辑（v3_math.go）

// token0 → token1：价格下降
// sqrtPriceAfter = (L * sqrtPrice) / (L + amount0 * sqrtPrice / Q96)
// amount1 = L * (sqrtPrice - sqrtPriceAfter) / Q96

func getAmount1ForAmount0(sqrtPriceX96, liquidity, amount0 *big.Int) (*big.Int, error) {
    numerator := new(big.Int).Mul(amount0, sqrtPriceX96)
    numerator.Div(numerator, Q96)

    denominator := new(big.Int).Add(liquidity, numerator)
    sqrtPriceAfterX96 := new(big.Int).Mul(liquidity, sqrtPriceX96)
    sqrtPriceAfterX96.Div(sqrtPriceAfterX96, denominator)

    priceDiff := new(big.Int).Sub(sqrtPriceX96, sqrtPriceAfterX96)
    amount1 := new(big.Int).Mul(liquidity, priceDiff)
    amount1.Div(amount1, Q96)

    return amount1, nil
}
```

> 全程使用 `big.Int` 整数运算，避免浮点精度丢失。这是从 Uniswap V3 官方 `SqrtPriceMath` 库移植过来的 Go 实现。

---

### 场景 5：MEV 防护策略

**面试官：** "你的交易在公共 mempool 里，怎么防止被抢跑？"

**回答：**

> 两层防护：
>
> **合约层：** 每笔交易设置精确的 `minAmountOut`（滑点保护），三明治攻击者即使推高价格，如果实际输出低于我们的最小值，交易会自动 revert，攻击者只能白付 Gas。
>
> **提交层：** 在 Ethereum 主网使用 Flashbots Protect RPC（`https://rpc.flashbots.net`），交易不进入公共 mempool，直接发给区块构建者。在 Arbitrum 上，Sequencer 使用 FCFS（先到先服务）排序，我们使用低延迟直连以获取排序优势。

```go
// private_tx.go
type PrivateTxSender struct {
    config        *PrivateTxConfig
    privateClient *ethclient.Client  // Flashbots / Sequencer 专用连接
}

func (s *PrivateTxSender) SendTransaction(ctx, signedTx, fallbackClient) error {
    if s.privateClient != nil {
        err := s.privateClient.SendTransaction(ctx, signedTx)
        if err == nil { return nil }
        // 私有通道失败则回退公共 RPC
    }
    return fallbackClient.SendTransaction(ctx, signedTx)
}
```

> 另外，每笔交易在提交前都经过 `eth_call` 模拟验证：

```go
// simulator.go — 关键逻辑
func (s *Simulator) SimulateArbitrage(ctx, params) (*SimResult, error) {
    // 1. 构建与真实交易相同的 calldata
    // 2. eth_call 模拟执行（不消耗 Gas）
    _, err := ethClient.CallContract(ctx, callMsg, nil)
    if err != nil {
        // 模拟失败 = 合约会 revert = 不提交真实交易
        return &SimResult{Profitable: false, Error: err.Error()}, nil
    }
    // 3. 估算 Gas 成本，计算净利润
    // 4. 只有 净利润 > 0 才提交
}
```

---

### 场景 6：差异化竞争策略

**面试官：** "MEV 竞争这么激烈，你作为一个小团队怎么跟专业搜索者竞争？"

**回答：**

> 不正面竞争，而是做差异化。专业 MEV 搜索者的基础设施成本高（自建节点 $2000+/月，co-location $1000+/月），所以他们只做 $10+ 的机会。我们的基础设施成本几乎为零（$5-70/月），$1-10 的机会对我们是正利润，对他们是亏损。

> 我们的 "Smart Tail" 策略专注三个方向：
>
> 1. **长尾代币多跳套利**——3-5 跳的复杂路径，涉及低流动性代币。专业 Bot 嫌路径长、利润低不做。我们降低了利润门槛（0.1% vs 行业标准 0.3%），扩展了搜索深度到 5 跳。
>
> 2. **蜜罐检测器**——做长尾必须有安全过滤。我们用 `eth_call` 模拟买入 + 卖出，检查卖出税（>10% 标记危险）、合约代码大小、可疑 function selector、黑名单。

```go
// honeypot_detector.go — 检测流程
func (d *HoneypotDetector) detectHoneypot(ctx, token) (*HoneypotResult, error) {
    // 1. 合约代码大小检查（> 25KB 提醒）
    // 2. 调用 totalSupply() 确认 ERC20
    // 3. 调用 decimals() 检查合理性
    // 4. 扫描 function selector 蜜罐特征
    // 5. 结果缓存 30 分钟 + 黑名单
}
```

> 3. **新池子狙击**——监听 Factory 合约的 `PairCreated` / `PoolCreated` 事件，在新池子创建后几秒内完成价格发现和套利。

---

### 场景 7：可观测性与生产运维

**面试官：** "你怎么知道系统在正常工作？出了问题怎么排查？"

**回答：**

> 三个层面：
>
> **Prometheus 指标**——采集了 15+ 个关键指标，包括：
> - `arb_opportunities_found_total`（发现的机会数，按路径长度和协议分类）
> - `arb_executions_total`（执行次数，按 success/failed 分类）
> - `arb_profit_per_execution_usd`（每笔利润分布直方图）
> - `arb_end_to_end_latency_seconds`（端到端延迟分布）
> - `arb_rpc_latency_seconds`（RPC 延迟，按方法分类）
>
> **Telegram 实时告警**——执行成功（附利润和 Gas）、执行失败（附原因）、系统异常（RPC 断连、Gas 价格异常）都会实时推送。
>
> **结构化日志**——使用 zerolog 输出 JSON 格式日志，每条日志带有模块标签（Collector/Strategy/Executor），方便 grep 和 ELK 分析。

---

## 四、高频追问清单

| 问题 | 回答要点 |
|------|----------|
| 为什么选 Go 而不是 Rust/Python？ | Go 的并发模型（goroutine + channel）天然适合事件驱动架构；编译速度快方便快速迭代；go-ethereum 是 Go 生态的，集成零摩擦 |
| 为什么选 Arbitrum？ | L2 Gas 便宜（~$0.01/tx vs L1 $5-50）；出块快（250ms）；TVL 在 L2 中最高（$10B+）；DEX 生态丰富 |
| 闪电贷和自有资金的区别？ | 闪电贷无需本金但有 0.05-0.09% 手续费，且必须在同一交易内还款；自有资金通过 Vault（ERC4626 标准）管理，允许外部用户参与收益分享 |
| 怎么处理交易失败？ | eth_call 模拟在前过滤 90%+ 的失败；剩余失败通过 Gas 估算 buffer（20-30%）覆盖；交易超时 15s 自动加速（Gas x 1.1 重发） |
| 合约怎么升级？ | UUPS 代理模式（EIP-1967），通过 `_authorizeUpgrade` 控制升级权限，只有 owner 可以指定新实现地址 |
| 有没有做过 Gas 优化？ | 移除双重 approve 模式节省 ~20K Gas；缓存 storage 读取到 memory；减少不必要的 balanceOf 调用；deadline 从 300s 缩短到 120s |
| 测试怎么做的？ | Foundry 单元测试（访问控制、边界条件、蜜罐模拟）；Hardhat 集成测试（完整套利流程）；eth_call 模拟（链上状态验证）|

---

## 五、数据指标（部署后可实际测量）

| 指标 | 目标值 |
|------|--------|
| 日均扫描池子数 | 5,000+ |
| 日均发现套利机会 | 200+ |
| 端到端延迟 | < 200ms |
| 合约 Gas 消耗 | < 400K per tx |
| 蜜罐检测准确率 | > 95% |
| 系统可用性 | > 99.5% |

---

## 六、技术亮点总结（面试尾声用）

1. **事件驱动架构重构** —— 从 10s 轮询到实时 WebSocket，延迟降低 10 倍
2. **5 个致命合约漏洞修复** —— 包括闪电贷还款、unchecked 下溢、权限绕过
3. **V3 精确数学实现** —— 基于 sqrtPriceX96 的 tick-level 计算替代 V2 近似
4. **eth_call 模拟验证** —— 零成本预执行确保每笔交易有利可图
5. **Smart Tail 差异化策略** —— 蜜罐检测 + 新池狙击 + 长尾代币，避开正面竞争
6. **生产级可观测性** —— Prometheus 15+ 指标 + Telegram 实时告警
