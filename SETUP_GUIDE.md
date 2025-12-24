# 🚀 DeFi 套利机器人 - 完整设置指南

> **一站式配置、启动、使用指南**

---

## 📚 目录

1. [快速开始](#快速开始)
2. [配置文件说明](#配置文件说明)
3. [Docker 环境配置](#docker-环境配置)
4. [WSL2 开发环境](#wsl2-开发环境)
5. [常用命令](#常用命令)
6. [故障排查](#故障排查)

---

## 🎯 快速开始

### 你的环境是什么？

| 环境 | 适合 | 启动命令 |
|------|------|---------|
| **WSL2 + Docker** | 开发/测试 | `make docker-dev` → `make migrate-seed` → `make run` |
| **纯 Docker** | 生产部署 | `cp env.example .env` → `docker-compose up -d` |

---

## ⚡ WSL2 开发模式（推荐新手）

### 第 1 步：启动基础服务

```bash
cd /mnt/d/Coding/Web3Hackathon/defi_bot/backend

# 启动 PostgreSQL + Redis + 管理工具
docker-compose -f docker-compose.dev.yml up -d
```

**启动的服务：**
- ✅ PostgreSQL → `localhost:5432`
- ✅ Redis → `localhost:6379`
- ✅ pgAdmin → `http://localhost:5050`
- ✅ Redis Commander → `http://localhost:8081`

**验证：**
```bash
docker ps
# 应该看到 4 个容器在运行
```

### 第 2 步：初始化数据库

```bash
# 使用 Makefile（推荐）
make migrate-seed

# 或手动执行
go run cmd/server/main.go -config configs/config.test.yaml -migrate -seed
```

**执行的操作：**
- 创建数据库表
- 初始化代币 (WETH, DAI, USDC)
- 初始化 DEX (Uniswap V2, V3×3)

### 第 3 步：运行后端

```bash
# 使用 Makefile（推荐）
make run

# 或手动执行
go run cmd/server/main.go -config configs/config.test.yaml
```

**成功标志：**
```
========================================
DeFi 套利机器人后端服务
========================================
配置加载成功: configs/config.test.yaml
数据库连接成功
✅ Redis 连接成功
服务已启动，按 Ctrl+C 退出
========================================
```

---

## 🐳 Docker 生产模式

### 第 1 步：准备环境变量

```bash
# 复制环境变量模板
cp env.example .env

# 编辑配置
nano .env
```

**必须修改的配置：**
```bash
# .env
RPC_URL=https://sepolia.infura.io/v3/YOUR_INFURA_KEY  # ← 填写你的密钥
DB_PASSWORD=your_secure_password                       # ← 修改默认密码
```

### 第 2 步：启动所有服务

```bash
# 构建并启动
docker-compose up -d

# 查看日志
docker-compose logs -f backend

# 查看状态
docker-compose ps
```

**启动的服务：**
- ✅ PostgreSQL (容器内网络)
- ✅ Redis (容器内网络)
- ✅ Backend (自动运行)
- ✅ pgAdmin (可选，`docker-compose --profile admin up -d`)

---

## 📂 配置文件说明

### 配置文件总览

```
backend/
├── configs/
│   ├── config.yaml         # 生产环境（主网）
│   └── config.test.yaml    # 开发环境（测试网）← 你最常用
│
├── docker-compose.yml      # Docker 生产（所有服务）
├── docker-compose.dev.yml  # Docker 开发（仅基础服务）← 推荐
│
├── .env                    # 环境变量（需创建）
└── env.example             # 环境变量模板
```

### 什么时候用哪个？

| 配置文件 | 读取时机 | 作用 |
|---------|---------|------|
| **config.test.yaml** | `go run ... -config config.test.yaml` | WSL2 后端配置 |
| **docker-compose.dev.yml** | `docker-compose -f docker-compose.dev.yml up` | 启动基础服务 |
| **docker-compose.yml** | `docker-compose up` | 启动所有服务 |
| **.env** | `docker-compose` 自动读取 | 环境变量 |

### 配置优先级

```
环境变量 (export DB_HOST=xxx)  ← 最高优先级
    ↓
.env 文件 (DB_HOST=postgres)
    ↓
config.yaml (database.host: ${DB_HOST})
    ↓
代码默认值                      ← 最低优先级
```

---

## 🔧 核心配置对比

### WSL2 vs Docker

| 配置项 | WSL2 开发 | Docker 生产 |
|--------|----------|------------|
| **数据库地址** | `localhost:5432` | `postgres:5432` |
| **Redis 地址** | `localhost:6379` | `redis:6379` |
| **配置文件** | `config.test.yaml` | `config.yaml` + `.env` |
| **后端运行** | WSL2 (go run) | Docker 容器 |
| **网络** | 端口映射 | Docker 内部网络 |

### 为什么地址不同？

```
┌─────────────────────────────────────────┐
│ WSL2 模式                                │
│                                         │
│  后端 (WSL2)                             │
│     ↓ 通过 localhost                    │
│  Docker 端口映射 (5432 → 5432)          │
│     ↓                                   │
│  PostgreSQL 容器                         │
└─────────────────────────────────────────┘

┌─────────────────────────────────────────┐
│ Docker 模式                              │
│                                         │
│  后端容器                                │
│     ↓ 通过服务名 "postgres"             │
│  Docker 内部网络                         │
│     ↓                                   │
│  PostgreSQL 容器                         │
└─────────────────────────────────────────┘
```

---

## 📋 配置文件详解

### 1. `config.test.yaml` (最常用)

**用途：** WSL2 本地开发

**关键配置：**
```yaml
database:
  host: localhost      # ← 连接 Docker 容器
  user: defi_user
  password: defi_pass123
  
redis:
  enabled: true
  host: localhost      # ← 连接 Docker 容器
  
blockchain:
  rpc_url: https://sepolia.infura.io/v3/YOUR_KEY  # ← 测试网
  chain_id: 11155111   # ← Sepolia
```

**修改后生效：**
- 重启后端 (Ctrl+C → `make run`)

---

### 2. `docker-compose.dev.yml`

**用途：** 开发环境基础服务

**包含服务：**
```yaml
services:
  postgres:        # PostgreSQL 数据库
  redis:           # Redis 缓存
  pgadmin:         # 数据库管理工具
  redis-commander: # Redis 管理工具
```

**不包含：**
- 后端服务（需在 WSL2 手动运行）

**启动：**
```bash
docker-compose -f docker-compose.dev.yml up -d
```

---

### 3. `.env` (Docker 专用)

**用途：** 为 Docker Compose 提供环境变量

**重要：** WSL2 本地运行不会读取此文件！

**示例：**
```bash
DB_USER=defi_user
DB_PASSWORD=defi_pass123
RPC_URL=https://sepolia.infura.io/v3/YOUR_KEY
REDIS_ENABLED=true
```

---

## 🛠️ 常用命令

### Makefile 命令（推荐）

```bash
make help           # 查看所有命令
make build          # 编译项目
make run            # 运行服务
make test           # 运行测试

make docker-dev     # 启动开发环境
make docker-up      # 启动生产环境
make docker-down    # 停止服务
make docker-logs    # 查看日志

make migrate        # 数据库迁移
make seed           # 初始化种子数据
make migrate-seed   # 迁移 + 种子数据

make db-connect     # 连接数据库
make redis-cli      # 连接 Redis
make redis-flush    # 清空缓存
```

### Docker Compose 命令

```bash
# 开发环境
docker-compose -f docker-compose.dev.yml up -d     # 启动
docker-compose -f docker-compose.dev.yml down      # 停止
docker-compose -f docker-compose.dev.yml ps        # 查看状态
docker-compose -f docker-compose.dev.yml logs -f   # 查看日志

# 生产环境
docker-compose up -d                # 启动
docker-compose down                 # 停止
docker-compose restart backend      # 重启后端
docker-compose logs -f backend      # 查看后端日志
```

### 数据库命令

```bash
# 连接数据库
docker-compose exec postgres psql -U defi_user -d defi_arbitrage

# 常用 SQL
\dt                          # 查看所有表
\d trading_pairs             # 查看表结构
SELECT * FROM dexes;         # 查看 DEX 数据
SELECT * FROM tokens;        # 查看代币数据
SELECT * FROM trading_pairs; # 查看交易对

# 备份数据库
docker-compose exec postgres pg_dump -U defi_user defi_arbitrage > backup.sql

# 恢复数据库
docker-compose exec -T postgres psql -U defi_user -d defi_arbitrage < backup.sql
```

### Redis 命令

```bash
# 连接 Redis
docker-compose exec redis redis-cli

# 常用命令
PING                    # 测试连接
KEYS *                  # 查看所有键
GET price:0x123...      # 获取缓存
FLUSHALL                # 清空所有缓存
INFO                    # 查看统计信息
```

---

## 📊 管理工具

### pgAdmin (数据库管理)

**访问：** http://localhost:5050

**登录信息：**
- Email: `admin@defibot.com`
- Password: `admin123`

**添加服务器：**
1. 右键 Servers → Register → Server
2. General Tab:
   - Name: `DeFi Bot DB`
3. Connection Tab:
   - Host: `localhost` (WSL2) 或 `postgres` (Docker)
   - Port: `5432`
   - Database: `defi_arbitrage`
   - Username: `defi_user`
   - Password: `defi_pass123`

### Redis Commander (Redis 管理)

**访问：** http://localhost:8081

- 无需登录
- 自动连接到 Redis
- 可视化查看所有缓存数据

---

## 🐛 故障排查

### 问题 1: 端口被占用

**错误信息：**
```
Error: Bind for 0.0.0.0:5432 failed: port is already allocated
```

**解决方案：**
```bash
# Windows 查找占用端口的进程
netstat -ano | findstr :5432

# 修改端口（docker-compose.dev.yml）
ports:
  - "15432:5432"  # 改用其他端口
```

### 问题 2: 容器无法启动

**检查步骤：**
```bash
# 查看容器状态
docker ps -a

# 查看详细日志
docker-compose -f docker-compose.dev.yml logs postgres
docker-compose -f docker-compose.dev.yml logs redis

# 重新构建
docker-compose -f docker-compose.dev.yml down
docker-compose -f docker-compose.dev.yml up -d --build
```

### 问题 3: 数据库连接失败

**检查清单：**
- [ ] 容器正在运行：`docker ps`
- [ ] 配置正确：
  - WSL2: `config.test.yaml` 中 `host: localhost`
  - Docker: 环境变量 `DB_HOST=postgres`
- [ ] 密码正确：`defi_pass123`
- [ ] 端口正确：`5432`

**测试连接：**
```bash
docker-compose exec postgres psql -U defi_user -d defi_arbitrage
```

### 问题 4: Redis 连接失败

**检查：**
```bash
# 确认 Redis 运行
docker ps | grep redis

# 测试连接
docker-compose exec redis redis-cli ping
# 应该返回: PONG

# 检查配置
# config.test.yaml: redis.enabled: true
```

### 问题 5: WSL2 网络问题

**解决方案：**
```powershell
# 在 Windows PowerShell (管理员) 中执行
wsl --shutdown

# 重启 Docker Desktop

# 重启 WSL2
wsl
```

---

## 📖 配置文件速查

### 配置文件使用场景

```
你在哪里运行后端？
├─ WSL2 (go run)
│  ├─ 配置: config.test.yaml
│  ├─ 数据库: docker-compose.dev.yml
│  └─ 地址: localhost
│
└─ Docker 容器
   ├─ 配置: config.yaml + .env
   ├─ 服务: docker-compose.yml
   └─ 地址: 服务名 (postgres, redis)
```

### 配置对照表

| 配置项 | WSL2 开发 | Docker 生产 | 说明 |
|--------|----------|------------|------|
| 后端位置 | WSL2 | Docker 容器 | - |
| DB_HOST | `localhost` | `postgres` | 网络地址 |
| REDIS_HOST | `localhost` | `redis` | 网络地址 |
| 配置文件 | `config.test.yaml` | `config.yaml` | 应用配置 |
| 环境变量 | 不需要 | `.env` | Docker 专用 |
| Compose 文件 | `docker-compose.dev.yml` | `docker-compose.yml` | 容器编排 |
| RPC 网络 | Sepolia 测试网 | 主网或测试网 | 区块链 |
| 修改配置 | 直接改 YAML | 改 .env | 配置方式 |

---

## 🎓 实战示例

### 示例 1: 修改数据库密码

**WSL2 开发：**
```yaml
# 编辑 config.test.yaml
database:
  password: my_new_password  # ← 改这里

# 重启后端
Ctrl+C
make run
```

**Docker 生产：**
```bash
# 编辑 .env
DB_PASSWORD=my_new_password  # ← 改这里

# 重启服务
docker-compose restart backend
```

---

### 示例 2: 切换 RPC 节点

**WSL2 开发：**
```yaml
# 编辑 config.test.yaml
blockchain:
  rpc_url: https://rpc.ankr.com/eth_sepolia  # ← 免费 RPC
  
# 重启后端
Ctrl+C
make run
```

**Docker 生产：**
```bash
# 编辑 .env
RPC_URL=https://rpc.ankr.com/eth_sepolia  # ← 改这里

# 重启
docker-compose restart backend
```

---

### 示例 3: 启用/禁用 Redis 缓存

**WSL2 开发：**
```yaml
# 编辑 config.test.yaml
redis:
  enabled: false  # ← 禁用 Redis
  
# 重启后端
Ctrl+C
make run
```

**Docker 生产：**
```bash
# 编辑 .env
REDIS_ENABLED=false  # ← 改这里

# 重启
docker-compose restart backend
```

---

## 📈 性能优化

### 数据库优化

连接 pgAdmin 执行：

```sql
-- 创建索引
CREATE INDEX idx_trading_pairs_is_active ON trading_pairs(is_active);
CREATE INDEX idx_price_records_timestamp ON price_records(timestamp);
CREATE INDEX idx_pair_reserves_timestamp ON pair_reserves(timestamp);

-- 查看数据库大小
SELECT pg_size_pretty(pg_database_size('defi_arbitrage'));

-- 查看表大小
SELECT 
  schemaname,
  tablename,
  pg_size_pretty(pg_total_relation_size(schemaname||'.'||tablename)) AS size
FROM pg_tables
WHERE schemaname = 'public'
ORDER BY pg_total_relation_size(schemaname||'.'||tablename) DESC;
```

### Redis 配置优化

编辑 `docker-compose.yml` 或 `docker-compose.dev.yml`：

```yaml
redis:
  command: >
    redis-server
    --appendonly yes
    --maxmemory 256mb
    --maxmemory-policy allkeys-lru
    --save 60 1000
```

---

## 🔍 配置详细说明

### config.test.yaml 完整配置

```yaml
# 数据库配置
database:
  host: localhost           # WSL2 使用 localhost
  port: 5432
  user: defi_user
  password: defi_pass123
  dbname: defi_arbitrage
  sslmode: disable
  timezone: Asia/Shanghai
  max_idle_conns: 5
  max_open_conns: 20
  conn_max_lifetime: 3600

# 区块链配置
blockchain:
  rpc_url: https://sepolia.infura.io/v3/YOUR_KEY
  chain_id: 11155111        # Sepolia 测试网
  timeout: 60               # 超时时间（秒）
  retry: 3                  # 重试次数

# DEX 配置
dexes:
  - name: "Uniswap V2"
    protocol: "uniswap_v2"
    router: "0x..."
    factory: "0x..."
    fee: 30                 # 0.3%
    fee_tier: 0             # V2 不使用
    version: "v2"
    chain_id: 11155111
    
  - name: "Uniswap V3 (0.3%)"
    protocol: "uniswap_v3"
    router: "0x..."
    factory: "0x..."
    fee: 30
    fee_tier: 3000          # V3 费率层级
    version: "v3"
    chain_id: 11155111

# 代币配置
tokens:
  - symbol: "WETH"
    address: "0x..."
    decimals: 18
  - symbol: "DAI"
    address: "0x..."
    decimals: 18
  - symbol: "USDC"
    address: "0x..."
    decimals: 6

# 定时任务
scheduler:
  collect_interval: 300     # 5 分钟采集一次
  analyze_interval: 600     # 10 分钟分析一次
  cleanup_interval: 24      # 24 小时清理一次

# Redis 配置
redis:
  enabled: true             # 启用缓存
  host: localhost
  port: 6379
  password: ""
  db: 0
  ttl: 300                  # 5 分钟过期
```

---

## 🔒 安全建议

### 生产环境部署

1. **修改默认密码**
   ```bash
   # .env
   DB_PASSWORD=your_strong_password_here
   REDIS_PASSWORD=your_redis_password
   ```

2. **限制网络访问**
   ```yaml
   # docker-compose.yml
   ports:
     - "127.0.0.1:5432:5432"  # 仅本地访问
     - "127.0.0.1:6379:6379"
   ```

3. **使用 HTTPS RPC**
   ```bash
   RPC_URL=https://eth-mainnet.g.alchemy.com/v2/YOUR_KEY
   ```

4. **定期备份**
   ```bash
   # 每天备份
   docker-compose exec postgres pg_dump -U defi_user defi_arbitrage > backup_$(date +%Y%m%d).sql
   ```

---

## 🎯 完整启动检查清单

### WSL2 开发环境

- [ ] Docker Desktop 已启动
- [ ] WSL2 已安装并配置
- [ ] Go 1.21+ 已安装
- [ ] 执行 `docker-compose -f docker-compose.dev.yml up -d`
- [ ] 验证容器运行：`docker ps`
- [ ] 修改 `config.test.yaml` 中的 RPC URL
- [ ] 执行 `make migrate-seed`
- [ ] 执行 `make run`
- [ ] 访问 pgAdmin: http://localhost:5050
- [ ] 访问 Redis Commander: http://localhost:8081

### Docker 生产环境

- [ ] Docker 已安装
- [ ] 复制 `env.example` 为 `.env`
- [ ] 修改 `.env` 中的 `RPC_URL`
- [ ] 修改 `.env` 中的 `DB_PASSWORD`
- [ ] 执行 `docker-compose config` 验证配置
- [ ] 执行 `docker-compose up -d`
- [ ] 验证服务：`docker-compose ps`
- [ ] 查看日志：`docker-compose logs -f backend`

---

## 🔄 配置读取流程

### WSL2 开发流程

```
1. docker-compose.dev.yml
   ↓ 启动 PostgreSQL + Redis
   
2. config.test.yaml
   ↓ 后端连接 localhost:5432 + localhost:6379
   
3. Sepolia RPC
   ↓ 从测试网获取数据
   
4. 数据存储
   ↓ PostgreSQL + Redis 缓存
```

### Docker 生产流程

```
1. .env
   ↓ 提供环境变量
   
2. docker-compose.yml
   ↓ 读取 .env，启动所有容器
   
3. Backend 容器
   ↓ 读取 config.yaml + 环境变量
   
4. 容器间通信
   ↓ postgres:5432 + redis:6379
```

---

## 💡 常见问题 FAQ

### Q1: 我应该修改哪个配置文件？

**A:** 
- WSL2 开发 → 修改 `config.test.yaml`
- Docker 生产 → 修改 `.env`

### Q2: .env 文件有什么用？

**A:** 仅在 `docker-compose` 时使用，WSL2 本地运行不会读取。

### Q3: 为什么有 localhost 和 postgres 两种地址？

**A:** 
- `localhost` - WSL2 通过端口映射访问 Docker 容器
- `postgres` - Docker 容器间通过内部网络通信

### Q4: 配置修改后如何生效？

**A:**
- WSL2: Ctrl+C 停止后端 → `make run` 重新运行
- Docker: `docker-compose restart backend`

### Q5: 如何验证配置正确？

**A:**
```bash
# 检查 Docker 配置
docker-compose config

# 查看后端日志
docker-compose logs -f backend

# 测试数据库连接
docker-compose exec postgres psql -U defi_user -d defi_arbitrage
```

### Q6: Redis 是必须的吗？

**A:** 不是必须的。可以通过设置 `redis.enabled: false` 禁用缓存。

### Q7: 如何切换到主网？

**WSL2:**
```yaml
# config.test.yaml
blockchain:
  rpc_url: https://eth-mainnet.g.alchemy.com/v2/YOUR_KEY
  chain_id: 1
```

**Docker:**
```bash
# .env
RPC_URL=https://eth-mainnet.g.alchemy.com/v2/YOUR_KEY
CHAIN_ID=1
```

---

## 📚 目录结构

```
backend/
├── bin/                    # 编译后的二进制文件
├── cmd/
│   └── server/
│       └── main.go         # 主程序入口
├── configs/
│   ├── config.yaml         # 生产配置
│   └── config.test.yaml    # 测试配置
├── internal/
│   ├── collector/          # 数据采集器
│   ├── config/             # 配置管理
│   ├── database/           # 数据库
│   ├── models/             # 数据模型
│   └── scheduler/          # 定时任务
├── pkg/
│   ├── cache/              # Redis 缓存
│   ├── dex/                # DEX 协议适配器
│   └── web3/               # Web3 客户端
├── scripts/                # 测试脚本
├── docs/                   # 详细文档
├── logs/                   # 日志和进度
├── docker-compose.yml      # Docker 生产配置
├── docker-compose.dev.yml  # Docker 开发配置
├── Dockerfile              # Docker 构建文件
├── Makefile                # 自动化命令
├── env.example             # 环境变量模板
└── SETUP_GUIDE.md          # 本文件
```

---

## 🚀 快速命令速查表

| 操作 | WSL2 命令 | Docker 命令 |
|------|----------|------------|
| **启动服务** | `make docker-dev` + `make run` | `docker-compose up -d` |
| **停止服务** | `Ctrl+C` | `docker-compose down` |
| **查看日志** | 终端输出 | `docker-compose logs -f` |
| **重启服务** | `Ctrl+C` → `make run` | `docker-compose restart` |
| **初始化DB** | `make migrate-seed` | 容器自动执行 |
| **连接DB** | `make db-connect` | `make db-connect` |
| **清空缓存** | `make redis-flush` | `make redis-flush` |
| **修改配置** | 编辑 `config.test.yaml` | 编辑 `.env` |

---

## 📞 获取帮助

```bash
# 查看所有 Make 命令
make help

# 查看 Docker 服务状态
docker-compose ps

# 查看后端日志（实时）
docker-compose logs -f backend

# 进入容器调试
docker-compose exec postgres bash
docker-compose exec redis sh
```

---

## 🎉 下一步

配置完成后，可以：

1. **查看项目进度：** `logs/PROGRESS.md`
2. **查看待办事项：** `logs/NEXT_STEPS.txt`
3. **运行性能测试：** `go run scripts/test_performance.go`
4. **查看采集数据：** 访问 pgAdmin 查询数据库

---

## 📊 环境要求

### 最低配置

- **OS:** Windows 10/11 + WSL2
- **RAM:** 4GB+
- **磁盘:** 10GB+
- **Docker:** Docker Desktop 4.0+
- **Go:** 1.21+

### 推荐配置

- **RAM:** 8GB+
- **磁盘:** 20GB+ SSD
- **网络:** 稳定网络连接
- **RPC:** 付费 RPC 节点（Infura/Alchemy）

---

**最后更新：** 2025-12-17  
**版本：** v1.2.0  
**维护者：** DeFi Bot Team
