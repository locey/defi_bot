// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

import "../interfaces/ICompoundV3Comet.sol";
import "../interfaces/IBalancerVault.sol";
import "../router/IUniswapV3Router.sol";
import "../router/IUniswapV2Router02.sol";

import "@openzeppelin/contracts/token/ERC20/IERC20.sol";
import "@openzeppelin/contracts/token/ERC20/utils/SafeERC20.sol";
import "@openzeppelin/contracts/access/Ownable.sol";
import "@openzeppelin/contracts/utils/ReentrancyGuard.sol";
import "@openzeppelin/contracts/utils/Pausable.sol";

/**
 * @title CompoundV3Liquidator
 * @notice 使用 Balancer 免费闪电贷执行 Compound V3 清算
 *
 * Compound V3 清算流程（两步式）：
 *   1. absorb() — 协议没收水下账户的全部抵押品，自动偿还债务
 *   2. buyCollateral() — 用 base asset（如 USDC）以折扣价买入被没收的抵押品
 *
 * 本合约的执行流程：
 *   1. Backend 调用 liquidate()，传入 Comet 市场地址和目标账户
 *   2. 向 Balancer Vault 请求免费闪电贷（借入 base asset）
 *   3. Balancer 回调 receiveFlashLoan()：
 *      a. 调用 comet.absorb() 没收账户
 *      b. approve base asset → 调用 comet.buyCollateral() 折价买入抵押品
 *      c. 将抵押品通过 DEX 换回 base asset
 *      d. 还款给 Balancer Vault（0 费用）
 *      e. 利润转入 profitReceiver
 */
contract CompoundV3Liquidator is IFlashLoanRecipient, ReentrancyGuard, Ownable, Pausable {
    using SafeERC20 for IERC20;

    // ==================== 状态变量 ====================

    IBalancerVault public immutable balancerVault;
    address public profitReceiver;

    // 白名单
    mapping(address => bool) public authorizedBackends;
    mapping(address => bool) public authorizedRouters;
    mapping(address => bool) public isV3Router;
    mapping(address => bool) public authorizedComets; // 允许的 Comet 市场

    // 闪电贷重入保护
    bool private _inFlashLoan;

    // 统计
    uint256 public totalLiquidations;

    // ==================== 事件 ====================

    event CompoundLiquidationExecuted(
        address indexed comet,
        address indexed user,
        address indexed collateralAsset,
        uint256 baseSpent,
        uint256 collateralReceived,
        uint256 profit
    );

    event CometAuthorized(address indexed comet, bool authorized);
    event BackendAuthorized(address indexed backend, bool authorized);
    event RouterAuthorized(address indexed router, bool authorized, bool v3);

    // ==================== 修饰器 ====================

    modifier onlyAuthorizedBackend() {
        require(authorizedBackends[msg.sender], "CompoundV3Liquidator: not authorized");
        _;
    }

    // ==================== 构造函数 ====================

    constructor(
        address _balancerVault,
        address _profitReceiver
    ) Ownable(msg.sender) {
        require(_balancerVault != address(0), "CompoundV3Liquidator: invalid vault");
        require(_profitReceiver != address(0), "CompoundV3Liquidator: invalid receiver");

        balancerVault = IBalancerVault(_balancerVault);
        profitReceiver = _profitReceiver;
        authorizedBackends[msg.sender] = true;
    }

    // ==================== 核心函数 ====================

    /**
     * @notice 执行 Compound V3 清算
     * @param comet Comet 市场地址
     * @param user 被清算用户
     * @param collateralAsset 要购买的抵押品
     * @param baseAmount 用于购买的 base asset 数量
     * @param swapRouter DEX router（将抵押品换回 base）
     * @param swapFeeTier V3 fee tier；0 = V2
     * @param minProfit 最小利润（MEV 保护）
     */
    function liquidate(
        address comet,
        address user,
        address collateralAsset,
        uint256 baseAmount,
        address swapRouter,
        uint24 swapFeeTier,
        uint256 minProfit
    ) external onlyAuthorizedBackend nonReentrant whenNotPaused {
        require(authorizedComets[comet], "CompoundV3Liquidator: unauthorized comet");
        require(authorizedRouters[swapRouter], "CompoundV3Liquidator: unauthorized router");
        require(baseAmount > 0, "CompoundV3Liquidator: zero amount");

        address baseToken = ICompoundV3Comet(comet).baseToken();

        bytes memory userData = abi.encode(
            comet,
            user,
            collateralAsset,
            baseAmount,
            swapRouter,
            swapFeeTier,
            minProfit
        );

        // Balancer 免费闪电贷
        IERC20[] memory tokens = new IERC20[](1);
        tokens[0] = IERC20(baseToken);

        uint256[] memory amounts = new uint256[](1);
        amounts[0] = baseAmount;

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
     * @notice Balancer 闪电贷回调
     */
    function receiveFlashLoan(
        IERC20[] memory, /* tokens */
        uint256[] memory amounts,
        uint256[] memory feeAmounts,
        bytes memory userData
    ) external override {
        require(msg.sender == address(balancerVault), "CompoundV3Liquidator: caller must be BalancerVault");
        require(_inFlashLoan, "CompoundV3Liquidator: not initiated by this contract");

        (
            address comet,
            address user,
            address collateralAsset,
            uint256 baseAmount,
            address swapRouter,
            uint24 swapFeeTier,
            uint256 minProfit
        ) = abi.decode(userData, (address, address, address, uint256, address, uint24, uint256));

        address baseToken = ICompoundV3Comet(comet).baseToken();
        uint256 totalDebt = amounts[0] + feeAmounts[0]; // feeAmounts[0] == 0

        // ===== Step 1: absorb — 没收水下仓位 =====
        address[] memory accounts = new address[](1);
        accounts[0] = user;
        ICompoundV3Comet(comet).absorb(address(this), accounts);

        // ===== Step 2: buyCollateral — 折价买入抵押品 =====
        IERC20(baseToken).forceApprove(comet, baseAmount);

        uint256 collateralBefore = IERC20(collateralAsset).balanceOf(address(this));
        ICompoundV3Comet(comet).buyCollateral(
            collateralAsset,
            1,              // minAmount: 最小接收 1 wei（利润检查在最后）
            baseAmount,
            address(this)
        );
        uint256 collateralReceived = IERC20(collateralAsset).balanceOf(address(this)) - collateralBefore;
        require(collateralReceived > 0, "CompoundV3Liquidator: no collateral received");

        // ===== Step 3: 将抵押品换回 base asset =====
        if (collateralAsset != baseToken) {
            _swap(collateralAsset, baseToken, collateralReceived, swapRouter, swapFeeTier);
        }

        uint256 baseBalance = IERC20(baseToken).balanceOf(address(this));
        require(baseBalance >= totalDebt, "CompoundV3Liquidator: insufficient to repay");

        // ===== Step 4: 还款给 Balancer =====
        IERC20(baseToken).safeTransfer(address(balancerVault), totalDebt);

        // ===== Step 5: 利润检查 + 转出 =====
        uint256 profit = baseBalance - totalDebt;
        require(profit >= minProfit, "CompoundV3Liquidator: profit below minimum (MEV protection)");

        if (profit > 0) {
            IERC20(baseToken).safeTransfer(profitReceiver, profit);
        }

        // 剩余抵押品也转出
        uint256 remaining = IERC20(collateralAsset).balanceOf(address(this));
        if (remaining > 0 && collateralAsset != baseToken) {
            IERC20(collateralAsset).safeTransfer(profitReceiver, remaining);
        }

        totalLiquidations++;

        emit CompoundLiquidationExecuted(
            comet,
            user,
            collateralAsset,
            baseAmount,
            collateralReceived,
            profit
        );
    }

    // ==================== 内部函数 ====================

    function _swap(
        address tokenIn,
        address tokenOut,
        uint256 amountIn,
        address router,
        uint24 feeTier
    ) internal {
        IERC20(tokenIn).forceApprove(router, amountIn);

        if (feeTier > 0 || isV3Router[router]) {
            uint24 fee = feeTier > 0 ? feeTier : 3000;
            IUniswapV3Router(router).exactInputSingle(
                IUniswapV3Router.ExactInputSingleParams({
                    tokenIn: tokenIn,
                    tokenOut: tokenOut,
                    fee: fee,
                    recipient: address(this),
                    deadline: block.timestamp + 120,
                    amountIn: amountIn,
                    amountOutMinimum: 1,
                    sqrtPriceLimitX96: 0
                })
            );
        } else {
            address[] memory path = new address[](2);
            path[0] = tokenIn;
            path[1] = tokenOut;
            IUniswapV2Router02(router).swapExactTokensForTokens(
                amountIn, 1, path, address(this), block.timestamp + 120
            );
        }

        // 清除残余 allowance
        IERC20(tokenIn).forceApprove(router, 0);
    }

    // ==================== 管理函数 ====================

    function setAuthorizedComet(address comet, bool authorized) external onlyOwner {
        require(comet != address(0), "CompoundV3Liquidator: zero address");
        authorizedComets[comet] = authorized;
        emit CometAuthorized(comet, authorized);
    }

    function setAuthorizedBackend(address backend, bool authorized) external onlyOwner {
        require(backend != address(0), "CompoundV3Liquidator: zero address");
        authorizedBackends[backend] = authorized;
        emit BackendAuthorized(backend, authorized);
    }

    function setAuthorizedRouter(address router, bool authorized, bool v3) external onlyOwner {
        require(router != address(0), "CompoundV3Liquidator: zero address");
        authorizedRouters[router] = authorized;
        isV3Router[router] = v3;
        emit RouterAuthorized(router, authorized, v3);
    }

    function setProfitReceiver(address _profitReceiver) external onlyOwner {
        require(_profitReceiver != address(0), "CompoundV3Liquidator: zero address");
        profitReceiver = _profitReceiver;
    }

    function pause() external onlyOwner { _pause(); }
    function unpause() external onlyOwner { _unpause(); }

    function emergencyWithdraw(address token, uint256 amount) external onlyOwner {
        IERC20(token).safeTransfer(owner(), amount);
    }
}
