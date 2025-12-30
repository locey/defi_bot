// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

/**
 * 多路由套利接口
 */
interface IDoubleRouterIntegration {
    
    function doubleRouterArbCheck(
        uint amountIn,
        address[] calldata path,
        address[] calldata routers
    ) external view returns(
        bool profitable,
        uint finalAmount,
        int profit
    );

    function doubleRouterSwap(
        address spot,
        address tokenIn,
        address tokenOut,
        uint256 amountIn,
        address[] calldata swapPath,
        address[] calldata dexes,
        uint256 expectProfit,
        uint256 minProfit
    ) external returns(uint256 amountOut);
}