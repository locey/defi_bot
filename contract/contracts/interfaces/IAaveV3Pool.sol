// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

/**
 * Aave V3 Pool 接口 — 仅包含清算和闪电贷所需方法
 * Arbitrum: 0x794a61358D6845594F94dc1DB02A252b5b4814aD
 */
interface IAaveV3Pool {
    /**
     * @dev 清算不健康仓位
     * @param collateralAsset 要接收的抵押品资产
     * @param debtAsset 要偿还的债务资产
     * @param user 被清算用户地址
     * @param debtToCover 要偿还的债务数量
     * @param receiveAToken true=接收 aToken，false=接收底层资产
     */
    function liquidationCall(
        address collateralAsset,
        address debtAsset,
        address user,
        uint256 debtToCover,
        bool receiveAToken
    ) external;

    /**
     * @dev 简单闪电贷（单资产）
     */
    function flashLoanSimple(
        address receiverAddress,
        address asset,
        uint256 amount,
        bytes calldata params,
        uint16 referralCode
    ) external;

    /**
     * @dev 获取用户账户数据
     * @return totalCollateralBase 总抵押品（以 base currency 计）
     * @return totalDebtBase 总债务
     * @return availableBorrowsBase 可借额度
     * @return currentLiquidationThreshold 清算阈值
     * @return ltv 贷款价值比
     * @return healthFactor 健康因子（< 1e18 时可被清算）
     */
    function getUserAccountData(address user) external view returns (
        uint256 totalCollateralBase,
        uint256 totalDebtBase,
        uint256 availableBorrowsBase,
        uint256 currentLiquidationThreshold,
        uint256 ltv,
        uint256 healthFactor
    );

    /**
     * @dev 获取资产储备配置数据
     */
    function getReserveData(address asset) external view returns (ReserveData memory);

    struct ReserveData {
        // 储备配置（位图）
        uint256 configuration;
        // 流动性指数（ray）
        uint128 liquidityIndex;
        uint128 currentLiquidityRate;
        uint128 variableBorrowIndex;
        uint128 currentVariableBorrowRate;
        uint128 currentStableBorrowRate;
        uint40 lastUpdateTimestamp;
        uint16 id;
        address aTokenAddress;
        address stableDebtTokenAddress;
        address variableDebtTokenAddress;
        address interestRateStrategyAddress;
        uint128 accruedToTreasury;
        uint128 unbacked;
        uint128 isolationModeTotalDebt;
    }
}
