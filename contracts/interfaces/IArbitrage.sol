// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

    /**
    *套利核心方法
    *参数包含：
    * tokenIn  初始代币，
    * tokenOut  目标代币，
    * amountIn  购买数量，
    * swapPath  策略路径，
    * dexes  Dex地址，
    * profit  实际利润
    *
    **/
interface IArbitrage {
    struct ArbitrageParams {
        address tokenIn;
        //address tokenOut;
        uint256 amountIn;
        address[] swapPath;
        address[] dexes;
        uint256 minProfit;
        //bool isFlashLoan;
        //uint8 flashLoanPlatForm;
    }

}

