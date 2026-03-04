# DeFi 套利机器人 — 盈利障碍深度分析与修复路线

> 作者：Web3 专家评审（基于 2026-03-04 测试结果）  
> 目标：系统性梳理所有阻碍实际盈利的问题，按层级分级，给出具体的修复路线和预期收益  
> 当前状态：`enable_execution: false`，`dry_run: true`，系统**完全不执行**任何真实交易

---

## 一、执行摘要

### 60 秒 dry-run 测试发现的问题

| 币对 | DEX 价格 | CEX 价格 | 虚报利润率 | 根本原因 |
|------|---------|---------|-----------|---------|
| ETHUSDT | 0.0000 | 1967.53 | 无（被过滤，正常） | byTokenPair 索引为空，找不到池子 |
| LINKUSDT | 2.94e-50 | 8.76 | **2.97×10⁵⁹%** | 找到错误池子，价格方向反了 |
| PENDLEUSDT | 异常小值 | ~3.6 | **303%** | 同上 |
| BTCUSDT | 0.0000148 | 67905 | **4594 亿%** | 单位/方向错误 |

### 系统距离盈利的真实距离

```
当前状态：0 笔真实交易
│
├── Phase 0（基础可信度）: 修复 9 个根本性 Bug → dry-run 机会变可信
│   预计 1-2 天
│
├── Phase 1（首次执行）: 接入模拟 + 开启 DEX-DEX 执行 → 首笔交易上链  
│   预计 2-3 天，日收益 $0-5（公共 RPC，竞争激烈）
│
├── Phase 2（CEX-DEX 执行）: buildSwapData + 单边执行  
│   预计 3-5 天，日收益 $5-50（取决于波动）
│
└── Phase 3（基础设施升级）: 付费 RPC + Flash Loan  
    预计 1 周，日收益 $50-500
```

---

## 二、问题分级总览

```
Blocker（根本性障碍）：修不好就绝对无法盈利 ..................... 5 个
Critical（严重缺陷）：开启执行后只会亏钱 ........................ 6 个
High（重要问题）：影响检测准确性和资金安全 ...................... 9 个
Medium（中等问题）：影响性能和稳定性 ........................... 8 个
Low（技术债）：不影响盈利，但影响代码质量 ....................... 5 个
```

---

## 三、合约层问题（Solidity）

### [Blocker-1] ArbitrageCore.sol：ERC20 转账未用 safeTransfer

- **文件**：`contract/contracts/core/ArbitrageCore.sol`，L189, L215, L217
- **代码**：
  ```solidity
  // L189 - 将资金转给 SpotArbitrage
  ERC20Upgradeable(asset).transfer(address(spotArbitrage), amountIn);
  
  // L215 - 返还本金 + 利润给 Vault
  ERC20Upgradeable(asset).transfer(address(vault), amountIn + netProfitToVault);
  
  // L217 - 转平台费
  ERC20Upgradeable(asset).transfer(platFormWallet, platFormFee);
  ```
- **影响**：USDT、USDC.e 等非标准 ERC20 的 `transfer` 在失败时返回 `false` 而非 revert。合约不检查返回值，资金实际未转出但继续执行，造成资产损失或账目混乱。
- **修复**：引入 `SafeERC20` 库，改用 `safeTransfer`：
  ```solidity
  using SafeERC20 for IERC20;
  IERC20(asset).safeTransfer(address(spotArbitrage), amountIn);
  ```

### [Blocker-2] SpotArbitrage.sol：`_handleCexFirst` dexes 索引错位

- **文件**：`contract/contracts/core/SpotArbitrage.sol`，L197-200
- **代码**：
  ```solidity
  address[] memory remainingDexes = new address[](dexes.length - 1);
  for (uint i = 0; i < remainingDexes.length; i++) {
      remainingDexes[i] = dexes[i];  // ← 错误！应该是 dexes[i + 1]
  }
  ```
- **影响**：CEX-first 路径的 DEX 路由器地址全部错位（`dexes[0]` 是 CEX 的占位符，应从 `dexes[1]` 开始）。所有 CEX-first 套利路径的 DEX swap 100% 失败。
- **修复**：`remainingDexes[i] = dexes[i + 1]`

### [Blocker-3] SpotArbitrage.sol：`_handleCexLast` 利润校验无意义

- **文件**：`contract/contracts/core/SpotArbitrage.sol`，L256-257
- **代码**：
  ```solidity
  uint256 finalAmount = IERC20(tokenOut).balanceOf(address(this));
  require(finalAmount >= intermediateAmount, "CEX last: insufficient final amount");
  ```
- **影响**：`intermediateAmount` 是 DEX 输出的中间代币量（以 USDT 计，精度 6），`finalAmount` 是 `tokenOut`（可能是 WETH，精度 18）。跨精度比较毫无意义，无法起到利润保护作用。此外，合约累积的残余余额会使 `finalAmount` 虚高，造成账目混乱。
- **修复**：记录执行前的 `tokenOut` 余额，用差值而非总余额：
  ```solidity
  uint256 balanceBefore = IERC20(tokenOut).balanceOf(address(this));
  // ... CEX 交换 ...
  uint256 finalAmount = IERC20(tokenOut).balanceOf(address(this)) - balanceBefore;
  require(finalAmount >= minProfit, "CEX last: profit below minimum");
  ```

### [Critical-1] ArbitrageCore.sol：`actProfit > minProfit` 应为 `>=`

- **文件**：`contract/contracts/core/ArbitrageCore.sol`，L207
- **代码**：`require(actProfit > minProfit, "Profit below minimum");`
- **影响**：当 `actProfit == minProfit` 时 revert。保本交易被错误拒绝，浪费 Gas。
- **修复**：`require(actProfit >= minProfit, "Profit below minimum");`

### [High-1] ArbitrageCore.sol：`balanceBefore` 计算含残余余额风险

- **文件**：`contract/contracts/core/ArbitrageCore.sol`，L187
- **问题**：若合约持有上次执行滞留的代币余额（非设计意图），`balanceBefore` 会偏高，导致 `actProfit = balanceAfter - balanceBefore` 计算出错。
- **修复**：增加合约余额清零断言，或在事件中打印 `balanceBefore` 便于监控。

---

## 四、数据层问题（PriceCache）

### [Blocker-4] price_cache.go：`Update/UpdateV3` 不更新 `byTokenPair` 索引

- **文件**：`backend/pkg/cache/price_cache.go`，L100-309
- **问题**：只有 `UpdateWithMetadata`（L312）才调用 `updateIndex()`，而 FastCollector 的 WebSocket 事件（Tier1，最高频的路径）调用的是 `Update` 和 `UpdateV3`，完全不走索引更新。
- **影响**：
  - `GetByTokenPair(WETH, USDT)` 永远返回空 → ETHUSDT 的 DEX 价格为 0
  - SpreadScanner 的 `incrementalScan` 完全失效
  - CEX-DEX 适配器在索引未建立时触发全量遍历 fallback，严重影响性能
- **修复**：在 `Update` 和 `UpdateV3` 中，若 `PoolPrice` 已有 Token0/Token1 元数据，调用 `updateIndex()`：
  ```go
  // 在 Update() 函数中，c.prices.Store 之后：
  if newPrice.Token0 != (common.Address{}) {
      c.updateIndex(newPrice)
  }
  ```

### [Blocker-5] price_cache.go：V2 与 V3 price 字段语义相反

- **文件**：`backend/pkg/cache/price_cache.go`，L247-259
- **问题**：
  - V2：`Price = reserve1 / reserve0 = token1 / token0`（L136：`adjustPriceByDecimals(rawPrice, decimals0, decimals1)`）
  - V3：当 `dbOrderMatchesContract == true` 时，将合约价格（token1/token0）取倒数存入 `Price`（L252：`newPriceFloat = 1.0 / contractPrice`）
  - 最终 V3 的 `Price` 是 `token0/token1`，而 V2 的 `Price` 是 `token1/token0`，**方向相反**
- **影响**：DEX-DEX 价差检测（SpreadScanner 对比同一代币对的 V2/V3 价格）会得到错误的价差方向，套利方向判断反了。
- **修复**：统一 V3 价格语义为 `token1/token0`（与 V2 一致），移除 `1.0 / contractPrice` 取倒数逻辑：
  ```go
  if dbOrderMatchesContract {
      // db 顺序与合约相同，price = contractPrice（token1/token0）
      newPriceFloat = contractPrice
  } else {
      // db 顺序与合约相反，需要取倒数使得语义统一为 db_token1/db_token0
      if contractPrice > 0 {
          newPriceFloat = 1.0 / contractPrice
      }
  }
  ```

### [Critical-2] price_cache.go：首次 `Update()` 不调整 decimals

- **文件**：`backend/pkg/cache/price_cache.go`，L123-138
- **代码**：`if oldPrice != nil { ... if oldPrice.Decimals0 > 0 { newPrice.Price = adjustPriceByDecimals(...) } }`
- **问题**：`oldPrice != nil` 判断使得首次更新时不做 decimals 调整。WETH/USDC 池首次价格约 `1e12`（原始 reserve1/reserve0），而非 `~2000`，触发错误的套利信号。
- **修复**：改为从 pool 的 Decimals 字段判断（而非 oldPrice 是否存在）：
  ```go
  if newPrice.Decimals0 > 0 || newPrice.Decimals1 > 0 {
      newPrice.Price = adjustPriceByDecimals(rawPrice, newPrice.Decimals0, newPrice.Decimals1)
  }
  ```

---

## 五、策略层问题（fast_detector, v3_math, profit_calculator）

### [Blocker-6] v3_math.go + profit_calculator.go：V3 fee 单位混淆（高估 100 倍）

- **文件**：`backend/internal/strategy/v3_math.go`，L63-65；`backend/internal/strategy/profit_calculator.go`
- **代码**：
  ```go
  // v3_math.go L63-65，注释自相矛盾：
  // "feeBps 30 = 0.3% = V3 fee 3000"  ← 注释说要传 30
  // 但调用方传入的是 pool.Fee（数据库值，是 3000 即 ppm 格式）
  feeAmount := new(big.Int).Mul(amountIn, big.NewInt(int64(feeBps)))
  feeAmount.Div(feeAmount, big.NewInt(10000))
  ```
- **问题**：Uniswap V3 的 fee 字段在数据库中存储为 ppm（parts per million，百万分之一）：`500 = 0.05%`，`3000 = 0.3%`，`10000 = 1%`。代码按 bps（basis points，万分之一）处理（`fee / 10000`），导致 3000 ppm 被当成 30% 扣除（实际应 0.3%）。
- **影响**：V3 池子手续费高估 100 倍 → 所有 V3 路径的净利润大幅低估 → 真实有利润的 V3 机会全被过滤
- **修复**：修改除数为 1,000,000：
  ```go
  // V3 fee 是 ppm（百万分之一）
  feeAmount := new(big.Int).Mul(amountIn, big.NewInt(int64(feeBps)))
  feeAmount.Div(feeAmount, big.NewInt(1_000_000))
  ```

### [Critical-3] fast_detector.go：测试金额与实际执行金额脱钩

- **文件**：`backend/internal/strategy/fast_detector.go`
- **问题**：
  - 利润率用 `testAmount = 1 token`（约 $2000 WETH 等值）计算
  - 执行时 `AmountIn = calculateOptimalAmount`（1% of reserves，大型池子可达数百万美元）
  - AMM 是非线性曲线：1 token 时 0.5% 利润率，100万 token 时因价格冲击可能变负
- **影响**：系统性误报套利机会，实际执行时因滑点亏钱
- **修复**：使用与 `calculateOptimalAmount` 相同量级的金额做利润率评估：
  ```go
  testAmount := calculateOptimalAmount(pool0, pool1) // 与执行金额一致
  amountOut := calculateSwapOutput(testAmount, ...)
  profitRate := (amountOut - testAmount) / testAmount
  ```

### [Critical-4] high_performance_scheduler.go：强制设 `MinProfit = 0`

- **文件**：`backend/internal/scheduler/high_performance_scheduler.go`，L433
- **代码**：`opp.MinProfit = big.NewInt(0)`
- **影响**：合约层的 `require(actProfit > minProfit)` 保护完全失效。即使套利实际亏损（actProfit = 0），也因 `0 > 0` 的条件不满足而 revert；但更危险的是，该设置传达给合约的意图是"接受任何结果"，在精确计算不准确时无任何兜底。
- **修复**：保留策略计算出的 `MinProfit` 值，只在调试/测试时设为 0：
  ```go
  // 删除强制覆盖，让策略的 MinProfit 透传到合约
  // opp.MinProfit = big.NewInt(0) ← 删除此行
  ```

### [High-2] v3_math.go：单 tick 假设，大额交易高估输出

- **文件**：`backend/internal/strategy/v3_math.go`，L42-91
- **问题**：`CalculateV3SwapOutput` 用单个 `liquidity` 值估算整笔交易。实际 V3 池的流动性分布在多个 tick 上，大额交易会穿越多个 tick，每个 tick 的流动性不同。
- **影响**：大额 V3 套利输出被系统性高估，提交的"套利"实际可能亏损
- **修复**：实现多 tick 遍历（需要 TickBitmap 数据）或在缺少完整 tick 数据时使用更保守的估算（如 10% 的 liquidity 上限）：
  ```go
  // 保守策略：假设只能使用 10% 的当前流动性
  effectiveLiquidity := new(big.Int).Div(liquidity, big.NewInt(10))
  amountOut, err = CalculateV3SwapOutput(sqrtPriceX96, effectiveLiquidity, amountIn, feeBps, zeroForOne)
  ```

### [High-3] fast_detector.go：`calculateOptimalAmount` 返回 1% 储备金

- **文件**：`backend/internal/strategy/fast_detector.go`
- **问题**：大型池子（如 Uniswap V3 WETH/USDC 储备可达数十亿美元），1% 就是数千万美元，远超闪贷上限和实际可用资金。
- **修复**：增加绝对上限，与 Vault 余额联动：
  ```go
  maxAmount := new(big.Int).SetUint64(100_000_000_000_000_000) // 0.1 ETH 上限
  optimalAmount := min(calculatedOptimal, maxAmount, vaultBalance*10/100)
  ```

---

## 六、执行层问题（executor, contract_caller, simulator）

### [Blocker-7] contract_caller.go：`SimulateArbitrage` 缺 `From` 字段

- **文件**：`backend/internal/executor/contract_caller.go`，L260-264
- **代码**：
  ```go
  gasEstimate, err := cc.web3Client.GetClient().EstimateGas(ctx, ethereum.CallMsg{
      To:    &cc.contractAddress,
      Data:  callData,
      Value: big.NewInt(0),
      // 缺少 From 字段！
  })
  ```
- **影响**：`EstimateGas` 时 `from = 0x000...000`，合约的 `onlybackCaller` 权限检查必然失败（因为零地址不是授权的 backCaller）。所有模拟永远 revert，`eth_call` 过滤层完全失效。
- **修复**：
  ```go
  gasEstimate, err := cc.web3Client.GetClient().EstimateGas(ctx, ethereum.CallMsg{
      From:  cc.keeperAddress,  // ← 补充 From 字段
      To:    &cc.contractAddress,
      Data:  callData,
      Value: big.NewInt(0),
  })
  ```

### [Critical-5] high_performance_scheduler.go：ChainID 默认为 1（以太坊主网）

- **文件**：`backend/internal/scheduler/high_performance_scheduler.go`，L621
- **代码**：`ChainID: 1, // 默认以太坊主网`
- **影响**：交易使用 chainID=1 签名，但发到 Arbitrum（chainID=42161）节点后会被拒绝。实际上代码在 L631 已经从 `appConfig` 覆盖，但 `CreateHighPerformanceScheduler` 的默认值设置是错误的，直接使用 `NewHighPerformanceScheduler` 时会有问题。
- **修复**：从 config 读取：`ChainID: int64(appConfig.Blockchain.ChainID)`（已有，确保所有创建路径都经过覆盖）

### [High-4] executor.go：eth_call 模拟被注释跳过

- **文件**：`backend/internal/executor/executor.go`，L104-105
- **代码**：`// 4. 跳过 eth_call 模拟（速度优先），直接提交交易`
- **影响**：开启执行后，所有机会盲发上链，只靠合约 revert 保护。在 Arbitrum 上每次 revert 损失约 $0.01-0.05 Gas，累积的无效 revert 持续耗费资金。
- **修复**：恢复 `Simulator.SimulateArbitrage()` 调用，仅在 `Profitable == true` 时发送交易。Arbitrum 上 eth_call 约 50-200ms，对于当前系统完全可以接受。

### [High-5] executor.go：利润统计包含平台费，数据虚高

- **文件**：`backend/internal/executor/executor.go`，L245
- **代码**：`return new(big.Int).SetBytes(lg.Data[32:64]) // profit`
- **问题**：事件格式为 `amountIn(0:32), profit(32:64), platFormFee(64:96), netProfitToVault(96:128)`。`profit` 是含平台服务费的总利润，机器人的净收益是 `netProfitToVault`。
- **修复**：解析 `lg.Data[96:128]` 获取 `netProfitToVault`。

### [High-6] executor.go：Receipt 轮询 1 秒，Arbitrum 出块 0.25 秒

- **文件**：`backend/internal/executor/executor.go`，L168-170
- **修复**：改为 250ms 轮询间隔。

### [High-7] private_tx.go：MEV 保护完全未接入主流程

- **文件**：`backend/internal/executor/private_tx.go`
- **问题**：`executor.go` 中无 `PrivateTxSender` 字段，`contract_caller.go` 直接调用 `execClient.SendTransaction`，完全绕过了 `private_tx.go`。
- **影响**：在竞争激烈时，交易可被其他 bot 抢跑或 sandwich。
- **修复**：在 `ContractCaller` 中引入 `privateTxSender`，执行路径优先走私有 RPC。

---

## 七、CEX-DEX 模块问题

### [Critical-6] detector.go：DEX 价格近零值未过滤

- **文件**：`backend/internal/cexdex/detector.go`，L177-183
- **代码**：
  ```go
  if dexPrice == 0 {
      return  // 只过滤严格等于 0，不过滤近零值
  }
  ```
- **实测数据**：LINK 的 DEX 价格 = `2.94×10⁻⁵⁰`，被当作真实价格，产生 `2.97×10⁵⁹%` 的虚假利润率。
- **修复**：增加合理区间校验：
  ```go
  // DEX 价格异常校验（价格应在 CEX 价格的 0.1% 至 1000 倍区间内）
  if dexPrice == 0 || dexPrice < cexPrice.BidPrice*0.001 || dexPrice > cexPrice.BidPrice*1000 {
      log.Warn("CEX-DEX: 无效 DEX 价格 symbol=%s dex=%.2e cex=%.4f",
          cexPrice.Symbol, dexPrice, cexPrice.BidPrice)
      return
  }
  ```

### [High-8] detector.go：Gas 成本硬编码，忽略 Arbitrum L1 数据费

- **文件**：`backend/internal/cexdex/detector.go`，L217
- **代码**：`gasCost := 0.05 // 硬编码 $0.05`
- **问题**：
  - Arbitrum L2 gas 实际约 $0.002-0.02
  - Arbitrum L1 calldata 费用（高峰期）：$0.1-1.0
  - 两者合计与硬编码值差距可达 10-50 倍
- **修复**：从链上实时读取：
  ```go
  // 使用 ArbGasInfo 预编译合约（0x000...006C）
  // 调用 getL1BaseFeeEstimate() + 估算 calldata bytes
  gasCostEth := estimateArbitrumGasCost(ctx, web3Client, callDataBytes)
  gasCost := gasCostEth * ethPrice
  ```

### [High-9] dex_price_adapter.go：全量 fallback 扫描无缓存

- **文件**：`backend/internal/cexdex/dex_price_adapter.go`，L174-190
- **问题**：每次 Binance tick 都可能遍历所有 80 个池子的字符串比较，无任何结果缓存。Binance ETHUSDT 每秒约 10-50 次更新，高频下此操作占用大量 CPU。
- **修复**：修复索引（Blocker-4）后此 fallback 不再被触发。另增加结果缓存（TTL 1 秒）防止索引空窗期频繁 fallback。

---

## 八、基础设施问题

### [High-10] 公共 RPC 延迟与稳定性

- **当前配置**：4 个公共节点（`arb1.arbitrum.io`、`publicnode.com`、`ankr.com`、`llamarpc.com`）
- **现实情况**：
  - 公共节点延迟：200-500ms（高峰期可达 2-5 秒）
  - 套利机会存活时间：WETH/USDC 等核心对 < 100ms，长尾对 0.5-30 秒
  - 在公共 RPC 下，绝大多数核心对机会在检测到时已被专业 bot 抢走
- **策略建议**：
  - 短期：继续用公共 RPC，但专注长尾代币对（机会存活时间更长，竞争更少）
  - 中期：接入 Alchemy/QuickNode，延迟降至 30-80ms

### [Medium-1] web3/client.go：单连接无重连

- **文件**：`backend/pkg/web3/client.go`
- **修复**：增加自动重连和指数退避重试逻辑。

### [Medium-2] price_monitor.go：WebSocket 重连失败无重试

- **文件**：`backend/internal/cexdex/price_monitor.go`，L302-309
- **代码**：`reconnect()` 失败后直接 return，进入 error → reconnect → fail → error 无效循环
- **修复**：增加指数退避重试：最多重试 10 次，初始等待 1s，最大等待 60s。

### [Medium-3] float64 精度问题（CEX-DEX 利润判断）

- **文件**：`backend/internal/cexdex/detector.go`，L190-205
- **问题**：WBTC 价格 ~68000 USD × 10^8 decimals = 6.8×10^12，超出 float64 有效精度边界（15-16 位），计算价差时低位误差可能与真实价差同量级。
- **修复**：利润判断改用 `big.Float`，核心精度计算中已有 `ConvertToBigInt` 函数（L437）但从未在主路径调用。

---

## 九、分阶段修复路线（按业务优先级）

### Phase 0：基础可信度（约 1-2 天）

**目标：让 dry-run 检测结果可信**

| 优先级 | 修复内容 | 文件 | 验收标准 |
|--------|---------|------|---------|
| P0.1 | 修复 `byTokenPair` 索引（Blocker-4） | price_cache.go | ETHUSDT DEX 价格 ≠ 0 |
| P0.2 | 修复 V3/V2 price 语义一致性（Blocker-5） | price_cache.go | V3 池价格方向与 V2 一致 |
| P0.3 | 修复 DEX 价格近零值过滤（Critical-6） | detector.go | LINK/AAVE 不再虚报天文数字 |
| P0.4 | 修复 V3 fee 单位（Blocker-6） | v3_math.go | V3 路径利润估算合理（< 10%） |
| P0.5 | 修复 `SimulateArbitrage` From 字段（Blocker-7） | contract_caller.go | eth_call 能正常执行 |
| P0.6 | 修复 `MinProfit=0` 强制覆盖（Critical-4） | scheduler | 合约利润保护恢复 |
| P0.7 | 修复首次 Update decimals（Critical-2） | price_cache.go | 启动后首批价格正确 |
| P0.8 | 修复合约 safeTransfer（Blocker-1） | ArbitrageCore.sol | USDT 路径不再静默失败 |
| P0.9 | 修复 `_handleCexFirst` dexes 错位（Blocker-2） | SpotArbitrage.sol | CEX-first 路径不再 100% revert |

**验收**：
- dry-run 中 CEX-DEX 不再出现 > 100% 利润率机会
- V3 路径利润估算 < 5%（合理范围）
- eth_call 模拟能正常执行并区分有利润 / 无利润路径

---

### Phase 1：首次真实执行（约 2-3 天）

**目标：开启 DEX-DEX 执行，首笔交易成功上链**

| 优先级 | 修复内容 | 文件 | 验收标准 |
|--------|---------|------|---------|
| P1.1 | 接入 Simulator 到 executor（High-4） | executor.go | eth_call 通过才发送 |
| P1.2 | 测试金额与执行金额对齐（Critical-3） | fast_detector.go | AmountIn 合理（< 0.1 ETH） |
| P1.3 | Arbitrum L1 数据费计算（High-8） | gas_estimator.go | Gas 成本误差 < 2x |
| P1.4 | Receipt 轮询 250ms（High-6） | executor.go | 确认延迟 < 500ms |
| P1.5 | 利润统计改为净利润（High-5） | executor.go | 报表数据准确 |
| P1.6 | 开启执行 | config.yaml | 首笔 DEX-DEX 交易上链无 revert |

---

### Phase 2：CEX-DEX 单边执行（约 3-5 天）

**目标：Binance 作为价格预言机，在 DEX 侧执行单边套利**

1. 修复 `_handleCexLast` 利润校验（Blocker-3）
2. 实现 `buildSwapData(opp *CEXDEXOpportunity) (*SwapParams, error)`
3. 实现 `ExecuteCEXDEX()` 方法（executor.go）
4. 接入 `private_tx.go` 到执行主路径（High-7）
5. 核心利润判断改用 `big.Float`（Medium-3）

**执行安全机制**：
- 每笔最大金额 $500（~0.25 ETH），扣除 Gas 后利润 > $2 才执行
- 单日最大亏损 $50 自动停止
- 机会有效期最长 500ms（超时丢弃）

---

### Phase 3：基础设施升级（持续）

1. **付费 RPC**：接入 Alchemy WebSocket，价格延迟从 200-500ms → 30-80ms
2. **WebSocket 重连**：指数退避重试（Medium-2）
3. **Flash Loan**：验证 Aave V3 完整路径（合约已部署 `FlashLoanRouter`、`FlashLoanArbitrage`）
4. **多 tick 遍历**：修复 V3 大额交易估算（High-2）
5. **Prometheus 指标**：每分钟检测次数、假机会过滤次数、真实机会通过率

---

## 十、盈利预期（现实评估）

| 阶段 | 条件 | 预期日均收益 | 主要来源 |
|------|------|------------|---------|
| Phase 0 | dry-run 验证 | $0 | 不执行 |
| Phase 1 | DEX-DEX 开启，公共 RPC | $0-5 | 长尾对偶发价差 |
| Phase 2 | + CEX-DEX 单边，公共 RPC | $5-50 | 大行情波动后 CEX-DEX 错位 |
| Phase 3 | + Alchemy + Flash Loan | $30-300 | 中低竞争对上的结构性机会 |
| 高波动市场（大行情日） | Phase 3 + | $300-3000 | CEX-DEX 价差持续 10-30 秒 |

### 竞争现实

- **WETH/USDC、WBTC/USDC**：已被 JIT（just-in-time liquidity）、Flashbots bundle 等专业 MEV bot 覆盖，公共 RPC 下几乎无法抢到
- **ARB、PENDLE、GMX 等**：流动性中等，竞争者较少，是当前阶段最现实的目标
- **长尾代币**：存活时间更长（5-30 秒），适合延迟较高的系统，蜜罐风险需控制

### 成本结构（已优化状态）

| 成本项 | 金额 | 说明 |
|--------|------|------|
| Arbitrum L2 Gas | $0.002-0.02 / 笔 | 基础执行费 |
| Arbitrum L1 数据费 | $0.01-0.5 / 笔 | 取决于 L1 gas price |
| 合约平台费 | 利润的 X% | 由 ConfigManager 设置 |
| 付费 RPC（Alchemy） | ~$50-200/月 | 约 3000 万次调用 |
| **最小利润门槛** | **> $0.5 / 笔** | 覆盖 Gas 后仍有净利润 |

---

## 附录：关键文件索引

| 文件 | 核心问题 | 修复阶段 |
|------|---------|---------|
| `contract/contracts/core/ArbitrageCore.sol` | safeTransfer，actProfit >= | Phase 0 |
| `contract/contracts/core/SpotArbitrage.sol` | dexes 索引错位，利润校验无意义 | Phase 0 |
| `backend/pkg/cache/price_cache.go` | 索引不更新，V2/V3 语义不一致，首次 decimals | Phase 0 |
| `backend/internal/cexdex/detector.go` | 近零价格不过滤，Gas 硬编码 | Phase 0 |
| `backend/internal/strategy/v3_math.go` | fee 单位混淆 | Phase 0 |
| `backend/internal/executor/contract_caller.go` | From 字段缺失 | Phase 0 |
| `backend/internal/scheduler/high_performance_scheduler.go` | MinProfit=0，ChainID | Phase 0 |
| `backend/internal/executor/executor.go` | Simulator 未接入，利润统计，轮询间隔 | Phase 1 |
| `backend/internal/strategy/fast_detector.go` | 测试/执行金额脱钩 | Phase 1 |
| `backend/internal/strategy/gas_estimator.go` | L1 数据费缺失 | Phase 1 |
| `backend/internal/executor/private_tx.go` | 未接入主流程 | Phase 2 |
| `backend/internal/cexdex/price_monitor.go` | WebSocket 重连无重试 | Phase 3 |
