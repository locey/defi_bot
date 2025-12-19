// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

interface IConfigManager {
    function profitShareFee() external view returns(uint256);  //ArbitrageCore使用，平台分成配置

    function getDepositFee(address vault) external view returns(uint256); //金库合约 获取存入手续费
    function getWithDrawFee(address vault) external view returns(uint256); //金库合约 获取提现手续费
    function getPlatFormFee(address vault) external view returns(uint256); //金库合约 获取平台服务费
    
}