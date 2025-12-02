// SPDX-License-Identifier: MIT
pragam solidity ^0.8.20;

import "./interfaces/IUniswapV2Router02.sol";
import "./interfaces/IExchange.sol";

contract UniswapV2Integration {

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
     *      platForm：转账地址
     */
    function swapV2(
        address router, 
        address token0, 
        address token1, 
        uint amountIn,
        address platForm) 
    external returns(uint amountOut) {
        require(amountIn > 0, "amountIn must be greater than zero");
        IUniswapV2Router02 swapRouter = IUniswapV2Router02(router);
        address[] memory path = _getSwapPath(router, token0, token1);
        uint256[] memory amounts;
        swapRouter.swapExactTokensForTokens(
            amountIn, 
            0, 
            path, 
            platForm, 
            block.timestamp + 30);
        return amounts[amounts.length-1];
    }

    /**
     * 单路由多路径
     * 请求参数：
     *      router：dex地址
     *      paths：策略路径，支持多路径{USDT->DAI->WETH}
     *          path[0]:[USDT,DAI]
     *          path[1]:[DAI,WETH] 
     *      amountIn：交易数量
     *      platForm：转账地址
     */
    function swapV2Multi(
        address router,
        address[][] memory paths,
        uint amountIn,
        address platForm) 
    external returns (uint amountOut) {

        IUniswapV2Router02 swapRouter = IUniswapV2Router02(router);
        uint currentAmount = amountIn;
        uint length = paths.length;
        for (uint i = 0; i < length; i++) {
            address[] memory path = paths[i];

            require(path.length >= 2, "Invalid path");

            uint[] memory result = swapRouter.swapExactTokensForTokens(
                currentAmount,
                0,
                path,
                platForm,
                block.timestamp + 30
            );

            currentAmount = result[result.length - 1];
        }

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