// SPDX-License-Identifier: MIT
pragam solidity ^0.8.20;

import "./interface/IFlashLoan.sol";

contract FlashLoanRouter {
    enum LendingPlatForm {Aave_V2, Aave_V3};

    struct PlatFormConfig {
        address lendingPool;// LendingPool 地址
        uint16 referralCode; // 推广码（通常为 0）
        uint256 maxLoanRatio; // 最大借款比例（资金池余额的 80%，防止借光）
    }



}