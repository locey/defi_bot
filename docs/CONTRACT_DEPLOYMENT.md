# ArbitrageX 合约部署文档

> 日期: 2026-03-06
> 链: Arbitrum One (Chain ID: 42161)

---

## 1. 当前生产环境合约 (2026-02-16 部署)

以下地址为当前 `backend/configs/config.yaml` 中使用的合约，2026-02-16 重新部署。

### 核心合约

| 合约 | 地址 | 说明 |
|------|------|------|
| **ArbitrageCore** | `0x0D14428b4e297344C2C51D087934a3e3a9b822B9` | 主入口合约 (UUPS 可升级)，协调整个套利流程 |
| **ConfigManager** | `0x8dD68621209D31D2c3202F894B956DDEdF893697` | 配置管理合约 |

### 套利执行合约

| 合约 | 地址 | 说明 |
|------|------|------|
| **ArbitrageVault** | `0xBE312B103f89489Df5bFf0A59d327c4C8B19d94F` | 资金金库，存储套利资金 (WETH) |
| **SpotArbitrage** | `0xceCCf5D3f5ab1dAB5b13Fd5Cf7deeA9C3DBaad17` | 现货套利执行合约 |

### 集成合约

| 合约 | 地址 | 说明 |
|------|------|------|
| **DoubleRouterIntegration** | `0xE075114d8C9142c9497eabc10b4CAaf2794c95dc` | 双路由集成，执行跨 DEX 交换 |
| **UniswapV2Integration** | `0x1c2897899948d35dbF394e5a1980986474cF0b27` | Uniswap V2 适配器 |

### 闪电贷合约

| 合约 | 地址 | 说明 |
|------|------|------|
| **FlashLoanRouter** | `0x4A89d8B8B0376Fc8c7e2119445c828719BeC019E` | 闪电贷路由合约 |
| **FlashLoanArbitrage** | _(未部署)_ | 闪电贷套利合约 (config 中为空) |

### 外部合约 (非自己部署)

| 合约 | 地址 | 说明 |
|------|------|------|
| **Multicall3** | `0xcA11bde05977b3631167028862bE2a173976CA11` | 批量调用工具 |
| **Aave V3 LendingPool** | `0x794a61358D6845594F94dc1DB02A252b5b4814aD` | 闪电贷来源 |

---

## 2. Keeper 钱包

| 项目 | 值 |
|------|-----|
| **地址** | _(见 `backend/configs/config.yaml` 中 keeper.address)_ |
| **链** | Arbitrum One (42161) |
| **角色** | 发起套利交易、支付 Gas |
| **私钥存储** | 环境变量 `KEEPER_PRIVATE_KEY`，绝不写入配置文件或文档 |

---

## 3. DEX 路由合约

| DEX | Router 地址 | Factory 地址 | 版本 |
|-----|-------------|--------------|------|
| **Uniswap V3** | `0xE592427A0AEce92De3Edee1F18E0157C05861564` | `0x1F98431c8aD98523631AE4a59f267346ea31F984` | V3 |
| **Uniswap V3 Quoter** | `0x61fFE014bA17989E743c5F6cB21bF9697530B21e` | - | V3 |
| **SushiSwap** | `0x1b02dA8Cb0d097eB8D57A175b88c7D8b47997506` | `0xc35DADB65012eC5796536bD9864eD8773aBc74C4` | V2 |
| **Camelot** | `0xc873fEcbd354f5A56E00E710B90EF4201db2448d` | `0x6EcCab422D763aC031210895C81787E87B43A652` | V2 |
| **GMX** | `0xaBBc5F99639c9B6bCb58544ddf04EFA6802F4064` | - | V1 |

---

## 4. 代币地址 (Arbitrum One)

| 代币 | 地址 | Decimals | CEX 交易对 |
|------|------|----------|-----------|
| **WETH** | `0x82aF49447D8a07e3bd95BD0d56f35241523fBab1` | 18 | ETHUSDT |
| **USDC** (Native) | `0xaf88d065e77c8cC2239327C5EDb3A432268e5831` | 6 | USDCUSDT |
| **USDC.e** (Bridged) | `0xFF970A61A04b1cA14834A43f5dE4533eBDDB5CC8` | 6 | USDCUSDT |
| **USDT** | `0xFd086bC7CD5C481DCC9C85ebE478A1C0b69FCbb9` | 6 | - |
| **DAI** | `0xDA10009cBd5D07dd0CeCc66161FC93D7c9000da1` | 18 | DAIUSDT |
| **WBTC** | `0x2f2a2543B76A4166549F7aaB2e75Bef0aefC5B0f` | 8 | BTCUSDT |
| **ARB** | `0x912CE59144191C1204E64559FE8253a0e49E6548` | 18 | ARBUSDT |
| **GMX** | `0xfc5A1A6EB076a2C7aD06eD22C90d7E710E35ad0a` | 18 | GMXUSDT |
| **LINK** | `0xf97f4df75117a78c1A5a0DBb814Af92458539FB4` | 18 | LINKUSDT |
| **UNI** | `0xFa7F8980b0f1E64A2062791cc3b0871572f1F7f0` | 18 | UNIUSDT |
| **AAVE** | `0xba5DdD1f9d7F570dc94a51479a000E3BCE967196` | 18 | AAVEUSDT |
| **CRV** | `0x11cDb42B0EB46D95f990BeDD4695A6e3fA034978` | 18 | CRVUSDT |
| **PENDLE** | `0x0c880f6761F1af8d9Aa9C466984b80DAb9a8c9e8` | 18 | PENDLEUSDT |
| **RDNT** | `0x3082CC23568eA640225c2467653dB90e9250AaA0` | 18 | RDNTUSDT |

---

## 5. 历史部署记录

### 第一次部署 (2026-01-15)

来源: `contract/deployments/arbitrum-addresses.json`

| 合约 | 地址 | 状态 |
|------|------|------|
| ArbitrageCore | `0x27ea15F931328474d75BE5B0a493278ba7041C74` | 已弃用 |
| Vault | `0xB7e37Fb429795E10A8D25752fFC69Fbab71df677` | 已弃用 |
| ConfigManager | `0x121D230710dc710f5AA29b73EFc8d403707173A3` | 已弃用 |
| SpotArbitrage | `0x7e9eC412aF1f8657b99Df8f419c5D98F11F41bd5` | 已弃用 |
| DoubleRouterIntegration | `0x67d905Da6b09733f1695db12A962066Ca85A95cC` | 已弃用 |
| UniswapV2Integration | `0xf9680677Da105F3B10538A55F6e214F9c72AA120` | 已弃用 |

### 第二次部署 (2026-02-16) — 当前生产环境

来源: `backend/configs/config.yaml` (见第 1 节)

**变更说明:**
- 重新部署所有核心合约
- 更新 ArbitrageCore 合约逻辑
- 新增 FlashLoanRouter
- Keeper 地址不变

### Sepolia 测试网部署

来源: `contract/deployments/sepolia.json`

| 合约 | 地址 |
|------|------|
| ArbitrageCore | `0xb961B5d696E56248612a4cb3Eb2bFD7E1C75c0bb` |
| Vault | `0x1F9B7d65B0529424206e0Fdbf05851eB8F2cd608` |
| SpotArbitrage | `0xe81161A3f2c3d95D403DF6803c6736cBC32B3acf` |
| FlashLoanRouter | `0xbf247006b77E23c2924Cd3A6F7dDD525cF68fF47` |

---

## 6. 合约交互流程

```
Keeper (EOA)
  │
  ▼
ArbitrageCore.executeStrategy(params)     ← 主入口
  │
  ├── ArbitrageVault.withdraw(asset, amount)    ← 取出套利资金
  │
  ├── SpotArbitrage.execute(...)                ← 执行 DEX 交换
  │     │
  │     └── DoubleRouterIntegration.doubleRouterSwap(...)
  │           │
  │           ├── DEX A (Uniswap V3 / SushiSwap / ...)
  │           └── DEX B (不同费率或不同 DEX)
  │
  ├── 检查利润 ≥ minProfit
  │
  ├── 扣除 10% 平台费
  │
  └── ArbitrageVault.deposit(netProfit)         ← 利润存回金库
```

### 关键事件

| 事件 | 合约 | 用途 |
|------|------|------|
| `VaultArbitrageExecuted` | ArbitrageVault | 记录套利执行结果 (amountIn, profit, fee, netProfit) |
| `StrategyExecuted` | ArbitrageCore | 记录策略执行状态 |

---

## 7. Arbiscan 验证链接

- ArbitrageCore: https://arbiscan.io/address/0x0D14428b4e297344C2C51D087934a3e3a9b822B9
- Vault: https://arbiscan.io/address/0xBE312B103f89489Df5bFf0A59d327c4C8B19d94F
- SpotArbitrage: https://arbiscan.io/address/0xceCCf5D3f5ab1dAB5b13Fd5Cf7deeA9C3DBaad17
- Keeper: _(见 config.yaml 中 keeper.address)_
