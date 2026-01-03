"use client";

import { ArbitrageBotPage } from "../../components/arbitrage/ArbitrageBotPage";
import { OpportunityList } from "../../components/arbitrage/OpportunityList";

export default function ArbitragePage() {
  return (
    <main className="min-h-screen bg-gradient-to-b from-slate-950 via-slate-900 to-slate-950 text-white">
      <div className="max-w-6xl mx-auto px-4 sm:px-6 lg:px-8 pt-20 pb-12">
        <ArbitrageBotPage />
        
        {/* Real-time arbitrage opportunities */}
        <div className="mt-8">
          <OpportunityList />
        </div>
      </div>
    </main>
  );
}
