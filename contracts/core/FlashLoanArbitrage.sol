// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

import "../interfaces/IFlashLoan.sol";
import "../router/FlashLoanRouter.sol";
import "../interfaces/ISpotArbitrage.sol";

import "@openzeppelin/contracts/token/ERC20/IERC20.sol";
import "@openzeppelin/contracts/access/Ownable.sol";
import "@openzeppelin/contracts/utils/ReentrancyGuard.sol";

contract FlashLoanArbitrage is IFlashLoanSimpleReceiver, ReentrancyGuard, Ownable  {
    /**
    *功能：集成Aave闪电贷
    *闪电贷回调
    *利润计算
    *自动还款
    *本合约为闪电贷套利工作流管理，
    *本合约调用实现合约SpotArbitrage实施套利动作
    *本合约被ArbitrageCore调用
    */

    //定义变量，借贷池，借贷平台等
    FlashLoanRouter public immutable flashLoan;     //路由合约
    ISpotArbitrage public immutable spotArbitrage;  //套利实现合约接口
    address public arbitrageCore;                   // 核心调度合约

    //项目平台手续费
    uint256 public constant PROFIT_SHARE_FEE = 1000;//平台收取利润的10%作为服务费

    //当前执行上下文，用于回调时获取参数
    struct ExcutionContext {
        address initiator;  //发起人
        address tokenIn;    //初始代币
        uint256 amountIn;   //借款额度（申请金额）
        address[] swapPath; //策略路径
        address[] dexes;    //dex路径
        uint256 minProfit;  //最小利润
        bool isExcuting;     //正在执行标识
    }

    ExcutionContext private currentContext;

    //执行记录
    struct ExcutionRecord {
        address initiator;  
        address tokenIn;    
        uint256 amountIn; 
        uint256 profit;
        uint256 timestamp;
    }
 
    ExcutionRecord[] public excutedHistory; 

    //定义事件，进行记录等
    event FlashLoanRequest(
        address indexed initiator,
        address indexed token,
        uint256 amount,
        FlashLoanRouter.LendingPlatForm platForm
    );

    event FlashLoanExcuted(
        address indexed initiator,
        address indexed token,
        uint256 borrowed, //实际借款
        uint256 premium,  //平台利息
        uint256 profit    //最后利润 
    );

    event FlashLoanFaild(
        address indexed initiator,
        string reason
    );

    //修饰器

    modifier onlyArbgitrageCore(){
        require(msg.sender == arbitrageCore, "only ArbgitrageCore can call ");
        _;
    }

    modifier notExcuting() {
        require(!currentContext.isExcuting, "It's excuting");
        _;
    }

    //初始化构造函数
    //lendingPoolAddressProvider:借贷池地址提供者
    constructor (
        address _flashLoanRouter,
        address _spotArbitrage,
        address _arbitrageCore
    ) Ownable(msg.sender) {
        require(_flashLoanRouter != address(0), "Invalid flashLoanRouter");
        require(_spotArbitrage != address(0), "Invalid spotArbitrage");
        require(_arbitrageCore != address(0), "Invalid arbitrageCore");

        flashLoan = FlashLoanRouter(_flashLoanRouter);
        spotArbitrage = ISpotArbitrage(_spotArbitrage);
        arbitrageCore = _arbitrageCore;
    }

    //闪电贷回调函数
    function executeOperation(
        address asset,
        uint256 amount,
        uint256 premium,
        address initiator,
        bytes calldata params
    ) external override returns (bool) {
        //解码参数
        (
            address _initiator,
            address tokenIn,
            uint256 amountIn,
            address[] memory swapPath,
            address[] memory dexes,
            uint256 minProfit
        ) = abi.decode(
            params,
            (address, address, uint256, address[], address[], uint256)
        );

        require(_initiator == initiator, "Invalid initiator");

        //实施套利
        uint256 amountOut = spotArbitrage.executeSwaps(
            tokenIn,
            swapPath[swapPath.length - 1],
            amountIn,
            swapPath,
            dexes,
            minProfit
        );

        //计算利润
        uint256 totalDebt = amount + premium;
        require(amountOut > totalDebt + minProfit, "No profit made");

        uint256 profit = amountOut - totalDebt;

        //支付闪电贷
        IERC20(asset).approve(address(flashLoan), totalDebt);

        //记录执行历史
        excutedHistory.push(ExcutionRecord({
            initiator: initiator,
            tokenIn: tokenIn,
            amountIn: amountIn,
            profit: profit,
            timestamp: block.timestamp
        }));

        emit FlashLoanExcuted(
            initiator,
            asset,
            amount,
            premium,
            profit
        );

        return true;
    }

    //核心函数：实施闪电贷，并调用内部函数_initFlashLoan发起闪电贷
    function executeFlashLoan(
        FlashLoanRouter.LendingPlatForm platform,
        address tokenIn,
        uint256 amountIn,
        address[] calldata swapPath,
        address[] calldata dexes,
        uint256 minProfit
    ) external onlyArbgitrageCore notExcuting {
        //设置当前执行上下文
        currentContext = ExcutionContext({
            initiator: msg.sender,
            tokenIn: tokenIn,
            amountIn: amountIn,
            swapPath: swapPath,
            dexes: dexes,
            minProfit: minProfit,
            isExcuting: true
        });

        //发起闪电贷
        _initFlashLoan(platform, tokenIn, amountIn);

        //清理当前执行上下文
        delete currentContext;
    }
    //内部函数_initFlashLoan，选择闪电贷平台（如AAVE），调用内部函数_initAAVEFlashLoan进行闪电贷
    function _initFlashLoan(
        FlashLoanRouter.LendingPlatForm platform,
        address asset,
        uint256 amount
    ) internal {
        //构造参数
        bytes memory params = abi.encode(
            currentContext.initiator,
            currentContext.tokenIn,
            currentContext.amountIn,
            currentContext.swapPath,
            currentContext.dexes,
            currentContext.minProfit
        );

        //发起闪电贷
        flashLoan.requsetFlashLoan(
            platform,
            address(this),
            asset,
            amount,
            params
        );

        emit FlashLoanRequest(
            currentContext.initiator,
            asset,
            amount,
            platform
        );
    }
    //内部函数_initAAVEFlashLoan,发起AAVE闪电贷
    function _initAAVEFlashLoan(
        address asset,
        uint256 amount,
        bytes memory params
    ) internal {
        //调用Aave闪电贷路由合约
        flashLoan.requsetFlashLoan(
            FlashLoanRouter.LendingPlatForm.Aave_V2,
            address(this),
            asset,
            amount,
            params
        );
    }
    //内部函数_initDYDXFlashLoan,发起DYDX闪电贷(扩展)


}