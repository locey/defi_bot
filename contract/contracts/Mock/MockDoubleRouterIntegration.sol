// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

import "../interfaces/IDoubleRouterIntegration.sol";
import "@openzeppelin/contracts/token/ERC20/IERC20.sol";
import "@openzeppelin/contracts/token/ERC20/utils/SafeERC20.sol";

contract MockDoubleRouterIntegration is IDoubleRouterIntegration {
    using SafeERC20 for IERC20;
    
    function doubleRouterSwap(
        address spot,
        address tokenIn,
        address tokenOut,
        uint256 amountIn,
        address[] calldata swapPath,
        address[] calldata dexes,
        uint256 expectProfit,
        uint256 minProfit
    ) external returns(uint256 amountOut) {
        // Mock 逻辑：直接从 spot 合约获取代币，然后返回更多代币
        // 不需要 transferFrom，因为这是 Mock 合约
        // 我们假设 spot 合约已经有足够的代币余额
        
        // 计算利润
        uint256 profit = expectProfit / 2;
        amountOut = amountIn + profit;
        
        // 直接将 tokenOut 转给 spot 合约
        // 注意：这个 Mock 合约实际上没有 tokenOut，它只是模拟返回结果
        // 在真实场景中，这里会执行真实的 DEX 交换
        
        return amountOut;
    }
    
    function doubleRouterArbCheck(
        uint amountIn,
        address[] calldata path,
        address[] calldata routers
    ) external pure returns(
        bool profitable,
        uint finalAmount,
        int profit
    ) {
        // 总是返回盈利
        finalAmount = amountIn * 105 / 100; // 5%利润
        profit = int(finalAmount) - int(amountIn);
        profitable = true;
        
        return (profitable, finalAmount, profit);
    }
}
