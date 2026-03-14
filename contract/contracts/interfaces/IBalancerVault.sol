// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

import "@openzeppelin/contracts/token/ERC20/IERC20.sol";

/**
 * @title IBalancerVault
 * @notice Balancer V2 Vault 闪电贷接口
 * @dev Vault 地址（所有链相同）: 0xBA12222222228d8Ba445958a75a0704d566BF2C8
 *      闪电贷费率: 0%（免费）
 */
interface IBalancerVault {
    /**
     * @dev 发起闪电贷（支持多 token）
     * @param recipient 接收闪电贷的合约（需实现 IFlashLoanRecipient）
     * @param tokens 借入的 token 列表（必须按地址升序排列，无重复）
     * @param amounts 每个 token 的借入数量
     * @param userData 传递给回调的自定义数据
     */
    function flashLoan(
        IFlashLoanRecipient recipient,
        IERC20[] memory tokens,
        uint256[] memory amounts,
        bytes memory userData
    ) external;
}

/**
 * @title IFlashLoanRecipient
 * @notice Balancer V2 闪电贷回调接口
 * @dev 合约需实现此接口才能接收 Balancer 闪电贷
 */
interface IFlashLoanRecipient {
    /**
     * @dev Vault 在转入 tokens 后调用此函数
     *      函数返回前，必须将 amounts + feeAmounts 转回给 Vault
     * @param tokens 借入的 token 列表
     * @param amounts 每个 token 的借入数量
     * @param feeAmounts 每个 token 的手续费（当前为 0）
     * @param userData 自定义数据（从 flashLoan 调用传入）
     */
    function receiveFlashLoan(
        IERC20[] memory tokens,
        uint256[] memory amounts,
        uint256[] memory feeAmounts,
        bytes memory userData
    ) external;
}
