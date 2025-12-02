// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

/**
 * 接口抽象
 */
interface IExchange {
    
    // 单路由验证是否可获利
    function routerArbCheck(
        address router,
        uint amountIn,
        address[] calldata path) 
    external view returns (
        bool profitable,
        uint finalAmount,
        int profit
    );
    
    // 多路由验证是否可获利
    function multiRouterArbCheck(
        uint amountIn,
        address[] calldata path,
        address[] calldata routers
    ) external view returns(
        bool profitable,
        uint finalAmount,
        int profit
    );

    // 单路由单路径交易
    function swapV2(
        address router, 
        address token0, 
        address token1, 
        uint amountIn,
        address platForm) 
    external returns(uint amountOut);

    // 单路由多路径交易
    function swapV2Multi(
        address router,
        address[][] memory paths,
        uint amountIn,
        address platForm) 
    external returns (uint amountOut);
}