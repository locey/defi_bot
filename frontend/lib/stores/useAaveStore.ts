import { create } from 'zustand';
import {
  Address,
  PublicClient,
  WalletClient,
  TransactionReceipt,
  Abi,
  Chain,
  Hex,
  formatUnits,
  decodeEventLog as viemDecodeEventLog,
} from 'viem';
import DefiAggregatorABI from '@/lib/abi/DefiAggregator.json';
import MockERC20ABI from '@/lib/abi/MockERC20.json';
import AaveAdapterABI from '@/lib/abi/AaveAdapter.json';
import AaveDeploymentInfo from '@/lib/abi/deployments-aave-adapter-sepolia.json';

// usdt 地址
import { getContractAddresses } from "@/lib/utils/contracts";

// 获取合约地址
const { USDT_ADDRESS } = getContractAddresses() as { USDT_ADDRESS: Address };
// ==================== 类型定义 ====================

/**
 * Aave 操作类型枚举（基于测试用例）
 */
export enum AaveOperationType {
  DEPOSIT = 0,   // 存入资产
  WITHDRAW = 1,  // 提取资产
}

/**
 * 操作参数类型（基于测试用例）
 */
export interface AaveOperationParams {
  tokens: Address[];
  amounts: bigint[];
  recipient: Address;
  deadline: number;
  tokenId: number;
  extraData: Hex;
}

/**
 * 操作结果类型（基于 DefiAggregator 返回结构）
 */
export interface AaveOperationResult {
  success: boolean;
  outputAmounts: bigint[];
  returnData: Hex;
  message: string;
}

/**
 * 交易结果类型
 */
export interface AaveTransactionResult {
  hash: `0x${string}`;
  receipt: TransactionReceipt;
  result: AaveOperationResult;
}

/**
 * Aave 池信息类型
 */
export interface AavePoolInfo {
  defiAggregator: Address;
  aaveAdapter: Address;
  usdtToken: Address;
  aUsdtToken: Address;
  adapterName: string;
  adapterVersion: string;
  contractVersion: string;
  supportedOperations: AaveOperationType[];
  feeRateBps: number; // 手续费率（基点）
}

/**
 * 用户余额信息类型
 */
export interface UserBalanceInfo {
  usdtBalance: bigint;    // 用户持有的 USDT 余额
  aUsdtBalance: bigint;   // 用户持有的 aUSDT (利息代币) 余额
  usdtAllowance: bigint;  // 用户授权给 DefiAggregator 的 USDT 数量
  aUsdtAllowance: bigint; // 用户授权给 DefiAggregator 的 aUSDT 数量
  depositedAmount: bigint; // 用户在 Aave 中存入的 USDT 数量
  earnedInterest: bigint;  // 用户赚取的利息
}

/**
 * OperationExecuted 事件参数类型
 */
export interface OperationExecutedEventArgs {
  user: Address;
  operationType: number;
  tokens: Address[];
  amounts: bigint[];
  returnData: Hex;
}

/**
 * 解码事件日志的返回类型
 */
export interface DecodedOperationExecutedEvent {
  eventName: 'OperationExecuted';
  args: OperationExecutedEventArgs;
}

// ==================== Store 状态定义 ====================
interface AaveState {
  // ==================== 状态 ====================
  /** DefiAggregator 合约地址 */
  defiAggregatorAddress: Address | null;
  /** Aave 适配器合约地址 */
  aaveAdapterAddress: Address | null;
  /** Aave 池信息 */
  poolInfo: AavePoolInfo | null;
  /** 用户余额信息 */
  userBalance: UserBalanceInfo | null;
  /** 加载状态 */
  isLoading: boolean;
  /** 操作执行中的加载状态 */
  isOperating: boolean;
  /** 错误信息 */
  error: string | null;

  // ==================== 初始化方法 ====================
  /** 初始化合约地址 */
  initContracts: (defiAggregatorAddress: Address, aaveAdapterAddress: Address) => void;
  /** 从部署文件初始化合约地址 */
  initFromDeployment: () => void;

  // ==================== 读取方法 ====================
  /** 获取 Aave 池信息 */
  fetchPoolInfo: (publicClient: PublicClient) => Promise<void>;
  /** 获取用户余额信息 */
  fetchUserBalance: (publicClient: PublicClient, userAddress: Address) => Promise<void>;
  /** 获取用户 USDT 余额 */
  fetchUserUSDTBalance: (publicClient: PublicClient, userAddress: Address) => Promise<bigint>;
  /** 获取用户 aUSDT 余额 */
  fetchUserAUSDTBalance: (publicClient: PublicClient, userAddress: Address) => Promise<bigint>;
  /** 获取授权信息 */
  fetchAllowances: (publicClient: PublicClient, userAddress: Address) => Promise<{ usdtAllowance: bigint; aUsdtAllowance: bigint }>;
  /** 获取手续费率 */
  fetchFeeRate: (publicClient: PublicClient) => Promise<number>;

  // ==================== 写入方法 ====================
  /** 授权 USDT 给 DefiAggregator */
  approveUSDT: (
    publicClient: PublicClient,
    walletClient: WalletClient,
    chain: Chain,
    amount: bigint,
    account: Address,
    userAddress: Address,
    gasConfig?: {
      gas?: bigint;
      gasPrice?: bigint;
      maxFeePerGas?: bigint;
      maxPriorityFeePerGas?: bigint;
    }
  ) => Promise<TransactionReceipt>;

  /** 授权 aUSDT 给 DefiAggregator */
  approveAUSDT: (
    publicClient: PublicClient,
    walletClient: WalletClient,
    chain: Chain,
    amount: bigint,
    account: Address,
    gasConfig?: {
      gas?: bigint;
      gasPrice?: bigint;
      maxFeePerGas?: bigint;
      maxPriorityFeePerGas?: bigint;
    }
  ) => Promise<TransactionReceipt>;

  /** 存入 USDT 到 Aave（基于测试用例逻辑） */
  supplyUSDT: (
    publicClient: PublicClient,
    walletClient: WalletClient,
    chain: Chain,
    amount: bigint,
    account: Address,
    gasConfig?: {
      gas?: bigint;
      gasPrice?: bigint;
      maxFeePerGas?: bigint;
      maxPriorityFeePerGas?: bigint;
    }
  ) => Promise<AaveTransactionResult>;

  /** 从 Aave 提取 USDT（基于测试用例逻辑） */
  withdrawUSDT: (
    publicClient: PublicClient,
    walletClient: WalletClient,
    chain: Chain,
    amount: bigint,
    account: Address,
    gasConfig?: {
      gas?: bigint;
      gasPrice?: bigint;
      maxFeePerGas?: bigint;
      maxPriorityFeePerGas?: bigint;
    }
  ) => Promise<AaveTransactionResult>;

  /** 卖出 USDT（从 Aave 提取） */
  sellUSDT: (
    publicClient: PublicClient,
    walletClient: WalletClient,
    chain: Chain,
    amount: bigint,
    account: Address,
    gasConfig?: {
      gas?: bigint;
      gasPrice?: bigint;
      maxFeePerGas?: bigint;
      maxPriorityFeePerGas?: bigint;
    }
  ) => Promise<AaveTransactionResult>;

  // ==================== 辅助方法 ====================
  /** 设置加载状态 */
  setLoading: (loading: boolean) => void;
  /** 设置操作状态 */
  setOperating: (operating: boolean) => void;
  /** 设置错误信息 */
  setError: (error: string | null) => void;
  /** 清除错误信息 */
  clearErrors: () => void;
  /** 重置状态 */
  reset: () => void;
}

// ==================== 类型化 ABI ====================
const typedDefiAggregatorABI = DefiAggregatorABI as Abi;
const typedMockERC20ABI = MockERC20ABI as Abi;
const typedAaveAdapterABI = AaveAdapterABI as Abi;

// ==================== Store 创建 ====================
export const useAaveStore = create<AaveState>((set, get) => ({
  // ==================== 初始状态 ====================
  defiAggregatorAddress: null,
  aaveAdapterAddress: null,
  poolInfo: null,
  userBalance: null,
  isLoading: false,
  isOperating: false,
  error: null,

  // ==================== 初始化方法 ====================
  /**
   * 初始化合约地址
   * @param defiAggregatorAddress DefiAggregator 合约地址
   * @param aaveAdapterAddress AaveAdapter 合约地址
   */
  initContracts: (defiAggregatorAddress: Address, aaveAdapterAddress: Address) => {
    try {
      set({
        defiAggregatorAddress,
        aaveAdapterAddress,
        error: null
      });
      console.log('✅ DefiAggregator 合约地址已初始化:', defiAggregatorAddress);
      console.log('✅ AaveAdapter 合约地址已初始化:', aaveAdapterAddress);
    } catch (error) {
      const errorMsg = error instanceof Error ? error.message : '初始化合约失败';
      set({ error: errorMsg });
      console.error('❌ 初始化合约失败:', errorMsg);
    }
  },

  /**
   * 从部署文件初始化合约地址
   * 读取 deployments-aave-adapter-sepolia.json 文件中的地址信息
   */
  initFromDeployment: () => {
    try {
      // 直接从导入的部署文件中获取地址
      const defiAggregatorAddress = AaveDeploymentInfo.contracts.DefiAggregator as Address;
      const aaveAdapterAddress = AaveDeploymentInfo.contracts.AaveAdapter as Address;
      const usdtTokenAddress = USDT_ADDRESS;
      const aUsdtTokenAddress = AaveDeploymentInfo.contracts.MockAToken_aUSDT as Address;

      set({
        defiAggregatorAddress,
        aaveAdapterAddress,
        error: null
      });

      console.log('✅ 从部署文件初始化合约地址:');
      console.log('   DefiAggregator:', defiAggregatorAddress);
      console.log('   AaveAdapter:', aaveAdapterAddress);
      console.log('   USDT Token:', usdtTokenAddress);
      console.log('   aUSDT Token:', aUsdtTokenAddress);
      console.log('   网络:', AaveDeploymentInfo.network);
      console.log('   手续费率:', AaveDeploymentInfo.feeRateBps, 'BPS');
    } catch (error) {
      const errorMsg = error instanceof Error ? error.message : '从部署文件初始化失败';
      set({ error: errorMsg });
      console.error('❌ 从部署文件初始化失败:', errorMsg);
    }
  },

  // ==================== 读取方法 ====================
  /**
   * 获取 Aave 池信息
   * @param publicClient 公共客户端
   */
  fetchPoolInfo: async (publicClient: PublicClient) => {
    const { defiAggregatorAddress, aaveAdapterAddress } = get();
    if (!defiAggregatorAddress || !aaveAdapterAddress) {
      set({ error: '合约地址未初始化' });
      return;
    }

    try {
      set({ isLoading: true, error: null });

      const [feeRateBps, usdtToken, aUsdtToken, adapterName, adapterVersion, contractVersion] = await Promise.all([
        publicClient.readContract({
          address: defiAggregatorAddress,
          abi: typedDefiAggregatorABI,
          functionName: 'feeRateBps',
        }),
        publicClient.readContract({
          address: aaveAdapterAddress,
          abi: typedAaveAdapterABI,
          functionName: 'usdtToken',
        }),
        publicClient.readContract({
          address: aaveAdapterAddress,
          abi: typedAaveAdapterABI,
          functionName: 'aUsdtToken',
        }),
        publicClient.readContract({
          address: aaveAdapterAddress,
          abi: typedAaveAdapterABI,
          functionName: 'getAdapterName',
        }),
        publicClient.readContract({
          address: aaveAdapterAddress,
          abi: typedAaveAdapterABI,
          functionName: 'getAdapterVersion',
        }),
        publicClient.readContract({
          address: aaveAdapterAddress,
          abi: typedAaveAdapterABI,
          functionName: 'getContractVersion',
        }),
      ]);

      const poolInfo: AavePoolInfo = {
        defiAggregator: defiAggregatorAddress,
        aaveAdapter: aaveAdapterAddress,
        usdtToken: usdtToken as Address,
        aUsdtToken: aUsdtToken as Address,
        adapterName: adapterName as string,
        adapterVersion: adapterVersion as string,
        contractVersion: contractVersion as string,
        supportedOperations: [AaveOperationType.DEPOSIT, AaveOperationType.WITHDRAW],
        feeRateBps: Number(feeRateBps),
      };

      console.log('✅ Aave 池信息获取成功:', poolInfo);
      set({ poolInfo, isLoading: false });
    } catch (error) {
      const errorMsg = error instanceof Error ? error.message : '获取 Aave 池信息失败';
      set({ error: errorMsg, isLoading: false });
      console.error('❌ 获取 Aave 池信息失败:', errorMsg);
    }
  },

  /**
   * 获取用户余额信息
   * @param publicClient 公共客户端
   * @param userAddress 用户地址
   */
  fetchUserBalance: async (publicClient: PublicClient, userAddress: Address) => {
    const { defiAggregatorAddress } = get();
    if (!defiAggregatorAddress) {
      set({ error: '合约地址未初始化' });
      return;
    }

    try {
      set({ isLoading: true, error: null });

      const [usdtBalance, aUsdtBalance, { usdtAllowance, aUsdtAllowance }] = await Promise.all([
        get().fetchUserUSDTBalance(publicClient, userAddress),
        get().fetchUserAUSDTBalance(publicClient, userAddress),
        get().fetchAllowances(publicClient, userAddress),
      ]);

      // 计算赚取的利息 (aUSDT - USDT = 利息)
      const earnedInterest = aUsdtBalance > usdtBalance ? aUsdtBalance - usdtBalance : BigInt(0);

      const balanceInfo: UserBalanceInfo = {
        usdtBalance,
        aUsdtBalance,
        usdtAllowance,
        aUsdtAllowance,
        depositedAmount: usdtBalance, // 简化假设：存入金额等于当前USDT余额
        earnedInterest,
      };

      console.log('✅ 用户余额信息获取成功:', balanceInfo);
      set({ userBalance: balanceInfo, isLoading: false });
    } catch (error) {
      const errorMsg = error instanceof Error ? error.message : '获取用户余额信息失败';
      set({ error: errorMsg, isLoading: false });
      console.error('❌ 获取用户余额信息失败:', errorMsg);
    }
  },

  /**
   * 获取用户 USDT 余额
   * @param publicClient 公共客户端
   * @param userAddress 用户地址
   */
  fetchUserUSDTBalance: async (publicClient: PublicClient, userAddress: Address): Promise<bigint> => {
    try {
      const balance = await publicClient.readContract({
        address: USDT_ADDRESS, // 使用动态获取的 USDT 地址
        abi: typedMockERC20ABI,
        functionName: 'balanceOf',
        args: [userAddress],
      });

      console.log(`💰 用户 USDT 余额: ${formatUnits(balance as bigint, 6)}`);
      return balance as bigint;
    } catch (error) {
      console.warn('获取用户 USDT 余额失败:', error);
      return BigInt(0);
    }
  },

  /**
   * 获取用户 aUSDT 余额
   * @param publicClient 公共客户端
   * @param userAddress 用户地址
   */
  fetchUserAUSDTBalance: async (publicClient: PublicClient, userAddress: Address): Promise<bigint> => {
    try {
      const balance = await publicClient.readContract({
        address: AaveDeploymentInfo.contracts.MockAToken_aUSDT as Address, // 从部署文件读取 aUSDT 地址
        abi: typedMockERC20ABI, // aUSDT 也是 ERC20 代币
        functionName: 'balanceOf',
        args: [userAddress],
      });

      console.log(`💰 用户 aUSDT 余额: ${formatUnits(balance as bigint, 6)}`);
      return balance as bigint;
    } catch (error) {
      console.warn('获取用户 aUSDT 余额失败:', error);
      return BigInt(0);
    }
  },

  /**
   * 获取授权信息
   * @param publicClient 公共客户端
   * @param userAddress 用户地址
   */
  fetchAllowances: async (publicClient: PublicClient, userAddress: Address): Promise<{ usdtAllowance: bigint; aUsdtAllowance: bigint }> => {
    const { aaveAdapterAddress } = get();
    if (!aaveAdapterAddress) {
      throw new Error('AaveAdapter 合约地址未初始化');
    }

    try {
      const [usdtAllowance, aUsdtAllowance] = await Promise.all([
        publicClient.readContract({
          address: USDT_ADDRESS  as Address,
          abi: typedMockERC20ABI,
          functionName: 'allowance',
          args: [userAddress, aaveAdapterAddress],
        }),
        publicClient.readContract({
          address: AaveDeploymentInfo.contracts.MockAToken_aUSDT as Address, // 从部署文件读取
          abi: typedMockERC20ABI,
          functionName: 'allowance',
          args: [userAddress, aaveAdapterAddress],
        }),
      ]);

      console.log(`🔑 USDT 授权额度: ${formatUnits(usdtAllowance as bigint, 6)}`);
      console.log(`🔑 aUSDT 授权额度: ${formatUnits(aUsdtAllowance as bigint, 6)}`);

      return {
        usdtAllowance: usdtAllowance as bigint,
        aUsdtAllowance: aUsdtAllowance as bigint,
      };
    } catch (error) {
      console.warn('获取授权信息失败:', error);
      return { usdtAllowance: BigInt(0), aUsdtAllowance: BigInt(0) };
    }
  },

  /**
   * 获取手续费率
   * @param publicClient 公共客户端
   */
  fetchFeeRate: async (publicClient: PublicClient): Promise<number> => {
    const { defiAggregatorAddress } = get();
    if (!defiAggregatorAddress) {
      throw new Error('DefiAggregator 合约地址未初始化');
    }

    try {
      const feeRateBps = await publicClient.readContract({
        address: defiAggregatorAddress,
        abi: typedDefiAggregatorABI,
        functionName: 'feeRateBps',
      });

      const feeRate = Number(feeRateBps);
      console.log(`💰 手续费率: ${feeRate} BPS (${feeRate / 100}%)`);
      return feeRate;
    } catch (error) {
      console.warn('获取手续费率失败:', error);
      return AaveDeploymentInfo.feeRateBps; // 从部署文件读取默认手续费率
    }
  },

  // ==================== 写入方法 ====================
  /**
   * 授权 USDT 给 DefiAggregator
   * @param publicClient 公共客户端
   * @param walletClient 钱包客户端
   * @param chain 链配置
   * @param amount 授权数量
   * @param account 用户地址
   */
  approveUSDT: async (
    publicClient: PublicClient,
    walletClient: WalletClient,
    chain: Chain,
    amount: bigint,
    account: Address,
    userAddress: Address,
    gasConfig?: {
      gas?: bigint;
      gasPrice?: bigint;
      maxFeePerGas?: bigint;
      maxPriorityFeePerGas?: bigint;
    }
  ): Promise<TransactionReceipt> => {
    const { aaveAdapterAddress } = get();
    if (!aaveAdapterAddress) {
      throw new Error('AaveAdapter 合约地址未初始化');
    }

    try {
      console.log('🔑 开始授权 USDT 给 AaveAdapter...');
      console.log('参数:', { amount: amount.toString(), account });

      // 构建交易参数，正确处理 gas 配置
      const baseParams = {
        address: USDT_ADDRESS, // 使用动态获取的 USDT 地址
        abi: typedMockERC20ABI,
        functionName: 'approve' as const,
        args: [aaveAdapterAddress, amount] as [`0x${string}`, bigint],
        chain,
        account,
      };

      // 根据gas配置动态构建参数，避免类型冲突
      const writeParams = { ...baseParams };

      if (gasConfig?.maxFeePerGas && gasConfig?.maxPriorityFeePerGas) {
        // EIP-1559 gas 配置
        Object.assign(writeParams, {
          ...(gasConfig?.gas && { gas: gasConfig.gas }),
          maxFeePerGas: gasConfig.maxFeePerGas,
          maxPriorityFeePerGas: gasConfig.maxPriorityFeePerGas,
        });
      } else {
        // Legacy gas 配置或默认
        Object.assign(writeParams, {
          ...(gasConfig?.gas && { gas: gasConfig.gas }),
          ...(gasConfig?.gasPrice && { gasPrice: gasConfig.gasPrice }),
        });
      }

      const hash = await walletClient.writeContract(writeParams as Parameters<typeof walletClient.writeContract>[0]);

      console.log('📝 授权交易哈希:', hash);

      const receipt = await publicClient.waitForTransactionReceipt({ hash });
      console.log('✅ USDT 授权完成');

      // 授权成功后更新授权状态（从 store 中刷新）
      await get().fetchAllowances(publicClient, userAddress);

      return receipt;
    } catch (error) {
      const errorMsg = error instanceof Error ? error.message : 'USDT 授权失败';
      console.error('❌ USDT 授权失败:', errorMsg);
      throw error;
    }
  },

  /**
   * 授权 aUSDT 给 DefiAggregator
   * @param publicClient 公共客户端
   * @param walletClient 钱包客户端
   * @param chain 链配置
   * @param amount 授权数量
   * @param account 用户地址
   */
  approveAUSDT: async (
    publicClient: PublicClient,
    walletClient: WalletClient,
    chain: Chain,
    amount: bigint,
    account: Address,
    gasConfig?: {
      gas?: bigint;
      gasPrice?: bigint;
      maxFeePerGas?: bigint;
      maxPriorityFeePerGas?: bigint;
    }
  ): Promise<TransactionReceipt> => {
    const { aaveAdapterAddress } = get();
    if (!aaveAdapterAddress) {
      throw new Error('AaveAdapter 合约地址未初始化');
    }

    try {
      console.log('🔑 开始授权 aUSDT 给 AaveAdapter...');
      console.log('参数:', { amount: amount.toString(), account });

      // 构建交易参数，正确处理 gas 配置
      type AaveTokenWriteParams = {
        address: Address;
        abi: typeof typedMockERC20ABI;
        functionName: 'approve';
        args: [`0x${string}`, bigint];
        chain: Chain;
        account: Address;
        gas?: bigint;
        gasPrice?: bigint;
        maxFeePerGas?: bigint;
        maxPriorityFeePerGas?: bigint;
      };

      const txParams: AaveTokenWriteParams = {
        address: AaveDeploymentInfo.contracts.MockAToken_aUSDT as Address, // 从部署文件读取 aUSDT 地址
        abi: typedMockERC20ABI,
        functionName: 'approve',
        args: [aaveAdapterAddress, amount] as [`0x${string}`, bigint],
        chain,
        account,
      };

      // 添加 gas 配置，避免 EIP-1559 和 legacy 同时存在
      if (gasConfig?.gas) {
        txParams.gas = gasConfig.gas;
      }
      if (gasConfig?.maxFeePerGas && gasConfig?.maxPriorityFeePerGas) {
        txParams.maxFeePerGas = gasConfig.maxFeePerGas;
        txParams.maxPriorityFeePerGas = gasConfig.maxPriorityFeePerGas;
      } else if (gasConfig?.gasPrice) {
        txParams.gasPrice = gasConfig.gasPrice;
      }

      const hash = await walletClient.writeContract(txParams as Parameters<typeof walletClient.writeContract>[0]);

      console.log('📝 授权交易哈希:', hash);

      const receipt = await publicClient.waitForTransactionReceipt({ hash });
      console.log('✅ aUSDT 授权完成');

      return receipt;
    } catch (error) {
      const errorMsg = error instanceof Error ? error.message : 'aUSDT 授权失败';
      console.error('❌ aUSDT 授权失败:', errorMsg);
      throw error;
    }
  },

  /**
   * 存入 USDT 到 Aave（基于测试用例逻辑）
   * @param publicClient 公共客户端
   * @param walletClient 钱包客户端
   * @param chain 链配置
   * @param amount 存入数量
   * @param account 用户地址
   */
  supplyUSDT: async (
    publicClient: PublicClient,
    walletClient: WalletClient,
    chain: Chain,
    amount: bigint,
    account: Address,
    gasConfig?: {
      gas?: bigint;
      gasPrice?: bigint;
      maxFeePerGas?: bigint;
      maxPriorityFeePerGas?: bigint;
    }
  ): Promise<AaveTransactionResult> => {
    const { defiAggregatorAddress } = get();
    if (!defiAggregatorAddress) {
      throw new Error('合约地址未初始化');
    }

    try {
      set({ isOperating: true, error: null });
      console.log('🚀 开始存入 USDT 到 Aave...');
      console.log('参数:', { amount: amount.toString(), account });

      // 构造操作参数（基于测试用例）
      const operationParams: AaveOperationParams = {
        tokens: [USDT_ADDRESS], // 使用动态获取的 USDT 地址
        amounts: [amount],
        recipient: account,
        deadline: Math.floor(Date.now() / 1000) + 3600, // 1小时后过期
        tokenId: 0, // Aave 不使用 NFT，设为 0
        extraData: '0x' as Hex, // 无额外数据
      };

      console.log('📋 操作参数:', operationParams);
      console.log('📋 执行参数:', {
        adapterName: 'aave', // 适配器名称
        operationType: AaveOperationType.DEPOSIT, // 操作类型：0
        operationParams,
        gasConfig
      });

      // 构建交易参数，正确处理 gas 配置
      type ExecuteOperationParams = {
        address: Address;
        abi: typeof typedDefiAggregatorABI;
        functionName: 'executeOperation';
        args: [string, number, AaveOperationParams];
        chain: Chain;
        account: Address;
        gas?: bigint;
        gasPrice?: bigint;
        maxFeePerGas?: bigint;
        maxPriorityFeePerGas?: bigint;
      };

      const txParams: ExecuteOperationParams = {
        address: defiAggregatorAddress,
        abi: typedDefiAggregatorABI,
        functionName: 'executeOperation',
        args: [
          'aave', // 适配器名称
          AaveOperationType.DEPOSIT, // 操作类型：0
          operationParams
        ] as [string, number, AaveOperationParams],
        chain,
        account,
      };

      // 添加 gas 配置，避免 EIP-1559 和 legacy 同时存在
      if (gasConfig?.gas) {
        txParams.gas = gasConfig.gas;
      }
      if (gasConfig?.maxFeePerGas && gasConfig?.maxPriorityFeePerGas) {
        txParams.maxFeePerGas = gasConfig.maxFeePerGas;
        txParams.maxPriorityFeePerGas = gasConfig.maxPriorityFeePerGas;
      } else if (gasConfig?.gasPrice) {
        txParams.gasPrice = gasConfig.gasPrice;
      }

      const hash = await walletClient.writeContract(txParams as Parameters<typeof walletClient.writeContract>[0]);

      console.log('📝 存款交易哈希:', hash);

      console.log('⏳ 等待交易确认...');
      const receipt = await publicClient.waitForTransactionReceipt({ hash });
      console.log('✅ 交易已确认');

      // 解析操作结果（从事件日志中）
      let operationResult: AaveOperationResult = {
        success: false,
        outputAmounts: [],
        returnData: '0x' as Hex,
        message: '无法解析操作结果',
      };

      if (receipt.logs) {
        for (const log of receipt.logs as Array<{ topics: readonly Hex[] } & typeof receipt.logs[0]>) {
          try {
            const event = viemDecodeEventLog({
              abi: typedDefiAggregatorABI,
              data: log.data,
              topics: log.topics as [signature: Hex, ...args: Hex[]],
            });

            if (event && event.eventName === 'OperationExecuted') {
              const operationEvent = event as unknown as DecodedOperationExecutedEvent;
              operationResult = {
                success: true,
                outputAmounts: operationEvent.args.amounts,
                returnData: operationEvent.args.returnData,
                message: '存款操作成功',
              };
              console.log('✅ 解析到 OperationExecuted 事件:', operationEvent);
              break;
            }
          } catch (e) {
            console.warn('解码事件日志失败:', e);
          }
        }
      }

      set({ isOperating: false });

      const result: AaveTransactionResult = {
        hash,
        receipt,
        result: operationResult,
      };

      console.log('✅ USDT 存入操作完成');
      return result;
    } catch (error) {
      const errorMsg = error instanceof Error ? error.message : '存入 USDT 失败';
      set({ error: errorMsg, isOperating: false });
      console.error('❌ 存入 USDT 失败:', errorMsg);
      throw error;
    }
  },

  /**
   * 从 Aave 提取 USDT（基于测试用例逻辑）
   * @param publicClient 公共客户端
   * @param walletClient 钱包客户端
   * @param chain 链配置
   * @param amount 提取数量
   * @param account 用户地址
   */
  withdrawUSDT: async (
    publicClient: PublicClient,
    walletClient: WalletClient,
    chain: Chain,
    amount: bigint,
    account: Address,
    gasConfig?: {
      gas?: bigint;
      gasPrice?: bigint;
      maxFeePerGas?: bigint;
      maxPriorityFeePerGas?: bigint;
    }
  ): Promise<AaveTransactionResult> => {
    const { defiAggregatorAddress } = get();
    if (!defiAggregatorAddress) {
      throw new Error('合约地址未初始化');
    }

    try {
      set({ isOperating: true, error: null });
      console.log('🚀 开始从 Aave 提取 USDT...');
      console.log('参数:', { amount: amount.toString(), account });

      // 构造操作参数（基于测试用例）
      const operationParams: AaveOperationParams = {
        tokens: [USDT_ADDRESS], // 使用动态获取的 USDT 地址
        amounts: [amount], // 这里是要取回的 USDT 数量
        recipient: account,
        deadline: Math.floor(Date.now() / 1000) + 3600, // 1小时后过期
        tokenId: 0, // Aave 不使用 NFT，设为 0
        extraData: '0x' as Hex, // 无额外数据
      };

      console.log('📋 取款操作参数:', operationParams);
      console.log('📋 执行参数:', {
        adapterName: 'aave', // 适配器名称
        operationType: AaveOperationType.WITHDRAW, // 操作类型：1
        operationParams,
        gasConfig
      });

      const hash = await walletClient.writeContract({
        address: defiAggregatorAddress,
        abi: typedDefiAggregatorABI,
        functionName: 'executeOperation',
        args: [
          'aave', // ��配器名称
          AaveOperationType.WITHDRAW, // 操作类型：1
          operationParams
        ],
        chain,
        account,
        ...(gasConfig?.gas && { gas: gasConfig.gas }),
        ...(gasConfig?.maxFeePerGas && gasConfig?.maxPriorityFeePerGas && {
          maxFeePerGas: gasConfig.maxFeePerGas,
          maxPriorityFeePerGas: gasConfig.maxPriorityFeePerGas,
        }),
        ...(gasConfig?.gasPrice && { gasPrice: gasConfig.gasPrice }),
      } as Parameters<typeof walletClient.writeContract>[0]);

      console.log('📝 取款交易哈希:', hash);

      console.log('⏳ 等待交易确认...');
      const receipt = await publicClient.waitForTransactionReceipt({ hash });
      console.log('✅ 交易已确认');

      // 解析操作结果（从事件日志中）
      let operationResult: AaveOperationResult = {
        success: false,
        outputAmounts: [],
        returnData: '0x' as Hex,
        message: '无法解析操作结果',
      };

      if (receipt.logs) {
        for (const log of receipt.logs as Array<{ topics: readonly Hex[] } & typeof receipt.logs[0]>) {
          try {
            const event = viemDecodeEventLog({
              abi: typedDefiAggregatorABI,
              data: log.data,
              topics: log.topics as [signature: Hex, ...args: Hex[]],
            });

            if (event && event.eventName === 'OperationExecuted') {
              const operationEvent = event as unknown as DecodedOperationExecutedEvent;
              operationResult = {
                success: true,
                outputAmounts: operationEvent.args.amounts,
                returnData: operationEvent.args.returnData,
                message: '取款操作成功',
              };
              console.log('✅ 解析到 OperationExecuted 事件:', operationEvent);
              break;
            }
          } catch (e) {
            console.warn('解码事件日志失败:', e);
          }
        }
      }

      set({ isOperating: false });

      const result: AaveTransactionResult = {
        hash,
        receipt,
        result: operationResult,
      };

      console.log('✅ USDT 提取操作完成');
      return result;
    } catch (error) {
      const errorMsg = error instanceof Error ? error.message : '提取 USDT 失败';
      set({ error: errorMsg, isOperating: false });
      console.error('❌ 提取 USDT 失败:', errorMsg);
      throw error;
    }
  },

  /**
   * 卖出 USDT（从 Aave 提取）
   * 实际上是调用 withdrawUSDT 的简化版本
   * @param publicClient 公共客户端
   * @param walletClient 钱包客户端
   * @param chain 链配置
   * @param amount 提取数量
   * @param account 用户地址
   */
  sellUSDT: async (
    publicClient: PublicClient,
    walletClient: WalletClient,
    chain: Chain,
    amount: bigint,
    account: Address,
    gasConfig?: {
      gas?: bigint;
      gasPrice?: bigint;
      maxFeePerGas?: bigint;
      maxPriorityFeePerGas?: bigint;
    }
  ): Promise<AaveTransactionResult> => {
    const { defiAggregatorAddress } = get();
    if (!defiAggregatorAddress) {
      throw new Error('合约地址未初始化');
    }

    try {
      set({ isOperating: true, error: null });
      console.log('💰 开始卖出 USDT（从 Aave 提取）...');
      console.log('参数:', {
        amount: amount.toString(),
        account
      });

      // 构造操作参数（提取操作）
      const operationParams: AaveOperationParams = {
        tokens: [USDT_ADDRESS], // 使用动态获取的 USDT 地址
        amounts: [amount], // 提取的 USDT 数量
        recipient: account,
        deadline: Math.floor(Date.now() / 1000) + 3600, // 1小时后过期
        tokenId: 0, // Aave 不使用 NFT，设为 0
        extraData: '0x' as Hex, // 无额外数据
      };

      console.log('📋 卖出（提取）操作参数:', operationParams);

      const hash = await walletClient.writeContract({
        address: defiAggregatorAddress,
        abi: typedDefiAggregatorABI,
        functionName: 'executeOperation',
        args: [
          'aave', // 适配器名称
          AaveOperationType.WITHDRAW, // 操作类型：WITHDRAW (1)
          operationParams
        ],
        chain,
        account,
        ...(gasConfig?.gas && { gas: gasConfig.gas }),
        ...(gasConfig?.maxFeePerGas && gasConfig?.maxPriorityFeePerGas && {
          maxFeePerGas: gasConfig.maxFeePerGas,
          maxPriorityFeePerGas: gasConfig.maxPriorityFeePerGas,
        }),
        ...(gasConfig?.gasPrice && { gasPrice: gasConfig.gasPrice }),
      } as Parameters<typeof walletClient.writeContract>[0]);

      console.log('📝 卖出（提取）交易哈希:', hash);

      console.log('⏳ 等待交易确认...');
      const receipt = await publicClient.waitForTransactionReceipt({ hash });
      console.log('✅ 交易已确认');

      // 解析操作结果（从事件日志中）
      let operationResult: AaveOperationResult = {
        success: false,
        outputAmounts: [],
        returnData: '0x' as Hex,
        message: '无法解析操作结果',
      };

      if (receipt.logs) {
        for (const log of receipt.logs as Array<{ topics: readonly Hex[] } & typeof receipt.logs[0]>) {
          try {
            const event = viemDecodeEventLog({
              abi: typedDefiAggregatorABI,
              data: log.data,
              topics: log.topics as [signature: Hex, ...args: Hex[]],
            });

            if (event && event.eventName === 'OperationExecuted') {
              const operationEvent = event as unknown as DecodedOperationExecutedEvent;
              operationResult = {
                success: true,
                outputAmounts: operationEvent.args.amounts,
                returnData: operationEvent.args.returnData,
                message: '卖出（提取）操作成功',
              };
              console.log('✅ 解析到 OperationExecuted 事件:', operationEvent);
              break;
            }
          } catch (e) {
            console.warn('解码事件日志失败:', e);
          }
        }
      }

      set({ isOperating: false });

      const result: AaveTransactionResult = {
        hash,
        receipt,
        result: operationResult,
      };

      console.log('✅ USDT 卖出（提取）操作完成');
      return result;
    } catch (error) {
      const errorMsg = error instanceof Error ? error.message : '卖出 USDT 失败';
      set({ error: errorMsg, isOperating: false });
      console.error('❌ 卖出 USDT 失败:', errorMsg);
      throw error;
    }
  },

  // ==================== 辅助方法 ====================
  /**
   * 设置加载状态
   * @param loading 是否加载中
   */
  setLoading: (loading: boolean) => {
    set({ isLoading: loading });
  },

  /**
   * 设置操作状态
   * @param operating 是否操作中
   */
  setOperating: (operating: boolean) => {
    set({ isOperating: operating });
  },

  /**
   * 设置错误信息
   * @param error 错误信息
   */
  setError: (error: string | null) => {
    set({ error: error });
  },

  /**
   * 清除错误信息
   */
  clearErrors: () => {
    set({ error: null });
  },

  /**
   * 重置状态
   */
  reset: () => {
    set({
      defiAggregatorAddress: null,
      aaveAdapterAddress: null,
      poolInfo: null,
      userBalance: null,
      isLoading: false,
      isOperating: false,
      error: null,
    });
  },
}));

export default useAaveStore;