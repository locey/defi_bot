// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

import "../core/ConfigManage.sol";
import "../interfaces/IUniswapV2Router02.sol";
import "../interfaces/IDoubleRouterIntegration.sol";

contract DoubleRouterIntegration is IDoubleRouterIntegration {

    address vault = ConfigManage.arbitrageVault;
    uint public slipageTolerance;

    constructor(address _platForm, uint _slipageTolerance) {
        require(_platForm != address(0), "Invalid platForm");
        slipageTolerance = _slipageTolerance;
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
    function doubleRouterArbCheck(
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
     * 双路由交易
     * @param routerA routerA地址
     * @param routerB routerB地址
     * @param amountIn 交易数量
     * @param pathA 交易路径A
     * @param pathB 交易路径B
     * @param minProfit 最小利润
     */    
    function doubleRouterSwap(
        address routerA,
        address routerB,
        uint256 amountIn,
        address[] calldata pathA,
        address[] calldata pathB,
        uint256 minProfit
    ) external {

        require(pathA.length >= 2 && pathB.length >= 2, "invalid path");
        require(amountIn > 0, "zero amount");

        address tokenA = pathA[0];
        address tokenB = pathA[pathA.length - 1];

        require(pathB[0] == tokenB, "pathB start mismatch");
        require(pathB[pathB.length - 1] == tokenA, "pathB end mismatch");

        // 获取初始余额
        uint256 startBalance = IERC20(tokenA).balanceOf(address(this));
        require(startBalance >= amountIn, "insufficient balance");

        uint256 deadline = block.timestamp + 300;

        // ===== Step 1: RouterA swap tokenA -> tokenB =====
        {
            uint256[] memory estimated1 = IUniswapV2Router(routerA).getAmountsOut(amountIn, pathA);
            uint256 expectedOut1 = estimated1[estimated1.length - 1];

            // 滑点保护
            uint256 amountOutMin1 = (expectedOut1 * (10000 - slipageTolerance)) / 10000;

            IERC20(tokenA).approve(routerA, amountIn);

            IUniswapV2Router(routerA).swapExactTokensForTokensSupportingFeeOnTransferTokens(
                amountIn,
                amountOutMin1,
                pathA,
                address(this),
                deadline
            );
        }

        uint256 tokenBBalance = IERC20(tokenB).balanceOf(address(this));
        require(tokenBBalance > 0, "no tokenB received");

        // ===== Step 2: RouterB swap tokenB -> tokenA =====
        {
            uint256[] memory estimated2 = IUniswapV2Router(routerB).getAmountsOut(tokenBBalance, pathB);
            uint256 expectedOut2 = estimated2[estimated2.length - 1];
            uint256 amountOutMin2 = (expectedOut2 * (10000 - slipageTolerance)) / 10000;

            IERC20(tokenB).approve(routerB, tokenBBalance);

            IUniswapV2Router(routerB).swapExactTokensForTokensSupportingFeeOnTransferTokens(
                tokenBBalance,
                amountOutMin2,
                pathB,
                address(this),
                deadline
            );
        }

        // 真实利润检测
        uint256 endBalance = IERC20(tokenA).balanceOf(address(this));
        require(endBalance > startBalance, "no profit");

        uint256 profit = endBalance - startBalance;
        require(profit >= minProfit, "profit < minProfit");

        emit DoubleRouterSwap(msg.sender, routerA, routerB, amountIn, profit);
    }

}