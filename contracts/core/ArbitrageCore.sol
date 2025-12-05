// SPDX-License-Identifier: MIT
pragam solidity ^0.8.20;

import "./interface/IArbitrage.sol";
import "./interface/IArbitrageVault.sol";
import "./interface/ISpotArbitrage.sol";
import "./interface/IFlashLoan.sol";

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

    ISpotArbitrage public spotArbitrage;
    IFlashLoan public flashLoanArbitrage;
    address public platForm;//利润转账地址(项目方钱包地址)
    address public spotArbImp;// 实现套利(实现IArbitrage.sol)
    address public backCaller;//后端

    uint256 public constant PROFIT_SHARE_FEE = 1000;//平台收取利润的10%作为服务费



    //金库地址
    mapping(address => IArbitrageVault) public vaults;
    address[] public supportAssets;

    //事件：金库资产套利
    event VaultArbitrageExcuted(
        address indexed vault,
        address indexed asset,
        uint256 amountIn,
        uint256 profit,
        uint256 timestamp
    );

    //事件：闪电贷套利
    event FlashLoanArgitrageExcuted(
        address indexed initiator,
        address indexed asset,
        address tokenIn,
        uint256 amountIn,
        uint256 profit,
        uint256 timestamp
    );

    event VaultAdd(address indexed asset, address indexed vault);

    modifier onlybackCaller() {
        require(msg.sender == backCaller || msg.sender == owner(), "Only back or owner can call");
        _;
    }

    constructor(address _spotArbitrage, address _flashLoanArbitrage){
        require(_spotArbitrage != address(0), "Invalid spot arbitrage");
        spotArbitrage = ISpotArbitrage(_spotArbitrage)
        require(_flashLoanArbitrage != address(0), "Invalid flashLoan arbitrage");
        flashLoanArbitrage = IFlashLoan(_flashLoanArbitrage);

        backCaller = msg.sender;

    }

    //金库函数：添加金库
    function addVault(
        address asset,
        address vault
    ) external onlyOwner {
        require(asset != address(0), "Invalid asset");
        require(vault != address(0), "Invalid vault")
        require(address(vault[asset]) == address(0), "vault already exists" )

        vault[asset] = IArbitrageVault(vault);
        supportAssets.push(asset);

        emit VaultAdd(asset, vault);
    }

    //金库函数：获取金库信息
    function getVaultInfo(
        address asset
    )external view returns (
        address vaultAddress,
        uint256 totalAssets,
        uint256 availableAssets
    ) {
        IArbitrageVault vault = vault[asset];
        require(address(vault) != address(0), "vault not exists");

        return (
            address(vault),
            vault.totalAssets,
            vault.getAvailableForArbitrage
        );
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
    *自有资金套利&闪电贷套利，两种套利模式对象不同，前者取自金库，可多人打包进行，后者针对用户，闪电贷不可跨用户
    *
    */
    function excutedSpotArbitrage(
        ArbitrageParams calldata params
    ) external noReentrant returns(uint256 profit){
        require(msg.sender == platForm || msg.sender == owner(), "No right ");
        require(params.amountIn > 0, "amountIn must > 0");
        require(params.swapPath.length >= 2, "swapPath Invalid");
        require(params.swapPath[0] == tokenIn, "tokenIn not match");
        //
        require(swapPath[swapPath.length -1] == tokenIn, "tokenOut not match");//in和out应该相同
        require(params.dexes.length == swapPath.length -1, "Dex count not match ");
        
        //授权
        IERC20(params.tokenIn).transferFrom(msg.sender, address(this), params.amountIn);
        //授权给套利策略合约
        IERC20(params.tokenIn).approve(spotArbImp, params.amountIn);
        //套利前余额
        uint256 balanceBefore = IERC20(tokenOut).balanceOf(address(this));
        
        //调度套利
        uint256 amountOut = IArbitrage(spotArbImp).excuteArbitrage(
            params.tokenIn,
            params.tokenOut,
            params.amountIn,
            params.swapPath,
            params.dexes
        );
        //套利后余额
        uint256 balanceAfter = IERC20(tokenOut).balanceOf(address(this));
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


    function excutedVaultArbitrage(
        address asset,
        uint256 amountIn,
        address[] swapPath,
        address[] dexes,
        uint256 minProfit
    ) external noReentrant, onlybackCaller returns (uint256 profit) {
        //参数验证
        require(amountIn > 0, "amountIn > 0");
        require(swapPath.length >= 3, "swapPath need 3 at least");
        require(dexes.length == swapPath.length - 1, "dexes = swapPath -1");
        require(swapPath[0] == asset, "swapPath[0] is tokenIn");
        require(asset == swapPath[swapPath.length - 1], "tokenIn = tokenOut");

        //获取vault,查询可用资金
        IArbitrageVault vault = vault[asset];
        require(address(vault) != address(0), "vault not exists");

        uint256 availableVault = vault.getAvailableForArbitrage();
        require(amountIn <= availableVault, "amountIn too much");

        //获取vault资金授权
        vault.approveForArbitrage(amountIn);

        //资金转入本合约
        IERC20(asset).transferFrom(address(vault), address(this), amountIn);

        //记录套利前余额
        uint256 balanceBefore = IERC20(asset).balanceOf(address(this));
        //授权给套利实现合约
        IERC20(asset).approve(address(spotArbitrage), amountIn);
        //执行套利策略
        uint256 amountOut = spotArbitrage.excuteSwaps(
            asset,
            swapPath[swapPath.length - 1],
            amountIn,
            swapPath,
            dexes
        )
        //记录套路后余额
        uint256 balanceAfter = IERC20(asset).balanceOf(address(this));
        
        //计算利润，分成（注意验证minProfit）
        uint256 actProfit = balanceAfter - balanceBefore;
        require(actProfit > minProfit, "Profit below minimum");

        //将资金返回vault
        IERC20(asset).transfer(address(vault), actProfit + amountIn);

        //通知金库记录盈利
        vault.recordProfit(actProfit);

        //事件触发VaultArbitrageExcuted
        emit VaultArbitrageExcuted(
            address(vault),
            asset,
            amountIn,
            actProfit,
            block.timestamp
        );

        return actProfit;
    }

    //闪电贷套利
    function excutedFlashLoanArbitrage() external noReentrant onlybackCaller returns (uint256 profit){
        //验证

        //调用闪电贷套利执行

        //事件触发FlashLoanArgitrageExcuted
    }
}