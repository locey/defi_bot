"use client";

import React from "react";
import { useArbitrageStats } from "@/lib/hooks/useArbitrageStats";
import { ArbitrageInvestmentCard } from "./ArbitrageInvestmentCard";
import { ArbitrageRevenueFlow } from "./ArbitrageRevenueFlow";
import { DepositWithdrawModal } from "./DepositWithdrawModal";
import { Card } from "@/components/ui/card";
import { useEthVault } from "@/lib/hooks/useEthVault";
import { Address } from "viem";
import { useToast } from "@/hooks/use-toast";
import { Input } from "@/components/ui/input";
import { Button } from "@/components/ui/button";

export function ArbitrageBotPage() {
  const { stats, revenueFlows, isLoading, addDeposit, addWithdraw } =
    useArbitrageStats();
  const [depositOpen, setDepositOpen] = React.useState(false);
  const [withdrawOpen, setWithdrawOpen] = React.useState(false);
  const { toast } = useToast();

  const vaultEnv = process.env.NEXT_PUBLIC_ETH_VAULT_ADDRESS as
    | `0x${string}`
    | undefined;
  const [vaultAddressStr, setVaultAddressStr] = React.useState<string>(vaultEnv ?? "");
  const [vaultDecimalsStr, setVaultDecimalsStr] = React.useState<string>(
    process.env.NEXT_PUBLIC_VAULT_ASSET_DECIMALS ?? "18"
  );
  React.useEffect(() => {
    if (!vaultEnv) {
      try {
        const savedAddr = localStorage.getItem("vaultAddress") || "";
        const savedDec = localStorage.getItem("vaultDecimals") || "18";
        if (savedAddr) setVaultAddressStr(savedAddr);
        if (savedDec) setVaultDecimalsStr(savedDec);
      } catch {}
    }
  }, [vaultEnv]);
  const computedVault = vaultAddressStr && vaultAddressStr.startsWith("0x") ? (vaultAddressStr as Address) : undefined;
  const ethVault = computedVault
    ? useEthVault(computedVault as Address, {
        erc20: { decimals: Number(vaultDecimalsStr || "18") },
      })
    : null;
  const statsView = {
    ...stats,
    principal: ethVault?.principalShares ?? stats.principal,
    currentBalance: ethVault?.walletEth ?? stats.currentBalance,
  };
  const live = ethVault;

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
      {!ethVault && (
        <Card className="bg-slate-900 border-slate-700 p-4">
          <div className="grid grid-cols-1 md:grid-cols-3 gap-3 items-end">
            <div>
              <p className="text-xs text-slate-400 mb-1">金库地址</p>
              <Input
                placeholder="0x..."
                value={vaultAddressStr}
                onChange={(e) => setVaultAddressStr(e.target.value)}
                className="bg-slate-800 border-slate-700 text-white"
              />
            </div>
            <div>
              <p className="text-xs text-slate-400 mb-1">资产精度</p>
              <Input
                type="number"
                placeholder="18"
                value={vaultDecimalsStr}
                onChange={(e) => setVaultDecimalsStr(e.target.value)}
                className="bg-slate-800 border-slate-700 text-white"
              />
            </div>
            <div className="mt-2 md:mt-0">
              <Button
                className="w-full bg-blue-600 hover:bg-blue-700"
                onClick={() => {
                  if (!vaultAddressStr || !vaultAddressStr.startsWith("0x") || vaultAddressStr.length !== 42) {
                    toast({ title: "地址无效", description: "请输入合法的合约地址", variant: "destructive" });
                    return;
                  }
                  try {
                    localStorage.setItem("vaultAddress", vaultAddressStr);
                    localStorage.setItem("vaultDecimals", vaultDecimalsStr || "18");
                  } catch {}
                  toast({ title: "已保存", description: "金库设置已更新" });
                }}
              >
                保存金库设置
              </Button>
            </div>
          </div>
        </Card>
      )}
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
          stats={statsView}
          onDepositClick={() => setDepositOpen(true)}
          onWithdrawClick={() => setWithdrawOpen(true)}
        />
      </div>

      {/* 存入/提取模态框 */}
      <div>
        <DepositWithdrawModal
          stats={statsView}
          onDeposit={async (amount) => {
            if (ethVault) {
              try {
                await ethVault.deposit(amount);
                await ethVault.refreshStats();
                addDeposit(amount);
                toast({ title: "存入成功", description: `已存入 ${amount} ETH` });
              } catch (e) {
                toast({ title: "存入失败", description: String(e), variant: "destructive" });
                throw e;
              }
            } else {
              toast({ title: "未配置金库地址", description: "请设置 NEXT_PUBLIC_ETH_VAULT_ADDRESS", variant: "destructive" });
              throw new Error("Vault address not configured");
            }
          }}
          onWithdraw={() => {}}
          openDeposit={depositOpen}
          onOpenDepositChange={setDepositOpen}
          showDeposit
          showWithdraw={false}
          walletBalanceEth={ethVault?.walletEth}
        />
        <DepositWithdrawModal
          stats={statsView}
          onDeposit={() => {}}
          onWithdraw={async (amount) => {
            if (ethVault) {
              try {
                await ethVault.withdraw(amount);
                await ethVault.refreshStats();
                addWithdraw(amount);
                toast({ title: "提取成功", description: `已提取 ${amount} ETH` });
              } catch (e) {
                toast({ title: "提取失败", description: String(e), variant: "destructive" });
                throw e;
              }
            } else {
              toast({ title: "未配置金库地址", description: "请设置 NEXT_PUBLIC_ETH_VAULT_ADDRESS", variant: "destructive" });
              throw new Error("Vault address not configured");
            }
          }}
          openWithdraw={withdrawOpen}
          onOpenWithdrawChange={setWithdrawOpen}
          showDeposit={false}
          showWithdraw
          walletBalanceEth={ethVault?.walletEth}
        />
      </div>

      {/* 第二层：收益流水详情表 */}
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
