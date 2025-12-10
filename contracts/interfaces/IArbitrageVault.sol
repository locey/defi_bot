// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

interface IArbitrageVault {
    
    //查询金库总资产
    function totalAssets() external view returns (uint256);
    
    //获取可用于套利的资金（保留一部分流动性）
    function getAvailableForArbitrage() external view returns (uint256);

    //为套利授权资金（由ArbitrageCore调用）
    function approveForArbitrage(uint256 amount) external;

    //记录套利利润（由ArbitrageCore调用）
    function recordProfit(uint256 profit) external;

    //记录套利亏损（理论上不应该发生，有minProfit保护）
    function recordLoss(uint256 loss) external;

    //返回金库管理的底层资产地址
    function assetAddress() external view returns (address);
}

