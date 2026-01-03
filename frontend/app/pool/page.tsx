"use client";

import { useState, useEffect, useMemo } from "react";
import { useTokenFactoryWithClients } from "@/lib/hooks/useTokenFactoryWithClients";
import { useWallet } from "yc-sdk-ui";
import { formatUnits, parseUnits } from "viem";
import { Button } from "@/components/ui/button";
import BuyModal from "@/components/BuyModal";
import { SellModal } from "@/components/SellModal";
import TokenVirtualList from "@/components/TokenVirtualList";
import TokenCardSkeleton from "@/components/TokenCardSkeleton";
import { useToast } from "@/hooks/use-toast";
import {
  formatNumber,
  formatPrice,
  formatPercent,
  formatMarketCap,
} from "@/lib/utils/format";
import useTokenFactoryStore from "@/lib/stores/useTokenFactoryStore";
import { TokenData } from "@/types/token";
import { getContractAddresses } from "@/lib/utils/contracts";

const { ORACLE_AGGREGATOR_ADDRESS, USDT_ADDRESS } = getContractAddresses();

// 分别定义 BuyModal 和 SellModal 的状态
interface BuyModalState {
  isOpen: boolean;
  token: TokenData | null;
}

interface SellModalState {
  isOpen: boolean;
  token: TokenData | null;
}

export default function TokenPool() {
  const { toast } = useToast();

  const walletState = useWallet();
  const { isConnected, address } = walletState;
  const { fetchTokensInfo } = useTokenFactoryWithClients();

  // 直接从store获取数据
  const storeAllTokens = useTokenFactoryStore((state) => state.allTokens);

  // 加载状态管理
  const [isLoading, setIsLoading] = useState(true);
  const [isInitialLoad, setIsInitialLoad] = useState(true);

  const [searchTerm, setSearchTerm] = useState("");
  const [sortBy, setSortBy] = useState<"marketCap" | "volume" | "price">(
    "marketCap"
  );
  const [sortOrder, setSortOrder] = useState<"asc" | "desc">("desc");
  const [buyModal, setBuyModal] = useState<BuyModalState>({
    isOpen: false,
    token: null,
  });
  const [sellModal, setSellModal] = useState<SellModalState>({
    isOpen: false,
    token: null,
  });

  // 优化数据转换逻辑 - 添加缓存和错误处理
  const tokens = useMemo(() => {
    if (!storeAllTokens || storeAllTokens.length === 0) {
      return [];
    }

    const convertedTokens = storeAllTokens
      .map((tokenInfo) => {
        try {
          // 安全转换用户余额
          let userBalance = 0;
          if (typeof tokenInfo.userBalance === "bigint") {
            const formattedBalance = formatUnits(
              tokenInfo.userBalance,
              tokenInfo.decimals
            );
            const rawBalance = Number(formattedBalance);
            userBalance = isFinite(rawBalance) ? rawBalance : 0;
          }

          const price = Number(
            formatUnits(tokenInfo.price, tokenInfo.decimals)
          );
          const volume24h = Number(
            formatUnits(tokenInfo.volume24h, tokenInfo.decimals)
          );
          const marketCap = Number(
            formatUnits(tokenInfo.marketCap, tokenInfo.decimals)
          );
          const totalSupply = Number(
            formatUnits(tokenInfo.totalSupply, tokenInfo.decimals)
          );

          return {
            symbol: tokenInfo.symbol,
            name: tokenInfo.name,
            address: tokenInfo.address as `0x${string}`,
            price,
            change24h: tokenInfo.change24h,
            volume24h,
            marketCap,
            totalSupply,
            userBalance,
            userValue: userBalance * price,
          };
        } catch (error) {
          console.error(`代币数据转换失败: ${tokenInfo.symbol}`, error);
          return null;
        }
      })
      .filter((token): token is TokenData => token !== null);

    return convertedTokens;
  }, [storeAllTokens]);

  // 初始化数据获取（只执行一次）
  useEffect(() => {
    const initializeData = async () => {
      setIsLoading(true);

      try {
        await fetchTokensInfo();
      } catch (error) {
        console.error("获取代币信息失败:", error);
        toast({
          title: "数据加载失败",
          description: "无法获取代币信息，请稍后重试",
          variant: "destructive",
        });
      } finally {
        setIsLoading(false);
        setIsInitialLoad(false);
      }
    };

    initializeData();
  }, [fetchTokensInfo, toast]);

  // 排序和过滤代币
  const filteredAndSortedTokens = useMemo(() => {
    return tokens
      .filter(
        (token) =>
          token.symbol.toLowerCase().includes(searchTerm.toLowerCase()) ||
          token.name.toLowerCase().includes(searchTerm.toLowerCase())
      )
      .sort((a, b) => {
        let aValue: number, bValue: number;

        switch (sortBy) {
          case "marketCap":
            aValue = a?.marketCap || 0;
            bValue = b?.marketCap || 0;
            break;
          case "volume":
            aValue = a?.volume24h || 0;
            bValue = b?.volume24h || 0;
            break;
          case "price":
            aValue = a?.price || 0;
            bValue = b?.price || 0;
            break;
          default:
            aValue = a?.marketCap || 0;
            bValue = b?.marketCap || 0;
        }

        return sortOrder === "asc" ? aValue - bValue : bValue - aValue;
      });
  }, [tokens, searchTerm, sortBy, sortOrder]);

  // 打开买入界面
  const openBuyModal = (token: TokenData) => {
    console.log("🚀 openBuyModal 调用:", {
      isConnected,
      address,
      tokenSymbol: token.symbol,
      addressType: typeof address,
      addressLength: address?.length,
      isConnectedType: typeof isConnected,
    });

    // 更严格的连接状态检查
    const isActuallyConnected =
      isConnected &&
      address &&
      address !== "0x0000000000000000000000000000000000000000";

    console.log("🔍 openBuyModal 连接状态检查:", {
      isConnected,
      address,
      isActuallyConnected,
    });

    if (!isActuallyConnected) {
      console.log("❌ 钱包未连接或无有效地址，阻止打开购买弹窗");
      toast({
        title: "连接钱包",
        description: "请先连接钱包后再进行交易",
        variant: "destructive",
      });
      return;
    }

    console.log("✅ 钱包连接正常，打开购买弹窗");

    // 先设置弹窗状态
    setBuyModal({
      isOpen: true,
      token,
    });

    // 初始化数据 (获取最新的 Pyth 数据等)
    console.log("🔄 打开购买弹窗时初始化交易数据...");
    // 注意：数据初始化现在在 BuyModal 组件内部处理
  };

  // 打开卖出界面
  const openSellModal = (token: TokenData) => {
    if (!isConnected) {
      toast({
        title: "连接钱包",
        description: "请先连接钱包后再进行交易",
        variant: "destructive",
      });
      return;
    }
    setSellModal({
      isOpen: true,
      token,
    });
  };

  // 关闭买入界面
  const closeBuyModal = () => {
    setBuyModal({
      isOpen: false,
      token: null,
    });
  };

  // 关闭卖出界面
  const closeSellModal = () => {
    setSellModal({
      isOpen: false,
      token: null,
    });
  };

  // 处理交易

  return (
    <div className="min-h-screen bg-black">
      {/* Header */}
      <div className="border-b border-gray-800">
        <div className="max-w-7xl mx-auto px-4 sm:px-6 lg:px-8 py-6 mt-73px">
          <div className="flex justify-between items-center">
            <div>
              <h1 className="text-3xl font-bold text-white mb-2">币股池</h1>
              <p className="text-gray-400">交易真实股票的 ERC20 代币</p>
            </div>
            <div className="text-right">
              <div className="text-sm text-gray-400">总市值</div>
              <div className="text-2xl font-bold text-white">
                {formatNumber(
                  tokens.reduce((sum, token) => sum + token.marketCap, 0)
                )}
              </div>
            </div>
          </div>
        </div>
      </div>

      {/* Search and Filter */}
      <div className="max-w-7xl mx-auto px-4 sm:px-6 lg:px-8 py-6">
        <div className="flex flex-col sm:flex-row gap-4 mb-6">
          <div className="flex-1">
            <input
              type="text"
              value={searchTerm}
              onChange={(e) => setSearchTerm(e.target.value)}
              placeholder="搜索代币..."
              className="w-full bg-gray-900 border border-gray-800 rounded-lg px-4 py-3 text-white focus:border-blue-500 focus:outline-none"
            />
          </div>
          <div className="flex gap-2">
            <select
              value={sortBy}
              onChange={(e) => setSortBy(e.target.value as any)}
              className="bg-gray-900 border border-gray-800 rounded-lg px-4 py-3 text-white focus:border-blue-500 focus:outline-none"
            >
              <option value="marketCap">市值</option>
              <option value="volume">成交量</option>
              <option value="price">价格</option>
            </select>
            <Button
              onClick={() => setSortOrder(sortOrder === "asc" ? "desc" : "asc")}
              variant="sort"
              size="sort"
            >
              {sortOrder === "asc" ? "↑" : "↓"}
            </Button>
          </div>
        </div>

        {/* 优化后的代币列表 */}
        <TokenVirtualList
          tokens={filteredAndSortedTokens}
          isLoading={isLoading}
          onBuy={openBuyModal}
          onSell={openSellModal}
        />

        {/* Empty State */}
        {!isInitialLoad &&
          filteredAndSortedTokens.length === 0 &&
          !isLoading && (
            <div className="col-span-full p-8 text-center text-gray-400">
              没有找到匹配的代币
            </div>
          )}
      </div>

      {/* Buy Modal */}
      {buyModal.isOpen && buyModal.token && (
        <BuyModal
          isOpen={buyModal.isOpen}
          onClose={closeBuyModal}
          token={{
            symbol: buyModal.token.symbol,
            name: buyModal.token.name,
            price: formatPrice(buyModal.token.price),
            change24h: buyModal.token.change24h,
            volume24h: buyModal.token.volume24h,
            marketCap: buyModal.token.marketCap,
            address: buyModal.token.address,
          }}
          oracleAddress={ORACLE_AGGREGATOR_ADDRESS as `0x${string}`}
          usdtAddress={USDT_ADDRESS as `0x${string}`}
        />
      )}

      {/* Sell Modal */}
      {sellModal.isOpen && sellModal.token && (
        <SellModal
          isOpen={sellModal.isOpen}
          onClose={closeSellModal}
          token={{
            symbol: sellModal.token.symbol,
            name: sellModal.token.name,
            price: formatPrice(sellModal.token.price),
            change24h: sellModal.token.change24h,
            volume24h: sellModal.token.volume24h,
            marketCap: sellModal.token.marketCap,
            address: sellModal.token.address as `0x${string}`,
          }}
          stockTokenAddress={sellModal.token.address}
        />
      )}
    </div>
  );
}
