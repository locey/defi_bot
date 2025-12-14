# 套利机器人 (Arbitrage Robot) - 模块化文件结构

这个文件夹包含了完整的套利机器人功能模块，组织清晰，易于维护和扩展。

## 📁 文件夹结构

```
arbitrage/
├── components/          # React 组件文件
│   ├── ArbitrageBotPage.tsx           # 主页面容器组件
│   ├── ArbitrageInvestmentCard.tsx    # 投资总览卡片（投入金额、当前余额、收益率等）
│   ├── ArbitrageRevenueChart.tsx      # 收益趋势图表（30天柱状图）
│   ├── ArbitrageRevenueFlow.tsx       # 收益流水交易历史表
│   └── DepositWithdrawModal.tsx       # 存入/提取弹窗
├── hooks/               # React 自定义 Hook
│   └── useArbitrageStats.ts           # 套利统计数据 Hook（Mock 数据生成、状态管理）
├── utils/               # 工具函数（预留）
├── docs/                # 文档文件（预留）
├── page.tsx             # 路由页面（Next.js app router）
└── README.md            # 本文件
```

## 🎯 各文件说明

### Components

#### **ArbitrageBotPage.tsx** (主容器)

- 组织页面布局
- 状态管理（存入/提取模态框）
- 数据流转发

#### **ArbitrageInvestmentCard.tsx** (投资卡片)

- 显示投入资金
- 显示当前余额
- 显示收益总额和收益率
- 显示 24h 收益
- 显示预期 APY
- 集成 [存入] [提取] 按钮

#### **ArbitrageRevenueChart.tsx** (趋势图表)

- 30 天日收益数据的柱状图
- 柱子高度基于日收益（每日利润）
- 鼠标悬停显示详细数值
- 显示统计指标：最高日收益、平均日收益、累计收益、预计年收益

#### **ArbitrageRevenueFlow.tsx** (流水记录)

- 显示所有收益交易历史
- 支持按协议筛选（Aave、Uniswap、Curve 等）
- 支持按状态筛选（成功、处理中、失败）
- 支持搜索交易 ID
- 100+ 条 Mock 交易记录

#### **DepositWithdrawModal.tsx** (操作弹窗)

- 存入 ETH 弹窗
  - 显示钱包余额
  - 输入存入金额
  - 快速选项：0.1, 0.5, 1 ETH
  - 显示成本估算
- 提取 ETH 弹窗
  - 显示当前余额
  - 输入提取金额
  - 安全提醒

### Hooks

#### **useArbitrageStats.ts** (数据管理 Hook)

返回的数据结构：

```typescript
{
  // 统计数据
  stats: {
    principal: number;          // 投入资金 (ETH)
    currentBalance: number;     // 当前余额 (ETH)
    totalProfit: number;        // 总收益 (ETH)
    profitRate: number;         // 收益率 (%)
    profit24h: number;          // 24小时收益 (ETH)
    apy: number;                // 预期年化收益率 (%)
  },

  // 收益流水记录 (100+ 条)
  revenueFlows: Array<{
    id: string;
    timestamp: number;
    protocol: string;           // 'Aave' | 'Uniswap' | 'Curve' | ...
    strategy: string;           // '交易所内套利' | '跨交易所套利' | ...
    profit: number;
    status: 'success' | 'pending' | 'failed';
  }>,

  // 30天日收益数据
  dailyRevenueData: Array<{
    date: string;               // 'Dec 7'
    daily: number;              // 当日收益 (ETH)
    cumulative: number;         // 累计收益 (ETH)
  }>,

  isLoading: boolean;

  // 操作方法
  addDeposit: (amount: number) => void;
  addWithdraw: (amount: number) => void;
}
```

## 🎨 样式特点

- **深色主题** - 完整的深色 Web3 UI 风格
- **Tailwind CSS** - 响应式设计，支持所有屏幕尺寸
- **渐变效果** - 蓝色/绿色/紫色的渐变卡片和按钮
- **交互反馈** - 悬停、加载态、成功/失败提示

## 📊 Mock 数据说明

### 投资数据

- 投入资金：1 ETH
- 当前余额：1.1238 ETH
- 总收益：0.1238 ETH
- 收益率：12.38%
- 24h 收益：0.00432 ETH

### 30 天收益数据

- 生成方式：正弦波 + 噪声
- 日收益范围：0.001 - 0.007 ETH
- 累计收益范围：0.1158 - 0.1238 ETH
- 用于趋势图表展示

### 流水记录

- 100+ 条交易记录
- 包含 5 种协议：Aave、Uniswap、Curve、Compound、PancakeSwap
- 包含 5 种策略：交易所内套利、跨交易所套利、闪电贷、LP 费用、借贷收益
- 包含 3 种状态：成功、处理中、失败

## 🔧 如何扩展

### 1. 添加新的统计指标

编辑 `hooks/useArbitrageStats.ts`，扩展 `ArbitrageStats` 接口和返回数据。

### 2. 添加新的交易策略

在 `hooks/useArbitrageStats.ts` 中的 `strategyOptions` 数组中添加新策略。

### 3. 集成真实 API

将 `useArbitrageStats.ts` 中的 Mock 数据生成函数替换为真实 API 调用。

### 4. 自定义样式

所有样式使用 Tailwind CSS，可在各组件中直接修改 `className` 属性。

## 📱 响应式设计

- **移动端** (< 640px) - 单列布局，卡片全宽
- **平板** (640px - 1024px) - 两列布局
- **桌面** (> 1024px) - 三列或网格布局

## 🚀 快速开始

### 使用现有组件

```tsx
import { ArbitrageBotPage } from "@/arbitrage/components/ArbitrageBotPage";

export default function Page() {
  return <ArbitrageBotPage />;
}
```

### 使用单个组件

```tsx
import { useArbitrageStats } from "@/arbitrage/hooks/useArbitrageStats";
import { ArbitrageInvestmentCard } from "@/arbitrage/components/ArbitrageInvestmentCard";

export function MyComponent() {
  const { stats } = useArbitrageStats();
  return <ArbitrageInvestmentCard stats={stats} />;
}
```

## 📝 类型定义

所有 TypeScript 类型都定义在 `hooks/useArbitrageStats.ts` 中：

```typescript
interface ArbitrageStats { ... }
interface DailyRevenuePoint { ... }
interface RevenueFlow { ... }
```

## 🎯 下一步

1. ✅ 完成前端 UI 和 Mock 数据
2. ⏳ 集成真实区块链数据（Wagmi + 合约调用）
3. ⏳ 实现真实的套利算法
4. ⏳ 添加实时数据更新（WebSocket）
5. ⏳ 优化性能和安全性

---

**最后更新**: 2025-12-07
**版本**: 1.0.0
