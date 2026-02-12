// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

import "../interfaces/ISpotArbitrage.sol";
import "../interfaces/IDoubleRouterIntegration.sol";
import "../interfaces/IUniswapV2Integration.sol";

import "@openzeppelin/contracts-upgradeable/utils/ReentrancyGuardUpgradeable.sol";
import "@openzeppelin/contracts/token/ERC20/utils/SafeERC20.sol";
import "@openzeppelin/contracts/token/ERC20/IERC20.sol";
import "@openzeppelin/contracts-upgradeable/proxy/utils/Initializable.sol";
import "@openzeppelin/contracts-upgradeable/proxy/utils/UUPSUpgradeable.sol";
import "@openzeppelin/contracts-upgradeable/access/OwnableUpgradeable.sol";


contract SpotArbitrage is ISpotArbitrage, Initializable, UUPSUpgradeable, OwnableUpgradeable, ReentrancyGuardUpgradeable {
    using SafeERC20 for IERC20;
    
    address public arbitrageCore;
    address public backendCaller;  // 后端调用者地址
    IDoubleRouterIntegration private doubleRouterIntegration;
    IUniswapV2Integration private uniswapV2Integration;

    /// @custom:oz-upgrades-unsafe-allow constructor
    constructor() {
        _disableInitializers();
    }

    function initialize(
        address _doubleRouterIntegration,
        address _uniswapV2Integration,
        address _arbitrageCore,
        address _backendCaller
    ) public initializer {
        __Ownable_init(msg.sender);
        __UUPSUpgradeable_init();
        __ReentrancyGuard_init();
        
        require(_doubleRouterIntegration != address(0), "Spot: invalid doubleRouterIntegration");
        require(_uniswapV2Integration != address(0), "Spot: invalid uniswapV2Integration");
        require(_backendCaller != address(0), "Spot: invalid backendCaller");
        
        doubleRouterIntegration = IDoubleRouterIntegration(_doubleRouterIntegration);
        uniswapV2Integration = IUniswapV2Integration(_uniswapV2Integration);
        arbitrageCore = _arbitrageCore;
        backendCaller = _backendCaller;
    }

    modifier onlyBackend() {
        require(msg.sender == backendCaller || msg.sender == owner(), "Spot: only backend");
        _;
    }

    /**
     *实施AMM-AMM套利的实现合约
     *对后端提供的策略（包含代币转换路径和交易所）循环遍历并进行swap
     *本合约被ArbitrageCore调用
     */

    function setDoubleRouterIntegration(address _doubleRouterIntegration)
        external
        onlyOwner
    {   
        require(_doubleRouterIntegration != address(0), "Spot: invalid doubleRouterIntegration");
        doubleRouterIntegration = IDoubleRouterIntegration(
            _doubleRouterIntegration
        );
    }

    function setUniswapV2Integration(address _uniswapV2Integration)
        external
        onlyOwner
    {   
        require(_uniswapV2Integration != address(0), "Spot: invalid uniswapV2Integration");
        uniswapV2Integration = IUniswapV2Integration(
            _uniswapV2Integration
        );
    }

    function setArbitrageCore(address _arbitrageCore) external onlyOwner {
        require(_arbitrageCore != address(0), "Spot: invalid arbitrageCore");
        require(_arbitrageCore != arbitrageCore, "Spot: same arbitrageCore");
        arbitrageCore = _arbitrageCore;
    }
    
    function setBackendCaller(address _backendCaller) external onlyOwner {
        require(_backendCaller != address(0), "Spot: invalid backendCaller");
        backendCaller = _backendCaller;
    }
    
    function executeSwaps(
        address asset,
        address tokenOut,
        uint256 amountIn,
        address[] calldata swapPath,
        address[] calldata dexes,
        uint256 expectProfit,
        uint256 minProfit,
        bool isCex
    ) external onlyBackend nonReentrant returns (uint256 amountOut) {
        // 检查金额是否已收到
        require(
            IERC20(asset).balanceOf(address(this)) >= amountIn,
            "Spot: no received"
        );

        if (isCex) {
            require(
                swapPath.length >= 3,
                "Spot: invalid swapPath for cex"
            );

            // CEX第一步：swapPath[0] != asset（合约收到的不是初始资产，而是CEX交换后的代币）
            // CEX最后一步：swapPath[最后一个] != tokenOut（最终产物不是目标代币，需要CEX完成）
            bool cexFirst = (swapPath[0] != asset);
            bool cexLast = (swapPath[swapPath.length - 1] != tokenOut);

            // CEX第一步和最后一步不能同时为true（因为swapPath必须包含完整的交换路径）
            require(!(cexFirst && cexLast), "Spot: CEX-DEX, invalid cex position");

            if (cexFirst) {
                return _handleCexFirst(
                    asset,
                    tokenOut,
                    amountIn,
                    swapPath,
                    dexes,
                    expectProfit,
                    minProfit
                );
            } else if (cexLast) {
                return _handleCexLast(
                    asset,
                    tokenOut,
                    amountIn,
                    swapPath,
                    dexes,
                    expectProfit,
                    minProfit
                );
            } else {
                revert("Spot: CEX-DEX, invalid cex position");
            }
        }

        // 非CEX路径，全部由DEX完成
        IERC20(asset).approve(address(doubleRouterIntegration), amountIn);


        // 执行套利操作
        amountOut = doubleRouterIntegration.doubleRouterSwap(
            address(this),
            asset,
            tokenOut,
            amountIn,
            swapPath,
            dexes,
            expectProfit,
            minProfit
        );

        // 将结果转回 ArbitrageCore
        IERC20(tokenOut).safeTransfer(address(arbitrageCore), amountOut);

        return amountOut;
    }

    function _handleCexFirst(
        address asset, 
        address tokenOut,
        uint256 amountIn,
        address[] calldata swapPath,
        address[] calldata dexes,
        uint256 expectProfit,
        uint256 minProfit
    ) internal returns (uint256 amountOut) {
        // CEX第一步：swapPath[0]是CEX后的代币，合约已经收到了这个代币
        address intermediateToken = swapPath[0];
        uint256 intermediateAmount = IERC20(intermediateToken).balanceOf(address(this));
        require(intermediateAmount >= amountIn, "Spot: no received from cex");

        // 跳过swapPath[0]，从swapPath[1]开始是DEX路径
        address[] memory remainingPath = new address[](swapPath.length - 1);
        for (uint i = 0; i < remainingPath.length; i++) {
            remainingPath[i] = swapPath[i + 1];
        }

        // DEX数量比路径少1
        address[] memory remainingDexes = new address[](dexes.length - 1);
        for (uint i = 0; i < remainingDexes.length; i++) {
            remainingDexes[i] = dexes[i];
        }

        IERC20(intermediateToken).approve(address(doubleRouterIntegration), intermediateAmount);

        // 执行DEX交换，最终得到tokenOut
        amountOut = doubleRouterIntegration.doubleRouterSwap(
            address(this),
            intermediateToken,
            tokenOut,
            intermediateAmount,
            remainingPath,
            remainingDexes,
            expectProfit,
            minProfit
        );

        // 将最终结果转回 ArbitrageCore
        IERC20(tokenOut).safeTransfer(address(arbitrageCore), amountOut);
        return amountOut;
    }

    function _handleCexLast(
        address asset, 
        address tokenOut,
        uint256 amountIn,
        address[] calldata swapPath,
        address[] calldata dexes,
        uint256 expectProfit,
        uint256 minProfit
    ) internal returns (uint256 amountOut) {

        address[] memory dexPath = new address[](dexes.length);
        for (uint i = 0; i < dexes.length; i++) {
            dexPath[i] = swapPath[i];
        }

        address[] memory dexRouters = new address[](dexes.length - 1);
        for (uint i = 0; i < dexRouters.length; i++) {
            dexRouters[i] = dexes[i];
        }

        // 执行前面的DEX交换
        IERC20(asset).approve(address(doubleRouterIntegration), amountIn);

        uint256 intermediateAmount = doubleRouterIntegration.doubleRouterSwap(
        address(this),
        asset,
        swapPath[swapPath.length-2], // 倒数第二个代币
        amountIn,
        dexPath,
        dexRouters,
        expectProfit,
        minProfit
        );
        
        // CEX动作由后端完成，直接将最终结果转回
        uint256 finalAmount = IERC20(tokenOut).balanceOf(address(this));
        require(finalAmount >= intermediateAmount, "CEX last: insufficient final amount");
        
        IERC20(tokenOut).safeTransfer(address(arbitrageCore), finalAmount);
        return finalAmount;
    }

    function _authorizeUpgrade(address newImplementation) internal override onlyOwner {}
}
