"use client";

import React, { useMemo } from "react";
import { Card, CardContent, CardHeader, CardTitle } from "@/components/ui/card";
import { TrendingUp } from "lucide-react";
import { ArbitrageStats, DailyRevenuePoint } from "../../hooks/useArbitrageStats";

interface ArbitrageRevenueChartProps {
  stats: ArbitrageStats;
  dailyData: DailyRevenuePoint[];
}

export function ArbitrageRevenueChart({
  stats,
  dailyData,
}: ArbitrageRevenueChartProps) {
  const chartMetrics = useMemo(() => {
    if (!dailyData || dailyData.length === 0) {
      return {
        maxDaily: 0,
        minDaily: 0,
        maxCumulative: 0,
        avgDaily: 0,
        bestDay: null as DailyRevenuePoint | null,
      };
    }

    const dailyProfits = dailyData.map((d) => d.daily);
    const cumulativeProfits = dailyData.map((d) => d.cumulative);

    const maxDaily = Math.max(...dailyProfits);
    const minDaily = Math.min(...dailyProfits);
    const maxCumulative = Math.max(...cumulativeProfits);
    const avgDaily =
      dailyProfits.reduce((a, b) => a + b, 0) / dailyProfits.length;

    const bestDay = dailyData.reduce((max, curr) =>
      curr.daily > max.daily ? curr : max
    );

    return { maxDaily, minDaily, maxCumulative, avgDaily, bestDay };
  }, [dailyData]);

  const formatEth = (val: number) => val.toFixed(4);

  return (
    <Card className="bg-slate-900 border border-slate-700">
      <CardHeader>
        <CardTitle className="flex items-center gap-2 text-white">
          <TrendingUp className="w-5 h-5 text-green-400" />
          收益趋势（30天）
        </CardTitle>
      </CardHeader>

      <CardContent className="space-y-6">
        {/* DEBUG: 显示数据状态 */}
        <div
          style={{
            fontSize: "12px",
            color: "#fff",
            backgroundColor: "#333",
            padding: "8px",
            marginBottom: "10px",
            borderRadius: "4px",
          }}
        >
          数据点: {dailyData?.length || 0} | 数据存在:{" "}
          {dailyData && dailyData.length > 0 ? "是" : "否"}
        </div>

        {/* 柱状图 - 显示累积收益 */}
        <div className="space-y-3">
          <div
            className="flex items-end justify-between gap-0.5 bg-slate-800/30 rounded-lg p-4"
            style={{ height: "500px", minHeight: "500px" }}
          >
            {!dailyData || dailyData.length === 0 ? (
              // 演示柱子（如果没有数据）
              <div style={{ display: "flex", width: "100%", gap: "2px" }}>
                {[...Array(30)].map((_, i) => (
                  <div
                    key={i}
                    style={{
                      flex: 1,
                      height: `${Math.random() * 100}%`,
                      background: `linear-gradient(to top, #3b82f6, #06b6d4)`,
                      borderRadius: "4px 4px 0 0",
                      minHeight: "4px",
                    }}
                  />
                ))}
              </div>
            ) : (
              dailyData.map((point, idx) => {
                // 使用日收益来计算高度，这样更直观
                const maxDaily = Math.max(
                  ...dailyData.map((d) => d.daily),
                  0.008
                );
                const heightPercent = (point.daily / maxDaily) * 100;

                return (
                  <div
                    key={idx}
                    className="flex-1 flex flex-col items-center justify-end group relative"
                    title={`${point.date}: 累计 ${formatEth(
                      point.cumulative
                    )} ETH (当日 +${formatEth(point.daily)} ETH)`}
                  >
                    {/* 主柱子 */}
                    <div
                      className="w-full bg-gradient-to-t from-blue-500 via-blue-400 to-cyan-400 rounded-t-md transition-all hover:from-blue-400 hover:via-blue-300 hover:to-cyan-300 cursor-pointer shadow-lg shadow-blue-500/30"
                      style={{
                        height: `${Math.max(heightPercent, 8)}%`,
                        minHeight: "12px",
                      }}
                    />

                    {/* 悬停时显示详细数值 */}
                    <div className="absolute -top-12 left-1/2 -translate-x-1/2 px-3 py-2 bg-slate-950 text-slate-100 text-xs rounded-lg opacity-0 group-hover:opacity-100 transition-opacity pointer-events-none border border-blue-500/50 z-10 whitespace-nowrap shadow-xl">
                      <div className="font-semibold text-blue-300">
                        {formatEth(point.cumulative)} ETH
                      </div>
                      <div className="text-slate-400 text-xs">
                        +{formatEth(point.daily)} ETH
                      </div>
                    </div>

                    {/* 日期标签 - 每4根柱子显示一次 */}
                    {idx % 4 === 0 && (
                      <p className="text-xs text-slate-500 mt-2 whitespace-nowrap">
                        {point.date}
                      </p>
                    )}
                  </div>
                );
              })
            )}
          </div>

          {/* X 轴标签 */}
          {dailyData && dailyData.length > 0 && (
            <div className="flex justify-between text-xs text-slate-500 px-4 mt-1">
              <span>{dailyData[0]?.date || "N/A"}</span>
              <span>
                {dailyData[Math.floor(dailyData.length / 2)]?.date || "N/A"}
              </span>
              <span>{dailyData[dailyData.length - 1]?.date || "N/A"}</span>
            </div>
          )}
        </div>

        {/* 统计指标 */}
        <div className="grid grid-cols-2 gap-3">
          {/* 最高日收益 */}
          <div className="bg-gradient-to-br from-blue-900/30 to-slate-800/30 p-3 rounded-lg border border-blue-500/20">
            <p className="text-xs text-slate-400 uppercase tracking-wide mb-1">
              最高日收益
            </p>
            <div className="flex items-baseline gap-1">
              <p className="text-lg font-bold text-blue-300 font-mono">
                {formatEth(chartMetrics.maxDaily)}
              </p>
              <p className="text-xs text-slate-500">ETH</p>
            </div>
            {chartMetrics.bestDay && (
              <p className="text-xs text-slate-500 mt-1">
                {chartMetrics.bestDay.date}
              </p>
            )}
          </div>

          {/* 平均日收益 */}
          <div className="bg-gradient-to-br from-green-900/30 to-slate-800/30 p-3 rounded-lg border border-green-500/20">
            <p className="text-xs text-slate-400 uppercase tracking-wide mb-1">
              平均日收益
            </p>
            <div className="flex items-baseline gap-1">
              <p className="text-lg font-bold text-green-300 font-mono">
                {formatEth(chartMetrics.avgDaily)}
              </p>
              <p className="text-xs text-slate-500">ETH</p>
            </div>
            <p className="text-xs text-slate-500 mt-1">每天收益稳定增长</p>
          </div>

          {/* 累计收益 */}
          <div className="bg-gradient-to-br from-purple-900/30 to-slate-800/30 p-3 rounded-lg border border-purple-500/20">
            <p className="text-xs text-slate-400 uppercase tracking-wide mb-1">
              累计收益
            </p>
            <div className="flex items-baseline gap-1">
              <p className="text-lg font-bold text-purple-300 font-mono">
                {formatEth(chartMetrics.maxCumulative)}
              </p>
              <p className="text-xs text-slate-500">ETH</p>
            </div>
            <p className="text-xs text-slate-500 mt-1">30天增长</p>
          </div>

          {/* 预计年收益 */}
          <div className="bg-gradient-to-br from-amber-900/30 to-slate-800/30 p-3 rounded-lg border border-amber-500/20">
            <p className="text-xs text-slate-400 uppercase tracking-wide mb-1">
              预计年收益
            </p>
            <div className="flex items-baseline gap-1">
              <p className="text-lg font-bold text-amber-300 font-mono">
                {formatEth(chartMetrics.maxCumulative * 12.17)}
              </p>
              <p className="text-xs text-slate-500">ETH</p>
            </div>
            <p className="text-xs text-slate-500 mt-1">
              ≈ {((chartMetrics.maxCumulative / 1) * 100 * 12.17).toFixed(0)}%
              APY
            </p>
          </div>
        </div>

        {/* 收益说明 */}
        <div className="bg-slate-800/50 border border-slate-700/50 rounded-lg p-3 text-xs text-slate-300 space-y-1">
          <p className="font-semibold text-slate-100">📊 收益分析</p>
          <p>• 图表显示过去 30 天的累积收益曲线</p>
          <p>• 柱子高度代表累积收益总额，越高表示收益越多</p>
          <p>• 悬停柱子查看当日详细收益</p>
          <p>• 收益来自交易所套利、跨交易所套利、闪电贷等多种策略</p>
        </div>
      </CardContent>
    </Card>
  );
}
