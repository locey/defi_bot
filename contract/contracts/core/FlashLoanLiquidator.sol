// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

import "../interfaces/IAaveV3Pool.sol";
import "../interfaces/IFlashLoanSimpleReceiver.sol";
import "../router/IUniswapV2Router02.sol";
import "../router/IUniswapV3Router.sol";

import "@openzeppelin/contracts/token/ERC20/IERC20.sol";
import "@openzeppelin/contracts/token/ERC20/utils/SafeERC20.sol";
import "@openzeppelin/contracts/access/Ownable.sol";
import "@openzeppelin/contracts/utils/ReentrancyGuard.sol";
import "@openzeppelin/contracts/utils/Pausable.sol";

/**
 * @title FlashLoanLiquidator
 * @notice 使用 Aave V3 闪电贷执行清算，获取清算奖励（5-10%）
 *
 * 核心流程：
 * 1. Backend 调用 executeLiquidation() 传入清算参数
 * 2. 向 Aave V3 Pool 请求闪电贷（借入 debtAsset）
 * 3. Aave 回调 executeOperation()：
 *    a. approve debtAsset → 调用 pool.liquidationCall() 偿还债务
 *    b. 获得 collateralAsset（含清算奖励 5-10%）
 *    c. 将 collateralAsset 通过 DEX 换回 debtAsset
 *    d. 偿还闪电贷（本金 + premium）
 *    e. 利润转入 profitReceiver
 */
contract FlashLoanLiquidator is ReentrancyGuard, Ownable, Pausable {
    using SafeERC20 for IERC20;

    // ==================== 状态变量 ====================

    IAaveV3Pool public immutable aavePool;
    address public profitReceiver;

    // Backend 白名单
    mapping(address => bool) public authorizedBackends;

    // DEX Router 白名单（用于清算后 collateral→debt swap）
    mapping(address => bool) public authorizedRouters;
    mapping(address => bool) public isV3Router;

    // 执行记录
    struct LiquidationRecord {
        address liquidator;
        address user;              // 被清算用户
        address collateralAsset;
        address debtAsset;
        uint256 debtCovered;
        uint256 collateralReceived;
        uint256 profit;
        uint256 timestamp;
    }
    LiquidationRecord[] public liquidationHistory;

    // ==================== 事件 ====================

    event LiquidationExecuted(
        address indexed user,
        address indexed collateralAsset,
        address indexed debtAsset,
        uint256 debtCovered,
        uint256 collateralReceived,
        uint256 profit
    );

    event LiquidationFailed(
        address indexed user,
        address indexed debtAsset,
        string reason
    );

    event ProfitReceiverUpdated(address oldReceiver, address newReceiver);
    event BackendAuthorized(address indexed backend, bool authorized);
    event RouterAuthorized(address indexed router, bool authorized, bool v3);

    // ==================== 修饰器 ====================

    modifier onlyAuthorizedBackend() {
        require(authorizedBackends[msg.sender], "FlashLoanLiquidator: not authorized");
        _;
    }

    // ==================== 构造函数 ====================

    constructor(
        address _aavePool,
        address _profitReceiver
    ) Ownable(msg.sender) {
        require(_aavePool != address(0), "FlashLoanLiquidator: invalid pool");
        require(_profitReceiver != address(0), "FlashLoanLiquidator: invalid receiver");

        aavePool = IAaveV3Pool(_aavePool);
        profitReceiver = _profitReceiver;

        // 部署者自动授权
        authorizedBackends[msg.sender] = true;
    }

    // ==================== 核心函数 ====================

    /**
     * @notice 发起闪电贷清算
     * @param collateralAsset 抵押品资产（清算后获得）
     * @param debtAsset 债务资产（闪电贷借入）
     * @param user 被清算用户
     * @param debtToCover 要覆盖的债务数量
     * @param swapRouter 清算后 collateral→debt 使用的 DEX router
     * @param swapFeeTier V3 fee tier (500/3000/10000)；0 表示 V2
     */
    function executeLiquidation(
        address collateralAsset,
        address debtAsset,
        address user,
        uint256 debtToCover,
        address swapRouter,
        uint24 swapFeeTier
    ) external onlyAuthorizedBackend nonReentrant whenNotPaused {
        // 保持向后兼容：minProfitAmount = 0 表示不检查最小利润
        _executeLiquidation(collateralAsset, debtAsset, user, debtToCover, swapRouter, swapFeeTier, 0);
    }

    /**
     * @notice 发起闪电贷清算（带最小利润保护）
     * @param collateralAsset 抵押品资产
     * @param debtAsset 债务资产
     * @param user 被清算用户
     * @param debtToCover 要覆盖的债务数量
     * @param swapRouter DEX router 地址
     * @param swapFeeTier V3 fee tier；0 = V2
     * @param minProfitAmount 最小利润（以 debtAsset 计），低于则 revert（防 MEV 三明治）
     */
    function executeLiquidationWithMinProfit(
        address collateralAsset,
        address debtAsset,
        address user,
        uint256 debtToCover,
        address swapRouter,
        uint24 swapFeeTier,
        uint256 minProfitAmount
    ) external onlyAuthorizedBackend nonReentrant whenNotPaused {
        _executeLiquidation(collateralAsset, debtAsset, user, debtToCover, swapRouter, swapFeeTier, minProfitAmount);
    }

    function _executeLiquidation(
        address collateralAsset,
        address debtAsset,
        address user,
        uint256 debtToCover,
        address swapRouter,
        uint24 swapFeeTier,
        uint256 minProfitAmount
    ) internal {
        require(collateralAsset != address(0), "FlashLoanLiquidator: invalid collateral");
        require(debtAsset != address(0), "FlashLoanLiquidator: invalid debt");
        require(user != address(0), "FlashLoanLiquidator: invalid user");
        require(debtToCover > 0, "FlashLoanLiquidator: zero debt");
        require(authorizedRouters[swapRouter], "FlashLoanLiquidator: unauthorized router");

        // 编码回调参数（含 minProfitAmount）
        bytes memory params = abi.encode(
            collateralAsset,
            debtAsset,
            user,
            debtToCover,
            swapRouter,
            swapFeeTier,
            minProfitAmount
        );

        // 向 Aave V3 Pool 直接请求闪电贷（借入 debtAsset）
        aavePool.flashLoanSimple(
            address(this),  // receiver = 本合约
            debtAsset,       // 借入债务代币
            debtToCover,     // 借入金额 = 要覆盖的债务
            params,
            0                // referralCode
        );
    }

    /**
     * @notice Aave 闪电贷回调 — 执行清算 + swap + 还款
     */
    function executeOperation(
        address asset,       // 借入的资产（= debtAsset）
        uint256 amount,      // 借入金额
        uint256 premium,     // 闪电贷手续费
        address initiator,   // 发起者（= 本合约）
        bytes calldata params
    ) external returns (bool) {
        // 安全校验：只接受 Aave Pool 的回调
        require(msg.sender == address(aavePool), "FlashLoanLiquidator: caller must be AavePool");
        require(initiator == address(this), "FlashLoanLiquidator: initiator must be self");

        // 解码参数（含 minProfitAmount）
        (
            address collateralAsset,
            address debtAsset,
            address user,
            uint256 debtToCover,
            address swapRouter,
            uint24 swapFeeTier,
            uint256 minProfitAmount
        ) = abi.decode(params, (address, address, address, uint256, address, uint24, uint256));

        // ===== Step 1: 执行清算 =====
        // approve debtAsset 给 Aave Pool 用于 liquidationCall
        IERC20(debtAsset).safeIncreaseAllowance(address(aavePool), debtToCover);

        uint256 collateralBefore = IERC20(collateralAsset).balanceOf(address(this));

        aavePool.liquidationCall(
            collateralAsset,
            debtAsset,
            user,
            debtToCover,
            false  // receiveAToken = false，直接接收底层资产
        );

        uint256 collateralReceived = IERC20(collateralAsset).balanceOf(address(this)) - collateralBefore;
        require(collateralReceived > 0, "FlashLoanLiquidator: no collateral received");

        // ===== Step 2: 将 collateral 换回 debtAsset =====
        uint256 totalDebt = amount + premium; // 需要还的总额
        if (collateralAsset != debtAsset) {
            // 需要 swap
            _swapCollateralToDebt(
                collateralAsset,
                debtAsset,
                collateralReceived,
                swapRouter,
                swapFeeTier
            );
        }

        uint256 debtBalance = IERC20(debtAsset).balanceOf(address(this));
        require(debtBalance >= totalDebt, "FlashLoanLiquidator: insufficient to repay");

        // ===== Step 3: 还款 =====
        IERC20(asset).safeIncreaseAllowance(msg.sender, totalDebt);

        // ===== Step 4: 利润检查 + 转入 profitReceiver =====
        uint256 profit = debtBalance - totalDebt;
        // MEV 三明治防护：如果利润低于预期最小值，revert 整笔交易（包括闪电贷）
        require(profit >= minProfitAmount, "FlashLoanLiquidator: profit below minimum (MEV protection)");
        if (profit > 0) {
            IERC20(debtAsset).safeTransfer(profitReceiver, profit);
        }

        // 如果还有剩余的 collateral（collateral == debtAsset 场景下不会有），也转出
        uint256 remainingCollateral = IERC20(collateralAsset).balanceOf(address(this));
        if (remainingCollateral > 0 && collateralAsset != debtAsset) {
            IERC20(collateralAsset).safeTransfer(profitReceiver, remainingCollateral);
        }

        // 记录历史
        liquidationHistory.push(LiquidationRecord({
            liquidator: tx.origin,
            user: user,
            collateralAsset: collateralAsset,
            debtAsset: debtAsset,
            debtCovered: debtToCover,
            collateralReceived: collateralReceived,
            profit: profit,
            timestamp: block.timestamp
        }));

        emit LiquidationExecuted(
            user,
            collateralAsset,
            debtAsset,
            debtToCover,
            collateralReceived,
            profit
        );

        return true;
    }

    // ==================== 内部函数 ====================

    /**
     * @dev 将清算获得的 collateral 换成 debtAsset
     */
    function _swapCollateralToDebt(
        address collateralAsset,
        address debtAsset,
        uint256 amountIn,
        address router,
        uint24 feeTier
    ) internal {
        IERC20(collateralAsset).safeIncreaseAllowance(router, amountIn);

        if (feeTier > 0 || isV3Router[router]) {
            // V3 swap
            uint24 fee = feeTier > 0 ? feeTier : 3000;
            IUniswapV3Router(router).exactInputSingle(
                IUniswapV3Router.ExactInputSingleParams({
                    tokenIn: collateralAsset,
                    tokenOut: debtAsset,
                    fee: fee,
                    recipient: address(this),
                    deadline: block.timestamp + 120,
                    amountIn: amountIn,
                    amountOutMinimum: 1, // 最小输出由整体利润检查保证
                    sqrtPriceLimitX96: 0
                })
            );
        } else {
            // V2 swap
            address[] memory path = new address[](2);
            path[0] = collateralAsset;
            path[1] = debtAsset;

            IUniswapV2Router02(router).swapExactTokensForTokens(
                amountIn,
                1,  // 最小输出由整体利润检查保证
                path,
                address(this),
                block.timestamp + 120
            );
        }
    }

    // ==================== 管理函数 ====================

    function setAuthorizedBackend(address backend, bool authorized) external onlyOwner {
        require(backend != address(0), "FlashLoanLiquidator: zero address");
        authorizedBackends[backend] = authorized;
        emit BackendAuthorized(backend, authorized);
    }

    function setAuthorizedRouter(address router, bool authorized, bool v3) external onlyOwner {
        require(router != address(0), "FlashLoanLiquidator: zero address");
        authorizedRouters[router] = authorized;
        isV3Router[router] = v3;
        emit RouterAuthorized(router, authorized, v3);
    }

    function setProfitReceiver(address _profitReceiver) external onlyOwner {
        require(_profitReceiver != address(0), "FlashLoanLiquidator: zero address");
        address old = profitReceiver;
        profitReceiver = _profitReceiver;
        emit ProfitReceiverUpdated(old, _profitReceiver);
    }

    function pause() external onlyOwner {
        _pause();
    }

    function unpause() external onlyOwner {
        _unpause();
    }

    function getLiquidationHistoryLength() external view returns (uint256) {
        return liquidationHistory.length;
    }

    /**
     * @dev 紧急提取误入合约的代币
     */
    function emergencyWithdraw(address token, uint256 amount) external onlyOwner {
        IERC20(token).safeTransfer(owner(), amount);
    }
}
