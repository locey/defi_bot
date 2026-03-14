// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

import "../interfaces/IAaveV3Pool.sol";
import "../interfaces/IBalancerVault.sol";
import "../router/IUniswapV2Router02.sol";
import "../router/IUniswapV3Router.sol";

import "@openzeppelin/contracts/token/ERC20/IERC20.sol";
import "@openzeppelin/contracts/token/ERC20/utils/SafeERC20.sol";
import "@openzeppelin/contracts/access/Ownable.sol";
import "@openzeppelin/contracts/utils/ReentrancyGuard.sol";
import "@openzeppelin/contracts/utils/Pausable.sol";

/**
 * @title BalancerLiquidator
 * @notice 使用 Balancer V2 免费闪电贷执行 Aave V3 清算
 *
 * 与 FlashLoanLiquidator 的区别：
 *   - 闪电贷来源: Balancer Vault（0% 费率）vs Aave Pool（0.05% 费率）
 *   - 回调接口: receiveFlashLoan() vs executeOperation()
 *   - 还款方式: safeTransfer 回 Vault vs approve 给 Pool
 *   - 省下的 0.05% 直接变成额外利润
 *
 * 核心流程：
 * 1. Backend 调用 executeLiquidation() 传入清算参数
 * 2. 向 Balancer Vault 请求免费闪电贷（借入 debtAsset）
 * 3. Balancer 回调 receiveFlashLoan()：
 *    a. approve debtAsset → 调用 Aave pool.liquidationCall() 偿还债务
 *    b. 获得 collateralAsset（含清算奖励 5-10%）
 *    c. 将 collateralAsset 通过 DEX 换回 debtAsset
 *    d. 将借入金额转回 Balancer Vault（0 费用）
 *    e. 利润转入 profitReceiver
 */
contract BalancerLiquidator is IFlashLoanRecipient, ReentrancyGuard, Ownable, Pausable {
    using SafeERC20 for IERC20;

    // ==================== 状态变量 ====================

    IBalancerVault public immutable balancerVault;
    IAaveV3Pool public immutable aavePool;
    address public profitReceiver;

    // Backend 白名单
    mapping(address => bool) public authorizedBackends;

    // DEX Router 白名单
    mapping(address => bool) public authorizedRouters;
    mapping(address => bool) public isV3Router;

    // 闪电贷重入保护：防止外部通过 Balancer Vault 直接触发回调
    bool private _inFlashLoan;

    // 执行统计
    uint256 public totalLiquidations;

    // ==================== 事件 ====================

    event LiquidationExecuted(
        address indexed user,
        address indexed collateralAsset,
        address indexed debtAsset,
        uint256 debtCovered,
        uint256 collateralReceived,
        uint256 profit
    );

    event ProfitReceiverUpdated(address oldReceiver, address newReceiver);
    event BackendAuthorized(address indexed backend, bool authorized);
    event RouterAuthorized(address indexed router, bool authorized, bool v3);

    // ==================== 修饰器 ====================

    modifier onlyAuthorizedBackend() {
        require(authorizedBackends[msg.sender], "BalancerLiquidator: not authorized");
        _;
    }

    // ==================== 构造函数 ====================

    constructor(
        address _balancerVault,
        address _aavePool,
        address _profitReceiver
    ) Ownable(msg.sender) {
        require(_balancerVault != address(0), "BalancerLiquidator: invalid vault");
        require(_aavePool != address(0), "BalancerLiquidator: invalid pool");
        require(_profitReceiver != address(0), "BalancerLiquidator: invalid receiver");

        balancerVault = IBalancerVault(_balancerVault);
        aavePool = IAaveV3Pool(_aavePool);
        profitReceiver = _profitReceiver;

        // 部署者自动授权
        authorizedBackends[msg.sender] = true;
    }

    // ==================== 核心函数 ====================

    /**
     * @notice 发起 Balancer 闪电贷清算（带 MEV 保护）
     * @param collateralAsset 抵押品资产（清算后获得）
     * @param debtAsset 债务资产（闪电贷借入）
     * @param user 被清算用户
     * @param debtToCover 要覆盖的债务数量
     * @param swapRouter DEX router 地址
     * @param swapFeeTier V3 fee tier (500/3000/10000)；0 = V2
     * @param minProfitAmount 最小利润（防 MEV 三明治攻击），0 = 不检查
     */
    function executeLiquidation(
        address collateralAsset,
        address debtAsset,
        address user,
        uint256 debtToCover,
        address swapRouter,
        uint24 swapFeeTier,
        uint256 minProfitAmount
    ) external onlyAuthorizedBackend nonReentrant whenNotPaused {
        require(collateralAsset != address(0), "BalancerLiquidator: invalid collateral");
        require(debtAsset != address(0), "BalancerLiquidator: invalid debt");
        require(user != address(0), "BalancerLiquidator: invalid user");
        require(debtToCover > 0, "BalancerLiquidator: zero debt");
        require(authorizedRouters[swapRouter], "BalancerLiquidator: unauthorized router");

        // 编码回调参数
        bytes memory userData = abi.encode(
            collateralAsset,
            debtAsset,
            user,
            debtToCover,
            swapRouter,
            swapFeeTier,
            minProfitAmount
        );

        // 构建 Balancer flashLoan 参数（单 token）
        IERC20[] memory tokens = new IERC20[](1);
        tokens[0] = IERC20(debtAsset);

        uint256[] memory amounts = new uint256[](1);
        amounts[0] = debtToCover;

        // 发起 Balancer 免费闪电贷（设置 _inFlashLoan 防止外部触发回调）
        _inFlashLoan = true;
        balancerVault.flashLoan(
            IFlashLoanRecipient(address(this)),
            tokens,
            amounts,
            userData
        );
        _inFlashLoan = false;
    }

    /**
     * @notice Balancer 闪电贷回调 — 执行清算 + swap + 还款
     * @dev 只接受 Balancer Vault 的回调
     */
    function receiveFlashLoan(
        IERC20[] memory tokens,
        uint256[] memory amounts,
        uint256[] memory feeAmounts,
        bytes memory userData
    ) external override {
        // 安全校验：只接受通过 executeLiquidation 触发的 Balancer Vault 回调
        // _inFlashLoan 防止外部直接调用 Vault.flashLoan(thisContract, ...) 绕过权限
        require(msg.sender == address(balancerVault), "BalancerLiquidator: caller must be BalancerVault");
        require(_inFlashLoan, "BalancerLiquidator: not initiated by this contract");

        // 解码参数
        (
            address collateralAsset,
            address debtAsset,
            address user,
            uint256 debtToCover,
            address swapRouter,
            uint24 swapFeeTier,
            uint256 minProfitAmount
        ) = abi.decode(userData, (address, address, address, uint256, address, uint24, uint256));

        // ===== Step 1: 执行 Aave V3 清算 =====
        IERC20(debtAsset).forceApprove(address(aavePool), debtToCover);

        uint256 collateralBefore = IERC20(collateralAsset).balanceOf(address(this));

        aavePool.liquidationCall(
            collateralAsset,
            debtAsset,
            user,
            debtToCover,
            false  // receiveAToken = false
        );

        uint256 collateralReceived = IERC20(collateralAsset).balanceOf(address(this)) - collateralBefore;
        require(collateralReceived > 0, "BalancerLiquidator: no collateral received");

        // ===== Step 2: 将 collateral 换回 debtAsset =====
        // Balancer 免费闪电贷：totalDebt = 借入金额 + 0 费用
        uint256 totalDebt = amounts[0] + feeAmounts[0]; // feeAmounts[0] == 0

        if (collateralAsset != debtAsset) {
            _swapCollateralToDebt(
                collateralAsset,
                debtAsset,
                collateralReceived,
                swapRouter,
                swapFeeTier
            );
        }

        uint256 debtBalance = IERC20(debtAsset).balanceOf(address(this));
        require(debtBalance >= totalDebt, "BalancerLiquidator: insufficient to repay");

        // ===== Step 3: 还款给 Balancer Vault（直接 transfer，不用 approve） =====
        IERC20(debtAsset).safeTransfer(address(balancerVault), totalDebt);

        // ===== Step 4: 利润检查 + 转入 profitReceiver =====
        uint256 profit = debtBalance - totalDebt;

        // MEV 三明治防护
        require(profit >= minProfitAmount, "BalancerLiquidator: profit below minimum (MEV protection)");

        if (profit > 0) {
            IERC20(debtAsset).safeTransfer(profitReceiver, profit);
        }

        // 剩余 collateral 也转出
        uint256 remainingCollateral = IERC20(collateralAsset).balanceOf(address(this));
        if (remainingCollateral > 0 && collateralAsset != debtAsset) {
            IERC20(collateralAsset).safeTransfer(profitReceiver, remainingCollateral);
        }

        // 统计
        totalLiquidations++;

        emit LiquidationExecuted(
            user,
            collateralAsset,
            debtAsset,
            debtToCover,
            collateralReceived,
            profit
        );
    }

    // ==================== 内部函数 ====================

    function _swapCollateralToDebt(
        address collateralAsset,
        address debtAsset,
        uint256 amountIn,
        address router,
        uint24 feeTier
    ) internal {
        IERC20(collateralAsset).forceApprove(router, amountIn);

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
                1,
                path,
                address(this),
                block.timestamp + 120
            );
        }

        // 清除残余 allowance（防御性：防止 router 被攻破后利用残余授权）
        IERC20(collateralAsset).forceApprove(router, 0);
    }

    // ==================== 管理函数 ====================

    function setAuthorizedBackend(address backend, bool authorized) external onlyOwner {
        require(backend != address(0), "BalancerLiquidator: zero address");
        authorizedBackends[backend] = authorized;
        emit BackendAuthorized(backend, authorized);
    }

    function setAuthorizedRouter(address router, bool authorized, bool v3) external onlyOwner {
        require(router != address(0), "BalancerLiquidator: zero address");
        authorizedRouters[router] = authorized;
        isV3Router[router] = v3;
        emit RouterAuthorized(router, authorized, v3);
    }

    function setProfitReceiver(address _profitReceiver) external onlyOwner {
        require(_profitReceiver != address(0), "BalancerLiquidator: zero address");
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

    function emergencyWithdraw(address token, uint256 amount) external onlyOwner {
        IERC20(token).safeTransfer(owner(), amount);
    }
}
