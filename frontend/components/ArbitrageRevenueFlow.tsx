"use client";

import React, { useState, useMemo } from "react";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Input } from "@/components/ui/input";
import {
  History,
  Search,
  Filter,
  ExternalLink,
  CheckCircle2,
  Clock,
  AlertCircle,
} from "lucide-react";
import { RevenueFlow } from "@/lib/hooks/useArbitrageStats";

interface ArbitrageRevenueFlowProps {
  flows: RevenueFlow[];
}

const protocolColors: Record<string, string> = {
  Aave: "bg-purple-500/10 text-purple-300 border-purple-500/20",
  Uniswap: "bg-pink-500/10 text-pink-300 border-pink-500/20",
  Curve: "bg-blue-500/10 text-blue-300 border-blue-500/20",
  Compound: "bg-cyan-500/10 text-cyan-300 border-cyan-500/20",
};

const strategyLabels: Record<string, string> = {
  交易所内套利: "DEX 内",
  跨交易所套利: "跨交易",
  闪电贷套利: "闪电贷",
  "LP 费用": "LP",
  借贷收益: "借贷",
};

const statusIcons: Record<string, React.ReactNode> = {
  success: <CheckCircle2 className="w-4 h-4 text-green-400" />,
  pending: <Clock className="w-4 h-4 text-yellow-400" />,
  failed: <AlertCircle className="w-4 h-4 text-red-400" />,
};

const statusLabels: Record<string, string> = {
  success: "成功",
  pending: "处理中",
  failed: "失败",
};

export function ArbitrageRevenueFlow({ flows }: ArbitrageRevenueFlowProps) {
  const [searchTerm, setSearchTerm] = useState("");
  const [filterProtocol, setFilterProtocol] = useState<string>("all");
  const [filterStatus, setFilterStatus] = useState<string>("all");

  const filteredFlows = useMemo(() => {
    return flows.filter((flow) => {
      const matchesSearch =
        flow.id.toLowerCase().includes(searchTerm.toLowerCase()) ||
        flow.protocol.toLowerCase().includes(searchTerm.toLowerCase());
      const matchesProtocol =
        filterProtocol === "all" || flow.protocol === filterProtocol;
      const matchesStatus =
        filterStatus === "all" || flow.status === filterStatus;
      return matchesSearch && matchesProtocol && matchesStatus;
    });
  }, [flows, searchTerm, filterProtocol, filterStatus]);

  const protocols = Array.from(new Set(flows.map((f) => f.protocol)));
  const statuses = Array.from(new Set(flows.map((f) => f.status)));

  const totalProfit = filteredFlows
    .filter((f) => f.status === "success")
    .reduce((sum, f) => sum + f.profit, 0);

  const formatEth = (val: number) => val.toFixed(6);
  const formatTime = (timestamp: number) => {
    const date = new Date(timestamp);
    return date.toLocaleTimeString("zh-CN", {
      hour: "2-digit",
      minute: "2-digit",
      second: "2-digit",
    });
  };

  return (
    <Card className="bg-slate-900 border border-slate-700">
      <CardHeader className="pb-4">
        <div className="flex items-center justify-between mb-4">
          <CardTitle className="flex items-center gap-2 text-white">
            <History className="w-5 h-5 text-blue-400" />
            收益流水
          </CardTitle>
          <div className="text-sm text-slate-400">
            总收益:{" "}
            <span className="text-green-400 font-mono font-bold">
              {formatEth(totalProfit)} ETH
            </span>
          </div>
        </div>

        {/* 搜索和筛选 */}
        <div className="space-y-3">
          <div className="relative">
            <Search className="absolute left-3 top-1/2 -translate-y-1/2 w-4 h-4 text-slate-500" />
            <Input
              placeholder="搜索交易ID或协议..."
              className="pl-10 bg-slate-800 border-slate-700 text-slate-100 placeholder-slate-500"
              value={searchTerm}
              onChange={(e) => setSearchTerm(e.target.value)}
            />
          </div>

          <div className="flex gap-2 flex-wrap">
            {/* 协议筛选 */}
            <div className="flex gap-1 items-center">
              <Filter className="w-4 h-4 text-slate-500" />
              <select
                value={filterProtocol}
                onChange={(e) => setFilterProtocol(e.target.value)}
                className="text-xs bg-slate-800 border border-slate-700 text-slate-300 rounded px-2 py-1 cursor-pointer"
              >
                <option value="all">所有协议</option>
                {protocols.map((p) => (
                  <option key={p} value={p}>
                    {p}
                  </option>
                ))}
              </select>
            </div>

            {/* 状态筛选 */}
            <div className="flex gap-1 items-center">
              <select
                value={filterStatus}
                onChange={(e) => setFilterStatus(e.target.value)}
                className="text-xs bg-slate-800 border border-slate-700 text-slate-300 rounded px-2 py-1 cursor-pointer"
              >
                <option value="all">所有状态</option>
                {statuses.map((s) => (
                  <option key={s} value={s}>
                    {statusLabels[s as keyof typeof statusLabels]}
                  </option>
                ))}
              </select>
            </div>
          </div>
        </div>
      </CardHeader>

      <CardContent>
        <div className="overflow-x-auto">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-slate-700">
                <th className="text-left py-3 px-3 text-slate-400 font-medium">
                  时间
                </th>
                <th className="text-left py-3 px-3 text-slate-400 font-medium">
                  协议
                </th>
                <th className="text-left py-3 px-3 text-slate-400 font-medium">
                  策略
                </th>
                <th className="text-left py-3 px-3 text-slate-400 font-medium">
                  收益
                </th>
                <th className="text-left py-3 px-3 text-slate-400 font-medium">
                  收益率
                </th>
                <th className="text-left py-3 px-3 text-slate-400 font-medium">
                  状态
                </th>
                <th className="text-left py-3 px-3 text-slate-400 font-medium">
                  交易
                </th>
              </tr>
            </thead>
            <tbody>
              {filteredFlows.length === 0 ? (
                <tr>
                  <td colSpan={7} className="text-center py-8 text-slate-400">
                    暂无收益流水
                  </td>
                </tr>
              ) : (
                filteredFlows.map((flow) => (
                  <tr
                    key={flow.id}
                    className="border-b border-slate-700/50 hover:bg-slate-800/50 transition-colors"
                  >
                    <td className="py-3 px-3 text-slate-300">
                      <div className="flex flex-col">
                        <span className="font-mono text-xs">{flow.date}</span>
                        <span className="text-slate-500 text-xs">
                          {formatTime(flow.timestamp)}
                        </span>
                      </div>
                    </td>
                    <td className="py-3 px-3">
                      <Badge
                        variant="outline"
                        className={`${
                          protocolColors[flow.protocol] ||
                          "bg-slate-800 text-slate-300 border-slate-700"
                        }`}
                      >
                        {flow.protocol}
                      </Badge>
                    </td>
                    <td className="py-3 px-3 text-slate-300">
                      <div className="flex items-center gap-1">
                        <Badge
                          variant="secondary"
                          className="bg-slate-700/50 text-slate-300"
                        >
                          {strategyLabels[flow.strategy] || flow.strategy}
                        </Badge>
                      </div>
                    </td>
                    <td className="py-3 px-3 font-mono text-green-400 font-semibold">
                      +{formatEth(flow.profit)} ETH
                    </td>
                    <td className="py-3 px-3">
                      <span className="text-green-400 font-mono text-sm">
                        +{flow.profitRate.toFixed(3)}%
                      </span>
                    </td>
                    <td className="py-3 px-3">
                      <div className="flex items-center gap-2">
                        {statusIcons[flow.status]}
                        <span className="text-xs text-slate-400">
                          {statusLabels[flow.status]}
                        </span>
                      </div>
                    </td>
                    <td className="py-3 px-3">
                      {flow.txHash && (
                        <a
                          href={`https://etherscan.io/tx/${flow.txHash}`}
                          target="_blank"
                          rel="noopener noreferrer"
                          className="text-blue-400 hover:text-blue-300 transition-colors"
                        >
                          <ExternalLink className="w-4 h-4" />
                        </a>
                      )}
                    </td>
                  </tr>
                ))
              )}
            </tbody>
          </table>
        </div>

        {/* 分页或加载更多 */}
        {filteredFlows.length > 0 && (
          <div className="mt-4 text-center text-xs text-slate-500">
            显示 {filteredFlows.length} / {flows.length} 条记录
          </div>
        )}
      </CardContent>
    </Card>
  );
}
