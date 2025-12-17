// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

import "../interfaces/IFlashLoanSimpleReceiver.sol";
import "../router/FlashLoanRouter.sol";
import "../interfaces/ISpotArbitrage.sol";
import "../interfaces/IConfigManager.sol";

import "@openzeppelin/contracts/token/ERC20/IERC20.sol";
import "@openzeppelin/contracts/access/Ownable.sol";
import "@openzeppelin/contracts/utils/ReentrancyGuard.sol";
import "@openzeppelin/contracts/utils/Pausable.sol";


// 修正接口继承顺序，规范命名
contract FlashLoanArbitrage is IFlashLoanSimpleReceiver, ReentrancyGuard, Ownable, Pausable {
    /**
     * 功能：集成闪电贷完成套利
     * 核心流程：
     * 1. ArbitrageCore调用executeFlashLoan发起闪电贷请求
     * 2. FlashLoanRouter完成资金借出，回调executeOperation
     * 3. executeOperation调用SpotArbitrage执行套利
     * 4. 扣取平台费，偿还闪电贷，记录收益
     */

    // 核心合约依赖（不可变，部署时初始化）
    FlashLoanRouter public immutable flashLoanRouter;
    ISpotArbitrage public immutable spotArbitrage;
    IConfigManager public immutable configManager;//参数管理

    address public immutable arbitrageCore; // 核心调度合约（不可变，避免被篡改）

    // 平台手续费：利润的10%（千分比，1000 = 10%）
    //uint256 public constant PROFIT_SHARE_FEE = 100; // 10% 平台分润手续费
    //address public feeRecipient; // 手续费接收地址
    //盈利接受：平台钱包地址
    address public platFormWallet;

    // 执行记录结构体及数组
    struct ExecutionRecord {
        address initiator;
        address tokenIn;
        uint256 amountIn;
        uint256 profit;
        uint256 timestamp;
    }
    ExecutionRecord[] public executedHistory;

    // 事件（修正拼写+完善）
    event FlashLoanRequested(
        address indexed initiator,
        address indexed token,
        uint256 amount,
        FlashLoanRouter.LendingPlatForm platform
    );

    event FlashLoanExecuted(
        address indexed initiator,
        address indexed token,
        uint256 borrowed,
        uint256 premium,
        uint256 profit
    );

    event FlashLoanFailed(
        address indexed initiator,
        address indexed token,
        string reason
    );

    event FeeRecipientUpdated(address oldRecipient, address newRecipient);

    // 修饰器
    modifier onlyArbitrageCore() {
        require(msg.sender == arbitrageCore, "FlashLoanArbitrage: only ArbitrageCore can call");
        _;
    }

    // 构造函数
    constructor (
        address _flashLoanRouter,
        address _spotArbitrage,
        address _arbitrageCore,
        //address _feeRecipient,
        address _platFormWallet,
        address _configManager
    ) Ownable(msg.sender) {
        require(_flashLoanRouter != address(0), "FlashLoanArbitrage: invalid flashLoanRouter");
        require(_spotArbitrage != address(0), "FlashLoanArbitrage: invalid spotArbitrage");
        require(_arbitrageCore != address(0), "FlashLoanArbitrage: invalid arbitrageCore");
        require(_platFormWallet != address(0), "FlashLoanArbitrage: invalid platFormWallet");
        require(_configManager != address(0), "FlashLoanArbitrage: invalid configManager");

        flashLoanRouter = FlashLoanRouter(_flashLoanRouter);
        spotArbitrage = ISpotArbitrage(_spotArbitrage);
        arbitrageCore = _arbitrageCore;
        //feeRecipient = _feeRecipient;
        platFormWallet = _platFormWallet;
        configManager = IConfigManager(_configManager);
    }

    // ===================== 核心回调函数=====================
    function executeOperation(
        address asset,
        uint256 amount,
        uint256 premium,
        address initiator,
        bytes calldata params
    ) external override nonReentrant whenNotPaused returns (bool) {
        // 关键：校验调用者是闪电贷路由合约（防止伪造调用）
        require(msg.sender == address(flashLoanRouter), "FlashLoanArbitrage: only flashLoanRouter can call");

        // 解码参数
        (
            address _initiator,
            address tokenIn,
            uint256 amountIn,
            address[] memory swapPath,
            address[] memory dexes,
            uint256 minProfit
        ) = abi.decode(params, (address, address, uint256, address[], address[], uint256));

        // 基础校验
        require(_initiator == initiator, "FlashLoanArbitrage: invalid initiator");
        require(swapPath.length >= 2, "FlashLoanArbitrage: swapPath length must >=2"); // 防止数组越界
        require(dexes.length > 0, "FlashLoanArbitrage: dexes cannot be empty");
        require(amountIn > 0, "FlashLoanArbitrage: amountIn must >0");

        try spotArbitrage.executeSwaps(
            tokenIn,
            swapPath[swapPath.length - 1],
            amountIn,
            swapPath,
            dexes,
            minProfit
        ) returns (uint256 amountOut) {
            // 计算总债务和利润 总借款+手续费
            uint256 totalDebt = amount + premium;
            require(amountOut > totalDebt + minProfit, "FlashLoanArbitrage: no profit made");

            // 计算平台手续费和净利润
            uint256 grossProfit = amountOut - totalDebt;//本次套利总收益
            //uint256 platformFee = (grossProfit * configManager.profitShareFee()) / 1000; // 平台10%手续费
            //uint256 netProfit = grossProfit - platformFee;//净利润

            // 检查余额是否足够还款
            uint256 contractBalance = IERC20(asset).balanceOf(address(this));
            require(contractBalance >= totalDebt, "FlashLoanArbitrage: insufficient balance to repay");

            // 1. 偿还闪电贷
            IERC20(asset).approve(address(flashLoanRouter), totalDebt);

            // 2. 支付平台手续费
            // if (platformFee > 0) {
            //     IERC20(asset).transfer(feeRecipient, platformFee);
            // }
            IERC20(asset).transfer(platFormWallet, grossProfit);

            // 3. 记录执行历史
            executedHistory.push(ExecutionRecord({
                initiator: initiator,
                tokenIn: tokenIn,
                amountIn: amountIn,
                profit: grossProfit,
                //platformFee: platformFee,
                timestamp: block.timestamp
            }));

            // 触发成功事件
            emit FlashLoanExecuted(
                initiator,
                asset,
                amount,
                premium,
                grossProfit
            );

            return true;
        } catch (bytes memory reason) {
            // 触发失败事件
            string memory errorReason = abi.decode(reason, (string));
            emit FlashLoanFailed(initiator, asset, errorReason);
            revert(string.concat("FlashLoanArbitrage: executeOperation failed - ", errorReason));
        }
    }

    // ===================== 发起闪电贷 =====================
    function executeFlashLoan(
        FlashLoanRouter.LendingPlatForm platform,
        address tokenIn,
        uint256 amountIn,
        address[] calldata swapPath,
        address[] calldata dexes,
        uint256 minProfit 
    ) external onlyArbitrageCore nonReentrant whenNotPaused {
        // 前置校验（防止无效请求）
        require(swapPath.length >= 3, "FlashLoanArbitrage: swapPath length must >=3");
        require(dexes.length > 0, "FlashLoanArbitrage: dexes cannot be empty");
        require(amountIn > 0, "FlashLoanArbitrage: amountIn must >0");
        require(minProfit > 0, "FlashLoanArbitrage: minProfit must >0");

        // 构造回调参数
        bytes memory params = abi.encode(
            msg.sender, // initiator = ArbitrageCore
            tokenIn,
            amountIn,
            swapPath,
            dexes,
            minProfit
        );

        // 发起闪电贷
        try flashLoanRouter.requestFlashLoan(
            platform,
            address(this),
            tokenIn,
            amountIn,
            params
        ) {
            emit FlashLoanRequested(msg.sender, tokenIn, amountIn, platform);
        } catch (bytes memory reason) {
            string memory errorReason = abi.decode(reason, (string));
            emit FlashLoanFailed(msg.sender, tokenIn, errorReason);
            revert(string.concat("FlashLoanArbitrage: request flashLoan failed - " , errorReason));
        }
    }

    // ===================== 管理函数=====================
    /**
     * @dev 紧急暂停/恢复合约（仅所有者）
     */
    function pause() external onlyOwner {
        _pause();
    }

    function unpause() external onlyOwner {
        _unpause();
    }

    /**
     * @dev 获取执行历史总数
     */
    function getExecutionHistoryLength() external view returns (uint256) {
        return executedHistory.length;
    }
}