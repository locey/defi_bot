// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

import "../core/ConfigManage.sol";
import "../router/IUniswapV2Router02.sol";
import "../interfaces/IDoubleRouterIntegration.sol";

import "@openzeppelin/contracts/token/ERC20/utils/SafeERC20.sol";
import "@openzeppelin/contracts-upgradeable/proxy/utils/Initializable.sol";
import "@openzeppelin/contracts-upgradeable/proxy/utils/UUPSUpgradeable.sol";
import "@openzeppelin/contracts-upgradeable/access/OwnableUpgradeable.sol";

contract DoubleRouterIntegration is IDoubleRouterIntegration, Initializable, UUPSUpgradeable, OwnableUpgradeable {

    using SafeERC20 for IERC20;

    ConfigManage public configManage;
    uint256 public slippageTolerance;
    address[] public mrouters;

    /// @custom:oz-upgrades-unsafe-allow constructor
    constructor() {
        _disableInitializers();
    }

    function initialize(address _configManage) public initializer {
        __Ownable_init(msg.sender);
        __UUPSUpgradeable_init();
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

    function setRouters(address[] calldata _routers) external onlyOwner {
        require(_routers.length > 0 , "Empty routers");
        mrouters = _routers;
    }

    function getRouters() external view returns(address[] memory) {
        return mrouters;
    }

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
                return (false, 0, -1);
            }

            currentAmount = amounts[1];
        }

        uint finalOut = currentAmount;

        bool isProfit = finalOut > amountIn;
        int profitAmount = int(finalOut) - int(amountIn);

        return (isProfit, finalOut, profitAmount);
    }

    function doubleRouterSwap(
        address spot,
        address tokenIn,
        address tokenOut,
        uint256 amountIn,
        address[] calldata swapPath,
        address[] calldata dexes,
        uint256 expectProfit,
        uint256 minProfit
    ) external returns(uint256 amountOut) {
        require(swapPath.length >= 2, "invalid swapPath");
        require(dexes.length == swapPath.length - 1, "dexes length mismatch");
        require(swapPath[swapPath.length - 1] == tokenOut, "tokenOut mismatch with swapPath");
        require(IERC20(tokenIn).balanceOf(spot) >= amountIn, "insufficient tokenIn balance");

        uint256 currentAmount = amountIn;
        uint256 deadline = block.timestamp + 300;
        uint256 aveProfit = expectProfit / dexes.length;
        for (uint i = 0; i < dexes.length; i++) {
            currentAmount = _executeSingleSwap(
                spot,
                dexes[i],
                swapPath[i],
                swapPath[i + 1],
                currentAmount,
                deadline,
                aveProfit
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
        uint256 aveProfit
    ) internal returns (uint256 outAmount) {
        IERC20(fromToken).approve(routerAddr, 0);
        IERC20(fromToken).approve(routerAddr, currentAmount);

        address[] memory path = new address[](2);
        path[0] = fromToken;
        path[1] = toToken;

        uint[] memory amounts = IUniswapV2Router02(routerAddr).getAmountsOut(currentAmount, path);
        uint256 expectedOut = amounts[amounts.length - 1];
        
        require(expectedOut > currentAmount, "No profit potential");
        require(expectedOut >= currentAmount + aveProfit, "Insufficient profit margin");
        
        uint256 maxLoss = expectedOut - currentAmount - aveProfit;
        require(maxLoss > 0, "No slippage room");
        
        uint256 slippageBps;
        unchecked {
            slippageBps = (maxLoss * 10000) / expectedOut;
        }
        
        if (slippageBps > slippageTolerance) {
            slippageBps = slippageTolerance;
        }
        
        uint256 minOut;
        unchecked {
            minOut = (expectedOut * (10000 - slippageBps)) / 10000;
        }
        minOut = minOut == 0 ? 1 : minOut;

        outAmount = IUniswapV2Router02(routerAddr).swapExactTokensForTokens(
            currentAmount,
            minOut,
            path,
            spot,
            deadline
        )[1];

        emit DoubleRouterSwap2(routerAddr, fromToken, toToken, currentAmount, outAmount);
    }

    function _authorizeUpgrade(address newImplementation) internal override onlyOwner {}
}