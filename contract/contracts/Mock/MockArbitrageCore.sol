// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

import "@openzeppelin/contracts/token/ERC20/IERC20.sol";
import "@openzeppelin/contracts/token/ERC20/utils/SafeERC20.sol";

contract MockArbitrageCore {
    using SafeERC20 for IERC20;
    
    // 接收从 SpotArbitrage 转来的代币
    function onArbitrageCompleted(address token, uint256 amount) external {
        IERC20(token).safeTransfer(msg.sender, amount);
    }
    
    // 允许其他合约转代币回来（用于测试）
    function executeCallback(
        address token,
        uint256 amount
    ) external {
        IERC20(token).safeTransfer(msg.sender, amount);
    }
    
    // 接收 ETH（如果有）
    receive() external payable {}
}
