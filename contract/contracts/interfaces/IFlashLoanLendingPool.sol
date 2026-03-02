// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;
// 引入 Aave V2 的闪电贷接口（平台提供）利息0.09%      v3的利息0.05%
interface ILendingPool {
    function flashLoanSimple(
        address receiver,   // 借款接收者
        address asset,      // 借款资产
        uint256 amount,     // 借款金额
        bytes calldata params, // 额外参数
        uint16 referralCode // 推广码
    ) external;
}