// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

/**
 * @title ICompoundV3Comet
 * @notice Compound V3 (Comet) 接口 — 仅清算所需方法
 *
 * Arbitrum 市场地址：
 *   USDC:   0x9c4ec768c28520B50860ea7a15bd7213a9fF58bf
 *   USDC.e: 0xA5EDBDD9646f8dFF606d7448e414884C7d905dCA
 *   USDT:   0xd98Be00b5D27fc98112BdE293e487f8D4cA57d07
 *   WETH:   0x6f7D514bbD4aFf3BcD1140B7344b32f063dEe486
 *
 * 清算流程（两步式）：
 *   1. absorb() — 协议没收水下仓位的全部抵押品，偿还债务
 *   2. buyCollateral() — 以折扣价从协议手中买入被没收的抵押品
 */
interface ICompoundV3Comet {
    /// @notice 没收水下仓位（全量，不可部分清算）
    /// @param absorber 吸收者地址（用于 gas 补偿追踪）
    /// @param accounts 要清算的账户列表
    function absorb(address absorber, address[] calldata accounts) external;

    /// @notice 以折扣价买入协议持有的被没收抵押品
    /// @param asset 要购买的抵押品地址
    /// @param minAmount 最小接收抵押品数量（滑点保护）
    /// @param baseAmount 支付的 base asset 数量
    /// @param recipient 接收抵押品的地址
    function buyCollateral(
        address asset,
        uint256 minAmount,
        uint256 baseAmount,
        address recipient
    ) external;

    /// @notice 查询账户是否可被清算
    function isLiquidatable(address account) external view returns (bool);

    /// @notice 查询账户借款余额
    function borrowBalanceOf(address account) external view returns (uint256);

    /// @notice 查询账户某资产的抵押品余额
    function collateralBalanceOf(address account, address asset) external view returns (uint128);

    /// @notice 预估给定 baseAmount 能买到多少抵押品
    function quoteCollateral(address asset, uint256 baseAmount) external view returns (uint256);

    /// @notice 获取资产信息
    function getAssetInfoByAddress(address asset) external view returns (AssetInfo memory);

    /// @notice 获取资产数量
    function numAssets() external view returns (uint8);

    /// @notice 按索引获取资产信息
    function getAssetInfo(uint8 i) external view returns (AssetInfo memory);

    /// @notice 获取 base token 地址
    function baseToken() external view returns (address);

    /// @notice 获取 base token 精度缩放
    function baseScale() external view returns (uint256);

    /// @notice 获取协议储备
    function getReserves() external view returns (int256);

    /// @notice 获取目标储备
    function targetReserves() external view returns (uint256);

    struct AssetInfo {
        uint8 offset;
        address asset;
        address priceFeed;
        uint64 scale;
        uint64 borrowCollateralFactor;
        uint64 liquidateCollateralFactor;
        uint64 liquidationFactor;
        uint128 supplyCap;
    }
}
