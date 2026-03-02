# 01 - 执行器模块测试 (internal/executor)

## 模块概述

执行器模块负责将策略引擎发现的套利机会转化为链上交易。核心组件：

- **ContractCaller**: ABI 编码/解码、CallData 构建、合约调用
- **ArbitrageExecutor**: 交易构建、签名、发送、确认、事件解析
- **ArbitrageParams**: 与合约 `IArbitrage.ArbitrageParams` 一一对应的参数结构

## 关键文件

| 文件 | 说明 |
|------|------|
| `internal/executor/contract_caller.go` | ABI 定义、CallData 构建、合约读写 |
| `internal/executor/executor.go` | 套利执行器主逻辑、事件解析 |
| `internal/executor/gas_pricer.go` | Gas 价格策略 (EIP-1559 / Legacy) |
| `internal/executor/nonce_tracker.go` | Nonce 管理、并发控制 |
| `internal/executor/tx_manager.go` | 交易生命周期管理 |
| `cmd/test-executor/main.go` | 测试入口 |

## 测试命令

```bash
go run cmd/test-executor/main.go
```

## 测试用例

### 1. RPC 连接测试
- **验证**: 能否连接 Arbitrum RPC 并获取最新区块号
- **预期**: 返回有效区块号 > 0，Chain ID = 42161

### 2. 合约部署验证
- **验证**: ArbitrageCore、ArbitrageVault、ConfigManager 合约是否已部署
- **预期**: 合约地址有字节码 (len(code) > 0)
- **合约地址**:
  - ArbitrageCore: `0x27ea15F931328474d75BE5B0a493278ba7041C74`
  - Vault: `0xB7e37Fb429795E10A8D25752fFC69Fbab71df677`
  - ConfigManager: `0x121D230710dc710f5AA29b73EFc8d403707173A3`

### 3. ContractCaller 创建
- **验证**: ContractCaller 能否正确初始化
- **预期**: 非 nil 对象，ABI 解析成功

### 4. ArbitrageExecutor 初始化
- **验证**: 执行器空初始化 (无私钥) 后统计数据正确
- **预期**:
  - TotalExecuted = 0
  - TotalProfit = 0
  - TotalGasSpent = 0
  - PendingTxs = 0

### 5. CallData 构建
- **验证**: 构建 `executeStrategy` 函数的 CallData
- **测试参数**:
  - 路径: WETH → USDC → WETH (环形套利)
  - DEXes: UniswapV2 → SushiSwap
  - AmountIn: 0.01 ETH (1e16 wei)
  - ExpectProfit: 0.0001 ETH
  - MinProfit: 0.00001 ETH
- **预期**:
  - CallData 非空
  - 函数选择器为 `executeStrategy(uint8,(address,address,uint256,address[],address[],uint256,uint256))` 的前 4 字节
  - CallData 长度合理 (> 100 bytes)

### 6. ABI 与合约一致性
- **验证**: 后端 `ArbitrageCoreABI` 与 Solidity `IArbitrage.ArbitrageParams` 字段顺序一致
- **字段顺序**: asset, tokenOut, amountIn, swapPath, dexes, expectProfit, minProfit
- **事件签名**:
  - `VaultArbitrageExecuted(address,address,uint256,uint256,uint256,uint256,uint256)`
  - `FlashLoanArbitrageExecuted(address,address,uint256,uint256,uint256)`

## 验证标准

| 检查项 | 通过条件 |
|--------|----------|
| RPC 连接 | 区块号 > 0 |
| 合约部署 | code 长度 > 0 |
| ContractCaller | 非 nil |
| Executor 初始化 | 统计数据全为 0 |
| CallData | 非空，选择器正确 |
