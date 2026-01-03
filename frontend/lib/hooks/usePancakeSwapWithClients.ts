/**
 * PancakeSwap Hook with Clients
 *
 * 这个 Hook 将 PancakeSwap Store 与 Web3 客户端结合，
 * 自动处理客户端依赖关系，提供完整的 PancakeSwap 功能。
 */

import { useCallback, useMemo, useEffect } from 'react';
import { Address, formatUnits, parseUnits, PublicClient, WalletClient, Chain } from 'viem';
import { useWallet } from 'yc-sdk-ui';
import { usePublicClient, useWalletClient } from 'yc-sdk-hooks';
import usePancakeSwapStore, {
  PancakeSwapOperationType,
  PancakeSwapTransactionResult,
  PancakeSwapUserBalanceInfo,
  PancakeSwapContractCallResult,
  PancakeSwapExchangeRateInfo
} from '../stores/usePancakeSwapStore';
import PancakeDeploymentInfo from '@/lib/abi/deployments-pancake-adapter-sepolia.json';

// 导入 ABI 文件
import PancakeAdapterABI from '@/lib/abi/PancakeAdapter.json';
import DefiAggregatorABI from '@/lib/abi/DefiAggregator.json';
import MockERC20ABI from '@/lib/abi/MockERC20.json';
import MockPancakeRouterABI from '@/lib/abi/MockPancakeRouter.json';

// 导入 USDT 地址配置，与其他模块保持一致
import { getContractAddresses } from "@/lib/utils/contracts";
const { USDT_ADDRESS } = getContractAddresses() as { USDT_ADDRESS: Address };

// 类型化 ABI
const typedPancakeAdapterABI = PancakeAdapterABI as any;
const typedDefiAggregatorABI = DefiAggregatorABI as any;
const typedMockERC20ABI = MockERC20ABI as any;
const typedMockPancakeRouterABI = MockPancakeRouterABI as any;

// 代币精度配置
const TOKEN_DECIMALS = {
  USDT: 6,      // USDT 使用 6 位小数
  CAKE: 18,     // CAKE 使用 18 位小数
} as const;

// 部署地址
const DEPLOYMENT_ADDRESSES = {
  defiAggregator: PancakeDeploymentInfo.contracts.DefiAggregator as Address,
  pancakeAdapter: PancakeDeploymentInfo.contracts.PancakeAdapter as Address,
  usdtToken: PancakeDeploymentInfo.contracts.MockERC20_USDT as Address,
  cakeToken: PancakeDeploymentInfo.contracts.MockCakeToken as Address,
  router: PancakeDeploymentInfo.contracts.MockPancakeRouter as Address,
};

// 常量配置
const PANCAKE_CONSTANTS = {
  DEFAULT_SLIPPAGE_BPS: 100,    // 1%
  DEFAULT_DEADLINE_OFFSET: 3600, // 1小时
} as const;

export const usePancakeSwapWithClients = () => {
  // 获取 store 和客户端
  const store = usePancakeSwapStore();
  const { isConnected, address } = useWallet();
  const { publicClient, chain } = usePublicClient();
  const { walletClient, getWalletClient } = useWalletClient();

  // 初始化合约 - 优化依赖，避免 store 变化导致的重新创建
  const initContracts = useCallback(() => {
    if (store.defiAggregatorAddress === null || store.pancakeAdapterAddress === null) {
      console.log("🔧 使用 Sepolia 测试网部署信息初始化 PancakeSwap 合约");
      store.initContracts();
    }
  }, [store.initContracts]);

  // 获取用户余额（包含客户端） - 直接使用 store
  const fetchUserBalance = useCallback(async () => {
    if (!publicClient || !address) {
      throw new Error('PublicClient 未初始化或钱包未连接');
    }
    await store.fetchUserBalance(publicClient as unknown as PublicClient & { getLogs: typeof publicClient.getLogs }, address);
  }, [publicClient, address]);

  // 获取授权信息（包含客户端） - 直接使用 store
  const fetchAllowances = useCallback(async () => {
    if (!publicClient || !address) {
      throw new Error('PublicClient 未初始化或钱包未连接');
    }
    await store.fetchAllowances(publicClient as unknown as PublicClient & { getLogs: typeof publicClient.getLogs }, address);
  }, [publicClient, address]);

  // 获取汇率信息（包含客户端） - 直接使用 store
  const fetchExchangeRate = useCallback(async (tokenIn: Address, tokenOut: Address) => {
    if (!publicClient) {
      throw new Error('PublicClient 未初始化');
    }
    await store.fetchExchangeRate(publicClient as unknown as PublicClient & { getLogs: typeof publicClient.getLogs }, tokenIn, tokenOut);
  }, [publicClient]);

  // 预估交换
  const estimateSwap = useCallback(async (
    amountIn: string,
    tokenIn: Address,
    tokenOut: Address,
    operationType: PancakeSwapOperationType
  ) => {
    if (!publicClient) {
      throw new Error('PublicClient 未初始化');
    }

    const { pancakeAdapterAddress } = store;
    if (!pancakeAdapterAddress) {
      throw new Error('合约地址未初始化');
    }

    try {
      const decimalsIn = tokenIn === DEPLOYMENT_ADDRESSES.usdtToken ? TOKEN_DECIMALS.USDT : TOKEN_DECIMALS.CAKE;
      const amountInBigInt = parseUnits(amountIn, decimalsIn);

      console.log('🔍 预估交换调试:', {
        amountIn,
        amountInBigInt: amountInBigInt.toString(),
        decimalsIn,
        tokenIn,
        tokenOut,
        operationType
      });

      // 构造操作参数
      const operationParams = {
        tokens: [tokenIn, tokenOut],
        amounts: [amountInBigInt.toString()],
        recipient: address || '0x0000000000000000000000000000000000000000' as Address,
        deadline: Math.floor(Date.now() / 1000) + PANCAKE_CONSTANTS.DEFAULT_DEADLINE_OFFSET,
        tokenId: "0",
        extraData: "0x" as const,
      };

      const result = await publicClient.readContract({
        address: pancakeAdapterAddress,
        abi: typedPancakeAdapterABI,
        functionName: 'estimateOperation',
        args: [operationType, operationParams],
      });

      console.log('📊 预估交换结果:', result);

      if (!(result as any).success) {
        throw new Error((result as any).message || '预估失败');
      }

      const outputAmount = (result as any).outputAmounts?.[0] || BigInt(0);
      const decimalsOut = tokenOut === DEPLOYMENT_ADDRESSES.usdtToken ? TOKEN_DECIMALS.USDT : TOKEN_DECIMALS.CAKE;
      const formattedOutput = formatUnits(outputAmount, decimalsOut);

      return {
        success: true,
        data: {
          outputAmount,
          formattedOutput,
          message: (result as any).message || '预估成功'
        }
      };
    } catch (error) {
      console.error('❌ 预估交换失败:', error);
      return {
        success: false,
        error: error instanceof Error ? error.message : '预估交换失败'
      };
    }
  }, [publicClient, store, address]);

  // 授权代币
  const approveToken = useCallback(async (token: Address, amount: string) => {
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

    const { pancakeAdapterAddress } = store;
    if (!pancakeAdapterAddress) {
      throw new Error('合约地址未初始化');
    }

    try {
      const decimals = token === DEPLOYMENT_ADDRESSES.usdtToken ? TOKEN_DECIMALS.USDT : TOKEN_DECIMALS.CAKE;
      const amountBigInt = parseUnits(amount, decimals);

      console.log('🔑 授权代币调试:', {
        token,
        amount,
        amountBigInt: amountBigInt.toString(),
        decimals,
        spender: pancakeAdapterAddress
      });

      const hash = await wc.writeContract({
        address: token,
        abi: typedMockERC20ABI,
        functionName: 'approve',
        args: [pancakeAdapterAddress, amountBigInt],
        chain,
        account: address,
      });

      const receipt = await publicClient.waitForTransactionReceipt({ hash });

      // 刷新授权状态
      await fetchAllowances();

      return {
        success: true,
        data: { hash, receipt }
      };
    } catch (error) {
      const errorMsg = error instanceof Error ? error.message : '代币授权失败';
      console.error('❌ 代币授权失败:', errorMsg);
      return {
        success: false,
        error: errorMsg
      };
    }
  }, [isConnected, publicClient, chain, getWalletClient, store, address, fetchAllowances]);

  // 精确输入交换 - 严格按照测试文件实现
  const swapExactInput = useCallback(async (
    amountIn: string,
    tokenIn: Address,
    tokenOut: Address,
    slippageBps: number = PANCAKE_CONSTANTS.DEFAULT_SLIPPAGE_BPS
  ) => {
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

    const { defiAggregatorAddress, pancakeAdapterAddress } = store;
    if (!defiAggregatorAddress || !pancakeAdapterAddress) {
      throw new Error('合约地址未初始化');
    }

    let isMounted = true;

    try {
      store.setOperating(true);
      store.setError(null);

      console.log('🚀 开始精确输入交换...', { amountIn, tokenIn, tokenOut, slippageBps });

      // 1. 预估输出数量 - 严格按照测试文件
      const estimateResult = await estimateSwap(amountIn, tokenIn, tokenOut, PancakeSwapOperationType.SWAP_EXACT_INPUT);
      if (!estimateResult.success || !estimateResult.data) {
        throw new Error('预估失败: ' + (estimateResult.error || '返回数据为空'));
      }

      const estimatedOutput = estimateResult.data.outputAmount;
      const decimalsOut = tokenOut === DEPLOYMENT_ADDRESSES.usdtToken ? TOKEN_DECIMALS.USDT : TOKEN_DECIMALS.CAKE;
      const minOutput = (estimatedOutput * BigInt(10000 - slippageBps)) / 10000n;

      console.log('📊 交换预估:', {
        estimatedOutput: estimatedOutput.toString(),
        minOutput: minOutput.toString(),
        formattedEstimated: formatUnits(estimatedOutput, decimalsOut),
        formattedMinOutput: formatUnits(minOutput, decimalsOut)
      });

      // 2. 用户授权代币给 PancakeAdapter - 严格按照测试文件
      const tokenContract = tokenIn === DEPLOYMENT_ADDRESSES.usdtToken ? tokenIn : tokenOut;
      const decimalsIn = tokenIn === DEPLOYMENT_ADDRESSES.usdtToken ? TOKEN_DECIMALS.USDT : TOKEN_DECIMALS.CAKE;
      const amountInBigInt = parseUnits(amountIn, decimalsIn);

      console.log(`🔑 授权 ${tokenIn === DEPLOYMENT_ADDRESSES.usdtToken ? 'USDT' : 'CAKE'} 给 PancakeAdapter...`);

      const approveHash = await wc.writeContract({
        address: tokenContract,
        abi: typedMockERC20ABI,
        functionName: 'approve',
        args: [pancakeAdapterAddress, amountInBigInt],
        chain,
        account: address,
      });

      const approveReceipt = await publicClient.waitForTransactionReceipt({ hash: approveHash });
      console.log('✅ 代币授权完成');

      // 3. 执行交换 - 严格按照测试文件
      const swapParams = {
        tokens: [tokenIn, tokenOut],
        amounts: [amountInBigInt.toString(), minOutput.toString()], // [amountIn, minAmountOut]
        recipient: address,
        deadline: Math.floor(Date.now() / 1000) + PANCAKE_CONSTANTS.DEFAULT_DEADLINE_OFFSET,
        tokenId: "0",
        extraData: "0x" as const,
      };

      console.log('🔄 执行交换参数:', swapParams);

      const swapHash = await wc.writeContract({
        address: defiAggregatorAddress,
        abi: typedDefiAggregatorABI,
        functionName: 'executeOperation',
        args: [
          "pancake",                                 // 使用部署配置中的注册名称
          PancakeSwapOperationType.SWAP_EXACT_INPUT, // 操作类型
          swapParams                                  // 操作参数
        ],
        chain,
        account: address,
      });

      console.log('📝 交换交易哈希:', swapHash);

      const swapReceipt = await publicClient.waitForTransactionReceipt({ hash: swapHash });
      console.log('✅ 精确输入交换完成');

      if (isMounted) {
        store.setOperating(false);

        // 刷新用户信息
        await fetchUserBalance();
        await fetchAllowances();
      }

      return {
        success: true,
        hash: swapHash,
        receipt: swapReceipt,
        message: '精确输入交换成功'
      };
    } catch (error) {
      const errorMsg = error instanceof Error ? error.message : '精确输入交换失败';
      if (isMounted) {
        store.setError(errorMsg);
        store.setOperating(false);
      }
      console.error('❌ 精确输入交换失败:', errorMsg);

      return {
        success: false,
        error: errorMsg
      };
    } finally {
      isMounted = false;
    }
  }, [isConnected, publicClient, chain, getWalletClient, store, address, fetchUserBalance, fetchAllowances, estimateSwap]);

  // 精确输出交换 - 严格按照测试文件实现
  const swapExactOutput = useCallback(async (
    amountOut: string,
    tokenIn: Address,
    tokenOut: Address,
    slippageBps: number = PANCAKE_CONSTANTS.DEFAULT_SLIPPAGE_BPS
  ) => {
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

    const { defiAggregatorAddress, pancakeAdapterAddress } = store;
    if (!defiAggregatorAddress || !pancakeAdapterAddress) {
      throw new Error('合约地址未初始化');
    }

    let isMounted = true;

    try {
      store.setOperating(true);
      store.setError(null);

      console.log('🚀 开始精确输出交换...', { amountOut, tokenIn, tokenOut, slippageBps });

      // 1. 预估需要的输入数量 - 严格按照测试文件
      const estimateResult = await estimateSwap(amountOut, tokenIn, tokenOut, PancakeSwapOperationType.SWAP_EXACT_OUTPUT);
      if (!estimateResult.success || !estimateResult.data) {
        throw new Error('预估失败: ' + (estimateResult.error || '返回数据为空'));
      }

      const estimatedInput = estimateResult.data.outputAmount;
      const decimalsIn = tokenIn === DEPLOYMENT_ADDRESSES.usdtToken ? TOKEN_DECIMALS.USDT : TOKEN_DECIMALS.CAKE;
      const maxInput = (estimatedInput * BigInt(10000 + slippageBps)) / 10000n;

      console.log('📊 交换预估:', {
        estimatedInput: estimatedInput.toString(),
        maxInput: maxInput.toString(),
        formattedEstimated: formatUnits(estimatedInput, decimalsIn),
        formattedMaxInput: formatUnits(maxInput, decimalsIn)
      });

      // 2. 用户授权代币给 PancakeAdapter - 严格按照测试文件
      const tokenContract = tokenIn === DEPLOYMENT_ADDRESSES.usdtToken ? tokenIn : tokenOut;
      const decimalsOut = tokenOut === DEPLOYMENT_ADDRESSES.usdtToken ? TOKEN_DECIMALS.USDT : TOKEN_DECIMALS.CAKE;
      const amountOutBigInt = parseUnits(amountOut, decimalsOut);

      console.log(`🔑 授权 ${tokenIn === DEPLOYMENT_ADDRESSES.usdtToken ? 'USDT' : 'CAKE'} 给 PancakeAdapter (精确输出)...`);

      const approveHash = await wc.writeContract({
        address: tokenContract,
        abi: typedMockERC20ABI,
        functionName: 'approve',
        args: [pancakeAdapterAddress, maxInput],
        chain,
        account: address,
      });

      const approveReceipt = await publicClient.waitForTransactionReceipt({ hash: approveHash });
      console.log('✅ 代币授权完成');

      // 3. 执行交换 - 严格按照测试文件
      const swapParams = {
        tokens: [tokenIn, tokenOut],
        amounts: [amountOutBigInt.toString(), maxInput.toString()], // [amountOut, maxAmountIn]
        recipient: address,
        deadline: Math.floor(Date.now() / 1000) + PANCAKE_CONSTANTS.DEFAULT_DEADLINE_OFFSET,
        tokenId: "0",
        extraData: "0x" as const,
      };

      console.log('🔄 执行交换参数:', swapParams);

      const swapHash = await wc.writeContract({
        address: defiAggregatorAddress,
        abi: typedDefiAggregatorABI,
        functionName: 'executeOperation',
        args: [
          "pancake",                                   // 使用部署配置中的注册名称
          PancakeSwapOperationType.SWAP_EXACT_OUTPUT, // 操作类型
          swapParams                                   // 操作参数
        ],
        chain,
        account: address,
      });

      console.log('📝 交换交易哈希:', swapHash);

      const swapReceipt = await publicClient.waitForTransactionReceipt({ hash: swapHash });
      console.log('✅ 精确输出交换完成');

      if (isMounted) {
        store.setOperating(false);

        // 刷新用户信息
        await fetchUserBalance();
        await fetchAllowances();
      }

      return {
        success: true,
        hash: swapHash,
        receipt: swapReceipt,
        message: '精确输出交换成功'
      };
    } catch (error) {
      const errorMsg = error instanceof Error ? error.message : '精确输出交换失败';
      if (isMounted) {
        store.setError(errorMsg);
        store.setOperating(false);
      }
      console.error('❌ 精确输出交换失败:', errorMsg);

      return {
        success: false,
        error: errorMsg
      };
    } finally {
      isMounted = false;
    }
  }, [isConnected, publicClient, chain, getWalletClient, store, address, fetchUserBalance, fetchAllowances, estimateSwap]);

  // 初始化 PancakeSwap 功能 - 优化依赖
  const initializePancakeSwap = useCallback(async () => {
    try {
      console.log('🚀 初始化 PancakeSwap 功能...');

      // 初始化合约地址
      initContracts();

      // 如果用户已连接钱包，获取用户信息
      if (isConnected && address) {
        await Promise.all([
          fetchUserBalance(),
          fetchAllowances()
        ]);
      }

      console.log('✅ PancakeSwap 功能初始化完成');
    } catch (error) {
      const errorMsg = error instanceof Error ? error.message : '初始化失败';
      store.setError(errorMsg);
      console.error('❌ PancakeSwap 功能初始化失败:', errorMsg);
      throw error;
    }
  }, [initContracts, isConnected, address]);

  // 刷新用户信息 - 优化依赖
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
  }, [isConnected, address]);

  // 计算属性：格式化的余额信息
  const formattedBalances = useMemo(() => {
    if (!store.userBalance) {
      return {
        usdtBalance: '0',
        cakeBalance: '0',
        usdtAllowance: '0',
        cakeAllowance: '0',
      };
    }

    const usdtBalance = formatUnits(store.userBalance.usdtBalance, TOKEN_DECIMALS.USDT);
    const cakeBalance = formatUnits(store.userBalance.cakeBalance, TOKEN_DECIMALS.CAKE);
    const usdtAllowance = formatUnits(store.userBalance.usdtAllowance, TOKEN_DECIMALS.USDT);
    const cakeAllowance = formatUnits(store.userBalance.cakeAllowance, TOKEN_DECIMALS.CAKE);

    return {
      usdtBalance,
      cakeBalance,
      usdtAllowance,
      cakeAllowance,
    };
  }, [store.userBalance]);

  // 检查是否需要授权
  const needsApproval = useMemo(() => {
    if (!store.userBalance) {
      return { usdt: true, cake: true };
    }

    return {
      usdt: store.userBalance.usdtAllowance === BigInt(0),
      cake: store.userBalance.cakeAllowance === BigInt(0),
    };
  }, [store.userBalance]);

  // 获取最大可用余额
  const maxBalances = useMemo(() => {
    if (!store.userBalance) {
      return {
        maxUSDTToSwap: '0',
        maxCAKEToSwap: '0',
      };
    }

    return {
      maxUSDTToSwap: formatUnits(store.userBalance.usdtBalance, TOKEN_DECIMALS.USDT),
      maxCAKEToSwap: formatUnits(store.userBalance.cakeBalance, TOKEN_DECIMALS.CAKE),
    };
  }, [store.userBalance]);

  // 自动初始化合约 - 修复无限循环
  useEffect(() => {
    const shouldInit = store.defiAggregatorAddress === null || store.pancakeAdapterAddress === null;
    if (shouldInit) {
      initContracts();
    }
  }, [store.defiAggregatorAddress, store.pancakeAdapterAddress]);

  // 钱包连接/断开时刷新数据 - 优化依赖
  useEffect(() => {
    let isMounted = true;
    let controller = new AbortController();

    if (isConnected && address) {
      refreshUserInfo().catch(error => {
        if (!controller.signal.aborted && isMounted) {
          console.error('刷新用户信息失败:', error);
        }
      });
    }

    return () => {
      isMounted = false;
      controller.abort();
    };
  }, [isConnected, address, refreshUserInfo]);

  return {
    // 基础状态
    isConnected,
    address,
    isLoading: store.isLoading,
    isOperating: store.isOperating,
    error: store.error,

    // 合约信息
    defiAggregatorAddress: store.defiAggregatorAddress,
    pancakeAdapterAddress: store.pancakeAdapterAddress,
    usdtTokenAddress: store.usdtTokenAddress,
    cakeTokenAddress: store.cakeTokenAddress,
    routerAddress: store.routerAddress,

    // 用户数据
    userBalance: store.userBalance,
    exchangeRate: store.exchangeRate,
    formattedBalances,
    needsApproval,
    maxBalances,

    // 客户端
    publicClient,
    walletClient,

    // 初始化方法
    initializePancakeSwap,
    refreshUserInfo,

    // 读取方法
    fetchUserBalance,
    fetchAllowances,
    fetchExchangeRate,
    estimateSwap,

    // 操作方法
    approveToken,
    swapExactInput,
    swapExactOutput,

    // 辅助方法
    setLoading: store.setLoading,
    setOperating: store.setOperating,
    setError: store.setError,
    clearError: store.clearError,
    reset: store.reset,
  };
};

export default usePancakeSwapWithClients;