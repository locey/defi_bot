# DeFi 套利机器人 - 完整测试指南

## 概述

本文档提供 DeFi 套利机器人后端的完整测试方案。测试覆盖从基础模块到端到端的所有功能。

---

## 环境要求

| 依赖 | 版本 | 说明 |
|------|------|------|
| Go | 1.21+ | 编程语言 |
| Docker | 20+ | 容器运行时 (OrbStack / Docker Desktop) |
| PostgreSQL | 15+ | 主数据库 (Docker 容器) |
| Redis | 7+ | 缓存层 (Docker 容器) |

### 环境准备

```bash
# 1. 启动 Docker 开发环境
cd backend && docker-compose -f docker-compose.dev.yml up -d

# 2. 等待服务就绪
sleep 10

# 3. 初始化数据库 (迁移 + 种子数据)
go run cmd/server/main.go -config configs/config.yaml -migrate -seed

# 4. 编译检查
go build ./...
```

---

## 测试文档索引

| 编号 | 文档 | 模块 | 测试命令 | 核心功能 |
|:----:|------|------|----------|----------|
| 01 | [executor](./01-executor.md) | 执行器 | `cmd/test-executor` | ABI 解析、CallData 构建、合约调用 |
| 02 | [strategy](./02-strategy.md) | 策略引擎 | `cmd/test-strategy` | 路径查找、利润计算、机会验证 |
| 03 | [collector](./03-collector.md) | 数据采集 | `cmd/test-collector` | 交易对发现、价格采集、Gas 采集 |
| 04 | [cexdex](./04-cexdex.md) | CEX-DEX | `cmd/test-cexdex` | 价差检测、跨市套利 |
| 05 | [web3](./05-web3.md) | Web3 客户端 | `cmd/test-modules` | RPC 连接、合约交互、区块查询 |
| 06 | [e2e](./06-e2e.md) | 端到端 | `cmd/e2e` | 采集→分析→执行完整流程 |

---

## 快速运行全部测试

```bash
# 编译检查 (静态分析)
go build ./...

# 模块基础测试 (配置 + RPC + 数据库)
go run cmd/test-modules/main.go -module all

# 执行器测试
go run cmd/test-executor/main.go

# 策略引擎测试
go run cmd/test-strategy/main.go

# 数据采集测试
go run cmd/test-collector/main.go

# E2E 测试
go run cmd/e2e/main.go -config configs/config.yaml -skip-pairs -skip-depth
```

---

## 网络说明

- **Arbitrum One (ChainID: 42161)**: 主网测试，使用公共 RPC
- **公共 RPC 限制**: `arb1.arbitrum.io/rpc` 有速率限制 (429 Too Many Requests)
- **建议**: 生产环境使用 Alchemy / Infura / QuickNode 等付费 RPC

---

## 测试结果

测试结果记录在 [TEST_RESULTS.md](./TEST_RESULTS.md)
