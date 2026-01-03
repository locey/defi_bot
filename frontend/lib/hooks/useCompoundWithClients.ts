import { useCallback, useMemo } from 'react';
import { Address, formatUnits, parseUnits, PublicClient, WalletClient, Chain, Hex } from 'viem';
import { useWallet } from 'yc-sdk-ui';
import { usePublicClient, useWalletClient } from 'yc-sdk-hooks';
import CompoundDeploymentInfo from '@/lib/abi/deployments-compound-adapter-sepolia.json';
import {
  useCompoundStore,
  CompoundOperationType,
  CompoundTransactionResult,
  CompoundPoolInfo,
} from '@/lib/stores/useCompoundStore';

interface TransactionResult {
  hash: string;
  blockNumber?: bigint;
  gasUsed?: bigint;
}

// ==================== 导出 Hook ====================
export const useCompoundWithClients = () => {
  // 获取 store 和客户端
  const store = useCompoundStore();
  const { isConnected, address } = useWallet();
  const { publicClient, chain } = usePublicClient();
  const { walletClient, getWalletClient } = useWalletClient();

  // 初始化合约（从部署文件）
  const initContracts = useCallback(() => {
    if (store.defiAggregatorAddress === null || store.compoundAdapterAddress === null) {
      console.log("🔧 使用 Sepolia 测试网部署信息初始化 Compound 合约:", {
        chainId: CompoundDeploymentInfo.chainId,
        defiAggregator: CompoundDeploymentInfo.contracts.DefiAggregator,
        compoundAdapter: CompoundDeploymentInfo.contracts.CompoundAdapter,
        cUsdtToken: CompoundDeploymentInfo.contracts.MockCToken_cUSDT,
        feeRateBps: CompoundDeploymentInfo.feeRateBps
      });
      store.initFromDeployment();
    }
  }, [store.defiAggregatorAddress, store.compoundAdapterAddress, store.initFromDeployment]);

  // 手动初始化合约地址
  const setContractAddresses = useCallback((defiAggregatorAddress: Address, compoundAdapterAddress: Address) => {
    store.initContracts(defiAggregatorAddress, compoundAdapterAddress);
  }, [store.initContracts]);

  // 包装读取方法
  const fetchPoolInfo = useCallback(async () => {
    if (!publicClient) {
      throw new Error('PublicClient 未初始化');
    }
    return store.fetchPoolInfo(publicClient as unknown as PublicClient & { getLogs: typeof publicClient.getLogs });
  }, [publicClient, store.fetchPoolInfo]);

  const fetchUserBalance = useCallback(async () => {
    if (!publicClient || !address) {
      throw new Error('PublicClient 未初始化或钱包未连接');
    }
    return store.fetchUserBalance(publicClient as unknown as PublicClient & { getLogs: typeof publicClient.getLogs }, address);
  }, [publicClient, store.fetchUserBalance, address]);

  const fetchUserUSDTBalance = useCallback(async () => {
    if (!publicClient || !address) {
      throw new Error('PublicClient 未初始化或钱包未连接');
    }
    return store.fetchUserUSDTBalance(publicClient as unknown as PublicClient & { getLogs: typeof publicClient.getLogs }, address);
  }, [publicClient, store.fetchUserUSDTBalance, address]);

  const fetchUserCUSDTBalance = useCallback(async () => {
    if (!publicClient || !address) {
      throw new Error('PublicClient 未初始化或钱包未连接');
    }
    return store.fetchUserCUSDTBalance(publicClient as unknown as PublicClient & { getLogs: typeof publicClient.getLogs }, address);
  }, [publicClient, store.fetchUserCUSDTBalance, address]);

  const fetchAllowances = useCallback(async () => {
    if (!publicClient || !address) {
      throw new Error('PublicClient 未初始化或钱包未连接');
    }
    return store.fetchAllowances(publicClient as unknown as PublicClient & { getLogs: typeof publicClient.getLogs }, address);
  }, [publicClient, store.fetchAllowances, address]);

  const fetchFeeRate = useCallback(async () => {
    if (!publicClient) {
      throw new Error('PublicClient 未初始化');
    }
    return store.fetchFeeRate(publicClient as unknown as PublicClient & { getLogs: typeof publicClient.getLogs });
  }, [publicClient, store.fetchFeeRate]);

  const fetchCurrentAPY = useCallback(async () => {
    if (!publicClient) {
      throw new Error('PublicClient 未初始化');
    }
    return store.fetchCurrentAPY(publicClient as unknown as PublicClient & { getLogs: typeof publicClient.getLogs });
  }, [publicClient, store.fetchCurrentAPY]);

  const fetchCurrentExchangeRate = useCallback(async () => {
    if (!publicClient) {
      throw new Error('PublicClient 未初始化');
    }
    return store.fetchCurrentExchangeRate(publicClient as unknown as PublicClient & { getLogs: typeof publicClient.getLogs });
  }, [publicClient, store.fetchCurrentExchangeRate]);

  // 包装写入方法
  const approveUSDT = useCallback(async (amount: string, userAddress?: Address) => {
    if (!isConnected || !address) {
      throw new Error('钱包未连接');
    }

    const wc = await getWalletClient();
    if (!wc) {
      throw new Error('WalletClient 未初始化');
    }

    const amountBigInt = parseUnits(amount, 6); // USDT 是 6 位小数

    // 自定义 gas 设置以提高成功率 (EIP-1559 兼容)
    const gasConfig: {
      gas?: bigint;
      maxFeePerGas?: bigint;
      maxPriorityFeePerGas?: bigint;
    } = {
      gas: 8000000n, // 增加到 8M gas limit (bigint)
      maxFeePerGas: 100000000000n, // 100 Gwei
      maxPriorityFeePerGas: 5000000000n, // 5 Gwei
      // 移除 gasPrice 以支持 EIP-1559
    };

    return store.approveUSDT(
      publicClient as unknown as PublicClient & { getLogs: typeof publicClient.getLogs },
      wc as unknown as WalletClient,
      chain,
      amountBigInt,
      address,
      userAddress || address,
      gasConfig
    );
  }, [isConnected, address, publicClient, chain, getWalletClient, store.approveUSDT]);

  const approveCUSDT = useCallback(async (amount: string) => {
    if (!isConnected || !address) {
      throw new Error('钱包未连接');
    }

    const wc = await getWalletClient();
    if (!wc) {
      throw new Error('WalletClient 未初始化');
    }

    const amountBigInt = parseUnits(amount, 6); // USDT 是 6 位小数

    // 自定义 gas 设置以提高成功率 (EIP-1559 兼容)
    const gasConfig: {
      gas?: bigint;
      maxFeePerGas?: bigint;
      maxPriorityFeePerGas?: bigint;
    } = {
      gas: 8000000n, // 增加到 8M gas limit (bigint)
      maxFeePerGas: 100000000000n, // 100 Gwei
      maxPriorityFeePerGas: 5000000000n, // 5 Gwei
      // 移除 gasPrice 以支持 EIP-1559
    };

    return store.approveCUSDT(
      publicClient as unknown as PublicClient & { getLogs: typeof publicClient.getLogs },
      wc as unknown as WalletClient,
      chain,
      amountBigInt,
      address,
      address, // userAddress is the same as account in this case
      gasConfig
    );
  }, [isConnected, address, publicClient, chain, getWalletClient, store.approveCUSDT]);

  const supplyUSDT = useCallback(async (amount: string): Promise<TransactionResult> => {
    if (!isConnected || !address) {
      throw new Error('钱包未连接');
    }

    const wc = await getWalletClient();
    if (!wc) {
      throw new Error('WalletClient 未初始化');
    }

    const amountBigInt = parseUnits(amount, 6); // USDT 是 6 位小数

    // 自定义 gas 设置以提高成功率 (EIP-1559 兼容)
    const gasConfig: {
      gas?: bigint;
      maxFeePerGas?: bigint;
      maxPriorityFeePerGas?: bigint;
    } = {
      gas: 8000000n, // 增加到 8M gas limit (bigint)
      maxFeePerGas: 100000000000n, // 100 Gwei
      maxPriorityFeePerGas: 5000000000n, // 5 Gwei
      // 移除 gasPrice 以支持 EIP-1559
    };

    const receipt = await store.supplyUSDT(
      publicClient as unknown as PublicClient & { getLogs: typeof publicClient.getLogs },
      wc as unknown as WalletClient,
      chain,
      amountBigInt,
      address,
      address, // userAddress is the same as account in this case
      gasConfig
    );

    return {
      hash: receipt.transactionHash,
      blockNumber: receipt.blockNumber,
      gasUsed: receipt.gasUsed,
    };
  }, [isConnected, address, publicClient, chain, getWalletClient, store.supplyUSDT]);

  const redeemUSDT = useCallback(async (amount: string): Promise<CompoundTransactionResult> => {
    if (!isConnected || !address) {
      throw new Error('钱包未连接');
    }

    const wc = await getWalletClient();
    if (!wc) {
      throw new Error('WalletClient 未初始化');
    }

    const amountBigInt = parseUnits(amount, 6); // USDT 是 6 位小数

    // 自定义 gas 设置以提高成功率 (EIP-1559 兼容)
    const gasConfig: {
      gas?: bigint;
      maxFeePerGas?: bigint;
      maxPriorityFeePerGas?: bigint;
    } = {
      gas: 8000000n, // 增加到 8M gas limit (bigint)
      maxFeePerGas: 100000000000n, // 100 Gwei
      maxPriorityFeePerGas: 5000000000n, // 5 Gwei
      // 移除 gasPrice 以支持 EIP-1559
    };

    const receipt = await store.redeemUSDT(
      publicClient as unknown as PublicClient & { getLogs: typeof publicClient.getLogs },
      wc as unknown as WalletClient,
      chain,
      amountBigInt,
      address,
      address, // userAddress is the same as account in this case
      gasConfig
    );

    return {
      success: true,
      outputAmounts: [amountBigInt],
      returnData: '0x' as Hex,
      message: 'Compound 提取成功',
      transactionHash: receipt.transactionHash,
      blockNumber: receipt.blockNumber,
      gasUsed: receipt.gasUsed,
    };
  }, [isConnected, address, publicClient, chain, getWalletClient, store.redeemUSDT]);

  const sellUSDT = useCallback(async (amount: string): Promise<CompoundTransactionResult> => {
    if (!isConnected || !address) {
      throw new Error('钱包未连接');
    }

    const wc = await getWalletClient();
    if (!wc) {
      throw new Error('WalletClient 未初始化');
    }

    const amountBigInt = parseUnits(amount, 6); // USDT 是 6 位小数

    // 自定义 gas 设置以提高成功率 (EIP-1559 兼容)
    const gasConfig: {
      gas?: bigint;
      maxFeePerGas?: bigint;
      maxPriorityFeePerGas?: bigint;
    } = {
      gas: 8000000n, // 增加到 8M gas limit (bigint)
      maxFeePerGas: 100000000000n, // 100 Gwei
      maxPriorityFeePerGas: 5000000000n, // 5 Gwei
      // 移除 gasPrice 以支持 EIP-1559
    };

    const receipt = await store.sellUSDT(
      publicClient as unknown as PublicClient & { getLogs: typeof publicClient.getLogs },
      wc as unknown as WalletClient,
      chain,
      amountBigInt,
      address,
      address, // userAddress is the same as account in this case
      gasConfig
    );

    return {
      success: true,
      outputAmounts: [amountBigInt],
      returnData: '0x' as Hex,
      message: 'Compound 卖出成功',
      transactionHash: receipt.transactionHash,
      blockNumber: receipt.blockNumber,
      gasUsed: receipt.gasUsed,
    };
  }, [isConnected, address, publicClient, chain, getWalletClient, store.sellUSDT]);

  // 初始化 Compound 交易功能
  const initializeCompoundTrading = useCallback(async () => {
    if (!isConnected || !address) {
      throw new Error('钱包未连接');
    }

    console.log('🔄 初始化 Compound 交易功能...');

    // 检查是否已初始化合约
    await initContracts();

    // 获取池信息
    await fetchPoolInfo();

    // 获取用户余额
    await fetchUserBalance();

    console.log('✅ Compound 交易功能初始化完成');
  }, [isConnected, address, initContracts, fetchPoolInfo, fetchUserBalance]);

  // 刷新用户余额
  const refreshUserBalance = useCallback(async () => {
    if (!isConnected || !address) {
      throw new Error('钱包未连接');
    }
    await fetchUserBalance();
  }, [isConnected, address, fetchUserBalance]);

  // 计算属性：格式化的余额信息
  const formattedBalances = useMemo(() => {
    if (!store.userBalance) {
      return {
        usdtBalance: '0',
        cUsdtBalance: '0',
        usdtAllowance: '0',
        cUsdtAllowance: '0',
        depositedAmount: '0',
        earnedInterest: '0',
      };
    }
    return {
      usdtBalance: formatUnits(store.userBalance.usdtBalance || 0n, 6),
      cUsdtBalance: formatUnits(store.userBalance.cUsdtBalance || 0n, 6),
      usdtAllowance: formatUnits(store.userBalance.usdtAllowance || 0n, 6),
      cUsdtAllowance: formatUnits(store.userBalance.cUsdtAllowance || 0n, 6),
      depositedAmount: formatUnits(store.userBalance.depositedAmount || 0n, 6),
      earnedInterest: formatUnits(store.userBalance.earnedInterest || 0n, 6),
    };
  }, [store.userBalance]);

  // 计算属性：格式化的池信息
  const poolInfo = useMemo(() => {
    if (!store.poolInfo) {
      return null;
    }
    return {
      ...store.poolInfo,
      feeRatePercent: `${store.poolInfo.feeRateBps / 100}%`,
    };
  }, [store.poolInfo]);

  // 检查是否需要授权（与 Aave 逻辑完全一致）
  const needsApproval = useMemo(() => {
    if (!store.userBalance) {
      return { usdt: true, cUsdt: true };
    }

    return {
      usdt: (store.userBalance.usdtAllowance || 0n) === BigInt(0),
      cUsdt: (store.userBalance.cUsdtAllowance || 0n) === BigInt(0),
    };
  }, [store.userBalance]);

  // 检查特定金额是否需要授权
  const checkApprovalForAmount = useCallback((amount: string, tokenType: 'usdt' | 'cUsdt'): boolean => {
    if (!store.userBalance || !store.poolInfo) {
      console.log('🔍 checkApprovalForAmount: 缺少数据', {
        hasUserBalance: !!store.userBalance,
        hasPoolInfo: !!store.poolInfo
      });
      return true;
    }

    const amountBigInt = parseUnits(amount, 6); // USDT 精度

    if (tokenType === 'usdt') {
      const allowance = store.userBalance.usdtAllowance || 0n;
        const needsApproval = allowance < amountBigInt;
    console.log('USDT 授权检查:', {
      amount: amountBigInt.toString(),
      allowance: allowance.toString(),
      needsApproval
    });
    return needsApproval;
    } else {
      // 对于 cUSDT，需要将 USDT 金额转换为 cUSDT 金额
      const exchangeRate = store.poolInfo.currentExchangeRate || 1n;
      const cUsdtAmount = (amountBigInt * 100n) / exchangeRate;
      const allowance = store.userBalance.cUsdtAllowance || 0n;
      const needsApproval = allowance < cUsdtAmount;
    console.log('cUSDT 授权检查:', {
      usdtAmount: amountBigInt.toString(),
      exchangeRate: exchangeRate.toString(),
      cUsdtAmount: cUsdtAmount.toString(),
      allowance: allowance.toString(),
      needsApproval
    });
    return needsApproval;
    }
  }, [store.userBalance, store.poolInfo]);

  // 获取最大可用余额
  const maxBalances = useMemo(() => {
    if (!store.userBalance || !store.poolInfo) {
      return {
        maxUSDTToSupply: '0',
        maxUSDTToWithdraw: '0',
      };
    }

    // cUSDT 转 USDT 需要通过汇率转换
    // cUSDT 精度是8位，USDT精度是6位
    const exchangeRate = store.poolInfo.currentExchangeRate || 1n;
    const cUsdtBalance = store.userBalance.cUsdtBalance || 0n;

    // cUSDT 金额 * 汇率 = USDT 金额 (需要考虑精度差异)
    const maxUSDTFromCUSDT = (cUsdtBalance * exchangeRate) / (10n ** 2n); // 8位精度转6位精度

    return {
      maxUSDTToSupply: formatUnits(store.userBalance.usdtBalance || 0n, 6), // 最大可存入的 USDT
      maxUSDTToWithdraw: formatUnits(maxUSDTFromCUSDT, 6), // 最大可提取的 USDT（通过汇率转换）
    };
  }, [store.userBalance, store.poolInfo]);

  // 清理错误状态
  const clearErrors = useCallback(() => {
    store.clearError();
  }, [store.clearError]);

  // 自动初始化合约
  if (store.defiAggregatorAddress === null || store.compoundAdapterAddress === null) {
    initContracts();
  }

  return {
    // 基础状态
    isConnected,
    address,
    isLoading: store.isLoading,
    isOperating: store.isOperating,
    error: store.error,

    // 合约信息
    defiAggregatorAddress: store.defiAggregatorAddress,
    compoundAdapterAddress: store.compoundAdapterAddress,
    poolInfo,

    // 用户余额信息
    userBalance: store.userBalance,
    formattedBalances,
    needsApproval,
    maxBalances,

    // 初始化方法
    initializeCompoundTrading,
    refreshUserBalance,

    // 读取方法
    fetchPoolInfo,
    fetchUserBalance,
    fetchUserUSDTBalance,
    fetchUserCUSDTBalance,
    fetchAllowances,
    fetchFeeRate,
    fetchCurrentAPY,
    fetchCurrentExchangeRate,

    // 写入方法
    approveUSDT,
    approveCUSDT,
    supplyUSDT,
    redeemUSDT,
    sellUSDT,

    // 辅助方法
    checkApprovalForAmount,

    // 状态管理
    clearErrors,
    reset: store.reset,
  };
};

// 导出 store hook 以便直接使用
export { useCompoundStore };