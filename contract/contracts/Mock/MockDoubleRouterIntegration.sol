// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

import "../interfaces/IDoubleRouterIntegration.sol";
import "@openzeppelin/contracts/token/ERC20/IERC20.sol";
import "@openzeppelin/contracts/token/ERC20/utils/SafeERC20.sol";

interface IMockERC20 {
    function mint(address to, uint256 amount) external;
}

contract MockDoubleRouterIntegration is IDoubleRouterIntegration {
    using SafeERC20 for IERC20;

    function doubleRouterSwap(
        address spot,
        address tokenIn,
        address tokenOut,
        uint256 amountIn,
        address[] calldata /* swapPath */,
        address[] calldata /* dexes */,
        uint24[] calldata /* feeTiers */,
        uint256 expectProfit,
        uint256 /* minProfit */
    ) external returns(uint256 amountOut) {
        // 从 spot 拉取 tokenIn
        IERC20(tokenIn).safeTransferFrom(spot, address(this), amountIn);

        // 计算产出
        uint256 profit = expectProfit / 2;
        amountOut = amountIn + profit;

        // 铸造 tokenOut 并转给 spot
        IMockERC20(tokenOut).mint(address(this), amountOut);
        IERC20(tokenOut).safeTransfer(spot, amountOut);

        return amountOut;
    }

    function doubleRouterArbCheck(
        uint amountIn,
        address[] calldata /* path */,
        address[] calldata /* routers */
    ) external pure returns(
        bool profitable,
        uint finalAmount,
        int profit
    ) {
        finalAmount = amountIn * 105 / 100;
        profit = int(finalAmount) - int(amountIn);
        profitable = true;
        return (profitable, finalAmount, profit);
    }
}
