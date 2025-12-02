// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

/**
 * 接口抽象
 */
interface IExchange {
    
function routerArbCheck(
        address router,
        uint amountIn,
        address[] calldata path) 
    external view returns (
        bool profitable,
        uint finalAmount,
        int profit);
    
    function multiRouterArbCheck(
        uint amountIn,
        address[] calldata path,
        address[] calldata routers
    ) external view returns(
        bool profitable,
        uint finalAmount,
        int profit
    );

    function swapV2(
        address router, 
        address token0, 
        address token1, 
        uint amountIn,
        address platForm) 
    external returns(uint amountOut);

    function swapV2Multi(
        address router,
        address[][] memory paths,
        uint amountIn,
        address platForm) 
    external returns (uint amountOut);
}