// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

import "@openzeppelin/contracts-upgradeable/proxy/utils/Initializable.sol";
import "@openzeppelin/contracts-upgradeable/access/OwnableUpgradeable.sol";
import "@openzeppelin/contracts-upgradeable/proxy/utils/UUPSUpgradeable.sol";

/**
 * 参数配置合约
 * 用于管理套利策略的参数配置
 * 包括：
 * - 关键合约地址配置
 * - 手续费配置
 * - 滑点设置
 */
contract ConfigManage is Initializable, OwnableUpgradeable, UUPSUpgradeable {

    address lendingPool;
    address uniswapV2Router;
    address uniswapV3Router;
    address sushiSwapRouter;
    address arbitrageVault;
    
    constructor() {
        _disableInitializers();
    }

    function initialize(
        address _lendingPool,
        address _uniswapV2Router,
        address _uniswapV3Router,
        address _sushiSwapRouter,
        address _arbitrageVault
    ) public initializer {
        __Owner_init();
        __UUPSUpgradeable_init();

        require(_lendingPool != address(0), "Invalid lending pool");
        require(_uniswapV2Router != address(0), "Invalid uniswapV2 router");
        require(_uniswapV3Router != address(0), "Invalid uniswapV3 router");
        require(_sushiSwapRouter != address(0), "Invalid sushiSwap router");

        lendingPool = _lendingPool;
        uniswapV2Router = _uniswapV2Router;
        uniswapV3Router = _uniswapV3Router;
        sushiSwapRouter = _sushiSwapRouter;
        arbitrageVault = _arbitrageVault;
    }
}