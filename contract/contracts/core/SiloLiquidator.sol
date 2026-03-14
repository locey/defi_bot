// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

import "../interfaces/ISiloV1.sol";
import "../router/IUniswapV3Router.sol";
import "../router/IUniswapV2Router02.sol";

import "@openzeppelin/contracts/token/ERC20/IERC20.sol";
import "@openzeppelin/contracts/token/ERC20/utils/SafeERC20.sol";
import "@openzeppelin/contracts/access/Ownable.sol";
import "@openzeppelin/contracts/utils/ReentrancyGuard.sol";
import "@openzeppelin/contracts/utils/Pausable.sol";

/**
 * @title SiloLiquidator
 * @notice Silo Finance V1 闪电清算 — 无需外部闪电贷
 *
 * Silo 内置闪电清算流程（比 Aave/Compound 更简单）：
 *   1. Backend 调用 liquidate()
 *   2. 本合约调用 silo.flashLiquidate()
 *   3. Silo 发送抵押品给本合约，然后回调 siloLiquidationCallback()
 *   4. 在回调中：swap 抵押品 → 债务资产 → 调用 silo.repayFor() 还债
 *   5. 多余的部分 = 利润，转入 profitReceiver
 *
 * 优势：无闪电贷费用（Silo 内置），合约更简单
 */
contract SiloLiquidator is IFlashLiquidationReceiver, ReentrancyGuard, Ownable, Pausable {
    using SafeERC20 for IERC20;

    // ==================== 状态变量 ====================

    address public profitReceiver;

    mapping(address => bool) public authorizedBackends;
    mapping(address => bool) public authorizedRouters;
    mapping(address => bool) public isV3Router;
    mapping(address => bool) public authorizedSilos;

    // 清算中的临时状态（用于回调验证）
    address private _activeSilo;

    uint256 public totalLiquidations;

    // ==================== 事件 ====================

    event SiloLiquidationExecuted(
        address indexed silo,
        address indexed user,
        uint256 assetsCount,
        uint256 profit
    );

    // ==================== 修饰器 ====================

    modifier onlyAuthorizedBackend() {
        require(authorizedBackends[msg.sender], "SiloLiquidator: not authorized");
        _;
    }

    // ==================== 构造函数 ====================

    constructor(address _profitReceiver) Ownable(msg.sender) {
        require(_profitReceiver != address(0), "SiloLiquidator: invalid receiver");
        profitReceiver = _profitReceiver;
        authorizedBackends[msg.sender] = true;
    }

    // ==================== 核心函数 ====================

    /**
     * @notice 执行 Silo 闪电清算
     * @param silo Silo 合约地址
     * @param users 要清算的用户列表
     * @param swapRouter DEX router
     * @param swapFeeTier V3 fee tier；0 = V2
     * @param debtAsset 债务资产地址（用于 swap 目标）
     */
    function liquidate(
        address silo,
        address[] calldata users,
        address swapRouter,
        uint24 swapFeeTier,
        address debtAsset
    ) external onlyAuthorizedBackend nonReentrant whenNotPaused {
        require(users.length > 0, "SiloLiquidator: no users");
        require(authorizedSilos[silo], "SiloLiquidator: unauthorized silo");
        require(authorizedRouters[swapRouter], "SiloLiquidator: unauthorized router");

        // 记录当前 Silo（回调验证用）
        _activeSilo = silo;

        // 编码回调数据
        bytes memory data = abi.encode(swapRouter, swapFeeTier, debtAsset);

        // 调用 Silo 闪电清算
        ISilo(silo).flashLiquidate(users, data);

        // 清除临时状态
        _activeSilo = address(0);
    }

    /**
     * @notice Silo 闪电清算回调
     * @dev Silo 在发送抵押品后调用此函数
     */
    function siloLiquidationCallback(
        address _user,
        address[] calldata _assets,
        uint256[] calldata _receivedCollaterals,
        uint256[] calldata _shareAmountsToRepay,
        bytes calldata _flashReceiverData
    ) external override {
        // 安全校验：只接受当前活跃 Silo 的回调
        require(msg.sender == _activeSilo, "SiloLiquidator: unauthorized callback");

        (
            address swapRouter,
            uint24 swapFeeTier,
            address debtAsset
        ) = abi.decode(_flashReceiverData, (address, uint24, address));

        uint256 totalProfit = 0;

        // 对每个资产：swap → repay
        for (uint256 i = 0; i < _assets.length; i++) {
            if (_receivedCollaterals[i] == 0) continue;

            address collateralAsset = _assets[i];
            uint256 collateralAmount = _receivedCollaterals[i];
            uint256 repayAmount = _shareAmountsToRepay[i];

            if (collateralAsset == debtAsset) {
                // 同资产，直接还债
                if (repayAmount > 0) {
                    IERC20(debtAsset).forceApprove(msg.sender, repayAmount);
                    ISilo(msg.sender).repayFor(debtAsset, _user, repayAmount);
                }
                uint256 remaining = collateralAmount > repayAmount ? collateralAmount - repayAmount : 0;
                totalProfit += remaining;
            } else {
                // 不同资产，swap 后还债
                uint256 debtBefore = IERC20(debtAsset).balanceOf(address(this));

                _swap(collateralAsset, debtAsset, collateralAmount, swapRouter, swapFeeTier);

                uint256 debtAfter = IERC20(debtAsset).balanceOf(address(this));
                uint256 swapOutput = debtAfter - debtBefore;

                // 还债（swap 不足时必须 revert，不能静默失败丢失抵押品）
                require(swapOutput >= repayAmount, "SiloLiquidator: swap output insufficient to repay");
                if (repayAmount > 0) {
                    IERC20(debtAsset).forceApprove(msg.sender, repayAmount);
                    ISilo(msg.sender).repayFor(debtAsset, _user, repayAmount);
                }
                totalProfit += (swapOutput - repayAmount);
            }
        }

        // 转出利润
        if (totalProfit > 0) {
            IERC20(debtAsset).safeTransfer(profitReceiver, totalProfit);
        }

        totalLiquidations++;

        emit SiloLiquidationExecuted(msg.sender, _user, _assets.length, totalProfit);
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

    function setAuthorizedSilo(address silo, bool authorized) external onlyOwner {
        require(silo != address(0), "SiloLiquidator: zero address");
        authorizedSilos[silo] = authorized;
    }

    function setAuthorizedBackend(address backend, bool authorized) external onlyOwner {
        require(backend != address(0), "SiloLiquidator: zero address");
        authorizedBackends[backend] = authorized;
    }

    function setAuthorizedRouter(address router, bool authorized, bool v3) external onlyOwner {
        require(router != address(0), "SiloLiquidator: zero address");
        authorizedRouters[router] = authorized;
        isV3Router[router] = v3;
    }

    function setProfitReceiver(address _profitReceiver) external onlyOwner {
        require(_profitReceiver != address(0), "SiloLiquidator: zero address");
        profitReceiver = _profitReceiver;
    }

    function pause() external onlyOwner { _pause(); }
    function unpause() external onlyOwner { _unpause(); }

    function emergencyWithdraw(address token, uint256 amount) external onlyOwner {
        IERC20(token).safeTransfer(owner(), amount);
    }
}
