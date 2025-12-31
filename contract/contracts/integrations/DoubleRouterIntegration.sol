// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

import "../core/ConfigManage.sol";
import "../router/IUniswapV2Router02.sol";

import "@openzeppelin/contracts/token/ERC20/utils/SafeERC20.sol";

contract DoubleRouterIntegration {

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
     * @param spot 入账地址
     * @param tokenIn 初始代币
     * @param tokenOut 最终代币
     * @param amountIn 交易数量
     * @param swapPath 交易路由
     * @param dexes AMM路由
     * @param expectProfit 期望利润
     * @param minProfit 最小利润
     */  
    struct DoubleRouterSwapParam {
        address spot;
        address tokenIn;
        address tokenOut;
        uint256 amountIn;
        address[] swapPath;
        address[] dexes;
        uint256 expectProfit;
        uint256 minProfit;
    }

    struct ExecuteSingleSwapParam {
        address spot;
        address routerAddr;
        address fromToken;
        address toToken;
        uint256 currentAmount;
        uint256 deadline;
        uint256 aveProfit;
    }

    function doubleRouterSwap(
        DoubleRouterSwapParam calldata param
    ) external returns(uint256 amountOut) {
        uint256 swapLength = param.swapPath.length;
        uint256 dexLength = param.dexes.length;
        require(swapLength >= 2, "invalid swapPath");
        require(dexLength == swapLength - 1, "dexes length mismatch");
        require(param.swapPath[swapLength - 1] == param.tokenOut, "tokenOut mismatch with swapPath");
        require(IERC20(param.tokenIn).balanceOf(param.spot) >= param.amountIn, "insufficient tokenIn balance");

        uint256 currentAmount = param.amountIn;
        uint256 deadline = block.timestamp + 300;
        uint256 aveProfit = param.expectProfit / dexLength;
        for (uint i = 0; i < dexLength; i++) {
            ExecuteSingleSwapParam memory singleSwapParam = ExecuteSingleSwapParam ({
                spot: param.spot,
                routerAddr: param.dexes[i],
                fromToken: param.swapPath[i],
                toToken: param.swapPath[i + 1],
                currentAmount: currentAmount,
                deadline: deadline,
                aveProfit: aveProfit
            });
            currentAmount = _executeSingleSwap(singleSwapParam);
        }
        require(currentAmount > param.minProfit, "no profit hop");
        amountOut = currentAmount;
    }

    function _executeSingleSwap(
        ExecuteSingleSwapParam memory param
    ) internal returns (uint256 outAmount) {
        // 授权
        IERC20(param.fromToken).approve(param.routerAddr, 0);
        IERC20(param.fromToken).approve(param.routerAddr, param.currentAmount);

        // 构建路径
        address[] memory path = new address[](2);
        path[0] = param.fromToken;
        path[1] = param.toToken;

        // 计算预期输出
        uint[] memory amounts = IUniswapV2Router02(param.routerAddr).getAmountsOut(param.currentAmount, path);
        uint256 expectedOut = amounts[amounts.length - 1];
        // 根据预期计算滑点容忍度，预期输出 - 输入 - 最小利润 = 最大容忍度
        uint256 maxLoss = expectedOut - param.currentAmount - param.aveProfit;
        require(maxLoss > 0, "no slippage room");
        uint256 slippageBps = (maxLoss * 10000) / expectedOut;
        // slippageTolerance为默认的最大滑点容忍度，不得超过这个值
        if (slippageBps > slippageTolerance) {
            slippageBps = slippageTolerance;
        }
        uint256 minOut = (expectedOut * (10000 - slippageBps)) / 10000;
        minOut = minOut == 0 ? 1 : minOut;

        // 执行兑换
        outAmount = IUniswapV2Router02(param.routerAddr).swapExactTokensForTokens(
            param.currentAmount,
            minOut,
            path,
            param.spot,
            param.deadline
        )[1];

        // 触发事件
        emit DoubleRouterSwap2(param.routerAddr, param.fromToken, param.toToken, param.currentAmount, outAmount);
    }                               

}