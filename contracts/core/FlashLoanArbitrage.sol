// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

import "./interfaces/IFlashLoan.sol";
import "./router/FlashLoanRouter.sol";
import "@openzeppelin/contracts/token/ERC20/IERC20.sol";
import "@openzeppelin/contracts/access/Ownable.sol";
import "@openzeppelin/contracts/security/ReentrancyGuard.sol";

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
    ISpotArbitrage public immutable SpotArbitrage;  //套利实现合约接口
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
    constructor {}

    //核心函数：实施闪电贷，并调用内部函数_initFlashLoan发起闪电贷

    //内部函数_initFlashLoan，选择闪电贷平台（如AAVE），调用内部函数_initAAVEFlashLoan进行闪电贷

    //内部函数_initAAVEFlashLoan,发起AAVE闪电贷

    //内部函数_initDYDXFlashLoan,发起DYDX闪电贷(扩展)


}