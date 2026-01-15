# 配置文件说明

## 📁 配置文件列表

### 🌐 主网环境 (`config.yaml`)
**用途**: 生产环境 / 主网数据采集和套利

**基础配置**:
- 数据库: `defi_arbitrage`
- 网络: Ethereum Mainnet (Chain ID: 1)
- 代币: 22个主流代币
- DEX: 5个（Uniswap V2/V3 + SushiSwap）

**高性能特性**:
- ✅ RPC 池（4个 RPC URL，自动负载均衡）
- ✅ WebSocket 支持（实时事件订阅）
- ✅ 分层监控（Tier1/2/3 按 TVL 分级）
- ✅ Multicall 批量调用（500个/批）
- ✅ 高性能连接池（50个数据库连接）
- ✅ 价格缓存（0.01% 变化阈值）
- ✅ 路径预计算
- ✅ 策略引擎（balanced/profit_first/low_risk）
- ✅ CEX 数据采集（Binance）
- ✅ Prometheus 监控支持

**安全限制**:
- 自动执行: 默认禁用（dry_run）
- 单笔限额: 10 ETH
- 每日亏损: 1 ETH

---

### 🧪 测试网环境 (`config.test.yaml`)
**用途**: 开发测试 / Sepolia 测试网

**基础配置**:
- 数据库: `defi_arbitrage_test`（独立数据库）
- 网络: Sepolia Testnet (Chain ID: 11155111)
- 代币: 23个测试代币
- DEX: 4个（Uniswap V2 + V3三个费率层级）

**高性能特性**:
- ✅ RPC 池（3个公共 RPC，故障转移）
- ✅ Multicall 支持（100个/批）
- ✅ 高性能连接池（50个数据库连接）
- ✅ 价格缓存支持
- ✅ 路径预计算
- ✅ 策略引擎
- ⚠️ WebSocket: 不支持（公共 RPC 限制）
- ⚠️ 高性能模式: 禁用（需要 WebSocket）

**安全限制**:
- 自动执行: 禁用
- 单笔限额: 0.1 ETH
- 每日亏损: 0.5 ETH

## RPC 配置说明

### Alchemy（推荐但有配额限制）
- 支持 RPC + WebSocket
- 月度配额：300M compute units
- 速度快，稳定性好
- **当前状态**: 配额已用完

### 公共 RPC（免费但有速率限制）
- 仅支持 RPC（无 WebSocket）
- 速率限制：600 请求/60秒
- 响应较慢
- **当前使用**: 测试环境

## 速率限制处理

### 减少请求数策略：
1. 使用极简配置（减少代币和DEX数量）
2. 增加采集间隔（60秒而不是30秒）
3. 使用 Redis 缓存减少重复请求
4. 交易对预加载（跳过已存在的交易对查询）

### RPC 轮换策略（待实现）：
- 配置多个 RPC URL
- 自动轮换避免单个RPC限流
- 失败自动切换到备用 RPC
