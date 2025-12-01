// SPDX-License-Identifier: MIT
pragam solidity ^0.8.20;

import "./interface/IArbitrage.sol";
import "@openzeppelin/contracts/access/Ownable.sol";
import "@openzeppelin/contracts/token/ERC20/IERC20.sol";
import "@openzeppelin/contracts/security/ReentrancyGuard.sol";

contract ArbitrageCore is Ownable, ReentrancyGuard {
    /**
    *套利调度核心合约
    *调用套利策略执行
    *验证获利
    *分润：暂定平台收取100（即10%），后续应将此值放置在router中进行管理，以便管理员随时调整
    */

    address public platForm;//利润转账地址(项目方钱包地址)
    address public spotArbImp;// 实现套利(实现IArbitrage.sol)

    uint256 public constant PROFIT_SHARE_FEE = 100;//平台收取利润的10%作为服务费

    event ArbitrageExcuted(
        address indexed user,
        address tokenIn,
        address tokenOut,
        uint256 amountIn,
        uint256 profit
    );

    constructor(address _platForm, address _spotArbImp){
        require(_platForm != address(0), "Invalid platForm");
        require(_spotArbImp != address(0), "Invalid spotArbImp");
        platForm = _platForm;
        spotArbImp = _spotArbImp;

    }

    function setplatForm(address _platForm) external onlyOwner{
        require(_platForm != address(0), "Invalid platForm");
        platForm = _platForm;
    }

    function setSpotArbImp(address _spotArbImp) external onlyOwner {
        require(_spotArbImp != address(0), "Invalid spotArbImp");
        spotArbImp = _spotArbImp;
    }

    //核心函数：实现套利的调用，并对盈利&分润计算
    /**
    *入参：
    *原代币(输入)，
    *兑换代币（输出），注意输入的代币要和输出的代币相同，以便比较收益，路径如：ETH-USDT-ETH
    *购买数量（输入数量），
    *交易路径(USDC,WETH,DAI,USDC三角套利后续扩展，当前先完成单点套利：ETH-USDT-ETH)，
    *dex地址：在dex集成路由中
    *
    */
    function excutedSpotArbitrage(
        ArbitrageParams calldata params
    ) external noReentrant returns(uint256 profit){
        require(msg.sender == platForm || msg.sender == owner(), "No right ");
        require(params.amountIn > 0, "amountIn must > 0");
        require(params.swapPath.length >= 2, "swapPath Invaild");
        require(params.swapPath[0] == tokenIn, "tokenIn not match");
        //
        require(swapPath[swapPath.length -1] == tokenIn, "tokenIn not match");
        require(params.dexes.length == swapPath.length -1, "Dex count not match ");
        
        //授权
        IERC20(params.tokenIn).transferFrom(msg.sender, address(this), params.amountIn);
        //授权给套利策略合约
        IERC20(params.tokenIn).approve(spotArbImp, params.amountIn);
        //套利前余额
        uint256 balanceBefore = IERC20(tokenOut).balanceof(address(this));
        
        //调度套利
        uint256 amountOut = IArbitrage(spotArbImp).excuteArbitrage(
            params.tokenIn,
            params.tokenOut,
            params.amountIn,
            params.swapPath,
            params.dexes
        );
        //套利后余额
        uint256 balanceAfter = IERC20(tokenOut).balanceof(address(this));
        //利润计算
        require(balanceAfter - balanceBefore >= 0, "no profit")
        profit = balanceAfter - balanceBefore;

        uint256 platFormShare = profit * PROFIT_SHARE_FEE / 10000;//平台分成
        uint256 customerShare = profit - platFormShare;//用户获利

        require(IERC20(params.tokenOut).transfer(platForm, platFormShare),"PlatForm transfer failed");
        require(IERC20(params.tokenOut).transfer(msg.sender, customerShare),"customer transfer failed");

        emit ArbitrageExcuted(msg.sender, params.tokenIn, params.tokenOut, params.amountIn, profit);

        return profit;
    }


}