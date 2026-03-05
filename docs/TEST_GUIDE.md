# ArbitrageX 测试指南

> 更新日期: 2026-03-06
> 测试总数: 94 Go + 30 Solidity = 124 tests

---

## 快速开始

```bash
# 1. Go 单元测试 (无需网络，~10s)
cd backend
go test -race -v ./internal/strategy/... ./internal/executor/... ./pkg/cache/... ./internal/cexdex/...

# 2. Solidity 合约测试 (无需网络，~3s)
cd contract
npm test

# 3. 一键运行全部测试
bash docs/scripts/run_all_tests.sh
```

---

## 环境要求

| 工具 | 最低版本 | 安装 |
|------|---------|------|
| Go | 1.21+ | `brew install go` |
| Node.js | 18+ | `brew install node` |
| npm | 9+ | 随 Node.js 安装 |

### 首次安装依赖

```bash
# Go 依赖
cd backend && go mod tidy

# Solidity 依赖
cd contract && npm install
```

---

## Phase 1: 单元测试 (94 Go + 30 Solidity)

### Go 测试分布

| 包 | 测试数 | 说明 | 外部依赖 |
|----|--------|------|---------|
| `internal/strategy/` | 45 | V3 数学、价差扫描、快速检测、Gas 估算 | 无 |
| `internal/executor/` | 23 | ABI 编码、Nonce 追踪、Gas 限额、利润解析 | 无 |
| `pkg/cache/` | 13 | PriceCache 存取、pub-sub、并发安全 | 无 |
| `internal/cexdex/` | 13 | CEX-DEX 检测、价差计算、过期清理 | 无 |

```bash
# 运行全部 Go 测试
cd backend
go test -race -v ./internal/strategy/... ./internal/executor/... ./pkg/cache/... ./internal/cexdex/...

# 运行单个包
go test -race -v ./internal/executor/...

# 运行单个测试
go test -race -v -run TestBuildCallData_ExecuteStrategy_Selector ./internal/executor/...
```

### Solidity 测试分布

| 测试组 | 测试数 | 说明 |
|--------|--------|------|
| 初始化测试 | 5 | 合约部署和参数验证 |
| 权限变更 | 7 | setter 权限和零地址检查 |
| 纯 DEX 套利 | 4 | V2/V3 fee tier 混合测试 |
| CEX-DEX 套利 | 4 | 路径验证和边界检查 |
| 边界条件 | 3 | 代币未收到、不存在代币 |
| 权限控制 | 4 | 非 owner 调用回滚 |
| 重入防护 | 1 | 连续调用安全 |
| UUPS 升级 | 2 | owner 升级 + 非 owner 回滚 |

```bash
# 运行合约测试
cd contract
npx hardhat test test/SpotArbitrage.test.js

# 含 Gas 报告
REPORT_GAS=true npx hardhat test
```

---

## Phase 2: 集成测试

### 编译检查

```bash
# Go 编译检查（不产生二进制）
cd backend && go build ./...

# Solidity 编译
cd contract && npx hardhat compile
```

### 集成测试程序

以下程序需要 Arbitrum RPC 连接:

```bash
cd backend

# 执行器测试 (calldata 生成 + ABI 编码)
go run cmd/test-executor/main.go

# 策略模块测试 (链上价格读取)
go run cmd/test-strategy/main.go

# E2E 测试 (跳过链上深度和交易对发现)
go run cmd/e2e/main.go -config configs/config.yaml -skip-pairs -skip-depth
```

### Dry-Run 模式

```yaml
# backend/configs/config.yaml 设置:
scheduler:
  enable_execution: false
  dry_run: true
```

```bash
cd backend && go run cmd/server/main.go -config configs/config.yaml
```

**监控指标**:
- PriceCache 池子数 >100
- PriceChangeEvents/min >10
- 内存/goroutine 数 30min 后稳定

---

## Phase 3: 主网执行 (需充值)

### 前置条件

- [ ] Phase 1 + 2 全部通过
- [ ] Keeper ETH 余额 ≥ 0.01 ETH
- [ ] Vault WETH 余额 ≥ 0.01 WETH
- [ ] `KEEPER_PRIVATE_KEY` 环境变量已设置
- [ ] 合约已升级到最新版本（含 feeTiers）

### 保守执行配置

```yaml
scheduler:
  enable_execution: true
  dry_run: false
  min_confidence: 0.8
  max_concurrent_exec: 1
  enable_flash_loan: false
```

### 监控

每 15 分钟检查:
- Keeper ETH 余额 (不低于 0.003 ETH)
- Vault WETH 余额 (via getVaultInfo)
- 日志中 nonce 错误
- Arbiscan 验证 tx hash

---

## 测试脚本

| 脚本 | 用途 | 依赖 |
|------|------|------|
| `docs/scripts/run_all_tests.sh` | 一键运行全部测试 | Go, Node.js |
| `docs/scripts/run_unit_tests.sh` | 仅 Go 单元测试 | Go |
| `docs/scripts/run_contract_tests.sh` | 仅 Solidity 测试 | Node.js |
| `docs/scripts/run_integration_tests.sh` | 集成测试 | Go, 网络 |
| `docs/scripts/check_balances.sh` | 检查链上余额 | Go, 网络 |
| `docs/scripts/run_dryrun.sh` | Dry-Run 模式测试 | Go, 网络 |
| `docs/scripts/monitor_phase3.sh` | 主网运行监控 | 无 |

---

## 故障排除

### Go 测试失败

```bash
# 清除缓存重跑
go clean -testcache
go test -race -v ./...
```

### Solidity 编译失败

```bash
# 清除 Hardhat 缓存
cd contract
rm -rf artifacts/ cache/
npx hardhat compile
```

### ld warning (macOS)

```
ld: warning: malformed LC_DYSYMTAB
```

这是 macOS + Go race detector 的已知 warning，不影响测试结果。

### 集成测试 RPC 超时

```
context deadline exceeded
```

检查网络连接和 RPC 配置。可在 `config.yaml` 中更换 RPC 节点:
```yaml
web3:
  rpc_urls:
    - "https://arbitrum-one.publicnode.com"
    - "https://arb1.arbitrum.io/rpc"
```
