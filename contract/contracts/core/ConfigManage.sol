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

    // 可配置的费用参数 - 不要在声明时赋初值！
    uint256 public depositFee;              // 存款费（basis points）
    uint256 public withdrawFee;             // 提款费（basis points） 暂定0.01%
    uint256 public performanceFee;          // 业绩费（basis points） 暂定10%

    // 滑点容忍度（basis points） - 不要在声明时赋初值！
    uint256 public slippageTolerance;

    address public lendingPool;
    address public uniswapV2Router;
    address public uniswapV3Router;
    address public sushiSwapRouter;
    address public arbitrageVault;
    address public uniswapV3Quoter;

    event Upgrade(address indexed implementation, uint256 version);
    event DepositFeeUpdated(uint256 newFee);
    event WithdrawFeeUpdated(uint256 newFee);
    event PerformanceFeeUpdated(uint256 newFee);
    event SlipageToleranceUpdated(uint256 newSlippage);
    
    
    // constructor() {
    //     _disableInitializers();
    // }

    function initialize(
        address _lendingPool,
        address _uniswapV2Router,
        address _uniswapV3Router,
        address _sushiSwapRouter,
        address _arbitrageVault,
        address _uniswapV3Quoter
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
        uniswapV3Quoter = _uniswapV3Quoter;
        version = 1;
        
        // 在 initialize 函数中赋初值，而不是在声明时
        slippageTolerance = 300;        // 默认滑点300（3%）
        depositFee = 0;                 // 存款费0%
        withdrawFee = 1;                // 提款费0.01%
        performanceFee = 1000;          // 业绩费10%
        _profitShareFee = 0;            // 初始平台分成0%
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

    // 添加set函数来更新关键合约地址
    function setLendingPool(address _lendingPool) external onlyOwner {
        require(_lendingPool != address(0), "Invalid lending pool");
        lendingPool = _lendingPool;
    }

    function setUniswapV2Router(address _uniswapV2Router) external onlyOwner {
        require(_uniswapV2Router != address(0), "Invalid uniswapV2 router");
        uniswapV2Router = _uniswapV2Router;
    }

    function setUniswapV3Router(address _uniswapV3Router) external onlyOwner {
        require(_uniswapV3Router != address(0), "Invalid uniswapV3 router");
        uniswapV3Router = _uniswapV3Router;
    }

    function setSushiSwapRouter(address _sushiSwapRouter) external onlyOwner {
        require(_sushiSwapRouter != address(0), "Invalid sushiSwap router");
        sushiSwapRouter = _sushiSwapRouter;
    }

    function setArbitrageVault(address _arbitrageVault) external onlyOwner {
        arbitrageVault = _arbitrageVault;
    }

    // ========== 可插拔费用配置 ==========
    
    /**
     * @dev 设置存款费
     * @param _depositFee 新的存款费（basis points，如100表示1%）
     */
    function setDepositFee(uint256 _depositFee) external onlyOwner {
        require(_depositFee <= 10000, "Fee too high");
        depositFee = _depositFee;
        emit DepositFeeUpdated(_depositFee);
    }

    /**
     * @dev 设置提款费
     * @param _withdrawFee 新的提款费（basis points，如100表示1%）
     */
    function setWithdrawFee(uint256 _withdrawFee) external onlyOwner {
        require(_withdrawFee <= 10000, "Fee too high");
        withdrawFee = _withdrawFee;
        emit WithdrawFeeUpdated(_withdrawFee);
    }

    /**
     * @dev 设置业绩费
     * @param _performanceFee 新的业绩费（basis points，如1000表示10%）
     */
    function setPerformanceFee(uint256 _performanceFee) external onlyOwner {
        require(_performanceFee <= 10000, "Fee too high");
        performanceFee = _performanceFee;
        emit PerformanceFeeUpdated(_performanceFee);
    }

    /**
     * @dev 批量设置所有费用
     */
    function setAllFees(
        uint256 _depositFee,
        uint256 _withdrawFee,
        uint256 _performanceFee
    ) external onlyOwner {
        require(_depositFee <= 10000, "Deposit fee too high");
        require(_withdrawFee <= 10000, "Withdraw fee too high");
        require(_performanceFee <= 10000, "Performance fee too high");
        
        depositFee = _depositFee;
        withdrawFee = _withdrawFee;
        performanceFee = _performanceFee;
        
        emit DepositFeeUpdated(_depositFee);
        emit WithdrawFeeUpdated(_withdrawFee);
        emit PerformanceFeeUpdated(_performanceFee);
    }

    function getDepositFee(address /* vault */) external view override returns(uint256) {
        return depositFee;
    }

    function getWithDrawFee(address /* vault */) external view override returns(uint256) {
        return withdrawFee;
    }

    function getPlatFormFee(address /* vault */) external view override returns(uint256) {
        return performanceFee;
    }

    /**
     * 动态配置滑点
     * 请求参数：
     *      _slipageTolerance：滑点容忍度
     */
    function setSlipageTolerance(uint _slippageTolerance) external onlyOwner{
        slippageTolerance = _slippageTolerance;
        emit SlipageToleranceUpdated(_slippageTolerance);
    }

    function _authorizeUpgrade(address newImplementation) internal override onlyOwner{
        require(newImplementation != address(0), "New implementation is zero address");
        version ++;
        emit Upgrade(newImplementation, version);
    }
}