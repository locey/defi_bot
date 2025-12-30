"use client";

import { useState, useEffect, useCallback, useRef } from "react";

export interface ArbitrageStats {
  // 投入资金
  principal: number;
  // 当前余额
  currentBalance: number;
  // 总收益（余额 - 投入）
  totalProfit: number;
  // 收益率百分比
  profitRate: number;
  // 24小时收益
  profit24h: number;
  // 年化收益率（APY）
  apy: number;
}

// 每日收益数据点
export interface DailyRevenuePoint {
  date: string;
  daily: number; // 当日收益
  cumulative: number; // 累计收益
}

export interface RevenueFlow {
  id: string;
  timestamp: number;
  date: string;
  type: "arbitrage" | "lending" | "lp_fee" | "harvest";
  protocol: string; // 'Aave', 'Uniswap', 'Curve', 'Compound'
  strategy: string; // 交易所内套利、跨交易所套利、闪电贷套利
  amount: number; // 收益金额（ETH）
  profit: number; // 利润
  profitRate: number; // 利润率
  txHash?: string;
  status: "success" | "pending" | "failed";
}

// 生成每日收益数据（直观展示）
const generateDailyRevenueData = (): DailyRevenuePoint[] => {
  const data: DailyRevenuePoint[] = [];
  let cumulativeProfit = 0;

  // 使用正弦波模拟真实的收益波动（有高有低，但总体上升）
  for (let i = 0; i < 30; i++) {
    const date = new Date();
    date.setDate(date.getDate() - (29 - i)); // 30天前到今天
    const dateStr = date.toLocaleDateString("zh-CN", {
      month: "short",
      day: "numeric",
    });

    // 基础收益 + 波动 + 噪声
    const baseDailyProfit = 0.0048; // 平均每天 0.0048 ETH
    const waveFactor = Math.sin((i / 30) * Math.PI * 4) * 0.0015; // 波动因子
    const noise = (Math.random() - 0.5) * 0.0008; // 噪声
    const randomFactor = Math.random() > 0.2 ? 1 : 0.4; // 20% 的日子收益较低

    let dailyProfit = (baseDailyProfit + waveFactor + noise) * randomFactor;
    dailyProfit = Math.max(0.0001, dailyProfit); // 至少 0.0001 ETH

    cumulativeProfit += dailyProfit;

    data.push({
      date: dateStr,
      daily: dailyProfit,
      cumulative: cumulativeProfit,
    });
  }

  return data;
};

// Mock 数据生成
const generateMockRevenueFlows = (
  dailyData: DailyRevenuePoint[]
): RevenueFlow[] => {
  const now = Date.now();
  const flows: RevenueFlow[] = [];

  const strategies = [
    {
      type: "arbitrage" as const,
      protocol: "Uniswap",
      strategy: "交易所内套利",
    },
    { type: "arbitrage" as const, protocol: "Curve", strategy: "跨交易所套利" },
    { type: "lending" as const, protocol: "Aave", strategy: "闪电贷套利" },
    { type: "lp_fee" as const, protocol: "Uniswap", strategy: "LP 费用" },
    { type: "harvest" as const, protocol: "Aave", strategy: "借贷收益" },
    { type: "harvest" as const, protocol: "Compound", strategy: "借贷收益" },
  ];

  // 根据每日数据生成交易流水
  for (let i = 0; i < 30; i++) {
    const daysAgo = 29 - i;
    const dayData = dailyData[i];

    // 根据每日总收益，分成 3-5 笔交易
    const txCount = Math.floor(Math.random() * 3) + 3;
    let remainingProfit = dayData.daily;

    for (let j = 0; j < txCount; j++) {
      const strategy =
        strategies[Math.floor(Math.random() * strategies.length)];

      // 将当日收益分配到各笔交易
      const profit =
        j === txCount - 1
          ? remainingProfit
          : Math.random() * remainingProfit * 0.5;

      remainingProfit -= profit;

      const txTimestamp =
        now -
        daysAgo * 24 * 60 * 60 * 1000 -
        Math.random() * 24 * 60 * 60 * 1000;

      flows.push({
        id: `flow-${i}-${j}`,
        timestamp: txTimestamp,
        date: dayData.date,
        type: strategy.type,
        protocol: strategy.protocol,
        strategy: strategy.strategy,
        amount: profit,
        profit: profit,
        profitRate: (profit / 1) * 100, // 相对于1ETH投入的收益率
        txHash: `0x${Math.random().toString(16).substr(2, 64)}`,
        status: Math.random() > 0.05 ? "success" : "pending",
      });
    }
  }

  return flows.sort((a, b) => b.timestamp - a.timestamp);
};

// 在模块级别生成一次数据（不在hook内部）
let cachedDailyRevenueData: DailyRevenuePoint[] | null = null;

const getCachedDailyRevenueData = (): DailyRevenuePoint[] => {
  if (!cachedDailyRevenueData) {
    cachedDailyRevenueData = generateDailyRevenueData();
    console.log(
      "✅ Generated dailyRevenueData:",
      cachedDailyRevenueData.length,
      "items"
    );
  }
  return cachedDailyRevenueData;
};

export const useArbitrageStats = () => {
  // 获取缓存的数据
  const dailyRevenueData = getCachedDailyRevenueData();

  // 初始化统计数据
  const [stats, setStats] = useState<ArbitrageStats>(() => {
    if (!dailyRevenueData || dailyRevenueData.length === 0) {
      return {
        principal: 1,
        currentBalance: 1,
        totalProfit: 0,
        profitRate: 0,
        profit24h: 0,
        apy: 0,
      };
    }

    const totalProfit =
      dailyRevenueData[dailyRevenueData.length - 1]?.cumulative || 0;
    const lastTwoDays = dailyRevenueData.slice(-2);
    const profit24h = lastTwoDays[lastTwoDays.length - 1]?.daily || 0;

    return {
      principal: 1,
      currentBalance: 1 + totalProfit,
      totalProfit: totalProfit,
      profitRate: (totalProfit / 1) * 100,
      profit24h: profit24h,
      apy: (totalProfit / 1) * (365 / 30) * 100,
    };
  });

  const [revenueFlows, setRevenueFlows] = useState<RevenueFlow[]>([]);
  const [isLoading, setIsLoading] = useState(true);

  // 模拟获取数据
  useEffect(() => {
    setIsLoading(true);
    // 模拟网络延迟
    const timer = setTimeout(() => {
      const flows = generateMockRevenueFlows(dailyRevenueData);
      setRevenueFlows(flows);
      setIsLoading(false);
    }, 100);

    return () => clearTimeout(timer);
  }, [dailyRevenueData]);

  // 模拟实时更新（每5秒更新一次，会有小幅波动）
  useEffect(() => {
    const interval = setInterval(() => {
      setStats((prev) => ({
        ...prev,
        currentBalance: prev.currentBalance + (Math.random() - 0.45) * 0.0001,
        profit24h: prev.profit24h + (Math.random() - 0.45) * 0.00001,
      }));
    }, 5000);

    return () => clearInterval(interval);
  }, []);

  const addDeposit = useCallback((amount: number) => {
    setStats((prev) => ({
      ...prev,
      principal: prev.principal + amount,
      currentBalance: prev.currentBalance + amount,
    }));
  }, []);

  const addWithdraw = useCallback((amount: number) => {
    setStats((prev) => ({
      ...prev,
      currentBalance: Math.max(0, prev.currentBalance - amount),
    }));
  }, []);

  return {
    stats,
    revenueFlows,
    isLoading,
    dailyRevenueData,
    addDeposit,
    addWithdraw,
  };
};
