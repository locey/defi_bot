// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

import "../interfaces/ISpotArbitrage.sol";
import "../interfaces/IDoubleRouterIntegration.sol";
import "../interfaces/IUniswapV2Integration.sol";

import "@openzeppelin/contracts/token/ERC20/utils/SafeERC20.sol";
import "@openzeppelin/contracts/token/ERC20/IERC20.sol";
import "@openzeppelin/contracts-upgradeable/proxy/utils/Initializable.sol";
import "@openzeppelin/contracts-upgradeable/proxy/utils/UUPSUpgradeable.sol";
import "@openzeppelin/contracts-upgradeable/access/OwnableUpgradeable.sol";


contract SpotArbitrage is ISpotArbitrage, Initializable, UUPSUpgradeable, OwnableUpgradeable {
    using SafeERC20 for IERC20;
    
    address public arbitrageCore;
    IDoubleRouterIntegration private doubleRouterIntegration;
    IUniswapV2Integration private uniswapV2Integration;

    /// @custom:oz-upgrades-unsafe-allow constructor
    constructor() {
        _disableInitializers();
    }

    function initialize(
        address _doubleRouterIntegration,
        address _uniswapV2Integration,
        address _arbitrageCore
    ) public initializer {
        __Ownable_init(msg.sender);
        __UUPSUpgradeable_init();
        
        require(_doubleRouterIntegration != address(0), "Spot: invalid doubleRouterIntegration");
        require(_uniswapV2Integration != address(0), "Spot: invalid uniswapV2Integration");
        // require(_arbitrageCore != address(0), "Spot: invalid arbitrageCore");
        
        doubleRouterIntegration = IDoubleRouterIntegration(_doubleRouterIntegration);
        uniswapV2Integration = IUniswapV2Integration(_uniswapV2Integration);
        arbitrageCore = _arbitrageCore;
    }

    /**
     *实施AMM-AMM套利的实现合约
     *对后端提供的策略（包含代币转换路径和交易所）循环遍历并进行swap
     *
     *
     *
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
    
    function executeSwaps(
        address asset,
        address tokenOut,
        uint256 amountIn,
        address[] calldata swapPath,
        address[] calldata dexes,
        uint256 expectProfit,
        uint256 minProfit
    ) external returns (uint256 amountOut) {
        // 检查金额是否已收到
        require(
            IERC20(asset).balanceOf(address(this)) >= amountIn,
            "Spot: no received"
        );
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
        IERC20(asset).safeTransfer(address(arbitrageCore), amountOut);

        return amountOut;
    }

    function _authorizeUpgrade(address newImplementation) internal override onlyOwner {}
}