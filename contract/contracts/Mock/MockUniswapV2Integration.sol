// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

import "../interfaces/IUniswapV2Integration.sol";

contract MockUniswapV2Integration is IUniswapV2Integration {
    
    function routerArbCheck(
        address /*router*/,
        uint256 amountIn,
        address[] calldata /*path*/
    ) external pure returns (
        bool profitable,
        uint256 finalAmount,
        int profit
    ) {
        // 总是返回盈利
        finalAmount = amountIn * 105 / 100; // 5%利润
        profit = int(finalAmount) - int(amountIn);
        profitable = true;
        
        return (profitable, finalAmount, profit);
    }
    
    function swapV2(
        address router, 
        address token0, 
        address token1, 
        uint256 amountIn,
        uint256 minProfit
    ) external {
        // Mock实现，不做任何操作
    }
    
    function swapV2Multi(
        address router,
        address[][] calldata paths,
        uint256 amountIn,
        uint256 minProfit
    ) external {
        // Mock实现，不做任何操作
    }
}
