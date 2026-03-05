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

    modifier onlyAuthorizedCaller() {
        require(
            msg.sender == arbitrageCore || msg.sender == owner(),
            "Spot: not authorized"
        );
        _;
    }

    function executeSwaps(
        address asset,
        address tokenOut,
        uint256 amountIn,
        address[] calldata swapPath,
        address[] calldata dexes,
        uint24[] calldata feeTiers,
        uint256 expectProfit,
        uint256 minProfit,
        bool isCex
    ) external onlyAuthorizedCaller nonReentrant returns (uint256 amountOut) {
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

            bool cexFirst = (swapPath[0] != asset);
            bool cexLast = (swapPath[swapPath.length - 1] != tokenOut);

            require(!(cexFirst && cexLast), "Spot: CEX-DEX, invalid cex position");

            if (cexFirst) {
                return _handleCexFirst(
                    asset, tokenOut, amountIn, swapPath, dexes, feeTiers,
                    expectProfit, minProfit
                );
            } else if (cexLast) {
                return _handleCexLast(
                    asset, tokenOut, amountIn, swapPath, dexes, feeTiers,
                    expectProfit, minProfit
                );
            } else {
                revert("Spot: CEX-DEX, invalid cex position");
            }
        }

        // 非CEX路径，全部由DEX完成
        IERC20(asset).approve(address(doubleRouterIntegration), amountIn);

        amountOut = doubleRouterIntegration.doubleRouterSwap(
            address(this),
            asset,
            tokenOut,
            amountIn,
            swapPath,
            dexes,
            feeTiers,
            expectProfit,
            minProfit
        );

        // 将结果转回 ArbitrageCore
        IERC20(tokenOut).safeTransfer(address(arbitrageCore), amountOut);

        return amountOut;
    }

    function _handleCexFirst(
        address /* asset */,
        address tokenOut,
        uint256 amountIn,
        address[] calldata swapPath,
        address[] calldata dexes,
        uint24[] calldata feeTiers,
        uint256 expectProfit,
        uint256 minProfit
    ) internal returns (uint256 amountOut) {
        address intermediateToken = swapPath[0];
        uint256 intermediateAmount = IERC20(intermediateToken).balanceOf(address(this));
        require(intermediateAmount >= amountIn, "Spot: no received from cex");

        // 跳过 swapPath[0] 和 dexes[0]（CEX 占位），从 [1] 开始是 DEX 路径
        address[] memory remainingPath = new address[](swapPath.length - 1);
        for (uint i = 0; i < remainingPath.length; i++) {
            remainingPath[i] = swapPath[i + 1];
        }

        address[] memory remainingDexes = new address[](dexes.length - 1);
        uint24[] memory remainingFees = new uint24[](feeTiers.length - 1);
        for (uint i = 0; i < remainingDexes.length; i++) {
            remainingDexes[i] = dexes[i + 1];
            remainingFees[i] = feeTiers[i + 1];
        }

        IERC20(intermediateToken).approve(address(doubleRouterIntegration), intermediateAmount);

        amountOut = doubleRouterIntegration.doubleRouterSwap(
            address(this),
            intermediateToken,
            tokenOut,
            intermediateAmount,
            remainingPath,
            remainingDexes,
            remainingFees,
            expectProfit,
            minProfit
        );

        IERC20(tokenOut).safeTransfer(address(arbitrageCore), amountOut);
        return amountOut;
    }

    function _handleCexLast(
        address asset,
        address tokenOut,
        uint256 amountIn,
        address[] calldata swapPath,
        address[] calldata dexes,
        uint24[] calldata feeTiers,
        uint256 expectProfit,
        uint256 minProfit
    ) internal returns (uint256 amountOut) {

        uint256 balanceBefore = IERC20(tokenOut).balanceOf(address(this));

        address[] memory dexPath = new address[](dexes.length);
        for (uint i = 0; i < dexes.length; i++) {
            dexPath[i] = swapPath[i];
        }

        address[] memory dexRouters = new address[](dexes.length - 1);
        uint24[] memory dexFees = new uint24[](feeTiers.length - 1);
        for (uint i = 0; i < dexRouters.length; i++) {
            dexRouters[i] = dexes[i];
            dexFees[i] = feeTiers[i];
        }

        IERC20(asset).approve(address(doubleRouterIntegration), amountIn);

        doubleRouterIntegration.doubleRouterSwap(
            address(this),
            asset,
            swapPath[swapPath.length - 2],
            amountIn,
            dexPath,
            dexRouters,
            dexFees,
            expectProfit,
            minProfit
        );

        uint256 balanceAfter = IERC20(tokenOut).balanceOf(address(this));
        require(balanceAfter >= balanceBefore, "CEX last: tokenOut balance decreased");
        uint256 received = balanceAfter - balanceBefore;
        require(received >= minProfit, "CEX last: profit below minimum");

        IERC20(tokenOut).safeTransfer(address(arbitrageCore), balanceAfter);
        return balanceAfter;
    }

    function _authorizeUpgrade(address newImplementation) internal override onlyOwner {}
}
