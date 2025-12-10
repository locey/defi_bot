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

        {/* 当前余额和收益 */}
        <div className="grid grid-cols-2 gap-3">
          {/* 当前余额 */}
          <div className="bg-slate-800/50 p-3 rounded-lg border border-slate-700/50">
            <p className="text-xs text-slate-400 uppercase tracking-wide mb-2">
              当前余额
            </p>
            <p className="text-xl font-bold text-white font-mono">
              {formatEth(stats.currentBalance)}
            </p>
            <p className="text-xs text-slate-500 mt-1">
              ≈ ${formatUsd(stats.currentBalance)}
            </p>
          </div>

          {/* 收益总额 */}
          <div className="bg-slate-800/50 p-3 rounded-lg border border-slate-700/50">
            <p className="text-xs text-slate-400 uppercase tracking-wide mb-2">
              收益总额
            </p>
            <p
              className={`text-xl font-bold font-mono ${
                stats.totalProfit >= 0 ? "text-green-400" : "text-red-400"
              }`}
            >
              {stats.totalProfit >= 0 ? "+" : ""}
              {formatEth(stats.totalProfit)}
            </p>
            <p className="text-xs text-slate-500 mt-1">
              ≈ ${formatUsd(stats.totalProfit)}
            </p>
          </div>
        </div>

        {/* 收益率和24h收益 */}
        <div className="grid grid-cols-2 gap-3">
          {/* 收益率 */}
          <div className="bg-gradient-to-br from-green-500/5 to-transparent p-3 rounded-lg border border-green-500/20">
            <p className="text-xs text-slate-400 uppercase tracking-wide mb-2">
              收益率
            </p>
            <div className="flex items-end gap-1">
              <p className="text-xl font-bold text-green-400 font-mono">
                {stats.profitRate.toFixed(2)}
              </p>
              <p className="text-xs text-slate-400 mb-0.5">%</p>
            </div>
            <p className="text-xs text-slate-500 mt-1">相对投入资金</p>
          </div>

          {/* 24小时收益 */}
          <div className="bg-gradient-to-br from-blue-500/5 to-transparent p-3 rounded-lg border border-blue-500/20">
            <p className="text-xs text-slate-400 uppercase tracking-wide mb-2">
              24h 收益
            </p>
            <p className="text-xl font-bold text-blue-400 font-mono">
              {formatEth(stats.profit24h)}
            </p>
            <p className="text-xs text-slate-500 mt-1">最近24小时</p>
          </div>
        </div>

        {/* 年化收益率 */}
        <div className="bg-gradient-to-r from-purple-500/10 to-pink-500/10 p-4 rounded-lg border border-purple-500/20 flex items-start justify-between">
          <div>
            <p className="text-xs text-slate-400 uppercase tracking-wide mb-1">
              预期年化收益率
            </p>
            <p className="text-2xl font-bold text-transparent bg-clip-text bg-gradient-to-r from-purple-400 to-pink-400 font-mono">
              {stats.apy.toFixed(1)}%
            </p>
            <p className="text-xs text-slate-500 mt-1">基于最近30天数据计算</p>
          </div>
          <TrendingUp className="w-8 h-8 text-purple-400 opacity-50" />
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
