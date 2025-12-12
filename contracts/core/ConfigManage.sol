// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

import "../interfaces/IConfigManager.sol";

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
contract ConfigManage is IConfigManager, Initializable, OwnableUpgradeable, UUPSUpgradeable {

    uint16 public version;
    uint256 private _profitShareFee;
    uint256 public slippageTolerance;

    address public lendingPool;
    address public uniswapV2Router;
    address public uniswapV3Router;
    address public sushiSwapRouter;
    address public arbitrageVault;

    event Upgrade(address indexed implemetation, uint256 version);
    
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
        __Ownable_init(msg.sender);
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
        version = 1;
        // 默认滑点500（5%）
        slippageTolerance = 500;
    }

    //ArbitrageCore合约用到平台分成
    function profitShareFee() external view override returns(uint256) {
        return _profitShareFee;
    }

    //管理员可对平台分成进行配置
    function setProfitShareFee(uint256 feeBps) external onlyOwner {
        require(feeBps < 10000, "feeBps too high");
        _profitShareFee = feeBps;
    }

    function _authorizeUpgrade(address newImplementation) internal override onlyOwner{
        require(newImplementation != address(0), "New implementation is zero address");
        version ++;
        emit Upgrade(newImplementation, version);
    }

    /**
     * 动态配置滑点
     * 请求参数：
     *      _slipageTolerance：滑点容忍度
     */
    function setSlipageTolerance(uint _slippageTolerance) external onlyOwner{
        slippageTolerance = _slippageTolerance;
    }
}