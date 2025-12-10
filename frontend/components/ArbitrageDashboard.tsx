"use client";

import React, { useRef, useEffect, useState } from "react";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Button } from "@/components/ui/button";
import { Badge } from "@/components/ui/badge";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Input } from "@/components/ui/input";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  DialogTrigger,
} from "@/components/ui/dialog";
import { Play, Square, Terminal, Wallet, TrendingUp, Activity, RefreshCw, ArrowUpRight, ArrowDownLeft, History } from "lucide-react";
import { BotStats, ArbitrageLog, Transaction } from "@/lib/hooks/useArbitrageBot";

interface ArbitrageDashboardProps {
  stats: BotStats;
  logs: ArbitrageLog[];
  transactions: Transaction[];
  isRunning: boolean;
  onStart: () => void;
  onStop: () => void;
  onDeposit: (amount: number) => void;
  onWithdraw: (amount: number) => void;
}

export function ArbitrageDashboard({
  stats,
  logs,
  transactions,
  isRunning,
  onStart,
  onStop,
  onDeposit,
  onWithdraw,
}: ArbitrageDashboardProps) {
  const scrollRef = useRef<HTMLDivElement>(null);
  const [activeTab, setActiveTab] = useState("logs");
  const [depositAmount, setDepositAmount] = useState("");
  const [withdrawAmount, setWithdrawAmount] = useState("");

  // Auto-scroll logs
  useEffect(() => {
    if (scrollRef.current && activeTab === "logs") {
      scrollRef.current.scrollTop = scrollRef.current.scrollHeight;
    }
  }, [logs, activeTab]);

  const formatEth = (val: number) => val.toFixed(6);
  const formatUsd = (eth: number) => (eth * 2200).toFixed(2); // Approx ETH price

  return (
    <div className="grid grid-cols-1 lg:grid-cols-3 gap-6 mb-8">
      {/* Stats Panel */}
      <Card className="lg:col-span-1 bg-gray-900 border-gray-800 h-full">
        <CardHeader>
          <CardTitle className="flex items-center gap-2 text-white">
            <Wallet className="w-5 h-5 text-blue-400" />
            Portfolio Overview
          </CardTitle>
        </CardHeader>
        <CardContent className="space-y-6">
          <div className="space-y-2">
            <div className="flex justify-between items-baseline">
                <div className="text-sm text-gray-400">Total Balance</div>
                <div className="text-xs text-gray-500">Principal: {formatEth(stats.principal)} ETH</div>
            </div>
            <div className="text-3xl font-bold text-white font-mono">
              {formatEth(stats.currentBalance)} <span className="text-lg text-gray-500">ETH</span>
            </div>
            <div className="text-sm text-gray-500">≈ ${formatUsd(stats.currentBalance)}</div>
          </div>

          <div className="grid grid-cols-2 gap-4">
            <div className="bg-gray-800/50 p-3 rounded-xl">
              <div className="text-xs text-gray-400 mb-1">Net Profit</div>
              <div className={`font-mono font-bold ${stats.totalProfit >= 0 ? 'text-green-400' : 'text-red-400'}`}>
                {stats.totalProfit >= 0 ? '+' : ''}{formatEth(stats.totalProfit)}
              </div>
            </div>
            <div className="bg-gray-800/50 p-3 rounded-xl">
              <div className="text-xs text-gray-400 mb-1">Win Rate</div>
              <div className="text-white font-mono font-bold">
                {stats.tradesCount > 0 ? ((stats.successfulTrades / stats.tradesCount) * 100).toFixed(1) : 0}%
              </div>
            </div>
          </div>
          
          <div className="flex gap-2">
            <Dialog>
                <DialogTrigger asChild>
                    <Button variant="outline" className="flex-1 border-gray-700 hover:bg-gray-800 text-gray-300">
                        <ArrowDownLeft className="w-4 h-4 mr-2" /> Deposit
                    </Button>
                </DialogTrigger>
                <DialogContent className="bg-gray-900 border-gray-800 text-white">
                    <DialogHeader>
                        <DialogTitle>Deposit ETH</DialogTitle>
                        <DialogDescription>Add funds to your arbitrage bot wallet.</DialogDescription>
                    </DialogHeader>
                    <div className="py-4">
                        <div className="flex items-center gap-2 border border-gray-700 rounded-lg px-3 py-2">
                            <input 
                                type="number" 
                                placeholder="0.0" 
                                className="bg-transparent outline-none flex-1 text-white"
                                value={depositAmount}
                                onChange={(e) => setDepositAmount(e.target.value)}
                            />
                            <span className="text-gray-500 font-bold">ETH</span>
                        </div>
                    </div>
                    <DialogFooter>
                        <Button onClick={() => {
                            const val = parseFloat(depositAmount);
                            if (val > 0) {
                                onDeposit(val);
                                setDepositAmount("");
                            }
                        }} className="bg-blue-600 hover:bg-blue-700">Confirm Deposit</Button>
                    </DialogFooter>
                </DialogContent>
            </Dialog>

            <Dialog>
                <DialogTrigger asChild>
                    <Button variant="outline" className="flex-1 border-gray-700 hover:bg-gray-800 text-gray-300">
                        <ArrowUpRight className="w-4 h-4 mr-2" /> Withdraw
                    </Button>
                </DialogTrigger>
                <DialogContent className="bg-gray-900 border-gray-800 text-white">
                    <DialogHeader>
                        <DialogTitle>Withdraw ETH</DialogTitle>
                        <DialogDescription>Withdraw funds to your external wallet.</DialogDescription>
                    </DialogHeader>
                    <div className="py-4">
                         <div className="flex justify-between mb-2 text-xs text-gray-400">
                            <span>Available:</span>
                            <span>{formatEth(stats.currentBalance)} ETH</span>
                        </div>
                        <div className="flex items-center gap-2 border border-gray-700 rounded-lg px-3 py-2">
                            <input 
                                type="number" 
                                placeholder="0.0" 
                                className="bg-transparent outline-none flex-1 text-white"
                                value={withdrawAmount}
                                onChange={(e) => setWithdrawAmount(e.target.value)}
                                max={stats.currentBalance}
                            />
                            <span className="text-gray-500 font-bold">ETH</span>
                        </div>
                    </div>
                    <DialogFooter>
                         <Button onClick={() => {
                            const val = parseFloat(withdrawAmount);
                            if (val > 0) {
                                onWithdraw(val);
                                setWithdrawAmount("");
                            }
                        }} className="bg-blue-600 hover:bg-blue-700">Confirm Withdraw</Button>
                    </DialogFooter>
                </DialogContent>
            </Dialog>
          </div>

          <div className="pt-4 border-t border-gray-800">
             <div className="flex items-center justify-between mb-2">
                <span className="text-sm text-gray-400">Bot Status</span>
                <Badge variant={isRunning ? "default" : "destructive"} className={isRunning ? "bg-green-500/20 text-green-400 hover:bg-green-500/30" : "bg-red-500/20 text-red-400 hover:bg-red-500/30"}>
                  {isRunning ? "RUNNING" : "STOPPED"}
                </Badge>
             </div>
          </div>

          <div className="flex gap-3 pt-2">
            {!isRunning ? (
              <Button onClick={onStart} className="flex-1 bg-green-600 hover:bg-green-700 text-white">
                <Play className="w-4 h-4 mr-2" /> Start Bot
              </Button>
            ) : (
              <Button onClick={onStop} variant="destructive" className="flex-1">
                <Square className="w-4 h-4 mr-2" /> Stop Bot
              </Button>
            )}
          </div>
        </CardContent>
      </Card>

      {/* Terminal / Logs / Transactions Panel */}
      <Card className="lg:col-span-2 bg-black border-gray-800 h-[450px] flex flex-col overflow-hidden">
        <Tabs value={activeTab} onValueChange={setActiveTab} className="flex flex-col h-full">
            <div className="flex items-center justify-between px-4 py-2 border-b border-gray-900 bg-gray-900/50">
                <TabsList className="bg-gray-900/50 border border-gray-800">
                    <TabsTrigger value="logs" className="data-[state=active]:bg-gray-800 text-xs">
                        <Terminal className="w-3 h-3 mr-2" /> Live Logs
                    </TabsTrigger>
                    <TabsTrigger value="transactions" className="data-[state=active]:bg-gray-800 text-xs">
                        <History className="w-3 h-3 mr-2" /> Transactions
                    </TabsTrigger>
                </TabsList>
                <div className="flex gap-2">
                    <div className="w-3 h-3 rounded-full bg-red-500/20 border border-red-500/50"></div>
                    <div className="w-3 h-3 rounded-full bg-yellow-500/20 border border-yellow-500/50"></div>
                    <div className="w-3 h-3 rounded-full bg-green-500/20 border border-green-500/50"></div>
                </div>
            </div>
            
            <TabsContent value="logs" className="flex-1 p-0 m-0 overflow-hidden relative">
                <div 
                    ref={scrollRef}
                    className="absolute inset-0 p-4 overflow-y-auto space-y-1 scrollbar-thin scrollbar-thumb-gray-800 font-mono text-sm"
                >
                    {logs.length === 0 && (
                    <div className="text-gray-600 italic">System ready. Waiting for start command...</div>
                    )}
                    {logs.slice().reverse().map((log) => (
                    <div key={log.id} className="flex gap-3">
                        <span className="text-gray-600 shrink-0">[{new Date(log.timestamp).toLocaleTimeString()}]</span>
                        <span className={`${
                        log.type === 'error' ? 'text-red-400' :
                        log.type === 'success' ? 'text-green-400' :
                        log.type === 'trade' ? 'text-yellow-400' :
                        'text-blue-300'
                        }`}>
                        {log.type.toUpperCase()}
                        </span>
                        <span className="text-gray-300 break-all">{log.message}</span>
                    </div>
                    ))}
                    {isRunning && (
                    <div className="animate-pulse text-green-500/50">_</div>
                    )}
                </div>
            </TabsContent>

            <TabsContent value="transactions" className="flex-1 p-0 m-0 overflow-hidden relative">
                 <div className="absolute inset-0 overflow-y-auto scrollbar-thin scrollbar-thumb-gray-800">
                    {transactions.length === 0 ? (
                        <div className="p-8 text-center text-gray-500">No transactions yet.</div>
                    ) : (
                        <table className="w-full text-left text-sm">
                            <thead className="bg-gray-900/50 text-gray-400 sticky top-0 backdrop-blur-sm z-10">
                                <tr>
                                    <th className="px-4 py-3 font-medium">Time</th>
                                    <th className="px-4 py-3 font-medium">Type</th>
                                    <th className="px-4 py-3 font-medium">Description</th>
                                    <th className="px-4 py-3 font-medium text-right">Amount (ETH)</th>
                                    <th className="px-4 py-3 font-medium">Hash</th>
                                </tr>
                            </thead>
                            <tbody className="divide-y divide-gray-800/50">
                                {transactions.map((tx) => (
                                    <tr key={tx.id} className="hover:bg-gray-900/30 transition-colors">
                                        <td className="px-4 py-3 text-gray-500 whitespace-nowrap">
                                            {new Date(tx.timestamp).toLocaleTimeString()}
                                        </td>
                                        <td className="px-4 py-3">
                                            <Badge variant="outline" className={`
                                                ${tx.type === 'deposit' ? 'border-blue-500/50 text-blue-400' : 
                                                  tx.type === 'withdraw' ? 'border-orange-500/50 text-orange-400' :
                                                  tx.type === 'trade_profit' ? 'border-green-500/50 text-green-400' :
                                                  tx.type === 'gas' ? 'border-red-500/50 text-red-400' :
                                                  'border-gray-500/50 text-gray-400'}
                                            `}>
                                                {tx.type.replace('_', ' ').toUpperCase()}
                                            </Badge>
                                        </td>
                                        <td className="px-4 py-3 text-gray-300">{tx.description}</td>
                                        <td className={`px-4 py-3 text-right font-mono font-medium ${tx.amount >= 0 ? 'text-green-400' : 'text-red-400'}`}>
                                            {tx.amount > 0 ? '+' : ''}{tx.amount.toFixed(6)}
                                        </td>
                                        <td className="px-4 py-3 text-gray-600 font-mono text-xs">
                                            {tx.hash?.slice(0, 6)}...{tx.hash?.slice(-4)}
                                        </td>
                                    </tr>
                                ))}
                            </tbody>
                        </table>
                    )}
                 </div>
            </TabsContent>
        </Tabs>
      </Card>
    </div>
  );
}
