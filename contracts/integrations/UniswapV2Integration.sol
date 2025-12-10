// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

import '../interfaces/IUniswapV2Integration.sol';
import '../interfaces/IUniswapV2Router02.sol';

import "@openzeppelin/contracts/token/ERC20/utils/SafeERC20.sol";

/**
* IUniswapV2Router02支持uniswap和sushuiswap
*/
contract UniswapV2Integration is IUniswapV2Integration {
    
    using SafeERC20 for IERC20;

    uint public slipageTolerance;

    constructor(uint _slipageTolerance) {
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
        address[] calldata path
    ) external view returns (
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
        uint256 amountIn,
        uint256 minProfit
    ) external {

        require(amountIn > 0, "amountIn must be > 0");

        IUniswapV2Router02 swapRouter = IUniswapV2Router02(router);

        address[] memory path = _getSwapPath(router, token0, token1);
        require(path.length >= 2, "invalid path");
        require(path[0] == token0, "path start mismatch");
        require(path[path.length - 1] == token1, "path end mismatch");

        // ===== 余额校验 =====
        uint256 startBalance = IERC20(token0).balanceOf(address(this));
        require(startBalance >= amountIn, "insufficient balance");

        // ===== 授权校验 =====
        uint256 allowance = IERC20(token0).allowance(address(this), router);
        if (allowance < amountIn) {
            IERC20(token0).approve(router, 0);
            IERC20(token0).approve(router, amountIn);
        }

        // ===== 获取预期输出 =====
        uint256[] memory expectedAmounts = swapRouter.getAmountsOut(amountIn, path);
        uint256 expectedOut = expectedAmounts[expectedAmounts.length - 1];
        require(expectedOut > 0, "zero expectedOut");

        // ===== 滑点保护 =====
        uint256 amountOutMin = expectedOut * (10000 - slipageTolerance) / 10000;
        require(amountOutMin > 0, "slippage too high");

        // ===== 执行 swap =====
        uint256[] memory amounts = swapRouter.swapExactTokensForTokens(
            amountIn,
            amountOutMin,
            path,
            address(this),
            block.timestamp + 60
        );

        uint256 endBalance = amounts[amounts.length - 1];
        uint256 profit = endBalance - startBalance;
        require(profit >= minProfit, "profit < minProfit");

        emit SwapV2(router, token0, token1, amountIn, endBalance, expectedOut, slipageTolerance);
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
        uint256 amountIn,
        uint256 minProfit
    ) external {

        require(paths.length > 0, "empty paths");
        require(amountIn > 0, "zero amount");

        // 获取初始余额
        address tokenA = paths[0][0];
        uint256 startBalance = IERC20(tokenA).balanceOf(address(this));
        require(startBalance >= amountIn, "insufficient balance");

        IUniswapV2Router02 swapRouter = IUniswapV2Router02(router);

        uint256 startAmount = amountIn;
        uint256 currentAmount = amountIn;

        for (uint256 i = 0; i < paths.length; i++) {
            address[] calldata path = paths[i];
            require(path.length >= 2, "invalid path");

            uint256[] memory amountsOut = swapRouter.getAmountsOut(currentAmount, path);
            uint256 expectedOut = amountsOut[amountsOut.length - 1];
            require(expectedOut > 0, "zero expected out");

            // 滑点保护
            uint256 amountOutMin = expectedOut * (10000 - slipageTolerance) / 10000;
            require(amountOutMin > 0, "slippage too high");

            uint256[] memory result = swapRouter.swapExactTokensForTokens(
                currentAmount,
                amountOutMin,
                path,
                address(this),
                block.timestamp
            );

            currentAmount = result[result.length - 1];
        }

        // 只在最终校验利润
        uint256 profit = currentAmount - startBalance;
        require(profit >= minProfit, "profit < minProfit");

        emit SwapV2Multi(router, paths, startAmount, currentAmount);
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

    /**
     * 动态配置滑点
     * 请求参数：
     *      _slipageTolerance：滑点容忍度
     */
    function setSlipageTolerance(uint _slipageTolerance) public {
        slipageTolerance = _slipageTolerance;
    }
}