// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

/**
 * swap实现业务合约
 */
interface IUniswapV2Integration {

    function routerArbCheck(
        address router,
        uint256 amountIn,
        address[] calldata path) 
    external view returns (
        bool profitable,
        uint256 finalAmount,
        int profit
    );

    function swapV2(
        address router, 
        address token0, 
        address token1, 
        uint256 amountIn,
        uint256 minProfit
    ) external;

    function swapV2Multi(
        address router,
        address[][] calldata paths,
        uint256 amountIn,
        uint256 minProfit
    ) external;
}