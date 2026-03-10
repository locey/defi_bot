"use client";

import React from "react";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import {
  TrendingUp,
  Wallet,
  Zap,
  ArrowDownLeft,
  ArrowUpRight,
} from "lucide-react";
import { Button } from "@/components/ui/button";
import { ArbitrageStats } from "@/lib/hooks/useArbitrageStats";

interface ArbitrageInvestmentCardProps {
  stats: ArbitrageStats;
  onDepositClick?: () => void;
  onWithdrawClick?: () => void;
}

export function ArbitrageInvestmentCard({
  stats,
  onDepositClick,
  onWithdrawClick,
}: ArbitrageInvestmentCardProps) {
  const formatEth = (val: number) => val.toFixed(4);
  const formatUsd = (eth: number) => (eth * 2200).toFixed(2); // 1 ETH ≈ $2200

  return (
    <Card className="bg-gradient-to-br from-slate-900 to-slate-800 border border-slate-700 shadow-xl">
      <CardHeader className="pb-3">
        <div className="flex items-center justify-between">
          <CardTitle className="flex items-center gap-2 text-white">
            <Wallet className="w-5 h-5 text-blue-400" />
            我的投资
          </CardTitle>
          <Badge
            variant="secondary"
            className="bg-green-500/10 text-green-400 border-green-500/30"
          >
            <Zap className="w-3 h-3 mr-1" />
            Active
          </Badge>
        </div>
      </CardHeader>

      <CardContent className="space-y-6">
        {/* 投入金额 */}
        <div className="bg-slate-800/50 p-4 rounded-lg border border-slate-700/50">
          <div className="flex justify-between items-start mb-2">
            <div>
              <p className="text-xs text-slate-400 uppercase tracking-wide mb-1">
                投入资金
              </p>
              <p className="text-2xl font-bold text-white font-mono">
                {formatEth(stats.principal)}
              </p>
              <p className="text-xs text-slate-500 mt-1">
                ≈ ${formatUsd(stats.principal)}
              </p>
            </div>
            <div className="w-12 h-12 rounded-lg bg-blue-500/10 border border-blue-500/20 flex items-center justify-center">
              <Wallet className="w-6 h-6 text-blue-400" />
            </div>
          </div>
        </div>

        {/* 净利润和Gas消耗 */}
        <div className="grid grid-cols-2 gap-3">
          {/* 净利润 */}
          <div className="bg-slate-800/50 p-3 rounded-lg border border-slate-700/50">
            <p className="text-xs text-slate-400 uppercase tracking-wide mb-2">
              净利润
            </p>
            <p
              className={`text-xl font-bold font-mono ${
                stats.netProfit >= 0 ? "text-green-400" : "text-red-400"
              }`}
            >
              {stats.netProfit >= 0 ? "+" : ""}
              {formatEth(stats.netProfit)}
            </p>
            <p className="text-xs text-slate-500 mt-1">
              ≈ ${formatUsd(stats.netProfit)}
            </p>
          </div>

          {/* Gas 消耗 */}
          <div className="bg-slate-800/50 p-3 rounded-lg border border-slate-700/50">
            <p className="text-xs text-slate-400 uppercase tracking-wide mb-2">
              Gas 消耗
            </p>
            <p className="text-xl font-bold text-orange-400 font-mono">
              -{formatEth(stats.totalGasSpent)}
            </p>
            <p className="text-xs text-slate-500 mt-1">
              ≈ ${formatUsd(stats.totalGasSpent)}
            </p>
          </div>
        </div>

        {/* 套利收益和24h数据 */}
        <div className="grid grid-cols-3 gap-3">
          {/* 套利收益（毛利） */}
          <div className="bg-gradient-to-br from-green-500/5 to-transparent p-3 rounded-lg border border-green-500/20">
            <p className="text-xs text-slate-400 uppercase tracking-wide mb-2">
              套利毛利
            </p>
            <p className="text-lg font-bold text-green-400 font-mono">
              +{formatEth(stats.totalProfit)}
            </p>
          </div>

          {/* 24小时收益 */}
          <div className="bg-gradient-to-br from-blue-500/5 to-transparent p-3 rounded-lg border border-blue-500/20">
            <p className="text-xs text-slate-400 uppercase tracking-wide mb-2">
              24h 收益
            </p>
            <p className="text-lg font-bold text-blue-400 font-mono">
              {formatEth(stats.profit24h)}
            </p>
          </div>

          {/* 24小时 Gas */}
          <div className="bg-gradient-to-br from-orange-500/5 to-transparent p-3 rounded-lg border border-orange-500/20">
            <p className="text-xs text-slate-400 uppercase tracking-wide mb-2">
              24h Gas
            </p>
            <p className="text-lg font-bold text-orange-400 font-mono">
              -{formatEth(stats.gas24h)}
            </p>
          </div>
        </div>

        {/* 风险提示 */}
        <div className="bg-amber-500/5 border border-amber-500/20 rounded-lg p-3">
          <p className="text-xs text-amber-600/80">
            ℹ️
            收益基于算法套利，存在市场风险。预期年化收益为估算值，实际收益可能有所偏差。
          </p>
        </div>

        {/* 操作按钮 */}
        <div className="flex gap-2 pt-2 border-t border-slate-700/50">
          <Button
            className="flex-1 bg-blue-600 hover:bg-blue-700 text-white"
            onClick={onDepositClick}
          >
            <ArrowDownLeft className="w-4 h-4 mr-2" />
            存入 ETH
          </Button>
          <Button
            variant="outline"
            className="flex-1 border-slate-700 hover:bg-slate-800 text-slate-300"
            onClick={onWithdrawClick}
          >
            <ArrowUpRight className="w-4 h-4 mr-2" />
            提取 ETH
          </Button>
        </div>
      </CardContent>
    </Card>
  );
}
