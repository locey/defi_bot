// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;


//实现套利合约接口
interface ISpotArbitrage {
    function executeSwaps(
        address asset,
        address tokenOut,
        uint256 amountIn,
        address[] calldata swapPath,
        address[] calldata dexes,
        uint256 expectProfit,
        uint256 minProfit
    ) external returns(uint256 amountOut);
}