// lib/utils/contracts.ts
// Contract address utilities

import { DEFAULT_CONFIG } from "@/lib/contracts";

// 获取合约地址
export function getContractAddresses() {
  // 使用 Sepolia 测试网配置
  return {
    ORACLE_AGGREGATOR_ADDRESS: DEFAULT_CONFIG.contracts.oracleAggregator,
    USDT_ADDRESS: DEFAULT_CONFIG.contracts.usdt,
  };
}

