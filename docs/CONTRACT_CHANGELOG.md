# ArbitrageX 合约修改记录

> 日期: 2026-03-06
> 修改原因: 修复 V3 fee tier 无法区分的 CRITICAL bug + OZ v5 编译兼容

---

## 修改背景

### 问题发现

在审计中发现 `DoubleRouterIntegration` 合约使用 `mapping(address => uint24) v3RouterFee` 存储 V3 fee tier。但 Uniswap V3 的三个 fee tier (0.05%, 0.3%, 1.0%) 共享同一个 Router 地址 (`0xE592427A0AEce92De3Edee1F18E0157C05861564`)，导致：

1. 一个 Router 地址只能存储一个 fee tier
2. 后端检测到的 V3 0.05% (fee=500) 机会，链上实际按 0.3% (fee=3000) 执行
3. 利润差异导致 `require(currentAmount >= amountIn + minProfit)` 失败
4. **所有 eth_call 模拟返回 "DoubleRouter: insufficient profit" revert**

### 修复方案

在整个调用链中新增 `uint24[] feeTiers` 参数，让后端可以为每一步 swap 指定正确的 V3 fee tier。

---

## Solidity 合约修改详情

### 1. 接口修改

#### `interfaces/IArbitrage.sol`

```diff
 struct ArbitrageParams {
     address asset;
     address tokenOut;
     uint256 amountIn;
     address[] swapPath;
     address[] dexes;
+    uint24[] feeTiers;      // V3 fee tier per swap step (500/3000/10000); 0 = V2
     uint256 expectProfit;
     uint256 minProfit;
     bool   isCex;
 }
```

#### `interfaces/IDoubleRouterIntegration.sol`

```diff
 function doubleRouterSwap(
     address spot,
     address tokenIn,
     address tokenOut,
     uint256 amountIn,
     address[] calldata swapPath,
     address[] calldata dexes,
+    uint24[] calldata feeTiers,
     uint256 expectProfit,
     uint256 minProfit
 ) external returns(uint256 amountOut);
```

#### `interfaces/ISpotArbitrage.sol`

```diff
 function executeSwaps(
     address asset,
     address tokenOut,
     uint256 amountIn,
     address[] calldata swapPath,
     address[] calldata dexes,
+    uint24[] calldata feeTiers,
     uint256 expectProfit,
     uint256 minProfit,
     bool isCex
 ) external returns (uint256 amountOut);
```

#### `interfaces/IFlashLoanSimpleReceiver.sol`

```diff
 function executeFlashLoan(
     FlashLoanRouter.LendingPlatForm platform,
     address asset,
     uint256 amountIn,
     address[] calldata swapPath,
     address[] calldata dexes,
+    uint24[] calldata feeTiers,
     uint256 expectProfit,
     uint256 minProfit
 ) external;
```

### 2. 核心合约修改

#### `integrations/DoubleRouterIntegration.sol` — 核心修复

- `doubleRouterSwap()`: 接收 `uint24[] calldata feeTiers`，校验 `feeTiers.length == dexes.length`
- `_executeSingleSwap()`: 新增 `uint24 feeTier` 参数
- Fee 选择逻辑: `feeTier > 0 ? feeTier : v3RouterFee[routerAddr]`（fallback 3000）
- V3 检测: `if (feeTier > 0 || isV3Router[routerAddr])` — feeTier > 0 即为 V3

#### `core/SpotArbitrage.sol`

- `executeSwaps()`: 新增 `uint24[] calldata feeTiers`，转发到 `doubleRouterIntegration`
- `_handleCexFirst()`: 构建 `remainingFees` 数组，跳过 CEX 占位的 index 0
- `_handleCexLast()`: 构建 `dexFees` 数组
- 增加 underflow 保护: `require(balanceAfter >= balanceBefore, "CEX last: tokenOut balance decreased")`

#### `core/ArbitrageCore.sol`

- 从 `params.feeTiers` 提取 feeTiers
- 新增验证: `require(feeTiers.length == dexes.length, "feeTiers = dexes")`
- 转发到 `spotArbitrage.executeSwaps(..., feeTiers, ...)`
- **OZ v5 修复**: `SafeERC20Upgradeable` → `SafeERC20`, `ERC20Upgradeable` → `IERC20`

#### `core/FlashLoanArbitrage.sol`

- `executeFlashLoan()`: 新增 `uint24[] calldata feeTiers`，abi.encode 包含 feeTiers
- `executeOperation()`: abi.decode 包含 `uint24[] memory feeTiers`，转发到 spotArbitrage

### 3. Mock 合约修改

#### `Mock/MockDoubleRouterIntegration.sol`

- 实现新的 `doubleRouterSwap` 接口（含 feeTiers 参数）
- 增加真实代币转账模拟: `safeTransferFrom` + `mint` + `safeTransfer`

### 4. 测试修改

#### `test/SpotArbitrage.test.js`

- 所有 `executeSwaps` 调用增加 `feeTiers` 参数
- 新增测试: "混合 V3 fee tier"（不同 hop 使用不同 fee tier）
- OZ v5 适配: `OwnableUnauthorizedAccount`, `InvalidInitialization`

---

## Go 后端修改详情

### 1. 数据结构

#### `internal/strategy/types.go`

```diff
 type ArbitrageOpportunity struct {
     // ...
     IsCex        bool             `json:"is_cex"`
+    FeeTiers     []uint32         `json:"fee_tiers"`     // 每步 V3 fee tier
 }
```

#### `internal/executor/executor.go`

```diff
 type ArbitrageParams struct {
     // ...
     Dexes        []common.Address
+    FeeTiers     []uint32 // 每步 V3 fee tier (500/3000/10000); 0 = V2
     ExpectProfit *big.Int
     // ...
 }
```

#### `internal/executor/contract_caller.go`

```diff
 type FlashLoanParams struct {
     // ...
     Dexes        []common.Address
+    FeeTiers     []uint32
     ExpectProfit *big.Int
     // ...
 }
```

### 2. ABI 定义更新

`ArbitrageCoreABI` 和 `FlashLoanArbitrageABI` 常量中新增 `feeTiers` 字段:
```json
{"internalType":"uint24[]","name":"feeTiers","type":"uint24[]"}
```

### 3. ABI 编码

`buildCallData()` 和 `buildFlashLoanCallData()` 中:
```go
// []uint32 → []*big.Int (go-ethereum ABI encoding 要求)
feeTiersBig := make([]*big.Int, len(params.FeeTiers))
for i, ft := range params.FeeTiers {
    feeTiersBig[i] = new(big.Int).SetUint64(uint64(ft))
}
```

### 4. FeeTiers 数据来源

- **FastDetector**: 从 `PriceCache.Get(poolAddr).Fee` 获取每个池子的 fee
- **SpreadScanner→Scheduler**: 从 `BuyPool.Fee` 和 `SellPool.Fee` 获取
- **安全兜底**: 如果 `len(feeTiers) != len(dexes)`，创建全 0 数组（默认 V2）

---

## 修改文件汇总

### Solidity (10 文件)

| 文件 | 改动类型 |
|------|---------|
| `interfaces/IArbitrage.sol` | struct 新增字段 |
| `interfaces/IDoubleRouterIntegration.sol` | 函数签名新增参数 |
| `interfaces/ISpotArbitrage.sol` | 函数签名新增参数 |
| `interfaces/IFlashLoanSimpleReceiver.sol` | 函数签名新增参数 |
| `integrations/DoubleRouterIntegration.sol` | 核心修复 — fee 选择逻辑 |
| `core/SpotArbitrage.sol` | 转发 feeTiers + underflow 修复 |
| `core/ArbitrageCore.sol` | 转发 feeTiers + OZ v5 修复 |
| `core/FlashLoanArbitrage.sol` | abi.encode/decode feeTiers |
| `Mock/MockDoubleRouterIntegration.sol` | 实现新接口 |
| `test/SpotArbitrage.test.js` | 新增 feeTiers 测试 |

### Go (11 文件)

| 文件 | 改动类型 |
|------|---------|
| `internal/strategy/types.go` | struct 新增字段 |
| `internal/strategy/fast_detector.go` | 从 PriceCache 填充 feeTiers |
| `internal/strategy/strategy.go` | DB 写入序列化 feeTiers |
| `internal/executor/executor.go` | struct + Execute() 填充 |
| `internal/executor/contract_caller.go` | ABI + buildCallData + FlashLoan |
| `internal/executor/simulator.go` | buildCallData 编码 |
| `internal/executor/contract_caller_test.go` | 测试数据更新 |
| `internal/scheduler/high_performance_scheduler.go` | 转换 + 模拟 params |
| `cmd/test-executor/main.go` | 测试数据更新 |
| `cmd/e2e/main.go` | params 填充 |
| `cmd/_archived/full-validation/main.go` | 测试数据更新 (已删除) |

---

## 部署注意事项

### 合约需要升级

由于接口签名变更，以下合约需要升级部署:

1. **ArbitrageCore** (UUPS) — `upgradeToAndCall()` 新实现
2. **SpotArbitrage** (UUPS) — 同上
3. **DoubleRouterIntegration** (UUPS) — 同上

### 不可升级的合约

4. **FlashLoanArbitrage** — 需要重新部署（非 UUPS）
5. **ArbitrageVault** — 不需要修改（不涉及 feeTiers）

### 后端部署

6. 后端需要同步更新，否则 ABI 编码不匹配会导致所有交易 revert
7. 确保后端和合约**同时上线**
