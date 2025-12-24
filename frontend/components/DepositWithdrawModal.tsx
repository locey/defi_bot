"use client";

import React, { useState } from "react";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Badge } from "@/components/ui/badge";
import {
  ArrowDownLeft,
  ArrowUpRight,
  AlertCircle,
  Loader2,
} from "lucide-react";
import { ArbitrageStats } from "@/lib/hooks/useArbitrageStats";

interface DepositWithdrawModalProps {
  stats: ArbitrageStats;
  onDeposit: (amount: number) => void;
  onWithdraw: (amount: number) => void;
  openDeposit?: boolean;
  openWithdraw?: boolean;
  onOpenDepositChange?: (open: boolean) => void;
  onOpenWithdrawChange?: (open: boolean) => void;
  showDeposit?: boolean;
  showWithdraw?: boolean;
}

export function DepositWithdrawModal({
  stats,
  onDeposit,
  onWithdraw,
  openDeposit,
  openWithdraw,
  onOpenDepositChange,
  onOpenWithdrawChange,
  showDeposit = true,
  showWithdraw = true,
}: DepositWithdrawModalProps) {
  const [internalDepositOpen, setInternalDepositOpen] = useState(false);
  const [internalWithdrawOpen, setInternalWithdrawOpen] = useState(false);
  const [depositAmount, setDepositAmount] = useState("");
  const [withdrawAmount, setWithdrawAmount] = useState("");
  const [isLoading, setIsLoading] = useState(false);

  const formatEth = (val: number) => val.toFixed(4);
  const formatUsd = (eth: number) => (eth * 2200).toFixed(2);

  const handleDeposit = async () => {
    const amount = parseFloat(depositAmount);
    if (!amount || amount <= 0) return;

    setIsLoading(true);
    // 模拟交易
    setTimeout(() => {
      onDeposit(amount);
      setDepositAmount("");
      if (onOpenDepositChange) onOpenDepositChange(false);
      setInternalDepositOpen(false);
      setIsLoading(false);
    }, 1500);
  };

  const handleWithdraw = async () => {
    const amount = parseFloat(withdrawAmount);
    if (!amount || amount <= 0 || amount > stats.currentBalance) return;

    setIsLoading(true);
    // 模拟交易
    setTimeout(() => {
      onWithdraw(amount);
      setWithdrawAmount("");
      if (onOpenWithdrawChange) onOpenWithdrawChange(false);
      setInternalWithdrawOpen(false);
      setIsLoading(false);
    }, 1500);
  };

  return (
    <div className="flex gap-3">
      {/* 存入按钮 */}
      {showDeposit && (
      <Dialog
        open={openDeposit !== undefined ? openDeposit : internalDepositOpen}
        onOpenChange={(o) => {
          if (onOpenDepositChange) onOpenDepositChange(o);
          setInternalDepositOpen(o);
        }}
      >
        {openDeposit === undefined && (
          <DialogTrigger asChild>
            <Button className="flex-1 bg-blue-600 hover:bg-blue-700 text-white">
              <ArrowDownLeft className="w-4 h-4 mr-2" />
              存入 ETH
            </Button>
          </DialogTrigger>
        )}
        <DialogContent className="sm:max-w-md bg-slate-900 border-slate-700 text-slate-100">
          <DialogHeader>
            <DialogTitle className="text-white">存入 ETH</DialogTitle>
            <DialogDescription className="text-slate-400">
              将 ETH 存入套利机器人以获得收益
            </DialogDescription>
          </DialogHeader>

          <div className="space-y-4 py-4">
            {/* 当前钱包余额 */}
            <div className="bg-slate-800/50 p-3 rounded-lg border border-slate-700/50">
              <p className="text-xs text-slate-400 uppercase tracking-wide mb-1">
                钱包余额
              </p>
              <p className="text-lg font-bold text-white font-mono">10.5 ETH</p>
              <p className="text-xs text-slate-500 mt-1">≈ $23,100</p>
            </div>

            {/* 输入框 */}
            <div className="space-y-2">
              <label className="text-sm text-slate-300">存入金额</label>
              <div className="relative">
                <Input
                  type="number"
                  placeholder="0.0"
                  value={depositAmount}
                  onChange={(e) => setDepositAmount(e.target.value)}
                  className="bg-slate-800 border-slate-700 text-white placeholder-slate-500 pr-12"
                  step="0.01"
                  min="0"
                />
                <span className="absolute right-3 top-1/2 -translate-y-1/2 text-slate-400 text-sm">
                  ETH
                </span>
              </div>
              <p className="text-xs text-slate-500">
                {depositAmount
                  ? `≈ $${formatUsd(parseFloat(depositAmount))}`
                  : "输入金额查看价值"}
              </p>
            </div>

            {/* 快速选择 */}
            <div className="space-y-2">
              <p className="text-xs text-slate-400 uppercase tracking-wide">
                快速选择
              </p>
              <div className="grid grid-cols-4 gap-2">
                {[0.1, 0.5, 1, 2].map((amount) => (
                  <Button
                    key={amount}
                    variant="outline"
                    size="sm"
                    className="border-slate-700 hover:border-blue-500 hover:bg-blue-500/10"
                    onClick={() => setDepositAmount(amount.toString())}
                  >
                    {amount}
                  </Button>
                ))}
              </div>
            </div>

            {/* 费用信息 */}
            <div className="bg-blue-500/10 border border-blue-500/20 rounded-lg p-3">
              <p className="text-xs text-blue-300">
                ℹ️ 首次存入无手续费。后续存入收取 0.1% 费用用于协议维护。
              </p>
            </div>
          </div>

          <DialogFooter className="gap-2">
            <Button
              variant="outline"
              className="border-slate-700"
              onClick={() => {
                if (onOpenDepositChange) onOpenDepositChange(false);
                setInternalDepositOpen(false);
              }}
              disabled={isLoading}
            >
              取消
            </Button>
            <Button
              className="bg-blue-600 hover:bg-blue-700"
              onClick={handleDeposit}
              disabled={
                !depositAmount || parseFloat(depositAmount) <= 0 || isLoading
              }
            >
              {isLoading ? (
                <>
                  <Loader2 className="w-4 h-4 mr-2 animate-spin" />
                  处理中...
                </>
              ) : (
                <>
                  <ArrowDownLeft className="w-4 h-4 mr-2" />
                  确认存入
                </>
              )}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
      )}

      {/* 提取按钮 */}
      {showWithdraw && (
      <Dialog
        open={openWithdraw !== undefined ? openWithdraw : internalWithdrawOpen}
        onOpenChange={(o) => {
          if (onOpenWithdrawChange) onOpenWithdrawChange(o);
          setInternalWithdrawOpen(o);
        }}
      >
        {openWithdraw === undefined && (
          <DialogTrigger asChild>
            <Button
              variant="outline"
              className="flex-1 border-slate-700 hover:bg-slate-800 text-slate-300"
            >
              <ArrowUpRight className="w-4 h-4 mr-2" />
              提取 ETH
            </Button>
          </DialogTrigger>
        )}
        <DialogContent className="sm:max-w-md bg-slate-900 border-slate-700 text-slate-100">
          <DialogHeader>
            <DialogTitle className="text-white">提取 ETH</DialogTitle>
            <DialogDescription className="text-slate-400">
              从套利机器人提取 ETH 到你的钱包
            </DialogDescription>
          </DialogHeader>

          <div className="space-y-4 py-4">
            {/* 可用余额 */}
            <div className="bg-slate-800/50 p-3 rounded-lg border border-slate-700/50">
              <p className="text-xs text-slate-400 uppercase tracking-wide mb-1">
                可提取余额
              </p>
              <p className="text-lg font-bold text-white font-mono">
                {formatEth(stats.currentBalance)} ETH
              </p>
              <p className="text-xs text-slate-500 mt-1">
                ≈ ${formatUsd(stats.currentBalance)}
              </p>
            </div>

            {/* 输入框 */}
            <div className="space-y-2">
              <label className="text-sm text-slate-300">提取金额</label>
              <div className="relative">
                <Input
                  type="number"
                  placeholder="0.0"
                  value={withdrawAmount}
                  onChange={(e) => setWithdrawAmount(e.target.value)}
                  className="bg-slate-800 border-slate-700 text-white placeholder-slate-500 pr-12"
                  step="0.01"
                  min="0"
                  max={stats.currentBalance}
                />
                <span className="absolute right-3 top-1/2 -translate-y-1/2 text-slate-400 text-sm">
                  ETH
                </span>
              </div>
              <p className="text-xs text-slate-500">
                {withdrawAmount
                  ? `≈ $${formatUsd(parseFloat(withdrawAmount))}`
                  : "输入金额查看价值"}
              </p>
            </div>

            {/* 快速选择 */}
            <div className="space-y-2">
              <p className="text-xs text-slate-400 uppercase tracking-wide">
                快速选择
              </p>
              <div className="grid grid-cols-3 gap-2">
                <Button
                  variant="outline"
                  size="sm"
                  className="border-slate-700 hover:border-blue-500 hover:bg-blue-500/10"
                  onClick={() =>
                    setWithdrawAmount((stats.currentBalance * 0.25).toString())
                  }
                >
                  25%
                </Button>
                <Button
                  variant="outline"
                  size="sm"
                  className="border-slate-700 hover:border-blue-500 hover:bg-blue-500/10"
                  onClick={() =>
                    setWithdrawAmount((stats.currentBalance * 0.5).toString())
                  }
                >
                  50%
                </Button>
                <Button
                  variant="outline"
                  size="sm"
                  className="border-slate-700 hover:border-blue-500 hover:bg-blue-500/10"
                  onClick={() =>
                    setWithdrawAmount(stats.currentBalance.toString())
                  }
                >
                  全部
                </Button>
              </div>
            </div>

            {/* 提取费用提示 */}
            {withdrawAmount && parseFloat(withdrawAmount) > 0 && (
              <div className="bg-amber-500/10 border border-amber-500/20 rounded-lg p-3">
                <p className="text-xs text-amber-600/80">
                  ⚠️ 提取 {formatEth(parseFloat(withdrawAmount))} ETH 会产生
                  0.05% 的网络费用
                </p>
              </div>
            )}
          </div>

          <DialogFooter className="gap-2">
            <Button
              variant="outline"
              className="border-slate-700"
              onClick={() => {
                if (onOpenWithdrawChange) onOpenWithdrawChange(false);
                setInternalWithdrawOpen(false);
              }}
              disabled={isLoading}
            >
              取消
            </Button>
            <Button
              className="bg-red-600 hover:bg-red-700"
              onClick={handleWithdraw}
              disabled={
                !withdrawAmount ||
                parseFloat(withdrawAmount) <= 0 ||
                parseFloat(withdrawAmount) > stats.currentBalance ||
                isLoading
              }
            >
              {isLoading ? (
                <>
                  <Loader2 className="w-4 h-4 mr-2 animate-spin" />
                  处理中...
                </>
              ) : (
                <>
                  <ArrowUpRight className="w-4 h-4 mr-2" />
                  确认提取
                </>
              )}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
      )}
    </div>
  );
}
