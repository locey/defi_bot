// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

import "../interfaces/ISpotArbitrage.sol";
import "../integrations/DoubleRouterIntegration.sol";
import "../integrations/UniswapV2Integration.sol";

import "@openzeppelin/contracts/token/ERC20/utils/SafeERC20.sol";
import "@openzeppelin/contracts/token/ERC20/IERC20.sol";


contract SpotArbitrage is ISpotArbitrage {
    using SafeERC20 for IERC20;
    address public immutable arbitrageCore;
    DoubleRouterIntegration private doubleRouterIntegration;
    UniswapV2Integration private uniswapV2Integration;

    constructor(
        address _doubleRouterIntegration,
        address _uniswapV2Integration,
        address _arbitrageCore
    ) {
        doubleRouterIntegration = DoubleRouterIntegration(
            _doubleRouterIntegration
        );
        uniswapV2Integration = UniswapV2Integration(_uniswapV2Integration);
        arbitrageCore = _arbitrageCore;
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
        address asset,
        address tokenOut,
        uint256 amountIn,
        address[] calldata swapPath,
        address[] calldata dexes,
        uint256 expectProfit,
        uint256 minProfit
    ) external returns (uint256 amountOut) {
        // 检查金额是否已收到
        require(
            IERC20(asset).balanceOf(address(arbitrageCore)) >= amountIn,
            "Spot: no received"
        );
        DoubleRouterIntegration.DoubleRouterSwapParam memory param = DoubleRouterIntegration.DoubleRouterSwapParam({
            spot: address(this),
            tokenIn: asset,
            tokenOut: tokenOut,
            amountIn: amountIn,
            swapPath: swapPath,
            dexes: dexes,
            expectProfit: expectProfit,
            minProfit: minProfit
        });
        // 执行套利操作
        amountOut = doubleRouterIntegration.doubleRouterSwap(param);

        // 将结果转回 ArbitrageCore
        IERC20(asset).safeTransfer(address(arbitrageCore), amountOut);

        return amountOut;
    }
}

