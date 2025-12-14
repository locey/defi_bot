// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

// 引入 Aave V2 的闪电贷接口（平台提供）利息0.09%      v3的利息0.05%
interface IFlashLoanSimpleReceiver {
    // 核心回调函数：LendingPool 放款后，会自动调用这个函数
    function executeOperation(
        address asset,      // 借款的资产（如 USDT 地址）
        uint256 amount,     // 借款金额
        uint256 premium,    // 闪电贷手续费（平台收取，通常是借款额的 0.05%~0.3%）
        address initiator,  // 借款发起者（我们的合约）
        bytes calldata params  // 额外参数（可传递套利需要的信息）
    ) external returns (bool); // 返回 true 表示操作成功，准备还款
}
