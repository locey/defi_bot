// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

import "@openzeppelin/contracts/token/ERC20/IERC20.sol";
import "@openzeppelin/contracts/token/ERC20/utils/SafeERC20.sol";

/**
 * @title MockLendingPool
 * @dev 模拟借贷池，用于测试环境中的闪电贷功能
 */
contract MockLendingPool {
    using SafeERC20 for IERC20;

    // 事件定义
    event FlashLoanSimple(
        address indexed receiver,
        address indexed asset,
        uint256 amount,
        uint256 premium,
        address indexed initiator
    );

    // 0.05% 作为闪贷款利息
    uint256 public constant FLASHLOAN_PREMIUM_BPS = 5; // 0.05% (basis points)
    uint256 public constant BPS_BASE = 10000; // Basis points base (100%)

    /**
     * @notice 执行闪电贷操作
     * @param receiver 接收闪电贷资金的合约地址
     * @param asset 申请借贷的资产地址
     * @param amount 申请借贷的数量
     * @param params 额外参数，将传递给接收方的回调函数
     * @param referralCode 推荐码（可选）
     */
    function flashLoanSimple(
        address receiver,
        address asset,
        uint256 amount,
        bytes calldata params,
        uint16 referralCode
    ) external {
        // 计算闪贷款利息（0.05%）
        uint256 premium = (amount * FLASHLOAN_PREMIUM_BPS) / BPS_BASE;

        // 将资金转移给接收方
        IERC20(asset).safeTransfer(receiver, amount);

        // 调用接收方的回调函数
        (bool success, bytes memory result) = receiver.call(
            abi.encodeWithSignature(
                "executeOperation(address,uint256,uint256,address,bytes)",
                asset,
                amount,
                premium,
                msg.sender, // initiator
                params
            )
        );

        if (!success) {
            // 如果调用失败，尝试解析错误信息
            if (result.length > 0) {
                assembly {
                    revert(add(32, result), mload(result))
                }
            } else {
                revert("MockLendingPool: flash loan operation failed");
            }
        }

        // 验证接收方已偿还本金加利息
        uint256 totalAmount = amount + premium;
        IERC20(asset).safeTransferFrom(receiver, address(this), totalAmount);

        emit FlashLoanSimple(receiver, asset, amount, premium, msg.sender);
    }

    /**
     * @notice 获取闪贷款利息费率（以基点表示）
     */
    function getFlashLoanPremiumTotal() external pure returns (uint256) {
        return FLASHLOAN_PREMIUM_BPS;
    }

    /**
     * @notice 设置合约拥有者可以提取多余的代币
     * @param token 要提取的代币地址
     * @param to 接收地址
     * @param amount 提取数量
     */
    function rescueTokens(
        address token,
        address to,
        uint256 amount
    ) external {
        IERC20(token).safeTransfer(to, amount);
    }

    /**
     * @notice 合约接收代币
     */
    receive() external payable {}
}