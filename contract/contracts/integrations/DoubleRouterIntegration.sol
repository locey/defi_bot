// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

import "../core/ConfigManage.sol";
import "../interfaces/IUniswapV2Router02.sol";
import "../interfaces/IDoubleRouterIntegration.sol";

import "@openzeppelin/contracts/token/ERC20/utils/SafeERC20.sol";

contract DoubleRouterIntegration is IDoubleRouterIntegration {

    using SafeERC20 for IERC20;

    // address vault = ConfigManage.arbitrageVault;
    ConfigManage public configManage;
    uint256 public slippageTolerance;
    address public admin;

    constructor(address _configManage) {
        admin = msg.sender;
        configManage = ConfigManage(_configManage);
        slippageTolerance = configManage.slippageTolerance();
    }

    event DoubleRouterSwap(
        address indexed sender, 
        address indexed routerA, 
        address indexed routerB, 
        uint256 amountIn, 
        uint256 profit
    );

    event DoubleRouterSwap2(
        address indexed routerAddr, 
        address indexed fromToken, 
        address indexed toToken, 
        uint256 currentAmount, 
        uint256 outAmount
    );

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
        require(pathB[0] == pathA[pathA.length - 1], "pathB start mismatch");
        require(pathB[pathB.length - 1] == pathA[0], "pathB end mismatch");
        require(IERC20(pathA[0]).balanceOf(address(this)) >= amountIn, "insufficient balance");

        uint256 deadline = block.timestamp + 300;
        address tokenA = pathA[0];
        address tokenB = pathA[pathA.length - 1];

        // Step 1: 调用子函数完成 RouterA 兑换
        _swapOnRouter(routerA, tokenA, amountIn, pathA, deadline);
        
        // 校验 tokenB 余额
        require(IERC20(tokenB).balanceOf(address(this)) > 0, "no tokenB received");
        
        // Step 2: 调用子函数完成 RouterB 兑换
        uint256 tokenBBalance = IERC20(tokenB).balanceOf(address(this));
        _swapOnRouter(routerB, tokenB, tokenBBalance, pathB, deadline);

        // 利润校验
        uint256 endBalance = IERC20(tokenA).balanceOf(address(this));
        uint256 profit = endBalance - (IERC20(tokenA).balanceOf(address(this)) - amountIn);
        require(profit > 0 && profit >= minProfit, "insufficient profit");

        emit DoubleRouterSwap(msg.sender, routerA, routerB, amountIn, profit);
    }

    function _swapOnRouter(
        address router,
        address tokenIn,
        uint256 amountIn,
        address[] calldata path,
        uint256 deadline
    ) internal {
        uint256 expectedOut = IUniswapV2Router02(router).getAmountsOut(amountIn, path)[path.length - 1];
        uint256 amountOutMin = (expectedOut * (10000 - slippageTolerance)) / 10000;

        IERC20(tokenIn).approve(router, amountIn);
        IUniswapV2Router02(router).swapExactTokensForTokensSupportingFeeOnTransferTokens(
            amountIn, amountOutMin, path, address(this), deadline
        );
    }
    
    /**
     * 双路由交易V2
     * @param spot 入账地址
     * @param tokenIn 初始代币
     * @param tokenOut 最终代币
     * @param amountIn 交易数量
     * @param swapPath 交易路由
     * @param dexes AMM路由
     * @param minProfit 最小利润
     */  
    function doubleRouterSwap2(
        address spot,
        address tokenIn,
        address tokenOut,
        uint256 amountIn,
        address[] calldata swapPath,
        address[] calldata dexes,
        uint256 minProfit
    ) external returns(uint256 amountOut) {
        require(swapPath.length >= 2, "invalid swapPath");
        require(dexes.length == swapPath.length - 1, "dexes length mismatch");
        require(swapPath[swapPath.length - 1] == tokenOut, "tokenOut mismatch with swapPath");
        // require(IERC20(tokenIn).balanceOf(spot) >= amountIn, "insufficient tokenIn balance");

        uint256 currentAmount = amountIn;
        uint256 deadline = block.timestamp + 300;

        for (uint i = 0; i < dexes.length; i++) {
            currentAmount = _executeSingleSwap(
                spot,
                dexes[i],
                swapPath[i],
                swapPath[i + 1],
                currentAmount,
                deadline,
                minProfit
            );
        }
        require(currentAmount > minProfit, "no profit hop");

        amountOut = currentAmount;
    }

    function _executeSingleSwap(
        address spot,
        address routerAddr,
        address fromToken,
        address toToken,
        uint256 currentAmount,
        uint256 deadline,
        uint256 minProfit
    ) internal returns (uint256 outAmount) {
        // 余额校验
        // require(IERC20(fromToken).balanceOf(spot) >= currentAmount, "insufficient token balance for hop");

        // 授权
        IERC20(fromToken).approve(routerAddr, 0);
        IERC20(fromToken).approve(routerAddr, currentAmount);

        // 构建路径
        address[] memory path = new address[](2);
        path[0] = fromToken;
        path[1] = toToken;

        // 计算预期输出
        uint[] memory amounts = IUniswapV2Router02(routerAddr).getAmountsOut(currentAmount, path);
        uint256 expectedOut = amounts[amounts.length - 1];
        // 根据预期计算滑点容忍度，预期输出 - 输入 - 最小利润 = 最大容忍度
        uint256 maxLoss = expectedOut - currentAmount - minProfit;
        require(maxLoss > 0, "no slippage room");
        uint256 slippageBps = (maxLoss * 10000) / expectedOut;
        // slippageTolerance为默认的最大滑点容忍度，不得超过这个值
        if (slippageBps > slippageTolerance) {
            slippageBps = slippageTolerance;
        }
        uint256 minOut = (expectedOut * (10000 - slippageBps)) / 10000;
        minOut = minOut == 0 ? 1 : minOut;

        // 执行兑换
        outAmount = IUniswapV2Router02(routerAddr).swapExactTokensForTokens(
            currentAmount,
            minOut,
            path,
            spot,
            deadline
        )[1];

        // 触发事件
        emit DoubleRouterSwap2(routerAddr, fromToken, toToken, currentAmount, outAmount);
    }                               

}