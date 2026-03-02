# 金库管理模块测试

<div align="center">

**📦 模块路径**: `internal/vault/`

</div>

---

## 📋 模块概述

金库管理模块负责与 ArbitrageVault (ERC4626) 合约交互，管理用户资金。

| 文件 | 组件 | 功能 |
|------|------|------|
| `vault_manager.go` | VaultManager | Vault 交互管理 |

---

## 🧪 测试用例

### 1. VaultManager 初始化测试

#### 1.1 基础初始化

**目的**: 验证 VaultManager 正确初始化

**测试步骤**:
```go
vaultManager, err := vault.NewVaultManager(web3Client, vaultAddress)
```

**验证点**:
- [ ] 返回非空 VaultManager
- [ ] ABI 解析成功
- [ ] 合约地址正确

**预期输出**:
```
✅ VaultManager 初始化成功
   Vault 地址: 0xB7e37Fb429795E10A8D25752fFC69Fbab71df677
```

---

### 2. 读取操作测试

#### 2.1 GetVaultStats 测试

**目的**: 验证能获取 Vault 状态

**测试步骤**:
```go
stats, err := vaultManager.GetVaultStats(ctx)
```

**验证点**:
- [ ] 返回 TotalAssets（总资产）
- [ ] 返回 TotalSupply（总份额）
- [ ] 数值合理

**预期输出**:
```
Vault 状态:
  总资产: 100.5 ETH
  总份额: 100.0 shares
  份额价格: 1.005 ETH
```

#### 2.2 GetUserInfo 测试

**目的**: 验证能获取用户信息

**测试步骤**:
```go
userInfo, err := vaultManager.GetUserInfo(ctx, userAddress)
```

**验证点**:
- [ ] 返回用户份额 (shares)
- [ ] 返回用户资产价值 (assets)
- [ ] 计算正确

**测试用例**:

| 用户 | 份额 | 预期资产 |
|------|------|----------|
| 用户 A | 10 shares | ~10.05 ETH |
| 用户 B | 0 shares | 0 ETH |
| 新用户 | 0 shares | 0 ETH |

#### 2.3 PreviewDeposit 测试

**目的**: 验证存入预览功能

**测试步骤**:
```go
shares, err := vaultManager.PreviewDeposit(ctx, depositAmount)
```

**验证点**:
- [ ] 返回预计获得的份额
- [ ] 符合 ERC4626 计算公式: shares = assets * totalSupply / totalAssets

**测试用例**:

| 存入金额 | Vault 状态 | 预期份额 |
|----------|------------|----------|
| 1 ETH | 100 ETH / 100 shares | ~0.995 shares |
| 10 ETH | 100 ETH / 100 shares | ~9.95 shares |
| 1 ETH | 0 ETH / 0 shares | 1 shares (首次存入) |

#### 2.4 PreviewRedeem 测试

**目的**: 验证赎回预览功能

**测试步骤**:
```go
assets, err := vaultManager.PreviewRedeem(ctx, redeemShares)
```

**验证点**:
- [ ] 返回预计获得的资产
- [ ] 符合 ERC4626 计算公式: assets = shares * totalAssets / totalSupply

**测试用例**:

| 赎回份额 | Vault 状态 | 预期资产 |
|----------|------------|----------|
| 10 shares | 100 ETH / 100 shares | ~10.05 ETH |
| 1 shares | 100 ETH / 100 shares | ~1.005 ETH |

---

### 3. 写入操作测试

#### 3.1 Deposit 测试

**目的**: 验证存入功能

**前置条件**:
- 用户有足够的 ETH/资产
- 用户已授权 Vault 合约
- 配置了 Keeper 私钥

**测试步骤**:
```go
txHash, shares, err := vaultManager.Deposit(ctx, depositAmount, receiver)
```

**验证点**:
- [ ] 交易发送成功
- [ ] 返回正确的份额数量
- [ ] 用户余额减少
- [ ] Vault 总资产增加

**测试流程**:
```
1. 查询存入前状态
   - 用户 ETH 余额
   - 用户份额
   - Vault 总资产

2. 执行存入
   - 金额: 0.1 ETH（测试网）

3. 查询存入后状态
   - 用户 ETH 余额应减少 0.1 + Gas
   - 用户份额应增加
   - Vault 总资产应增加 0.1

4. 验证事件
   - Deposit(sender, owner, assets, shares) 事件
```

#### 3.2 Redeem 测试

**目的**: 验证赎回功能

**前置条件**:
- 用户有份额
- 配置了 Keeper 私钥

**测试步骤**:
```go
txHash, assets, err := vaultManager.Redeem(ctx, redeemShares, receiver, owner)
```

**验证点**:
- [ ] 交易发送成功
- [ ] 返回正确的资产数量
- [ ] 用户份额减少
- [ ] 用户余额增加

**测试流程**:
```
1. 查询赎回前状态
   - 用户份额
   - 用户 ETH 余额
   - Vault 总资产

2. 执行赎回
   - 份额: 用户所有份额的 50%

3. 查询赎回后状态
   - 用户份额应减少 50%
   - 用户 ETH 余额应增加
   - Vault 总资产应减少

4. 验证事件
   - Withdraw(sender, receiver, owner, assets, shares) 事件
```

---

### 4. 边界条件测试

#### 4.1 零金额测试

**测试用例**:

| 操作 | 金额 | 预期结果 |
|------|------|----------|
| Deposit | 0 | 错误或 0 份额 |
| Redeem | 0 | 错误或 0 资产 |

#### 4.2 超额测试

**测试用例**:

| 操作 | 条件 | 预期结果 |
|------|------|----------|
| Deposit | 超过余额 | 交易失败 |
| Redeem | 超过持有份额 | 交易失败 |

#### 4.3 首次存入测试

**目的**: 验证 Vault 为空时的首次存入

**测试步骤**:
```go
// Vault 为空时
shares, _ := vaultManager.PreviewDeposit(ctx, big.NewInt(1e18))
// shares 应该 = depositAmount（1:1 比例）
```

---

## 🔧 测试命令汇总

```bash
# Vault 模块测试
go run cmd/test-modules/main.go -module vault

# 完整测试（含写入操作，需要私钥）
go run cmd/test-modules/main.go -module vault -execute

# 只读测试
go run cmd/test-modules/main.go -module vault -readonly
```

---

## 📊 测试检查清单

| 测试项 | 状态 | 备注 |
|--------|:----:|------|
| VaultManager 初始化 | ⬜ | |
| GetVaultStats | ⬜ | |
| GetUserInfo | ⬜ | |
| PreviewDeposit | ⬜ | |
| PreviewRedeem | ⬜ | |
| Deposit | ⬜ | 需要私钥 |
| Redeem | ⬜ | 需要私钥 |
| 边界条件 | ⬜ | |

---

## 📐 ERC4626 公式参考

### 存入计算
```
shares = assets * totalSupply / totalAssets
```

### 赎回计算
```
assets = shares * totalAssets / totalSupply
```

### 份额价格
```
pricePerShare = totalAssets / totalSupply
```

---

## ⚠️ 注意事项

1. 写入测试需要配置 Keeper 私钥
2. 测试网测试需要测试 ETH
3. 首次存入时 totalSupply = 0，需要特殊处理
4. 注意 Gas 费用影响余额计算
