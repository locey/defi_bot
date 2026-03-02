# API 接口模块测试

<div align="center">

**📦 模块路径**: `internal/api/`

</div>

---

## 📋 模块概述

API 模块提供 RESTful 接口，包含以下核心组件：

| 文件 | 组件 | 功能 |
|------|------|------|
| `api.go` | API Server | API 服务器 |
| `handlers/executions.go` | ExecutionsHandler | 执行记录接口 |
| `handlers/opportunities.go` | OpportunitiesHandler | 套利机会接口 |
| `handlers/stats.go` | StatsHandler | 统计数据接口 |
| `handlers/tokens.go` | TokensHandler | 代币信息接口 |
| `handlers/vault.go` | VaultHandler | 金库接口 |
| `middleware/middleware.go` | Middleware | 中间件 |

---

## 🧪 测试用例

### 1. API 服务器测试

#### 1.1 启动测试

**目的**: 验证 API 服务器正确启动

**测试步骤**:
```bash
# 启动服务器
go run cmd/server/main.go -config configs/config.test.yaml

# 测试健康检查
curl http://localhost:8080/health
```

**验证点**:
- [ ] 服务器启动成功
- [ ] 监听正确端口
- [ ] 健康检查返回 200

**预期输出**:
```json
{
  "status": "ok",
  "timestamp": "2024-01-01T00:00:00Z"
}
```

---

### 2. Tokens 接口测试

#### 2.1 获取代币列表

**接口**: `GET /api/v1/tokens`

**测试步骤**:
```bash
curl http://localhost:8080/api/v1/tokens
```

**验证点**:
- [ ] 返回 200
- [ ] 返回代币数组
- [ ] 每个代币包含 address, symbol, decimals

**预期响应**:
```json
{
  "data": [
    {
      "address": "0x82aF49447D8a07e3bd95BD0d56f35241523fBab1",
      "symbol": "WETH",
      "name": "Wrapped Ether",
      "decimals": 18,
      "chain_id": 42161
    }
  ],
  "total": 22
}
```

#### 2.2 获取单个代币

**接口**: `GET /api/v1/tokens/:address`

**测试步骤**:
```bash
curl http://localhost:8080/api/v1/tokens/0x82aF49447D8a07e3bd95BD0d56f35241523fBab1
```

**验证点**:
- [ ] 返回 200
- [ ] 返回代币详情
- [ ] 地址不存在返回 404

---

### 3. Opportunities 接口测试

#### 3.1 获取套利机会列表

**接口**: `GET /api/v1/opportunities`

**测试步骤**:
```bash
curl "http://localhost:8080/api/v1/opportunities?limit=10&status=pending"
```

**查询参数**:

| 参数 | 类型 | 说明 |
|------|------|------|
| limit | int | 返回数量限制 |
| offset | int | 分页偏移 |
| status | string | 状态过滤 (pending/executed/expired) |
| min_profit | float | 最小利润过滤 |

**验证点**:
- [ ] 返回 200
- [ ] 分页正确
- [ ] 过滤生效

**预期响应**:
```json
{
  "data": [
    {
      "id": "opp-123",
      "path": ["WETH", "USDC", "WETH"],
      "dexes": ["UniswapV3", "SushiSwap"],
      "expected_profit": "0.01",
      "profit_rate": "0.5%",
      "status": "pending",
      "created_at": "2024-01-01T00:00:00Z"
    }
  ],
  "total": 100,
  "limit": 10,
  "offset": 0
}
```

#### 3.2 获取单个机会详情

**接口**: `GET /api/v1/opportunities/:id`

**测试步骤**:
```bash
curl http://localhost:8080/api/v1/opportunities/opp-123
```

**验证点**:
- [ ] 返回 200
- [ ] 包含完整路径信息
- [ ] 包含利润计算详情

---

### 4. Executions 接口测试

#### 4.1 获取执行记录列表

**接口**: `GET /api/v1/executions`

**测试步骤**:
```bash
curl "http://localhost:8080/api/v1/executions?limit=20"
```

**验证点**:
- [ ] 返回 200
- [ ] 包含交易哈希
- [ ] 包含实际利润

**预期响应**:
```json
{
  "data": [
    {
      "id": "exec-456",
      "opportunity_id": "opp-123",
      "tx_hash": "0x...",
      "status": "success",
      "actual_profit": "0.0095",
      "gas_used": "250000",
      "gas_price": "0.1",
      "executed_at": "2024-01-01T00:01:00Z"
    }
  ],
  "total": 50
}
```

#### 4.2 获取执行详情

**接口**: `GET /api/v1/executions/:id`

**测试步骤**:
```bash
curl http://localhost:8080/api/v1/executions/exec-456
```

**验证点**:
- [ ] 返回完整执行信息
- [ ] 包含区块信息
- [ ] 包含事件日志

---

### 5. Stats 接口测试

#### 5.1 获取总体统计

**接口**: `GET /api/v1/stats`

**测试步骤**:
```bash
curl http://localhost:8080/api/v1/stats
```

**验证点**:
- [ ] 返回 200
- [ ] 包含总执行次数
- [ ] 包含总利润
- [ ] 包含成功率

**预期响应**:
```json
{
  "total_executions": 150,
  "successful_executions": 142,
  "total_profit": "1.5",
  "total_gas_spent": "0.3",
  "net_profit": "1.2",
  "success_rate": "94.67%",
  "avg_profit_per_execution": "0.01",
  "last_24h": {
    "executions": 12,
    "profit": "0.15"
  }
}
```

#### 5.2 获取历史统计

**接口**: `GET /api/v1/stats/history`

**测试步骤**:
```bash
curl "http://localhost:8080/api/v1/stats/history?period=7d"
```

**验证点**:
- [ ] 返回时间序列数据
- [ ] 按日/小时聚合

---

### 6. Vault 接口测试

#### 6.1 获取 Vault 信息

**接口**: `GET /api/v1/vault`

**测试步骤**:
```bash
curl http://localhost:8080/api/v1/vault
```

**验证点**:
- [ ] 返回 Vault 状态
- [ ] 包含总资产
- [ ] 包含总份额

**预期响应**:
```json
{
  "address": "0xB7e37Fb429795E10A8D25752fFC69Fbab71df677",
  "total_assets": "100.5",
  "total_supply": "100.0",
  "price_per_share": "1.005",
  "asset_token": "WETH"
}
```

#### 6.2 获取用户 Vault 信息

**接口**: `GET /api/v1/vault/user/:address`

**测试步骤**:
```bash
curl http://localhost:8080/api/v1/vault/user/0x1234...
```

**验证点**:
- [ ] 返回用户份额
- [ ] 返回用户资产价值

#### 6.3 预览存入

**接口**: `GET /api/v1/vault/preview-deposit`

**测试步骤**:
```bash
curl "http://localhost:8080/api/v1/vault/preview-deposit?amount=1000000000000000000"
```

**验证点**:
- [ ] 返回预计份额
- [ ] 金额单位正确 (wei)

#### 6.4 预览赎回

**接口**: `GET /api/v1/vault/preview-redeem`

**测试步骤**:
```bash
curl "http://localhost:8080/api/v1/vault/preview-redeem?shares=1000000000000000000"
```

**验证点**:
- [ ] 返回预计资产
- [ ] 份额单位正确

---

### 7. 中间件测试

#### 7.1 CORS 测试

**测试步骤**:
```bash
curl -X OPTIONS http://localhost:8080/api/v1/tokens \
  -H "Origin: http://localhost:3000" \
  -H "Access-Control-Request-Method: GET"
```

**验证点**:
- [ ] 返回 CORS 头
- [ ] 允许配置的 Origin

#### 7.2 认证测试

**测试步骤**:
```bash
# 无认证
curl http://localhost:8080/api/v1/protected

# 有认证
curl http://localhost:8080/api/v1/protected \
  -H "Authorization: Bearer xxx"
```

**验证点**:
- [ ] 无认证返回 401
- [ ] 有效认证返回 200

#### 7.3 限流测试

**测试步骤**:
```bash
# 快速发送多个请求
for i in {1..100}; do
  curl http://localhost:8080/api/v1/tokens &
done
```

**验证点**:
- [ ] 超过限制返回 429
- [ ] 返回 Retry-After 头

---

## 🔧 测试命令汇总

```bash
# 启动 API 服务器
go run cmd/server/main.go -config configs/config.test.yaml

# 使用 curl 测试
curl http://localhost:8080/health
curl http://localhost:8080/api/v1/tokens
curl http://localhost:8080/api/v1/stats

# 使用 httpie（更友好）
http GET localhost:8080/api/v1/tokens
http GET localhost:8080/api/v1/opportunities limit==10
```

---

## 📊 API 端点汇总

| 方法 | 路径 | 描述 |
|------|------|------|
| GET | /health | 健康检查 |
| GET | /api/v1/tokens | 代币列表 |
| GET | /api/v1/tokens/:address | 代币详情 |
| GET | /api/v1/opportunities | 机会列表 |
| GET | /api/v1/opportunities/:id | 机会详情 |
| GET | /api/v1/executions | 执行记录 |
| GET | /api/v1/executions/:id | 执行详情 |
| GET | /api/v1/stats | 总体统计 |
| GET | /api/v1/stats/history | 历史统计 |
| GET | /api/v1/vault | Vault 信息 |
| GET | /api/v1/vault/user/:address | 用户 Vault |
| GET | /api/v1/vault/preview-deposit | 存入预览 |
| GET | /api/v1/vault/preview-redeem | 赎回预览 |

---

## 📊 测试检查清单

| 测试项 | 状态 | 备注 |
|--------|:----:|------|
| 服务器启动 | ⬜ | |
| 健康检查 | ⬜ | |
| 代币接口 | ⬜ | |
| 机会接口 | ⬜ | |
| 执行接口 | ⬜ | |
| 统计接口 | ⬜ | |
| Vault 接口 | ⬜ | |
| CORS | ⬜ | |
| 认证 | ⬜ | |
| 限流 | ⬜ | |

---

## ⚠️ 注意事项

1. 测试前确保数据库有数据
2. 部分接口需要认证
3. 注意请求限流
4. 金额单位为 wei（18 位小数）
