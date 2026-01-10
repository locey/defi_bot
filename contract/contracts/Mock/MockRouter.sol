// SPDX-License-Identifier: UNLICENSED
pragma solidity ^0.8.20;

import "@openzeppelin/contracts/token/ERC20/IERC20.sol";

contract MockRouter {
    // ==============================================
    // ✅ 修复点1：补全你缺失的核心价格变量！！！必加
    // ==============================================
    uint256 public priceTokenA; // 对应入金代币价格 (如 USDC)
    uint256 public priceTokenB; // 对应出金代币价格 (如 WETH)

    // ==============================================
    // ✅ 修复点2：补全你缺失的setPrice函数！！！测试脚本一直在调用
    // ==============================================
    function setPrice(uint256 _priceA, uint256 _priceB) external {
        priceTokenA = _priceA;
        priceTokenB = _priceB;
    }

    // ==============================================
    // ✅ 修复点3：解决deadline未使用警告【2种方案2选1，推荐方案1】
    // 方案1 (推荐)：只保留参数类型，删除变量名 → 编译器无警告，完全合规
    // 方案2：变量名前加下划线 → uint256 _deadline (也可以，只是习惯问题)
    // ==============================================
    function swapExactTokensForTokens(
        uint256 amountIn,
        uint256 amountOutMin,
        address[] calldata swapPath,
        address to,
        uint256 // deadline
    ) external returns (uint256[] memory amounts) {
        require(swapPath.length >= 2, "Invalid path"); // 修正为 >=2，与getAmountsOut一致
        address tokenIn = swapPath[0];
        address tokenOut = swapPath[swapPath.length - 1];
        IERC20 inToken = IERC20(tokenIn);
        IERC20 outToken = IERC20(tokenOut);

        // 授权校验
        uint256 allowance = inToken.allowance(msg.sender, address(this));
        require(allowance >= amountIn, "MockRouter: insufficient allowance");

        // 转入代币到Router合约
        bool transferInOk = inToken.transferFrom(msg.sender, address(this), amountIn);
        require(transferInOk, "MockRouter: transferIn failed");

        // 计算出金数量（使用unchecked避免溢出）
        uint256 amountOut;
        unchecked {
            amountOut = (amountIn * priceTokenB) / priceTokenA;
        }
        require(amountOut >= amountOutMin, "MockRouter: Insufficient output amount");

        // 余额检查
        uint256 outBalance = outToken.balanceOf(address(this));
        require(outBalance >= amountOut, "MockRouter: insufficient tokenOut balance");

        // 转出代币到目标地址
        bool transferOutOk = outToken.transfer(to, amountOut);
        require(transferOutOk, "MockRouter: transferOut failed");

        // 组装返回值数组
        amounts = new uint256[](swapPath.length);
        amounts[0] = amountIn;
        amounts[swapPath.length - 1] = amountOut;
        // 填充中间路径数值
        for(uint i=1; i<swapPath.length-1; i++) {
            amounts[i] = amountOut;
        }

        return amounts;
    }

    // ==============================================
    // ✅ 可选：增加给Router充值代币的函数，测试时必用！
    // 测试脚本中可以调用这个函数给Router转USDC/WETH，避免余额不足
    // ==============================================
    function depositToken(address token, uint256 amount) external {
        IERC20(token).transferFrom(msg.sender, address(this), amount);
    }

    function getAmountsOut(uint256 amountIn, address[] calldata swapPath) external view returns (uint256[] memory amounts) {
        require(swapPath.length >= 2, "Invalid path");
        amounts = new uint256[](swapPath.length);
        amounts[0] = amountIn;
        
        // 计算输出数量（使用unchecked避免溢出）
        uint256 amountOut;
        unchecked {
            amountOut = (amountIn * priceTokenB) / priceTokenA;
        }
        
        // 填充所有路径数值
        for (uint i = 1; i < swapPath.length; i++) {
            amounts[i] = amountOut;
        }
        
        return amounts;
    }
}