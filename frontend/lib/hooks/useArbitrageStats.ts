"use client";

import { useState, useEffect, useCallback } from "react";
import apiClient, { Execution, StatsData, DailyStats } from "@/lib/api/client";

export interface ArbitrageStats {
  // 投入资金
  principal: number;
  // 当前余额
  currentBalance: number;
  // 总收益（余额 - 投入）
  totalProfit: number;
  // 总 Gas 消耗
  totalGasSpent: number;
  // 净利润（收益 - Gas）
  netProfit: number;
  // 收益率百分比
  profitRate: number;
  // 24小时收益
  profit24h: number;
  // 24小时 Gas 消耗
  gas24h: number;
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
  protocol: string;
  strategy: string;
  amount: number;
  profit: number;
  profitRate: number;
  txHash?: string;
  status: "success" | "pending" | "failed";
}

// Convert API execution to RevenueFlow
function executionToRevenueFlow(exec: Execution): RevenueFlow {
  let protocol = "Unknown";
  let strategy = "跨DEX套利";

  try {
    const dexPath = JSON.parse(exec.dex_path || "[]");
    if (dexPath.length > 0) {
      protocol = dexPath[0];
      strategy = dexPath.length > 1 ? "跨DEX套利" : "交易所内套利";
    }
  } catch {
    // Keep defaults
  }

  const timestamp = new Date(exec.timestamp).getTime();
  const date = new Date(exec.timestamp).toLocaleDateString("zh-CN", {
    month: "short",
    day: "numeric",
  });

  // Convert from wei to ETH (assuming 18 decimals)
  const amountIn = parseFloat(exec.amount_in) / 1e18;
  const profit = parseFloat(exec.actual_profit) / 1e18;

  return {
    id: exec.id.toString(),
    timestamp,
    date,
    type: "arbitrage",
    protocol,
    strategy,
    amount: amountIn,
    profit,
    profitRate: exec.profit_rate,
    txHash: exec.tx_hash,
    status: exec.status as "success" | "pending" | "failed",
  };
}

// Convert API daily stats to DailyRevenuePoint
function dailyStatsToRevenuePoints(dailyStats: DailyStats[]): DailyRevenuePoint[] {
  let cumulative = 0;
  
  return dailyStats
    .sort((a, b) => new Date(a.date).getTime() - new Date(b.date).getTime())
    .map((stat) => {
      const daily = parseFloat(stat.profit) / 1e18;
      cumulative += daily;
      
      return {
        date: new Date(stat.date).toLocaleDateString("zh-CN", {
          month: "short",
          day: "numeric",
        }),
        daily,
        cumulative,
      };
    });
}

// Calculate APY from stats
function calculateAPY(stats: StatsData): number {
  const profit24h = parseFloat(stats.last_24h_profit || "0") / 1e18;
  const totalProfit = parseFloat(stats.total_profit || "0") / 1e18;
  
  // If we have 24h profit, use it to estimate APY
  if (profit24h > 0) {
    // Assume 1 ETH principal for calculation
    return profit24h * 365 * 100;
  }
  
  // Otherwise use total profit over 30 days
  if (totalProfit > 0) {
    return (totalProfit / 30) * 365 * 100;
  }
  
  return 0;
}

export const useArbitrageStats = () => {
  const [stats, setStats] = useState<ArbitrageStats>({
    principal: 0,
    currentBalance: 0,
    totalProfit: 0,
    totalGasSpent: 0,
    netProfit: 0,
    profitRate: 0,
    profit24h: 0,
    gas24h: 0,
    apy: 0,
  });

  const [revenueFlows, setRevenueFlows] = useState<RevenueFlow[]>([]);
  const [dailyRevenueData, setDailyRevenueData] = useState<DailyRevenuePoint[]>([]);
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  // Fetch all data from API
  const fetchData = useCallback(async () => {
    try {
      setError(null);

      // Fetch stats, executions, and daily stats in parallel
      const [statsData, executions, dailyStats] = await Promise.all([
        apiClient.getStats().catch(() => null),
        apiClient.getExecutions({ limit: 100 }).catch(() => []),
        apiClient.getDailyStats().catch(() => []),
      ]);

      // Process stats
      if (statsData) {
        const totalProfit = parseFloat(statsData.total_profit || "0") / 1e18;
        const totalGasSpent = parseFloat(statsData.total_gas_spent || "0") / 1e18;
        const netProfit = parseFloat(statsData.net_profit || "0") / 1e18;
        const profit24h = parseFloat(statsData.last_24h_profit || "0") / 1e18;
        const gas24h = parseFloat(statsData.last_24h_gas_spent || "0") / 1e18;
        const apy = calculateAPY(statsData);

        setStats({
          principal: 1, // Default, will be overridden by vault data if available
          currentBalance: 1 + netProfit,
          totalProfit,
          totalGasSpent,
          netProfit,
          profitRate: statsData.avg_profit_rate || 0,
          profit24h,
          gas24h,
          apy,
        });
      }

      // Process executions to revenue flows
      if (executions && executions.length > 0) {
        const flows = executions.map(executionToRevenueFlow);
        setRevenueFlows(flows);
      }

      // Process daily stats
      if (dailyStats && dailyStats.length > 0) {
        const revenuePoints = dailyStatsToRevenuePoints(dailyStats);
        setDailyRevenueData(revenuePoints);
      }
    } catch (err) {
      const message = err instanceof Error ? err.message : "Failed to fetch data";
      setError(message);
      console.error("Failed to fetch arbitrage data:", err);
    } finally {
      setIsLoading(false);
    }
  }, []);

  // Initial load
  useEffect(() => {
    fetchData();
  }, [fetchData]);

  // Auto refresh every 30 seconds
  useEffect(() => {
    const interval = setInterval(() => {
      // Only refresh stats, not full data
      apiClient.getStats()
        .then((statsData) => {
          const totalProfit = parseFloat(statsData.total_profit || "0") / 1e18;
          const totalGasSpent = parseFloat(statsData.total_gas_spent || "0") / 1e18;
          const netProfit = parseFloat(statsData.net_profit || "0") / 1e18;
          const profit24h = parseFloat(statsData.last_24h_profit || "0") / 1e18;
          const gas24h = parseFloat(statsData.last_24h_gas_spent || "0") / 1e18;
          const apy = calculateAPY(statsData);

          setStats((prev) => ({
            ...prev,
            totalProfit,
            totalGasSpent,
            netProfit,
            profitRate: statsData.avg_profit_rate || 0,
            profit24h,
            gas24h,
            apy,
            currentBalance: prev.principal + netProfit,
          }));
        })
        .catch((err) => {
          console.error("Failed to refresh stats:", err);
        });
    }, 30000);

    return () => clearInterval(interval);
  }, []);

  const refreshData = useCallback(async () => {
    setIsLoading(true);
    await fetchData();
  }, [fetchData]);

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
    dailyRevenueData,
    isLoading,
    error,
    refreshData,
    addDeposit,
    addWithdraw,
  };
};
