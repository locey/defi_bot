"use client";

import React from "react";
import { useArbitrageStats } from "@/lib/hooks/useArbitrageStats";
import { ArbitrageInvestmentCard } from "@/components/ArbitrageInvestmentCard";
import { ArbitrageRevenueChart } from "@/components/ArbitrageRevenueChart";
import { ArbitrageRevenueFlow } from "@/components/ArbitrageRevenueFlow";
import { DepositWithdrawModal } from "@/components/DepositWithdrawModal";
import { Card } from "@/components/ui/card";

export function ArbitrageBotPage() {
  const {
    stats,
    revenueFlows,
    isLoading,
    dailyRevenueData,
    addDeposit,
    addWithdraw,
  } = useArbitrageStats();
  const [depositOpen, setDepositOpen] = React.useState(false);
  const [withdrawOpen, setWithdrawOpen] = React.useState(false);

  if (isLoading) {
    return (
      <div className="space-y-6">
        <Card className="h-72 bg-slate-800 border-slate-700 animate-pulse" />
        <Card className="h-96 bg-slate-800 border-slate-700 animate-pulse" />
        <Card className="h-96 bg-slate-800 border-slate-700 animate-pulse" />
      </div>
    );
  }

  return (
    <div className="space-y-8">
      {/* 页面标题 */}
      <div className="space-y-2">
        <h1 className="text-3xl font-bold text-white">套利机器人</h1>
        <p className="text-slate-400">
          自动化套利策略，为你的 ETH 持仓带来稳定收益
        </p>
      </div>

      {/* 第一层：投资总览卡片 + 存入/提取按钮 */}
      <div className="space-y-6">
        <ArbitrageInvestmentCard
          stats={stats}
          onDepositClick={() => setDepositOpen(true)}
          onWithdrawClick={() => setWithdrawOpen(true)}
        />
      </div>

      {/* 存入/提取模态框 */}
      <div>
        <DepositWithdrawModal
          stats={stats}
          onDeposit={(amount) => {
            addDeposit(amount);
            setDepositOpen(false);
          }}
          onWithdraw={(amount) => {}
          }
          openDeposit={depositOpen}
          onOpenDepositChange={setDepositOpen}
          showDeposit
          showWithdraw={false}
        />
        <DepositWithdrawModal
          stats={stats}
          onDeposit={(amount) => {}}
          onWithdraw={(amount) => {
            addWithdraw(amount);
            setWithdrawOpen(false);
          }}
          openWithdraw={withdrawOpen}
          onOpenWithdrawChange={setWithdrawOpen}
          showDeposit={false}
          showWithdraw
        />
      </div>

      {/* 第二层：实时收益图表 */}
      <div>
        <div
          style={{
            fontSize: "11px",
            color: "#f0f",
            backgroundColor: "#222",
            padding: "8px",
            marginBottom: "8px",
            borderRadius: "4px",
          }}
        >
          DEBUG ArbitrageBotPage - dailyRevenueData.length:{" "}
          {dailyRevenueData?.length || 0}
        </div>
        <ArbitrageRevenueChart stats={stats} dailyData={dailyRevenueData} />
      </div>

      {/* 第三层：收益流水详情表 */}
      <div>
        <ArbitrageRevenueFlow flows={revenueFlows} />
      </div>

      {/* 页脚提示 */}
      <div className="bg-slate-800/30 border border-slate-700/50 rounded-lg p-4 text-center">
        <p className="text-sm text-slate-400">
          💡 提示：页面数据每 5 秒自动更新一次。投资金额和收益实时同步至区块链。
        </p>
      </div>
    </div>
  );
}
