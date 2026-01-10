// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

import "@openzeppelin/contracts/token/ERC20/IERC20.sol";

contract MockDEXRouter {
    mapping(address => mapping(address => uint256)) public prices; // tokenA => tokenB => price

    function setPrice(address tokenA, address tokenB, uint256 price) external {
        prices[tokenA][tokenB] = price;
        prices[tokenB][tokenA] = 1e18 * 1e18 / price; // 反向价格
    }

    function swapExactsTokensForTokens(
        uint256 amountIn,
        uint256 /* amountOutMin */,
        address[] calldata swapPath,
        address to,
        uint256 /* deadline */
    ) external returns (uint256[] memory amounts) {
        require(swapPath.length >= 3, "Invalid path");
        address tokenA = swapPath[0];
        address tokenB = swapPath[swapPath.length - 1];

        //模拟价格计算
        uint256 price = prices[tokenA][tokenB];
        if (price == 0) { 
            //价格未设置，直接1:1交换
            price = 1e18;
        }
        uint256 amountOut = (amountIn * price) / 1e18;

        IERC20(tokenA).transferFrom(msg.sender, address(this), amountIn);
        IERC20(tokenB).transfer(to, amountOut);

        amounts = new uint256[](swapPath.length);
        amounts[0] = amountIn;
        amounts[swapPath.length - 1] = amountOut;

        return amounts;
    }

    function getAmountsOut(uint256 amountIn, address[] calldata swapPath) external view returns (uint256[] memory amounts) {
        require(swapPath.length >= 3, "Invalid path");
        amounts = new uint256[](swapPath.length);
        amounts[0] = amountIn;

        for (uint256 i = 0; i < swapPath.length - 1; i++) {
            address tokenA = swapPath[i];
            address tokenB = swapPath[i + 1];
            uint256 price = prices[tokenA][tokenB];
            require(price > 0, "Price not set");

            amounts[i + 1] = (amounts[i] * price) / 1e18;
        }
    }
}