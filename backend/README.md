# DeFi 套利机器人后端

<div align="center">

**🤖 自动化 DeFi 套利系统 - 后端服务**

[![Go Version](https://img.shields.io/badge/Go-1.21+-00ADD8?style=flat&logo=go)](https://golang.org)
[![PostgreSQL](https://img.shields.io/badge/PostgreSQL-15+-316192?style=flat&logo=postgresql)](https://www.postgresql.org)
[![Redis](https://img.shields.io/badge/Redis-7+-DC382D?style=flat&logo=redis)](https://redis.io)
[![Docker](https://img.shields.io/badge/Docker-Ready-2496ED?style=flat&logo=docker)](https://www.docker.com)
[![License](https://img.shields.io/badge/License-MIT-green.svg)](LICENSE)



---

##  项目概述

专业的 DeFi 套利机器人后端服务，支持多个 DEX 的实时数据采集、价格监控和套利分析。

### 核心功能

-  **多 DEX 支持**：Uniswap V2/V3（3个 fee tier）、SushiSwap
-  **智能协议**：协议适配器架构，轻松扩展新 DEX
-  **容器化**：完整的 Docker 配置，一键部署

### 当前进度：

```
阶段1: 数据采集和存储  [████████████████████████████] 100% ✅
阶段2: 多DEX支持      [████████████████████████████] 100% ✅
阶段3: 套利分析       [░░░░░░░░░░░░░░░░░░░░░░░░░░░░]   0% ⚪
阶段4: API开发        [░░░░░░░░░░░░░░░░░░░░░░░░░░░░]   0% ⚪
```

---

##  快速开始

###  WSL2 + Docker Desktop（推荐）

```bash
# 1️⃣ 启动基础服务（PostgreSQL + Redis）
docker-compose -f docker-compose.dev.yml up -d

# 2️⃣ 初始化数据库
make migrate-seed

# 3️⃣ 运行后端
make run
```

**访问管理工具：**
- pgAdmin: http://localhost:5050
- Redis Commander: http://localhost:8081

###  纯 Docker 部署（生产）

```bash
# 1️⃣ 准备配置
cp env.example .env
nano .env  # 修改 RPC_URL 等

# 2️⃣ 启动所有服务
docker-compose up -d

# 3️⃣ 查看日志
docker-compose logs -f backend
```

**详细文档：** 📖 [SETUP_GUIDE.md](SETUP_GUIDE.md)

---

## 功能特性

###  已完成

#### 第一阶段：数据采集和存储
- ✅ 数据库设计（7张表，完整关系模型）
- ✅ GORM ORM 集成
- ✅ Web3 客户端封装
- ✅ Uniswap V2 数据采集
- ✅ 定时任务调度器

#### 第二阶段：多 DEX 支持
- ✅ **Uniswap V3 完整支持**（0.05%, 0.3%, 1% fee tiers）
- ✅ **协议适配器架构**（统一 V2/V3 接口）
- ✅ **协议工厂模式**（轻松扩展新协议）
- ✅ SushiSwap 支持
- ✅ 配置驱动的 DEX 管理

### 开发中

#### 第三阶段：套利分析
- ⚪ 三角套利路径算法
- ⚪ 利润计算引擎
- ⚪ Gas 费用估算
- ⚪ 机会过滤和排序

###  计划中

#### 第四阶段：API 开发
- ⚪ RESTful API
- ⚪ WebSocket 实时推送
- ⚪ Swagger 文档

---

##  架构设计

```
┌──────────────────────────────────────────────────────────────┐
│                        区块链网络                              │
│               (Ethereum Mainnet / Sepolia)                   │
└────────────────────┬─────────────────────────────────────────┘
                     │ HTTPS JSON-RPC
                     ▼
┌──────────────────────────────────────────────────────────────┐
│                    多 RPC 节点池                              │
│          (负载均衡 + 故障转移 + 健康检查)                      │
└────────────────────┬─────────────────────────────────────────┘
                     │
         ┌───────────┴───────────┐
         ▼                       ▼
┌──────────────────┐    ┌──────────────────┐
│  协议适配器层     │    │   Web3 客户端     │
│                  │    │                  │
│ • V2 Protocol    │    │ • Factory 调用   │
│ • V3 Protocol    │    │ • Pair 调用      │
│ • Protocol       │    │ • Pool 调用      │
│   Factory        │    │                  │
└────────┬─────────┘    └────────┬─────────┘
         │                       │
         └───────────┬───────────┘
                     ▼
         ┌────────────────────────┐
         │     数据采集器          │
         │    (Collector)         │
         │                        │
         │ • 并发采集 (20 协程)   │
         │ • 协议适配            │
         │ • 重试机制            │
         │ • 缓存集成            │
         └───────────┬────────────┘
                     │
         ┌───────────┴───────────┐
         ▼                       ▼
┌──────────────────┐    ┌──────────────────┐
│   Redis 缓存      │    │  定时任务调度器   │
│                  │    │   (Scheduler)    │
│ • 5分钟 TTL      │    │                  │
│ • 价格数据缓存    │    │ • 采集 (5分钟)   │
│ • 批量操作        │    │ • 分析 (10分钟)  │
│                  │    │ • 清理 (24小时)  │
└──────────────────┘    └────────┬─────────┘
                                 ▼
┌──────────────────────────────────────────────────────────────┐
│                    PostgreSQL 数据库                          │
│                                                              │
│  ┌──────────┐  ┌──────────┐  ┌──────────────┐             │
│  │ Tokens   │  │  Dexes   │  │TradingPairs  │             │
│  │(代币表)   │  │(DEX表)   │  │(交易对表)     │             │
│  └────┬─────┘  └────┬─────┘  └──────┬───────┘             │
│       │             │               │                      │
│       └─────────────┴───────────────┘                      │
│                     │                                       │
│       ┌─────────────┴─────────────┐                        │
│       ▼                           ▼                        │
│  ┌──────────────┐         ┌──────────────┐                │
│  │PairReserves  │         │PriceRecords  │                │
│  │(储备量时序)   │         │(价格时序)     │                │
│  └──────────────┘         └──────────────┘                │
│                                                            │
│  ┌──────────────────┐    ┌──────────────────┐            │
│  │Opportunities     │    │Executions        │            │
│  │(套利机会)         │    │(执行记录)         │            │
│  └──────────────────┘    └──────────────────┘            │
└──────────────────────────────────────────────────────────────┘
```

---

## 项目结构

```
backend/
├── cmd/
│   └── server/
│       └── main.go             # 主程序入口
│
├── internal/                   # 内部包
│   ├── collector/              # 数据采集器
│   │   ├── collector.go
│   │   └── collector_concurrent.go  # 并发采集优化
│   ├── config/                 # 配置管理
│   ├── database/               # 数据库连接
│   ├── models/                 # 数据模型（7个表）
│   └── scheduler/              # 定时任务
│
├── pkg/                        # 公共包
│   ├── cache/                  # Redis 缓存
│   │   └── redis.go
│   ├── dex/                    # DEX 协议适配器
│   │   ├── protocol.go         # 协议接口
│   │   ├── factory.go          # 协议工厂
│   │   ├── uniswap_v2.go       # V2 适配器
│   │   └── uniswap_v3.go       # V3 适配器
│   └── web3/                   # Web3 客户端
│       ├── client.go           # 基础客户端
│       ├── client_pool.go      # RPC 节点池
│       ├── uniswap_v2_pair.go  # V2 合约
│       └── uniswap_v3_pool.go  # V3 合约
│
├── configs/                    # 配置文件
│   ├── config.yaml             # 生产配置（主网）
│   └── config.test.yaml        # 测试配置（Sepolia）
│
├── docker-compose.yml          # Docker 生产部署
├── docker-compose.dev.yml      # Docker 开发环境
├── Dockerfile                  # Docker 镜像
├── Makefile                    # 自动化命令
├── env.example                 # 环境变量模板
├── .gitignore                  # Git 忽略规则
│
├── README.md                   # 本文件
└── SETUP_GUIDE.md             # 完整设置指南
```

---

## 技术栈

| 组件 | 技术 | 版本 | 说明 |
|------|------|------|------|
| **语言** | Go | 1.21+ | 高性能、并发友好 |
| **数据库** | PostgreSQL | 15+ | 可靠的关系型数据库 |
| **缓存** | Redis | 7+ | 高性能缓存 |
| **ORM** | GORM | 1.25+ | 强大的 Go ORM |
| **区块链** | go-ethereum | 1.13+ | 官方以太坊客户端 |
| **定时任务** | robfig/cron | 3.0+ | 类 Unix cron 表达式 |
| **配置** | viper | 1.18+ | 配置管理 |
| **容器化** | Docker | 20+ | 容器化部署 |

---

##  核心特性

### 多 DEX 支持

| DEX | 协议版本 | Fee Tiers | 状态 |
|-----|---------|-----------|------|
| Uniswap V2 | V2 | 0.3% | ✅ |
| Uniswap V3 | V3 | 0.05%, 0.3%, 1% | ✅ |
| SushiSwap | V2 | 0.3% | ✅ |
| PancakeSwap | V2/V3 | - | 🔜 |
| Curve | Stable | - | 🔜 |

###  性能优化

| 优化项 | 优化前 | 优化后 | 提升 |
|--------|--------|--------|------|
| 采集速度 | 2 对/秒 | 33 对/秒 | **16x** |
| 数据库写入 | 200 条/秒 | 2000 条/秒 | **10x** |
| RPC 可靠性 | 单节点 | 多节点池 | **3x** |
| 数据库负载 | 100% | 50% | **-50%** |

###  架构亮点

- **协议适配器模式**：统一接口，轻松扩展新 DEX
- **RPC 客户端池**：负载均衡 + 自动故障转移
- **Redis 缓存**：智能缓存策略，降低链上查询
- **并发采集**：20 个协程并发处理
- **批量写入**：事务保证数据一致性

---

##  数据库设计

### 核心表结构

| 表名 | 说明 | 记录数预估 |
|------|------|-----------|
| `tokens` | 代币信息 | 100+ |
| `dexes` | DEX 信息（支持 V2/V3） | 10+ |
| `trading_pairs` | 交易对 | 1,000+ |
| `pair_reserves` | 流动性储备（时序） | 百万级 |
| `price_records` | 价格记录（时序） | 百万级 |
| `arbitrage_opportunities` | 套利机会 | 10,000+ |
| `arbitrage_executions` | 执行记录 | 1,000+ |

**关键字段（V3 支持）：**
- `dexes.protocol` - 协议类型（uniswap_v2, uniswap_v3）
- `dexes.fee_tier` - V3 费率层级（500, 3000, 10000）
- `dexes.version` - 版本标识（v2, v3）

---

##  Makefile 命令

### 基础命令

```bash
make help           # 显示所有命令
make build          # 编译项目
make run            # 运行服务
make test           # 运行测试
make clean          # 清理编译产物
```

### Docker 命令

```bash
make docker-dev     # 启动开发环境（仅基础服务）
make docker-up      # 启动生产环境（所有服务）
make docker-down    # 停止服务
make docker-logs    # 查看日志
make docker-restart # 重启服务
```

### 数据库命令

```bash
make migrate        # 执行数据库迁移
make seed           # 初始化种子数据
make migrate-seed   # 迁移 + 种子数据
make db-connect     # 连接数据库
make db-backup      # 备份数据库
```

### Redis 命令

```bash
make redis-cli      # 连接 Redis
make redis-flush    # 清空缓存
```

---

##  配置说明

### 环境变量

```bash
# 复制环境变量模板
cp env.example .env

# 必须配置的变量
RPC_URL=https://sepolia.infura.io/v3/YOUR_KEY  # RPC 节点地址
DB_PASSWORD=your_secure_password                # 数据库密码
REDIS_ENABLED=true                              # 启用 Redis
```

### 配置文件

- `config.test.yaml` - 测试网配置（Sepolia + localhost）
- `config.yaml` - 生产配置（主网 + 环境变量）

---

