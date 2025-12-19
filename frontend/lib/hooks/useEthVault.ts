import { useState, useCallback, useEffect, useMemo } from "react";
import { Address, Abi, Chain, parseEther, parseUnits, formatUnits } from "viem";
import { usePublicClient, useWalletClient } from "@/hooks/usePublicClient";
import ArbitrageVaultArtifact from "@/lib/abi/ArbitrageVault.json";
import  ERC20Artifact from "@/lib/abi/MockERC20.json";

// 从Hardhat artifact中提取abi数组，并断言为viem的Abi类型
const ArbitrageVaultABI = (Array.isArray(ArbitrageVaultArtifact.abi) 
  ? ArbitrageVaultArtifact.abi 
  : []) as Abi;
const ERC20ABI = (Array.isArray(ERC20Artifact.abi) 
  ? ERC20Artifact.abi 
  : []) as Abi;
type TxResult = {
  hash: `0x${string}`;
  receipt: { status: "success" | "reverted" } | any;
};

type DepositConfig = {
  abi?: Abi;
  functionName?: string;
  args?: any[];
};

type WithdrawConfig = {
  abi: Abi;
  functionName: string;
  buildArgs?: (amountWei: bigint) => any[];
};

type Erc20Config = {
  assetAddress?: Address;
  decimals?: number; // default 18
};

export function useEthVault(
  vaultAddress: Address,
  opts?: { deposit?: DepositConfig; withdraw?: WithdrawConfig; erc20?: Erc20Config; sharesDecimals?: number }
) {
  const { publicClient, chain } = usePublicClient();
  const { walletClient } = useWalletClient();
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [principal, setPrincipal] = useState<number>(0);
  const [currentAssets, setCurrentAssets] = useState<number>(0);
  const [totalProfit, setTotalProfit] = useState<number>(0);
  const [apy, setApy] = useState<number>(0);
  const [walletEth, setWalletEth] = useState<number>(0);
  const owner: Address | undefined = useMemo(() => {
    const acc = (walletClient as any)?.account;
    const addr = acc?.address ?? acc;
    return addr as Address | undefined;
  }, [walletClient]);
  const assetDecimals = opts?.erc20?.decimals ?? 18;

  const deposit = useCallback(async (amountEth: string | number): Promise<TxResult> => {
    if (!walletClient || !publicClient || !chain) throw new Error("钱包未连接");
    setLoading(true);
    setError(null);
    const value = parseEther(String(amountEth));
    try {
      let hash: `0x${string}`;
      // ERC20 模式：自动 approve 然后调用 vault.deposit(assets, receiver)
      if (opts?.erc20) {
        const decimals = opts.erc20.decimals ?? 18;
        const assetsWei = parseUnits(String(amountEth), decimals);
        // 获取资产地址：优先使用传入，否则从 vault 读取
        const assetAddr: Address = opts.erc20.assetAddress
          ? opts.erc20.assetAddress
          : (await publicClient.readContract({
              address: vaultAddress,
              abi: ArbitrageVaultABI,
              functionName: "assetAddress",
            })) as Address;

        // 检查并授权
        const owner = ((walletClient as any).account?.address ?? (walletClient as any).account) as Address;
        const allowance = (await publicClient.readContract({
          address: assetAddr,
          abi: ERC20ABI,
          functionName: "allowance",
          args: [owner, vaultAddress],
        })) as bigint;

        if (allowance < assetsWei) {
          const { request } = await publicClient.simulateContract({
            address: assetAddr,
            abi: ERC20ABI,
            functionName: "approve",
            args: [vaultAddress, assetsWei],
            chain: chain as Chain,
            account: owner,
          } as any);
          await walletClient.writeContract(request);
        }

        // 调用存款
        {
          const { request } = await publicClient.simulateContract({
            address: vaultAddress,
            abi: ArbitrageVaultABI,
            functionName: "deposit",
            args: [assetsWei, owner],
            chain: chain as Chain,
            account: owner,
          } as any);
          hash = await walletClient.writeContract(request);
        }
      } else if (opts?.deposit?.abi && opts.deposit.functionName) {
        const isPayable = Array.isArray(opts.deposit.abi)
          ? !!(opts.deposit.abi as any[]).find(
              (f) => f?.type === "function" && f?.name === opts.deposit!.functionName && f?.stateMutability === "payable"
            )
          : false;

        const owner = ((walletClient as any).account?.address ?? (walletClient as any).account) as Address;
        const { request } = await publicClient.simulateContract({
          address: vaultAddress,
          abi: opts.deposit.abi,
          functionName: opts.deposit.functionName as any,
          args: opts.deposit.args || [],
          chain: chain as Chain,
          account: owner,
          ...(isPayable ? { value } : {}),
        } as any);
        hash = await walletClient.writeContract(request);
      } else {
        hash = await walletClient.sendTransaction({
          to: vaultAddress,
          value,
          chain: chain as Chain,
          account: ((walletClient as any).account?.address ?? (walletClient as any).account) as Address,
        } as any);
      }
      const receipt = await publicClient.waitForTransactionReceipt({ hash });
      setLoading(false);
      return { hash, receipt };
    } catch (e: any) {
      setLoading(false);
      setError(e?.message || String(e));
      throw e;
    }
  }, [walletClient, publicClient, chain, vaultAddress, opts?.deposit, opts?.erc20]);

  const withdraw = useCallback(async (amountEth?: string | number): Promise<TxResult> => {
    if (!walletClient || !publicClient || !chain) throw new Error("钱包未连接");
    setLoading(true);
    setError(null);
    const isErc20 = !!opts?.erc20;
    const decimals = opts?.erc20?.decimals ?? 18;
    const amountWei = amountEth !== undefined ? (isErc20 ? parseUnits(String(amountEth), decimals) : parseEther(String(amountEth))) : undefined;
    try {
      let hash: `0x${string}`;
      if (isErc20 && amountWei !== undefined) {
        const owner = ((walletClient as any).account?.address ?? (walletClient as any).account) as Address;
        const { request } = await publicClient.simulateContract({
          address: vaultAddress,
          abi: ArbitrageVaultABI as Abi,
          functionName: "withdraw",
          args: [amountWei, owner, owner],
          chain: chain as Chain,
          account: owner,
        } as any);
        hash = await walletClient.writeContract(request);
      } else if (opts?.withdraw) {
        const owner = ((walletClient as any).account?.address ?? (walletClient as any).account) as Address;
        const args = opts.withdraw.buildArgs && amountWei !== undefined ? opts.withdraw.buildArgs(amountWei) : [];
        const { request } = await publicClient.simulateContract({
          address: vaultAddress,
          abi: opts.withdraw.abi,
          functionName: opts.withdraw.functionName as any,
          args: args as any,
          chain: chain as Chain,
          account: owner,
        } as any);
        hash = await walletClient.writeContract(request);
      } else {
        throw new Error("未配置提取方法");
      }
      const receipt = await publicClient.waitForTransactionReceipt({ hash });
      setLoading(false);
      return { hash, receipt };
    } catch (e: any) {
      setLoading(false);
      setError(e?.message || String(e));
      throw e;
    }
  }, [walletClient, publicClient, chain, vaultAddress, opts?.withdraw, opts?.erc20]);

  const refreshStats = useCallback(async () => {
    if (!publicClient || !owner) return;
    try {
      if (vaultAddress) {
        const [principalWei, assetsWei] = await Promise.all([
          publicClient.readContract({
            address: vaultAddress,
            abi: ArbitrageVaultABI as Abi,
            functionName: "getUserBalance",
            args: [owner],
          }) as Promise<bigint>,
          publicClient.readContract({
            address: vaultAddress,
            abi: ArbitrageVaultABI as Abi,
            functionName: "assetsOf",
            args: [owner],
          }) as Promise<bigint>,
        ]);
        const p = parseFloat(formatUnits(principalWei as bigint, assetDecimals));
        const c = parseFloat(formatUnits(assetsWei as bigint, assetDecimals));
        setPrincipal(p);
        setCurrentAssets(c);
        
        // 计算收益总额：当前资产 - 本金
        const profit = c - p;
        setTotalProfit(profit > 0 ? profit : 0);

        // 计算 APY：这里简单估算，假设当前收益是最近一次套利产生的，实际应根据时间加权
        // 为了演示，这里假设如果 profit > 0，则 APY = (profit / principal) * 365 * 100 (假设是一天的收益)
        // 实际上合约应该提供更准确的 APY 数据，这里暂时用简单的收益率代替
        if (p > 0) {
            setApy((profit / p) * 100); 
        } else {
            setApy(0);
        }
      }
      const bal = await publicClient.getBalance({ address: owner });
      setWalletEth(parseFloat(formatUnits(bal, 18)));
    } catch {}
  }, [publicClient, owner, vaultAddress, assetDecimals]);

  useEffect(() => {
    refreshStats();
    const id = setInterval(refreshStats, 5000);
    return () => clearInterval(id);
  }, [refreshStats]);

  return { deposit, withdraw, loading, error, principal, currentAssets, totalProfit, apy, walletEth, refreshStats };
}
