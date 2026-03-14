"use client";

import React, { useState, useEffect, useCallback } from "react";
import { Card } from "@/components/ui/card";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import apiClient, { Opportunity } from "@/lib/api/client";

interface OpportunityCardProps {
  opportunity: Opportunity;
}

function OpportunityCard({ opportunity }: OpportunityCardProps) {
  const [timeLeft, setTimeLeft] = useState<string>("");

  // Parse paths
  let swapPath: string[] = [];
  let dexPath: string[] = [];
  
  try {
    swapPath = JSON.parse(opportunity.swap_path || "[]");
    dexPath = JSON.parse(opportunity.dex_path || "[]");
  } catch {
    // Keep empty arrays
  }

  // CEX-DEX 用 USDT 精度 (6位), 其他用 ETH 精度 (18位)
  const isCexDex = opportunity.arbitrage_type === "cex_dex";
  const divisor = isCexDex ? 1e6 : 1e18;
  const unit = isCexDex ? "USDT" : "ETH";

  // Format amount
  const formatAmount = (amount: string) => {
    const num = parseFloat(amount) / divisor;
    if (isCexDex) {
      return `$${num.toFixed(4)}`;
    }
    if (num < 0.0001) return "<0.0001";
    return num.toFixed(6);
  };

  // Update countdown
  useEffect(() => {
    const updateTimeLeft = () => {
      const expires = new Date(opportunity.expires_at).getTime();
      const now = Date.now();
      const seconds = Math.max(0, Math.floor((expires - now) / 1000));
      
      if (seconds === 0) {
        setTimeLeft("已过期");
      } else if (seconds < 60) {
        setTimeLeft(`${seconds}s`);
      } else {
        const mins = Math.floor(seconds / 60);
        const secs = seconds % 60;
        setTimeLeft(`${mins}m ${secs}s`);
      }
    };

    updateTimeLeft();
    const interval = setInterval(updateTimeLeft, 1000);
    return () => clearInterval(interval);
  }, [opportunity.expires_at]);

  // Get type badge color
  const getTypeBadgeColor = (type: string) => {
    switch (type) {
      case "cross_dex":
        return "bg-blue-500/20 text-blue-400 border-blue-500/30";
      case "cex_dex":
        return "bg-purple-500/20 text-purple-400 border-purple-500/30";
      case "fee_tier":
        return "bg-orange-500/20 text-orange-400 border-orange-500/30";
      default:
        return "bg-slate-500/20 text-slate-400 border-slate-500/30";
    }
  };

  // Get type display name
  const getTypeName = (type: string) => {
    switch (type) {
      case "cross_dex":
        return "跨DEX";
      case "cex_dex":
        return "CEX-DEX";
      case "fee_tier":
        return "费率套利";
      case "triangular":
        return "三角套利";
      default:
        return type;
    }
  };

  const isExpired = timeLeft === "已过期";

  return (
    <Card 
      className={`p-4 border transition-all duration-200 ${
        isExpired 
          ? "bg-slate-900/50 border-slate-800 opacity-60" 
          : "bg-slate-800 border-slate-700 hover:border-green-500/50 hover:shadow-lg hover:shadow-green-500/10"
      }`}
    >
      <div className="flex justify-between items-start gap-4">
        {/* Left: Info */}
        <div className="flex-1 min-w-0">
          <div className="flex items-center gap-2 mb-2 flex-wrap">
            <Badge className="bg-green-500/20 text-green-400 border border-green-500/30">
              +{opportunity.profit_rate.toFixed(2)}%
            </Badge>
            <Badge variant="outline" className={getTypeBadgeColor(opportunity.arbitrage_type)}>
              {getTypeName(opportunity.arbitrage_type)}
            </Badge>
            <Badge variant="outline" className="text-slate-400 border-slate-600">
              Gas: {opportunity.gas_estimate.toLocaleString()}
            </Badge>
          </div>

          {/* Path display */}
          <div className="space-y-1">
            <p className="text-sm text-slate-300 truncate">
              <span className="text-slate-500">路径: </span>
              {swapPath.length > 0 
                ? swapPath.map((addr) => addr.slice(0, 6) + "..." + addr.slice(-4)).join(" → ")
                : "N/A"
              }
            </p>
            <p className="text-xs text-slate-500 truncate">
              <span className="text-slate-600">DEX: </span>
              {dexPath.length > 0 ? dexPath.join(" → ") : "N/A"}
            </p>
          </div>
        </div>

        {/* Right: Amounts */}
        <div className="text-right shrink-0">
          <p className={`text-lg font-bold ${isExpired ? "text-slate-500" : "text-green-400"}`}>
            +{formatAmount(opportunity.expected_profit)} {isCexDex ? "" : unit}
          </p>
          <p className="text-xs text-slate-500">
            投入: {formatAmount(opportunity.amount_in)} {isCexDex ? "" : unit}
          </p>
          <p className={`text-xs mt-1 ${isExpired ? "text-red-400" : "text-orange-400"}`}>
            ⏱️ {timeLeft}
          </p>
        </div>
      </div>
    </Card>
  );
}

export function OpportunityList() {
  const [opportunities, setOpportunities] = useState<Opportunity[]>([]);
  const [isLoading, setIsLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [lastUpdate, setLastUpdate] = useState<Date | null>(null);

  const fetchOpportunities = useCallback(async () => {
    try {
      setError(null);
      const data = await apiClient.getOpportunities({
        status: "all",
        limit: 20
      });
      setOpportunities(data || []);
      setLastUpdate(new Date());
    } catch (err) {
      const message = err instanceof Error ? err.message : "获取数据失败";
      setError(message);
      console.error("Failed to fetch opportunities:", err);
    } finally {
      setIsLoading(false);
    }
  }, []);

  // Initial load and auto refresh
  useEffect(() => {
    fetchOpportunities();
    
    // Refresh every 10 seconds
    const interval = setInterval(fetchOpportunities, 10000);
    return () => clearInterval(interval);
  }, [fetchOpportunities]);

  // Loading state
  if (isLoading) {
    return (
      <div className="space-y-4">
        <div className="flex justify-between items-center">
          <Skeleton className="h-7 w-36 bg-slate-700" />
          <Skeleton className="h-6 w-20 bg-slate-700" />
        </div>
        {[...Array(3)].map((_, i) => (
          <Skeleton key={i} className="h-24 w-full bg-slate-800" />
        ))}
      </div>
    );
  }

  // Error state
  if (error) {
    return (
      <Card className="p-6 bg-red-900/20 border-red-500/50">
        <div className="flex items-center gap-3 mb-4">
          <span className="text-2xl">⚠️</span>
          <div>
            <p className="text-red-400 font-medium">加载套利机会失败</p>
            <p className="text-sm text-red-400/70">{error}</p>
          </div>
        </div>
        <Button 
          onClick={fetchOpportunities} 
          variant="outline"
          className="border-red-500/50 text-red-400 hover:bg-red-500/10"
        >
          重试
        </Button>
      </Card>
    );
  }

  // Empty state
  if (opportunities.length === 0) {
    return (
      <Card className="p-8 bg-slate-800/50 border-slate-700 text-center">
        <div className="text-4xl mb-4">🔍</div>
        <p className="text-slate-300 font-medium mb-2">当前没有发现套利机会</p>
        <p className="text-sm text-slate-500">
          系统每 10 秒自动扫描链上价格差异
        </p>
        {lastUpdate && (
          <p className="text-xs text-slate-600 mt-4">
            上次更新: {lastUpdate.toLocaleTimeString()}
          </p>
        )}
      </Card>
    );
  }

  return (
    <div className="space-y-4">
      {/* Header */}
      <div className="flex justify-between items-center">
        <div className="flex items-center gap-3">
          <h3 className="text-lg font-semibold text-white">实时套利机会</h3>
          <div className="flex items-center gap-1.5">
            <span className="w-2 h-2 bg-green-500 rounded-full animate-pulse" />
            <span className="text-xs text-slate-500">实时更新</span>
          </div>
        </div>
        <div className="flex items-center gap-3">
          <Badge 
            variant="outline" 
            className="text-green-400 border-green-500/50 bg-green-500/10"
          >
            {opportunities.length} 个机会
          </Badge>
          <Button
            size="sm"
            variant="ghost"
            onClick={fetchOpportunities}
            className="text-slate-400 hover:text-white"
          >
            🔄 刷新
          </Button>
        </div>
      </div>

      {/* Opportunity list */}
      <div className="space-y-3">
        {opportunities.map((opp) => (
          <OpportunityCard key={opp.id} opportunity={opp} />
        ))}
      </div>

      {/* Footer */}
      {lastUpdate && (
        <p className="text-xs text-slate-600 text-center">
          上次更新: {lastUpdate.toLocaleTimeString()}
        </p>
      )}
    </div>
  );
}

export default OpportunityList;

