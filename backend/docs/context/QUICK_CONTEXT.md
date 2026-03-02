# 快速上下文 - 直接复制到新 Chat

把下面的内容复制给新的 AI Chat：

---

## 上下文交接

**项目**: DeFi 套利机器人后端 (Arbitrum 主网)
**分支**: backend
**路径**: /Users/adamli/Project/MetaNode/Hackathon/defi_bot

### 已完成的工作

1. **修复 UniswapV3 价格查询问题**
   - `multicall.go` 新增 V3 slot0/liquidity 批量查询
   - `price_cache.go` 修复 V3 价格计算（decimals 调整方向、token 顺序）
   - `fast_collector.go` 区分 V2/V3 池子分别处理
   - `dex_price_adapter.go` 新文件，实现 DEXPriceProvider 接口

2. **三种套利模式测试结果**
   - ✅ CEX-DEX: ETHUSDT/BTCUSDT 价格正确（DEX≈1979/68213）
   - ✅ 跨 DEX: 高性能调度器正常（2.2M+ 路径）
   - ✅ 单 DEX: 执行器初始化成功

3. **已提交未推送**
   ```bash
   git push origin backend  # SSH 连接问题，需手动执行
   ```

### 关键文件位置

- 详细上下文文档: `backend/docs/context/2026-02-11_arbitrage_fix_context.md`
- 配置: `backend/configs/config.yaml`
- 环境变量: `backend/.env`
- 主程序: `backend/cmd/server/main.go`

### 修复的核心问题

1. V3 价格查询用 `slot0`+`liquidity`（不是 `getReserves`）
2. V3 sqrtPriceX96 转价格时 decimals 调整: `价格 * 10^(dec0-dec1)`
3. 数据库 token 顺序可能与合约相反，需动态判断
4. CEX 价格语义是 `quoteToken/baseToken`，如 ETHUSDT=1978 表示 1 ETH = 1978 USDT

### 待做工作

1. 执行 `git push origin backend`（或诊断 SSH 问题）
2. 可选：启用 CEX-DEX 执行、集成单 DEX 套利

### 运行方式

```bash
cd backend
go run cmd/server/main.go
```

请先阅读 `backend/docs/context/2026-02-11_arbitrage_fix_context.md` 获取完整上下文。
