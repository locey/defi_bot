// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

import "../core/ConfigManage.sol";
import "../router/IUniswapV2Router02.sol";
import "../router/IUniswapV3QuoterV2.sol";
import "../router/IUniswapV3Router.sol";

import "@openzeppelin/contracts/utils/ReentrancyGuard.sol";
import "@openzeppelin/contracts/token/ERC20/IERC20.sol";
import "@openzeppelin/contracts/token/ERC20/utils/SafeERC20.sol";

contract UniswapV2V3Integration is ReentrancyGuard {

    using SafeERC20 for IERC20;

    ConfigManage public configManage;
    address public immutable uniswapV2Router;
    address public immutable uniswapV3Router;
    address public immutable uniswapV3Quoter;
    uint256 public immutable slippageTolerance;
    constructor(address _configManage) {
        configManage = ConfigManage(_configManage);
        uniswapV2Router = configManage.uniswapV2Router();
        uniswapV3Router = configManage.uniswapV3Router();
        uniswapV3Quoter = configManage.uniswapV3Quoter();
        slippageTolerance = configManage.slippageTolerance();
    }

    uint256 private DEADLINE_DURATION = 300;

    event ArbitrageV2ToV3(
        address indexed tokenA,
        address indexed tokenB,
        uint24 v3Fee,
        uint256 amountInV2,
        uint256 amountOutV3,
        uint256 profit,
        uint256 timestamp
    );

    event ArbitrageV3ToV2(
        address indexed tokenA,
        address indexed tokenB,
        uint24 v3Fee,
        uint256 amountInV3,
        uint256 amountOutV2,
        uint256 profit,
        uint256 timestamp
    );

    struct ArbitrageV2ToV3Param {
        // 接受代币合约
        address recipient;
        // 初始输入代币（如 WETH）
        address tokenIn;
        // tokenOut 最终输出代币（如 USDC）
        address tokenOut;
        // V3 池费率（0.05%=500, 0.3%=3000, 1%=10000）
        uint24 v3Fee;
        // 初始输入代币数量
        uint256 amountIn;
        // 实际套利利润
        uint256 minProfit;
    }

    // ---------------------- 核心套利函数 1：V2 买入 → V3 卖出 ----------------------
    /**
     * @dev 从 Uniswap V2 低价买入代币，再到 Uniswap V3 高价卖出，实现套利
     */
    function arbitrageV2ToV3(
        ArbitrageV2ToV3Param calldata param
    ) external nonReentrant returns (uint256 finalAmountOut, uint256 profit) {
        // 1. 前置校验
        require(param.tokenIn != param.tokenOut, "Tokens cannot be the same");
        require(param.amountIn > 0, "AmountIn must be greater than 0");

        uint256 deadline = block.timestamp + DEADLINE_DURATION;

        // 3. V2 兑换：tokenIn → tokenOut（低价买入 tokenOut）
        address[] memory v2Path = new address[](2);
        v2Path[0] = param.tokenIn;
        v2Path[1] = param.tokenOut;

        // 3.1 V2 预演报价
        uint256[] memory v2Amounts = IUniswapV2Router02(uniswapV2Router).getAmountsOut(param.amountIn, v2Path);
        uint256 v2AmountOutExpected = v2Amounts[1];
        require(v2AmountOutExpected > 0, "V2 quote returns zero");

        // 3.2 计算 V2 滑点控制（不超过最大滑点容忍度）
        uint256 v2SlippageBps = _calculateSlippageBps(v2AmountOutExpected, param.minProfit, param.amountIn);
        uint256 v2AmountOutMin = _calculateMinOutput(v2AmountOutExpected, v2SlippageBps);

        // 3.3 安全授权 + 执行 V2 兑换
        IERC20(param.tokenIn).approve(uniswapV2Router, 0);
        IERC20(param.tokenIn).approve(uniswapV2Router, param.amountIn);

        uint256[] memory v2SwapResults = IUniswapV2Router02(uniswapV2Router).swapExactTokensForTokens(
            param.amountIn,
            v2AmountOutMin,
            v2Path,
            address(this),   // Phase 0.6: 发到本合约，V3 需要从这里发起 swap
            deadline
        );
        uint256 v2AmountOutActual = v2SwapResults[1];
        require(v2AmountOutActual >= v2AmountOutMin, "V2 swap slippage exceeded");

        // 4. V3 兑换：tokenOut → tokenIn（高价卖出 V2 买到的 tokenOut，换回 tokenIn）
        // Phase 0.6 修复: V3 报价和 swap 方向必须是 tokenOut → tokenIn
        IUniswapV3QuoterV2.QuoteExactInputSingleParams memory params = IUniswapV3QuoterV2.QuoteExactInputSingleParams({
            tokenIn: param.tokenOut,        // V2 买到的 tokenOut 作为 V3 输入
            tokenOut: param.tokenIn,        // 换回原始 tokenIn
            amountIn: v2AmountOutActual,    // 用 V2 实际输出量
            fee: param.v3Fee,
            sqrtPriceLimitX96: 0
        });
        (uint256 v3AmountOutExpected,,,) = IUniswapV3QuoterV2(uniswapV3Quoter).quoteExactInputSingle(params);
        require(v3AmountOutExpected > 0, "V3 quote returns zero");

        // 4.2 计算 V3 滑点控制
        uint256 v3SlippageBps = _calculateSlippageBps(v3AmountOutExpected, param.minProfit, v2AmountOutActual);
        uint256 v3AmountOutMin = _calculateMinOutput(v3AmountOutExpected, v3SlippageBps);

        // 4.3 授权 V3 Router 花费 tokenOut
        IERC20(param.tokenOut).approve(uniswapV3Router, 0);
        IERC20(param.tokenOut).approve(uniswapV3Router, v2AmountOutActual);

        // Phase 0.6 修复: V3 swap 方向也是 tokenOut → tokenIn
        IUniswapV3Router.ExactInputSingleParams memory v3Params = IUniswapV3Router.ExactInputSingleParams({
            tokenIn: param.tokenOut,
            tokenOut: param.tokenIn,
            fee: param.v3Fee,
            recipient: param.recipient,
            deadline: deadline,
            amountIn: v2AmountOutActual,
            amountOutMinimum: v3AmountOutMin,
            sqrtPriceLimitX96: 0
        });

        uint256 v3AmountOutActual = IUniswapV3Router(uniswapV3Router).exactInputSingle(v3Params);
        require(v3AmountOutActual >= v3AmountOutMin, "V3 swap slippage exceeded");

        // 5. 利润校验与结算
        profit = v3AmountOutActual - param.amountIn;
        require(profit >= param.minProfit, "Insufficient profit");

        // 5.1 将最终代币（含利润）转回用户
        finalAmountOut = v3AmountOutActual;

        // 6. 触发事件（链下监控与审计）
        emit ArbitrageV2ToV3(
            param.tokenIn,
            param.tokenOut,
            param.v3Fee,
            param.amountIn,
            finalAmountOut,
            profit,
            block.timestamp
        );
    }

    struct ArbitrageV3ToV2Param {
        // 接受代币地址
        address recipient;
        // 初始输入代币（如 WETH）
        address tokenIn;
        // 最终输出代币（如 USDC）
        address tokenOut;
        // 池费率（0.05%=500, 0.3%=3000, 1%=10000）
        uint24 v3Fee;
        // 初始输入代币数量
        uint256 amountIn;
        // 最小预期利润（最终输出 - 初始等价价值 > 该值）
        uint256 minProfit;
    }

    // ---------------------- 核心套利函数 2：V3 买入 → V2 卖出 ----------------------
    /**
     * @dev 从 Uniswap V3 低价买入代币，再到 Uniswap V2 高价卖出，实现套利
     */
    function arbitrageV3ToV2(
        ArbitrageV3ToV2Param calldata param
    ) external nonReentrant returns (uint256 finalAmountOut, uint256 profit) {
        // 1. 前置校验
        require(param.tokenIn != param.tokenOut, "Tokens cannot be the same");
        require(param.amountIn > 0, "AmountIn must be greater than 0");

        uint256 deadline = block.timestamp + DEADLINE_DURATION;

        // 3. V3 兑换：tokenIn → tokenOut（低价买入 tokenOut）
        // 3.1 V3 预演报价
        IUniswapV3QuoterV2.QuoteExactInputSingleParams memory params = IUniswapV3QuoterV2.QuoteExactInputSingleParams({
            tokenIn: param.tokenIn,
            tokenOut: param.tokenOut,
            amountIn: param.amountIn,
            fee: param.v3Fee,
            sqrtPriceLimitX96: 0
        });
        (uint256 v3AmountOutExpected,,,) = IUniswapV3QuoterV2(uniswapV3Quoter).quoteExactInputSingle(params);
        require(v3AmountOutExpected > 0, "V3 quote returns zero");

        // 3.2 计算 V3 滑点控制
        uint256 v3SlippageBps = _calculateSlippageBps(v3AmountOutExpected, param.minProfit, param.amountIn);
        uint256 v3AmountOutMin = _calculateMinOutput(v3AmountOutExpected, v3SlippageBps);

        // 3.3 执行 V3 兑换
        IERC20(param.tokenIn).approve(uniswapV3Router, 0);
        IERC20(param.tokenIn).approve(uniswapV3Router, param.amountIn);

        IUniswapV3Router.ExactInputSingleParams memory v3Params = IUniswapV3Router.ExactInputSingleParams({
            tokenIn: param.tokenIn,
            tokenOut: param.tokenOut,
            fee: param.v3Fee,
            recipient: param.recipient,
            deadline: deadline,
            amountIn: param.amountIn,
            amountOutMinimum: v3AmountOutMin,
            sqrtPriceLimitX96: 0
        });

        uint256 v3AmountOutActual = IUniswapV3Router(uniswapV3Router).exactInputSingle(v3Params);
        require(v3AmountOutActual >= v3AmountOutMin, "V3 swap slippage exceeded");

        // 4. V2 兑换：tokenOut → tokenIn（高价卖出 tokenOut，换回更多 tokenIn）
        address[] memory v2Path = new address[](2);
        v2Path[0] = param.tokenOut;
        v2Path[1] = param.tokenIn;

        // 4.1 V2 预演报价
        uint256[] memory v2Amounts = IUniswapV2Router02(uniswapV2Router).getAmountsOut(v3AmountOutActual, v2Path);
        uint256 v2AmountOutExpected = v2Amounts[1];
        require(v2AmountOutExpected > 0, "V2 quote returns zero");

        // 4.2 计算 V2 滑点控制
        uint256 v2SlippageBps = _calculateSlippageBps(v2AmountOutExpected, param.minProfit, v3AmountOutActual);
        uint256 v2AmountOutMin = _calculateMinOutput(v2AmountOutExpected, v2SlippageBps);

        // 4.3 执行 V2 兑换
        IERC20(param.tokenOut).approve(uniswapV2Router, 0);
        IERC20(param.tokenOut).approve(uniswapV2Router, v3AmountOutActual);

        uint256[] memory v2SwapResults = IUniswapV2Router02(uniswapV2Router).swapExactTokensForTokens(
            v3AmountOutActual,
            v2AmountOutMin,
            v2Path,
            param.recipient,
            deadline
        );
        uint256 v2AmountOutActual = v2SwapResults[1];
        require(v2AmountOutActual >= v2AmountOutMin, "V2 swap slippage exceeded");

        // 5. 利润校验与结算
        profit = v2AmountOutActual - param.amountIn;
        require(profit >= param.minProfit, "Insufficient profit");

        // 5.1 将最终代币（含利润）转回用户
        finalAmountOut = v2AmountOutActual;

        // 6. 触发事件（链下监控与审计）
        emit ArbitrageV3ToV2(
            param.tokenIn,
            param.tokenOut,
            param.v3Fee,
            param.amountIn,
            finalAmountOut,
            profit,
            block.timestamp
        );
    }

    // ---------------------- 内部辅助函数（滑点计算） ----------------------
    /**
     * @dev 计算滑点比例（万分之几）
     * @param expectedOut 预期输出金额
     * @param minProfit 最小利润
     * @param currentIn 当前输入金额
     * @return slippageBps 滑点比例（万分之几）
     */
    function _calculateSlippageBps(
        uint256 expectedOut,
        uint256 minProfit,
        uint256 currentIn
    ) internal view returns (uint256 slippageBps) {
        if (expectedOut <= currentIn + minProfit) {
            return 0; // 无套利空间，返回 0 滑点（后续会校验利润）
        }

        // 最大可容忍亏损 = 预期输出 - 当前输入 - 最小利润
        uint256 maxLoss = expectedOut - currentIn - minProfit;
        // 转换为万分之几的滑点比例
        slippageBps = (maxLoss * 10000) / expectedOut;

        // 限制滑点不超过最大容忍度
        if (slippageBps > slippageTolerance) {
            slippageBps = slippageTolerance;
        }
    }

    /**
     * @dev 计算最小输出金额（滑点保护）
     * @param expectedOut 预期输出金额
     * @param slippageBps 滑点比例（万分之几）
     * @return minOutput 最小输出金额
     */
    function _calculateMinOutput(
        uint256 expectedOut,
        uint256 slippageBps
    ) internal pure returns (uint256 minOutput) {
        if (slippageBps >= 10000) {
            return 1; // 极端情况，返回最小非零值
        }

        // 最小输出 = 预期输出 * (10000 - 滑点比例) / 10000
        minOutput = (expectedOut * (10000 - slippageBps)) / 10000;
        // 避免最小输出为 0（导致交易失败）
        minOutput = minOutput == 0 ? 1 : minOutput;
    }
}