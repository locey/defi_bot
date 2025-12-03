// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

import './interfaces/IExchange.sol';
import './interfaces/IUniswapV2Router02.sol';

/**
* IUniswapV2Router02支持uniswap和sushuiswap
*/
contract UniswapV2Integration is IExchange {

    address public immutable platForm;
    uint public slipageTolerance;

    constructor(address _platForm, uint _slipageTolerance) {
        require(_platForm != address(0), "Invalid platForm");
        platForm = _platForm;
        slipageTolerance = _slipageTolerance;
    }

    /**
     * 单路由验证路径是否可获利
     * 请求参数：
     *      router: dex地址
     *      amountIn：交易数量
     *      path：交易路径
     * 返回参数：
     *      profitable: 是否可获利
     *      finalAmount：返回数量
     *      profit：利润（负数表示亏损）
     */
    function routerArbCheck(
        address router,
        uint amountIn,
        address[] calldata path) 
    external view returns (
        bool profitable,
        uint finalAmount,
        int profit
    ) {
        require(path.length >= 2, "Invalid path");
        require(path[0] == path[path.length - 1], "Path must return to start");

        IUniswapV2Router02 swapRouter = IUniswapV2Router02(router);

        uint256[] memory amounts;

        // 计算整个路径的 amountsOut
        try swapRouter.getAmountsOut(amountIn, path) returns (uint256[] memory result) {
            amounts = result;
        } catch {
            // path 不存在、池子没流动性等情况
            return (false, 0, -1);
        }

        uint finalOut = amounts[amounts.length - 1];

        // 盈利条件：最终 A 数量 > 初始 A 数量
        bool isProfit = finalOut > amountIn;

        // 利润（signed integer）
        int profitAmount = int(finalOut) - int(amountIn);
        return (isProfit, finalOut, profitAmount);
    }   

    /**
     * 多路由验证路径是否可获利
     * 请求参数：
     *      amountIn：交易数量
     *      path：交易路径
     *           [A, B, C, A]
     *      routers：路由
     *           每跳的 router，如 [uni, sushi, uni]
     * 返回参数：
     *      profitable: 是否可获利
     *      finalAmount：返回数量
     *      profit：利润（负数表示亏损）
     */
    function multiRouterArbCheck(
        uint amountIn,
        address[] calldata path,
        address[] calldata routers
    ) external view returns(
        bool profitable,
        uint finalAmount,
        int profit
    ) {
        require(path.length >= 2, "Invalid path");
        require(path[0] == path[path.length - 1], "Must return to start");
        require(routers.length == path.length - 1, "Router count mismatch");

        uint256 currentAmount = amountIn;

        for (uint i = 0; i < routers.length; i++) {
            address router = routers[i];
            address from = path[i];
            address to = path[i + 1];

            address[] memory stepPath;
            stepPath[0] = from;
            stepPath[1] = to;

            IUniswapV2Router02 swapRouter = IUniswapV2Router02(router);

            uint256[] memory amounts;
            try swapRouter.getAmountsOut(currentAmount, stepPath) returns (uint256[] memory result) {
                amounts = result;
            } catch {
                // 任意跳失败 = 套利不成立
                return (false, 0, -1);
            }

            currentAmount = amounts[1];
        }

        uint finalOut = currentAmount;

        bool isProfit = finalOut > amountIn;
        int profitAmount = int(finalOut) - int(amountIn);

        return (isProfit, finalOut, profitAmount);
    }



    /** 
     * 单路由单路径
     * 请求参数：
     *      router：dex地址
     *      token0：代币地址0
     *      token1：代币地址1
     *      amountIn：交易数量
     */
    function swapV2(
        address router, 
        address token0, 
        address token1, 
        uint amountIn) 
    external returns(uint amountOut) {
        require(amountIn > 0, "amountIn must be greater than zero");
        IUniswapV2Router02 swapRouter = IUniswapV2Router02(router);
        address[] memory path = _getSwapPath(router, token0, token1);

        // getAmountsOut：根据输入数量和路径，计算理论上能兑换到的各步代币数量
        uint256[] memory expectedAmounts = swapRouter.getAmountsOut(amountIn, path);
        // 预期最终输出的目标代币数量（数组最后一个元素）
        uint expectedOut = expectedAmounts[expectedAmounts.length - 1];
        require(expectedOut > 0, "Expected output is zero (invalid path/liquidity)");

        // 计算 amountOutMin（最小接收数量 = 预期输出 * (1 - 滑点容忍度/10000)）
        // 滑点容忍度用「万分比」避免浮点数：500 = 500/10000 = 5%，100 = 1%
        uint amountOutMin = expectedOut * (10000 - slipageTolerance) / 10000;
        // 防止极端情况下 amountOutMin 为 0（比如滑点设为 10000，虽已校验，但双重保险）
        require(amountOutMin > 0, "amountOutMin is zero");
        uint256[] memory amounts = swapRouter.swapExactTokensForTokens(
            amountIn, 
            amountOutMin, 
            path, 
            platForm, 
            block.timestamp + 30
        );
        // 最终兑换到的目标代币数量
        amountOut = amounts[amounts.length - 1]; 
        emit SwapV2(router, token0, token1, amountIn, amountOut, expectedOut, slipageTolerance);
        return amountOut;
    }

    /**
     * 单路由多路径
     * 请求参数：
     *      router：dex地址
     *      paths：策略路径，支持多路径{USDT->DAI->WETH}
     *          path[0]:[USDT,DAI]
     *          path[1]:[DAI,WETH] 
     *      amountIn：交易数量
     */
    function swapV2Multi(
        address router,
        address[][] calldata paths,
        uint amountIn
    ) external returns (uint amountOut) {

        IUniswapV2Router02 swapRouter = IUniswapV2Router02(router);
        uint currentAmount = amountIn;
        uint pathCount = paths.length;

        address[] memory currentPath;
        uint256[] memory expectedAmounts;
        uint expectedOut;
        uint amountOutMin;
        uint[] memory swapResult;

        for (uint i = 0; i < pathCount; i++) {
            currentPath = paths[i];

            require(currentPath.length >= 2, "Invalid path");
            
            expectedAmounts = swapRouter.getAmountsOut(currentAmount, currentPath);
            require(expectedAmounts[expectedAmounts.length - 1] > 0, "Expected output is zero");
            expectedOut = expectedAmounts[expectedAmounts.length - 1];

            //滑点计算保留原逻辑
            amountOutMin = expectedOut * (10000 - slipageTolerance) / 10000;
            require(amountOutMin > 0, "amountOutMin is zero");

            swapResult = swapRouter.swapExactTokensForTokens(
                currentAmount,
                amountOutMin,
                currentPath,
                platForm,
                block.timestamp + 30
            );
            currentAmount = swapResult[swapResult.length - 1];

            // 利润校验,确保无亏损
            require(currentAmount >= amountIn, "No profit");
        }

        emit SwapV2Multi(router, paths, amountIn, currentAmount);
        return currentAmount;
    }

    /**
     * Get the swap path
     */
    function _getSwapPath(address _swapRouter,address token0,address token1) internal pure returns (address[] memory path){
        IUniswapV2Router02 IUniswap = IUniswapV2Router02(_swapRouter);
        path = new address[](2);
        path[0] = token0 == address(0) ? IUniswap.WETH() : token0;
        path[1] = token1 == address(0) ? IUniswap.WETH() : token1;
    }
}