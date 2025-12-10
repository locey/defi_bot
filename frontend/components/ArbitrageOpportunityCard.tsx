"use client";

import { Button } from "@/components/ui/button";
import { Card, CardContent, CardHeader, CardTitle, CardFooter } from "@/components/ui/card";

export type Venue = "Uniswap" | "Curve" | "Pancake";

export interface ArbitrageOpportunity {
  symbol: string;
  basePrice: number;
  venuePrices: Record<Venue, number>;
  buyVenue: Venue;
  sellVenue: Venue;
  grossSpreadPct: number;
  netSpreadPct: number;
}

export function ArbitrageOpportunityCard({
  opportunity,
  onExecute,
  notional = 1000,
}: {
  opportunity: ArbitrageOpportunity;
  onExecute: (opp: ArbitrageOpportunity) => void;
  notional?: number;
}) {
  const roi = opportunity.netSpreadPct;
  const estProfit = (roi / 100) * notional;
  const riskLevel = roi >= 1.5 ? "HIGH" : roi >= 0.8 ? "MEDIUM" : "LOW";
  const riskColor = riskLevel === "HIGH" ? "bg-red-500/20 text-red-400" : riskLevel === "MEDIUM" ? "bg-yellow-500/20 text-yellow-400" : "bg-green-500/20 text-green-400";

  return (
    <Card className="bg-gray-900 border-gray-800 rounded-2xl">
      <CardHeader className="flex flex-row items-center justify-between">
        <CardTitle className="text-white text-xl">{opportunity.symbol}/USDT</CardTitle>
        <div className={`px-3 py-1 rounded-lg text-xs font-semibold ${riskColor}`}>RISK: {riskLevel}</div>
      </CardHeader>
      <CardContent>
        <div className="grid grid-cols-2 gap-6 text-sm">
          <div className="space-y-2 text-gray-400">
            <div className="flex justify-between">
              <span>Buy on:</span>
              <span className="text-white">{opportunity.buyVenue}</span>
            </div>
            <div className="flex justify-between">
              <span>Buy Price:</span>
              <span className="text-white">${opportunity.venuePrices[opportunity.buyVenue].toFixed(2)}</span>
            </div>
          </div>
          <div className="space-y-2 text-gray-400">
            <div className="flex justify-between">
              <span>Sell on:</span>
              <span className="text-white">{opportunity.sellVenue}</span>
            </div>
            <div className="flex justify-between">
              <span>Sell Price:</span>
              <span className="text-white">${opportunity.venuePrices[opportunity.sellVenue].toFixed(2)}</span>
            </div>
          </div>
        </div>

        <div className="mt-6 bg-gray-800/40 rounded-xl p-4 flex items-center justify-between">
          <div>
            <div className="text-xs text-gray-400">Est. Profit</div>
            <div className="text-green-400 text-xl font-bold">${estProfit.toFixed(2)}</div>
          </div>
          <div className="text-right">
            <div className="text-xs text-gray-400">ROI</div>
            <div className="text-green-400 text-xl font-bold">{roi.toFixed(2)}%</div>
          </div>
        </div>
      </CardContent>
      <CardFooter>
        <Button onClick={() => onExecute(opportunity)} className="w-full bg-gradient-to-r from-blue-500 to-cyan-500 text-white">
          Execute Trade
        </Button>
      </CardFooter>
    </Card>
  );
}

