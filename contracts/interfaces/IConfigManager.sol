// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

interface IConfigManager {
    function profitShareFee() external view returns(uint256);  //ArbitrageCore使用，平台分成配置
}