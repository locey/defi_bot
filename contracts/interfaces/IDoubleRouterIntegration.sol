// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

/**
 * 多路由套利接口
 */
interface IDoubleRouterIntegration {
    
    event DoubleRouterSwap(
        address indexed sender, 
        address indexed routerA, 
        address indexed routerB, 
        uint256 amountIn, 
        uint256 profit
    );

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
        address routerA,
        address routerB,
        uint256 amountIn,
        address[] calldata pathA,
        address[] calldata pathB,
        uint256 minProfit
    ) external;
}