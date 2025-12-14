// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

import "../interfaces/ISpotArbitrage.sol";
import "../interfaces/IDoubleRouterIntegration.sol";
import "../interfaces/IUniswapV2Integration.sol";



contract SpotArbitrage is ISpotArbitrage{

    IDoubleRouterIntegration private doubleRouterIntegration;
    IUniswapV2Integration private uniswapV2Integration;

    constructor(address _doubleRouterIntegration, address _uniswapV2Integration) {
        doubleRouterIntegration = IDoubleRouterIntegration(_doubleRouterIntegration);
        uniswapV2Integration = IUniswapV2Integration(_uniswapV2Integration);
    }

    /**
    *实施AMM-AMM套利的实现合约
    *对后端提供的策略（包含代币转换路径和交易所）循环遍历并进行swap
    *
    *
    *
    *本合约被ArbitrageCore调用
    */
    function executeSwaps(
        address tokenIn,
        address tokenOut,
        uint256 amountIn,
        address[] calldata swapPath,
        address[] calldata dexes,
        uint256 /* minProfit */
    ) external returns(uint256 amountOut) {
        // minProfit parameter is part of the ISpotArbitrage interface for checks at higher level;
        // this implementation delegates swaps to doubleRouterIntegration and ignores minProfit here.
        return doubleRouterIntegration.doubleRouterSwap2(address(this), tokenIn, tokenOut, amountIn, swapPath, dexes);
    }
}