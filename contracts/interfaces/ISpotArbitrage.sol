// SPDX-License-Identifier: MIT
pragam solidity ^0.8.20;


//实现套利合约接口
interface ISpotArbitrage {
    function excuteSwaps(
        address tokenIn,
        address tokenOut,
        uint256 amountIn,
        address[] swapPath,
        address[] dexes
    ) external returns(uint256 amountOut);
}