import { useState, useEffect, useCallback, useRef } from 'react';
import { Venue } from '@/components/ArbitrageOpportunityCard';

export interface ArbitrageLog {
  id: string;
  timestamp: number;
  type: 'info' | 'success' | 'error' | 'trade';
  message: string;
  details?: {
    symbol: string;
    buyVenue: string;
    sellVenue: string;
    profit: number;
  };
}

export interface Transaction {
  id: string;
  timestamp: number;
  type: 'deposit' | 'withdraw' | 'trade_profit' | 'yield' | 'gas';
  amount: number;
  hash?: string;
  description: string;
  status: 'pending' | 'completed' | 'failed';
}

export interface BotStats {
  principal: number; // User invested amount
  currentBalance: number; // Total balance including profit
  totalProfit: number;
  tradesCount: number;
  successfulTrades: number;
  startTime: number | null;
}

export function useArbitrageBot(initialEth: number = 1.0) {
  const [isRunning, setIsRunning] = useState(false);
  const [stats, setStats] = useState<BotStats>({
    principal: initialEth,
    currentBalance: initialEth,
    totalProfit: 0,
    tradesCount: 0,
    successfulTrades: 0,
    startTime: null,
  });
  const [logs, setLogs] = useState<ArbitrageLog[]>([]);
  const [transactions, setTransactions] = useState<Transaction[]>([]);
  const logIdCounter = useRef(0);
  const txIdCounter = useRef(0);

  const addLog = useCallback((type: ArbitrageLog['type'], message: string, details?: ArbitrageLog['details']) => {
    const newLog: ArbitrageLog = {
      id: `log-${Date.now()}-${logIdCounter.current++}`,
      timestamp: Date.now(),
      type,
      message,
      details,
    };
    setLogs((prev) => [newLog, ...prev].slice(0, 100)); // Keep last 100 logs
  }, []);

  const addTransaction = useCallback((type: Transaction['type'], amount: number, description: string) => {
    const newTx: Transaction = {
      id: `tx-${Date.now()}-${txIdCounter.current++}`,
      timestamp: Date.now(),
      type,
      amount,
      description,
      status: 'completed',
      hash: '0x' + Math.random().toString(16).slice(2) + Math.random().toString(16).slice(2),
    };
    setTransactions((prev) => [newTx, ...prev].slice(0, 100));
  }, []);

  const deposit = useCallback((amount: number) => {
    if (amount <= 0) return;
    setStats((prev) => ({
      ...prev,
      principal: prev.principal + amount,
      currentBalance: prev.currentBalance + amount,
    }));
    addLog('info', `Deposited ${amount.toFixed(4)} ETH`);
    addTransaction('deposit', amount, 'User Deposit');
  }, [addLog, addTransaction]);

  const withdraw = useCallback((amount: number) => {
    if (amount <= 0) return;
    if (amount > stats.currentBalance) {
      addLog('error', 'Insufficient balance for withdrawal');
      return;
    }
    
    // Withdrawal logic: First reduce profit, then principal? Or just proportional?
    // Usually withdraw reduces balance. Principal tracking is tricky on withdrawal.
    // Let's assume we just reduce balance. Principal remains "Invested" historically? 
    // Or reduce principal if we withdraw capital?
    // Let's reduce principal if withdrawing more than profit?
    // Simple approach: Reduce currentBalance. Reduce principal if currentBalance falls below original principal?
    // Better: Just reduce currentBalance. principal tracks "Net Invested" (Deposits - Withdrawals).
    
    setStats((prev) => {
        const newPrincipal = Math.max(0, prev.principal - amount); // Simple logic
        return {
          ...prev,
          principal: newPrincipal,
          currentBalance: prev.currentBalance - amount,
        };
    });
    
    addLog('info', `Withdrew ${amount.toFixed(4)} ETH`);
    addTransaction('withdraw', -amount, 'User Withdrawal');
  }, [stats.currentBalance, addLog, addTransaction]);

  const startBot = useCallback(() => {
    setIsRunning(true);
    setStats((prev) => ({ ...prev, startTime: prev.startTime || Date.now() })); // Keep original start time if resuming
    addLog('info', 'Arbitrage bot started. Scanning for opportunities...');
  }, [addLog]);

  const stopBot = useCallback(() => {
    setIsRunning(false);
    addLog('info', 'Arbitrage bot stopped.');
  }, [addLog]);

  // Simulated execution function
  const executeTrade = useCallback((opportunity: {
    symbol: string;
    buyVenue: Venue;
    sellVenue: Venue;
    netSpreadPct: number;
    basePrice: number;
  }) => {
    // Simulate gas cost (in ETH, random between 0.0005 and 0.002)
    const gasCost = 0.0005 + Math.random() * 0.0015;
    
    // Simulate trade size (e.g., use 20% of available balance)
    const tradeSize = stats.currentBalance * 0.2;
    
    // Calculate gross profit
    const profitEth = (tradeSize * (opportunity.netSpreadPct / 100));
    const netProfitEth = profitEth - gasCost;

    if (netProfitEth > 0) {
      setStats((prev) => ({
        ...prev,
        currentBalance: prev.currentBalance + netProfitEth,
        totalProfit: prev.totalProfit + netProfitEth,
        tradesCount: prev.tradesCount + 1,
        successfulTrades: prev.successfulTrades + 1,
      }));
      addLog('success', `Executed arbitrage on ${opportunity.symbol}`, {
        symbol: opportunity.symbol,
        buyVenue: opportunity.buyVenue,
        sellVenue: opportunity.sellVenue,
        profit: netProfitEth,
      });
      addTransaction('trade_profit', netProfitEth, `Arbitrage Profit: ${opportunity.symbol} (${opportunity.buyVenue} -> ${opportunity.sellVenue})`);
    } else {
       setStats((prev) => ({
        ...prev,
        currentBalance: prev.currentBalance - gasCost, // Failed trade pays gas
        tradesCount: prev.tradesCount + 1,
      }));
      addLog('error', `Trade failed due to high gas or slippage on ${opportunity.symbol}. Gas: ${gasCost.toFixed(5)} ETH`);
      addTransaction('gas', -gasCost, `Failed Trade Gas: ${opportunity.symbol}`);
    }
  }, [stats.currentBalance, addLog, addTransaction]);

  // Idle yield farming simulation (Aave)
  useEffect(() => {
    let interval: NodeJS.Timeout;
    if (isRunning) {
      interval = setInterval(() => {
        // Simulate 3% APY on idle funds (very small per second)
        // 3% per year / 365 / 24 / 60 / 60 approx 9.5e-8 per second
        // We update every 5 seconds
        const apy = 0.03;
        const seconds = 5;
        const yieldEarned = stats.currentBalance * (apy / (365 * 24 * 60 * 60)) * seconds;
        
        if (yieldEarned > 0) {
           setStats((prev) => ({
            ...prev,
            currentBalance: prev.currentBalance + yieldEarned,
            totalProfit: prev.totalProfit + yieldEarned,
          }));
          // Add invisible micro-transaction log or aggregate?
          // Let's add it periodically or just update state without spamming logs
          // For demo, maybe log every 10th yield? No, just update balance.
          // Or add a transaction for "Yield Harvest" every minute?
          // Let's keep it simple: just update balance.
        }
      }, 5000);
    }
    return () => clearInterval(interval);
  }, [isRunning, stats.currentBalance]);

  return {
    isRunning,
    stats,
    logs,
    transactions,
    startBot,
    stopBot,
    executeTrade,
    deposit,
    withdraw,
    addLog
  };
}
