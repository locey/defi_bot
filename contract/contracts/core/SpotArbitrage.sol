// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

import "../interfaces/ISpotArbitrage.sol";
import "../interfaces/IDoubleRouterIntegration.sol";
import "../interfaces/IUniswapV2Integration.sol";

    
    function executeSwaps(
        address asset,
        address tokenOut,
        uint256 amountIn,
        address[] calldata swapPath,
        address[] calldata dexes,
        uint256 expectProfit,
        uint256 minProfit
    ) external onlyArbitrageCore returns (uint256 amountOut) {
        // 检查金额是否已收到
        require(
            IERC20(asset).balanceOf(address(this)) >= amountIn,
            "Spot: no received"
        );
        IERC20(asset).approve(address(doubleRouterIntegration), amountIn);


        // 执行套利操作
        amountOut = doubleRouterIntegration.doubleRouterSwap(
            address(this),
            asset,
            tokenOut,
            amountIn,
            swapPath,
            dexes,
            expectProfit,
            minProfit
        );

        // 将结果转回 ArbitrageCore
        IERC20(asset).safeTransfer(address(arbitrageCore), amountOut);

        return amountOut;
    }

    function _authorizeUpgrade(address newImplementation) internal override onlyOwner {}
}