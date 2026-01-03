/**
 * Curve Hook with Clients
 *
 * 这个 Hook 将 Curve Store 与 Web3 客户端结合，
 * 自动处理客户端依赖关系，提供更简单的 API。
 * 基于 deployments-curve-adapter-sepolia.json 中的合约地址
 */

import { useCallback, useMemo, useEffect } from 'react';
import { Address, formatUnits, parseUnits, PublicClient, WalletClient, Chain } from 'viem';
import { useWallet } from 'yc-sdk-ui';
import { usePublicClient, useWalletClient } from 'yc-sdk-hooks';
import useCurveStore, {
  CurveOperationType,
  CurveTransactionResult,
  CurvePoolInfo,
  CurveUserBalanceInfo,
  CurveContractCallResult
} from '../stores/useCurveStore';
import CurveDeploymentInfo from '@/lib/abi/deployments-curve-adapter-sepolia.json';

// 类型别名，避免复杂类型推导问题
type SafePublicClient = PublicClient;
type SafeWalletClient = WalletClient;
type SafeChain = Chain;

export const useCurveWithClients = () => {
  // 获取 store 和客户端
  const store = useCurveStore();
  const { isConnected, address } = useWallet();
  const { publicClient, chain } = usePublicClient();
  const { walletClient, getWalletClient } = useWalletClient();

  // 初始化合约（从部署文件）
  const initContracts = useCallback(() => {
    if (store.defiAggregatorAddress === null || store.curveAdapterAddress === null) {
      console.log("🔧 使用 Sepolia 测试网部署信息初始化 Curve 合约:", {
        chainId: CurveDeploymentInfo.chainId,
        defiAggregator: CurveDeploymentInfo.contracts.DefiAggregator,
        curveAdapter: CurveDeploymentInfo.contracts.CurveAdapter,
        usdcToken: CurveDeploymentInfo.contracts.MockERC20_USDC,
        usdtToken: CurveDeploymentInfo.contracts.MockERC20_USDT,
        daiToken: CurveDeploymentInfo.contracts.MockERC20_DAI,
        curvePool: CurveDeploymentInfo.contracts.MockCurve,
        feeRateBps: CurveDeploymentInfo.feeRateBps
      });
      store.initContracts();
    }
  }, [store.defiAggregatorAddress, store.curveAdapterAddress, store.initContracts]);

  // 包装读取方法
  const fetchPoolInfo = useCallback(async () => {
    if (!publicClient) {
      throw new Error('PublicClient 未初始化');
    }
    return store.fetchPoolInfo(publicClient as unknown as PublicClient);
  }, [publicClient, store.fetchPoolInfo]);

  const fetchUserBalance = useCallback(async () => {
    if (!publicClient || !address) {
      throw new Error('PublicClient 未初始化或钱包未连接');
    }
    return store.fetchUserBalance(publicClient as unknown as PublicClient, address);
  }, [publicClient, store.fetchUserBalance, address]);

  const fetchAllowances = useCallback(async () => {
    if (!publicClient || !address) {
      throw new Error('PublicClient 未初始化或钱包未连接');
    }
    return store.fetchAllowances(publicClient as unknown as PublicClient, address);
  }, [publicClient, store.fetchAllowances, address]);

  const previewAddLiquidity = useCallback(async (amounts: [string, string, string]) => {
    if (!publicClient) {
      throw new Error('PublicClient 未初始化');
    }

    // 转换为 bigint
    const bigintAmounts: [bigint, bigint, bigint] = [
      parseUnits(amounts[0], 6),   // USDC 6位小数
      parseUnits(amounts[1], 6),   // USDT 6位小数
      parseUnits(amounts[2], 18),  // DAI 18位小数
    ];

    return store.previewAddLiquidity(publicClient as unknown as PublicClient, bigintAmounts);
  }, [publicClient, store.previewAddLiquidity]);

  const previewRemoveLiquidity = useCallback(async (lpAmount: string) => {
    if (!publicClient) {
      throw new Error('PublicClient 未初始化');
    }

    const bigintAmount = parseUnits(lpAmount, 18); // LP Token 18位小数
    return store.previewRemoveLiquidity(publicClient as unknown as PublicClient, bigintAmount);
  }, [publicClient, store.previewRemoveLiquidity]);

  // 包装授权方法
  const approveUSDC = useCallback(async (amount: string) => {
    if (!isConnected || !address) {
      throw new Error('请先连接钱包');
    }

    if (!publicClient) {
      throw new Error('PublicClient 未初始化');
    }

    if (!chain) {
      throw new Error('Chain 未初始化');
    }

    const wc = await getWalletClient();
    if (!wc) {
      throw new Error('WalletClient 未初始化');
    }

    const amountBigInt = parseUnits(amount, 6); // USDC 是 6 位小数

    // 自定义 gas 设置以提高成功率
    const gasConfig = {
      gas: 8000000n, // 增加到 8M gas limit
      maxFeePerGas: 100000000000n, // 100 Gwei
      maxPriorityFeePerGas: 5000000000n, // 5 Gwei
    };

    return store.approveUSDC(
      publicClient as unknown as PublicClient,
      wc as unknown as WalletClient,
      chain,
      address,
      amountBigInt
    );
  }, [isConnected, address, publicClient, chain, getWalletClient, store.approveUSDC]);

  const approveUSDT = useCallback(async (amount: string) => {
    if (!isConnected || !address) {
      throw new Error('请先连接钱包');
    }

    if (!publicClient) {
      throw new Error('PublicClient 未初始化');
    }

    if (!chain) {
      throw new Error('Chain 未初始化');
    }

    const wc = await getWalletClient();
    if (!wc) {
      throw new Error('WalletClient 未初始化');
    }

    const amountBigInt = parseUnits(amount, 6); // USDT 是 6 位小数

    // 自定义 gas 设置以提高成功率
    const gasConfig = {
      gas: 8000000n, // 增加到 8M gas limit
      maxFeePerGas: 100000000000n, // 100 Gwei
      maxPriorityFeePerGas: 5000000000n, // 5 Gwei
    };

    return store.approveUSDT(
      publicClient as unknown as PublicClient,
      wc as unknown as WalletClient,
      chain,
      address,
      amountBigInt
    );
  }, [isConnected, address, publicClient, chain, getWalletClient, store.approveUSDT]);

  const approveDAI = useCallback(async (amount: string) => {
    if (!isConnected || !address) {
      throw new Error('请先连接钱包');
    }

    if (!publicClient) {
      throw new Error('PublicClient 未初始化');
    }

    if (!chain) {
      throw new Error('Chain 未初始化');
    }

    const wc = await getWalletClient();
    if (!wc) {
      throw new Error('WalletClient 未初始化');
    }

    const amountBigInt = parseUnits(amount, 18); // DAI 是 18 位小数

    // 自定义 gas 设置以提高成功率
    const gasConfig = {
      gas: 8000000n, // 增加到 8M gas limit
      maxFeePerGas: 100000000000n, // 100 Gwei
      maxPriorityFeePerGas: 5000000000n, // 5 Gwei
    };

    return store.approveDAI(
      publicClient as unknown as PublicClient,
      wc as unknown as WalletClient,
      chain,
      address,
      amountBigInt
    );
  }, [isConnected, address, publicClient, chain, getWalletClient, store.approveDAI]);

  const approveLPToken = useCallback(async (amount: string) => {
    if (!isConnected || !address) {
      throw new Error('请先连接钱包');
    }

    if (!publicClient) {
      throw new Error('PublicClient 未初始化');
    }

    if (!chain) {
      throw new Error('Chain 未初始化');
    }

    const wc = await getWalletClient();
    if (!wc) {
      throw new Error('WalletClient 未初始化');
    }

    const amountBigInt = parseUnits(amount, 18); // LP Token 是 18 位小数

    // 自定义 gas 设置以提高成功率
    const gasConfig = {
      gas: 8000000n, // 增加到 8M gas limit
      maxFeePerGas: 100000000000n, // 100 Gwei
      maxPriorityFeePerGas: 5000000000n, // 5 Gwei
    };

    return store.approveLPToken(
      publicClient as unknown as PublicClient,
      wc as unknown as WalletClient,
      chain,
      address,
      amountBigInt
    );
  }, [isConnected, address, publicClient, chain, getWalletClient, store.approveLPToken]);

  // 包装交易方法
  const addLiquidity = useCallback(async (params: {
    amounts: [string, string, string]; // [USDC, USDT, DAI]
    recipient?: Address;
    deadline?: number;
  }) => {
    if (!isConnected || !address) {
      throw new Error('请先连接钱包');
    }

    if (!publicClient) {
      throw new Error('PublicClient 未初始化');
    }

    if (!chain) {
      throw new Error('Chain 未初始化');
    }

    const wc = await getWalletClient();
    if (!wc) {
      throw new Error('WalletClient 未初始化');
    }

    // 自定义 gas 设置以提高成功率
    const gasConfig = {
      gas: 8000000n, // 增加到 8M gas limit
      maxFeePerGas: 100000000000n, // 100 Gwei
      maxPriorityFeePerGas: 5000000000n, // 5 Gwei
    };

    return store.addLiquidity(
      publicClient as unknown as PublicClient,
      wc as unknown as WalletClient,
      chain,
      address,
      {
        ...params,
        recipient: params.recipient || address,
      }
    );
  }, [isConnected, address, publicClient, chain, getWalletClient, store.addLiquidity]);

  const removeLiquidity = useCallback(async (params: {
    lpAmount: string;
    recipient?: Address;
    deadline?: number;
  }) => {
    if (!isConnected || !address) {
      throw new Error('请先连接钱包');
    }

    if (!publicClient) {
      throw new Error('PublicClient 未初始化');
    }

    if (!chain) {
      throw new Error('Chain 未初始化');
    }

    const wc = await getWalletClient();
    if (!wc) {
      throw new Error('WalletClient 未初始化');
    }

    // 自定义 gas 设置以提高成功率
    const gasConfig = {
      gas: 8000000n, // 增加到 8M gas limit
      maxFeePerGas: 100000000000n, // 100 Gwei
      maxPriorityFeePerGas: 5000000000n, // 5 Gwei
    };

    return store.removeLiquidity(
      publicClient as unknown as PublicClient,
      wc as unknown as WalletClient,
      chain,
      address,
      {
        ...params,
        recipient: params.recipient || address,
      }
    );
  }, [isConnected, address, publicClient, chain, getWalletClient, store.removeLiquidity]);

  // 初始化 Curve 交易功能
  const initializeCurveTrading = useCallback(async () => {
    try {
      console.log('🚀 初始化 Curve 交易功能...');

      // 初始化合约地址
      initContracts();

      // 获取池信息
      await fetchPoolInfo();

      // 如果用户已连接钱包，获取用户余额信息
      if (isConnected && address) {
        await fetchUserBalance();
        await fetchAllowances();
      }

      console.log('✅ Curve 交易功能初始化完成');
    } catch (error) {
      const errorMsg = error instanceof Error ? error.message : '初始化失败';
      store.setError(errorMsg);
      console.error('❌ Curve 交易功能初始化失败:', errorMsg);
      throw error;
    }
  }, [initContracts, fetchPoolInfo, fetchUserBalance, fetchAllowances, isConnected, address, store.setError]);

  // 刷新用户信息
  const refreshUserInfo = useCallback(async () => {
    if (!isConnected || !address) {
      throw new Error('钱包未连接');
    }

    try {
      console.log('🔄 刷新用户信息...');
      await Promise.all([
        fetchUserBalance(),
        fetchAllowances()
      ]);
      console.log('✅ 用户信息刷新完成');
    } catch (error) {
      const errorMsg = error instanceof Error ? error.message : '刷新用户信息失败';
      store.setError(errorMsg);
      console.error('❌ 刷新用户信息失败:', errorMsg);
      throw error;
    }
  }, [isConnected, address, fetchUserBalance, fetchAllowances, store.setError]);

  // 计算属性：格式化的余额信息
  const formattedBalances = useMemo(() => {
    if (!store.userBalance) {
      return {
        usdcBalance: '0',
        usdtBalance: '0',
        daiBalance: '0',
        lpTokenBalance: '0',
        usdcAllowance: '0',
        usdtAllowance: '0',
        daiAllowance: '0',
        lpTokenAllowance: '0',
        address: address || '未连接',
      };
    }

    return {
      usdcBalance: formatUnits(store.userBalance.usdcBalance, 6),
      usdtBalance: formatUnits(store.userBalance.usdtBalance, 6),
      daiBalance: formatUnits(store.userBalance.daiBalance, 18),
      lpTokenBalance: formatUnits(store.userBalance.lpTokenBalance, 18),
      usdcAllowance: formatUnits(store.userBalance.usdcAllowance, 6),
      usdtAllowance: formatUnits(store.userBalance.usdtAllowance, 6),
      daiAllowance: formatUnits(store.userBalance.daiAllowance, 18),
      lpTokenAllowance: formatUnits(store.userBalance.lpTokenAllowance, 18),
      address: address || '未连接',
    };
  }, [store.userBalance, address]);

  // 计算属性：格式化的池信息
  const formattedPoolInfo = useMemo(() => {
    if (!store.poolInfo) {
      return null;
    }

    return {
      ...store.poolInfo,
      feeRatePercent: `${store.poolInfo.feeRateBps / 100}%`,
      supportedOperationsFormatted: store.poolInfo.supportedOperations.map(op =>
        op === CurveOperationType.ADD_LIQUIDITY ? '添加流动性' :
        op === CurveOperationType.REMOVE_LIQUIDITY ? '移除流动性' : '未知操作'
      ),
    };
  }, [store.poolInfo]);

  // 检查是否需要授权
  const needsApproval = useMemo(() => {
    if (!store.userBalance) {
      return { usdc: true, usdt: true, dai: true, lpToken: true };
    }

    return {
      usdc: store.userBalance.usdcAllowance === BigInt(0),
      usdt: store.userBalance.usdtAllowance === BigInt(0),
      dai: store.userBalance.daiAllowance === BigInt(0),
      lpToken: store.userBalance.lpTokenAllowance === BigInt(0),
    };
  }, [store.userBalance]);

  // 获取最大可用余额
  const maxBalances = useMemo(() => {
    if (!store.userBalance) {
      return {
        maxUSDCToSupply: '0',
        maxUSDTToSupply: '0',
        maxDAIToSupply: '0',
        maxLPToRemove: '0',
      };
    }

    return {
      maxUSDCToSupply: formatUnits(store.userBalance.usdcBalance, 6),
      maxUSDTToSupply: formatUnits(store.userBalance.usdtBalance, 6),
      maxDAIToSupply: formatUnits(store.userBalance.daiBalance, 18),
      maxLPToRemove: formatUnits(store.userBalance.lpTokenBalance, 18),
    };
  }, [store.userBalance]);

  // 自动初始化合约
  if (store.defiAggregatorAddress === null || store.curveAdapterAddress === null) {
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
    curveAdapterAddress: store.curveAdapterAddress,
    poolInfo: formattedPoolInfo,

    // 用户余额信息
    userBalance: store.userBalance,
    formattedBalances,
    needsApproval,
    maxBalances,

    // 初始化方法
    initializeCurveTrading,
    initContracts,

    // 读取方法
    fetchPoolInfo,
    fetchUserBalance,
    fetchAllowances,
    previewAddLiquidity,
    previewRemoveLiquidity,
    refreshUserInfo,

    // 授权方法
    approveUSDC,
    approveUSDT,
    approveDAI,
    approveLPToken,

    // 交易方法
    addLiquidity,
    removeLiquidity,

    // 辅助方法
    setLoading: store.setLoading,
    setOperating: store.setOperating,
    setError: store.setError,
    clearErrors: store.clearError,
    reset: store.reset,
  };
};

export default useCurveWithClients;