// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

/**
 * swap实现业务合约
 */
interface IUniswapV2Integration {
    event SwapV2(address indexed router, address indexed token0, address indexed token1, uint amountIn, uint amountOut, uint expectedOut, uint256 slipageTolerance);
    event SwapV2Multi(address indexed router, address[][] paths, uint amountIn, uint amountOut);

    function routerArbCheck(
        address router,
        uint amountIn,
        address[] calldata path) 
    external view returns (
        bool profitable,
        uint finalAmount,
        int profit
    );

    function swapV2(
        address router, 
        address token0, 
        address token1, 
        uint amountIn
    ) external;

    function swapV2Multi(
        address router,
        address[][] calldata paths,
        uint amountIn
    ) external;
}