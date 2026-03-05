# ArbitrageX 合约安全审计报告

> 审计日期: 2026-03-06
> 审计范围: 全部 31 个 Solidity 文件 (contracts/)
> 链: Arbitrum One (Chain ID: 42161)
> 审计轮次: 2 轮 (第一轮发现 → 修复 → 第二轮验证)

---

## 1. 审计范围

### 核心合约

| 合约 | 文件 | 链上地址 | 类型 |
|------|------|---------|------|
| ArbitrageCore | `core/ArbitrageCore.sol` | `0x0D14428b...` | UUPS 可升级 |
| ArbitrageVault | `core/ArbitrageVault.sol` | `0xBE312B10...` | ERC4626 |
| SpotArbitrage | `core/SpotArbitrage.sol` | `0xceCCf5D3...` | UUPS 可升级 |
| ConfigManage | `core/ConfigManage.sol` | `0x8dD68621...` | UUPS 可升级 |
| DoubleRouterIntegration | `integrations/DoubleRouterIntegration.sol` | `0xE075114d...` | UUPS 可升级 |
| FlashLoanArbitrage | `core/FlashLoanArbitrage.sol` | 未部署 | 非升级 |

### 其他合约

| 合约 | 文件 | 说明 |
|------|------|------|
| UniswapV2Integration | `integrations/UniswapV2Integration.sol` | V2 适配器 |
| UniswapV2V3Integration | `integrations/UniswapV2V3Integration.sol` | V2↔V3 跨协议 |
| FlashLoanRouter | `router/FlashLoanRouter.sol` | 闪电贷路由 |
| Mock 合约 (10 个) | `Mock/` | 测试用 |
| 接口 (9 个) | `interfaces/` | 接口定义 |

---

## 2. 已修复的问题

### [已修复] CRITICAL-1: V3 Fee Tier 无法区分 (第一轮发现)

**原问题**: `DoubleRouterIntegration` 用 `mapping(address => uint24) v3RouterFee` 存储 fee，但 V3 的 3 个 fee tier (500/3000/10000) 共享同一个 Router 地址 `0xE592427A...`，导致一个 router 只能对应一个 fee tier。

**影响**: 后端算出的利润 ≠ 链上实际利润 → eth_call 全部 revert "DoubleRouter: insufficient profit"。

**修复**: 在整个调用链中新增 `uint24[] feeTiers` 参数，按每步传入正确的 fee tier。详见 `CONTRACT_CHANGELOG.md`。

**验证**: 30/30 Solidity 测试通过，94/94 Go 测试通过。

### [已修复] `_handleCexLast` underflow panic

**原问题**: 当 `asset == tokenOut` 时，DEX swap 消耗 tokenOut，导致 `balanceAfter < balanceBefore`，触发 Solidity 0.8 的 arithmetic overflow panic (0x11)。

**修复**: 添加 `require(balanceAfter >= balanceBefore, "CEX last: tokenOut balance decreased")`。

### [已修复] ArbitrageCore OZ v5 编译错误

**原问题**: 引用 `SafeERC20Upgradeable`（OZ v4），项目用的是 OZ v5。

**修复**: 改为 `@openzeppelin/contracts/token/ERC20/utils/SafeERC20.sol`。

---

## 3. 未修复的问题

### CRITICAL

#### C-1: FlashLoanArbitrage 利润全部转入 platFormWallet

**文件**: `FlashLoanArbitrage.sol:151-163`

```solidity
uint256 grossProfit = amountOut - totalDebt;
// 以下两行被注释掉：
// uint256 platformFee = (grossProfit * configManager.profitShareFee()) / 1000;
// uint256 netProfit = grossProfit - platformFee;
IERC20(asset).transfer(platFormWallet, grossProfit); // 100% 利润转平台
```

**影响**: 闪电贷利润 100% 给平台，0% 给 Vault 用户。未部署，不影响生产。
**修复建议**: 部署前取消注释分润逻辑。

#### C-2: `_handleCexLast` 架构不可行

链上合约无法执行 CEX 交易。`isCex=false` 时不影响。
**修复建议**: 移除 cexLast 路径或重构为多步异步。

#### C-3: ArbitrageVault 首次存款捐赠攻击

**文件**: `ArbitrageVault.sol:155-159`

攻击者存 1 wei → 捐赠大量代币 → 后续存款者获得近 0 shares。
**修复建议**: 要求最低首次存款 (≥1e6)，或使用 OZ ERC4626 virtual offset。

### HIGH

| 编号 | 问题 | 文件 | 风险 |
|------|------|------|------|
| H-1 | ArbitrageCore 余额比较可被外部代币膨胀 | `ArbitrageCore.sol:191-211` | 理论风险 |
| H-2 | DoubleRouterIntegration 循环中重复 approve | `DoubleRouterIntegration.sol:169` | 残留 allowance |
| H-3 | DoubleRouterIntegration 中间步 swap 返回 0 不检测 | `DoubleRouterIntegration.sol:136-149` | **影响生产** |
| H-4 | UniswapV2V3Integration 零利润空间→swap 失败 | `UniswapV2V3Integration.sol:279-291` | 不使用此合约 |

### MEDIUM

| 编号 | 问题 | 文件 |
|------|------|------|
| M-1 | ArbitrageCore + ArbitrageVault 双重收费 | `ArbitrageCore.sol:215` + `ArbitrageVault.sol:362` |
| M-2 | ConfigManage profitShareFee 初始 0 | `ConfigManage.sol:48-78` |
| M-3 | FlashLoanRouter 仅配置 Aave V2 | `FlashLoanRouter.sol:31-36` |
| M-4 | DoubleRouterIntegration 固定 120s deadline | `DoubleRouterIntegration.sol:138` |
| M-5 | FlashLoanArbitrage 错误处理 abi.decode panic | `FlashLoanArbitrage.sol:185-189` |

### LOW

| 编号 | 问题 |
|------|------|
| L-1 | DoubleRouterIntegration slippageTolerance 初始化后不同步 |
| L-2 | `uint` vs `uint256` 混用 |
| L-3 | ArbitrageVault.deposit() 缺少 receiver 零地址检查 |
| L-4 | 多个管理函数缺少 event 发射 |
| L-5 | 整数除法截断导致微量 wei 损失 |

---

## 4. 问题优先级总结

| 编号 | 严重度 | 问题 | 影响当前生产 | 状态 |
|------|--------|------|:----------:|:----:|
| ~~CRITICAL-1~~ | ~~CRITICAL~~ | ~~V3 fee tier 无法区分~~ | ~~是~~ | **已修复 ✅** |
| C-1 | CRITICAL | FlashLoan 利润分配 | 否 (未部署) | 待修复 |
| C-2 | CRITICAL | cexLast 不可行 | 否 (isCex=false) | 待修复 |
| C-3 | CRITICAL | Vault 捐赠攻击 | **是** | **待修复** |
| H-1 | HIGH | 余额膨胀 | 理论风险 | 待修复 |
| H-2 | HIGH | approve 残留 | 微弱 | 待修复 |
| H-3 | HIGH | swap 中间步无验证 | **是** | **待修复** |
| H-4 | HIGH | slippage 计算 bug | 否 | 待修复 |
| M-1~5 | MEDIUM | 各项中等问题 | 否 | 待修复 |
| L-1~5 | LOW | 各项小问题 | 否 | 视情况 |

---

## 5. 测试覆盖

### Solidity 合约测试: 30/30 PASS

```
SpotArbitrage Comprehensive Tests
  初始化测试 (5) ✔  |  权限变更 (7) ✔  |  纯DEX套利 (4, 含混合V3 feeTier) ✔
  CEX-DEX (4) ✔  |  边界条件 (3) ✔  |  权限控制 (4) ✔
  重入防护 (1) ✔  |  UUPS升级 (2) ✔
```

### Go 单元测试: 94/94 PASS (-race)

```
executor:  23 tests (ABI selector, encoding, nonce, gas loss, profit parsing)
strategy:  45 tests (V3 math, spread scanner, fast detector, gas estimator)
cache:     13 tests (PriceCache pub-sub, concurrent, V3)
cexdex:    13 tests (detector, spread, trade amount, cleanup)
```

---

## 6. 关键建议

### 立即修复 (影响当前生产)
1. **C-3**: ArbitrageVault 添加最低首次存款防护
2. **H-3**: DoubleRouterIntegration 每步检查 `currentAmount > 0`

### 合约升级时修复
3. **H-2**: approve 清零后再 approve
4. **M-4**: deadline 可配置

### FlashLoanArbitrage 部署前
5. **C-1**: 取消注释分润逻辑
6. **M-5**: 错误处理检查 reason.length

### 架构改进
7. **C-2**: 移除 cexLast 路径
8. **M-1**: 统一费用结构
