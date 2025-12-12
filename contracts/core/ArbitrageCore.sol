// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

import "../interfaces/IArbitrage.sol";
import "../interfaces/IArbitrageVault.sol";
import "../interfaces/ISpotArbitrage.sol";
import "../interfaces/IFlashLoan.sol";
import "../interfaces/IConfigManager.sol";

import "@openzeppelin/contracts-upgradeable/token/ERC20/ERC20Upgradeable.sol";
import "@openzeppelin/contracts-upgradeable/utils/ReentrancyGuardUpgradeable.sol";
import "@openzeppelin/contracts-upgradeable/proxy/utils/UUPSUpgradeable.sol";
import "@openzeppelin/contracts-upgradeable/access/OwnableUpgradeable.sol";


contract ArbitrageCore is UUPSUpgradeable, OwnableUpgradeable, ReentrancyGuardUpgradeable {

    /**
    *套利调度核心合约
    *调用套利策略执行
    *验证获利
    *分润：暂定平台收取10%（即应配置100），此值放置在configManager中进行管理，以便管理员随时调整
    */

    

    ISpotArbitrage public spotArbitrage;
    IFlashLoan public flashLoanArbitrage;
    IConfigManager public configManager;    //参数管理   平台收取利润的10%作为服务费等
         
    address public spotArbImp;              // 实现套利(实现IArbitrage.sol)
    address public platFormWallet;
    address public backCaller;              //后端
    address[] public supportAssets;


    //金库地址
    mapping(address => IArbitrageVault) public vaults;

    //事件：金库资产套利
    event VaultArbitrageExecuted(
        address indexed vault,
        address indexed asset,
        uint256 amountIn,
        uint256 profit,
        uint256 platFormFee,//平台服务费
        uint256 netProfitToVault,//转入金库的净收益（刨除平台服务费）
        uint256 timestamp
    );

    //事件：闪电贷套利
    event FlashLoanArbitrageExecuted(
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

    constructor () {
        _disableInitializers();
    }

    function initialize(
        address _spotArbitrage,
        address _flashLoanArbitrage,
        address _platFormWallet,
        address _configManager,
        address _backCaller
    ) public initializer {
        __Ownable_init(msg.sender);
        __ReentrancyGuard_init();
        __UUPSUpgradeable_init();

        require(_spotArbitrage != address(0), "Invalid spot Arbitrage");
        require(_flashLoanArbitrage != address(0), "Invalid flashLoan Arbitrage");
        require(_platFormWallet != address(0), "Invalid platForm Wallet");
        require(_configManager != address(0), "Invalid config Manager");
        require(_backCaller != address(0), "Invalid backend Caller");

        spotArbitrage = ISpotArbitrage(_spotArbitrage);
        flashLoanArbitrage = IFlashLoan(_flashLoanArbitrage);
        platFormWallet = _platFormWallet; //利润转账地址(项目方钱包地址)
        configManager = IConfigManager(_configManager);
        backCaller = _backCaller;


    }

    //金库函数：添加金库
    function addVault(
        address asset,
        address vault
    ) external onlyOwner {
        require(asset != address(0), "Invalid asset");
        require(vault != address(0), "Invalid vault");
        require(address(vaults[asset]) == address(0), "vault already exists" );

        vaults[asset] = IArbitrageVault(vault);
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
        IArbitrageVault vault = vaults[asset];
        require(address(vault) != address(0), "vault not exists");

        return (
            address(vault),
            vault.totalAssets(),
            vault.getAvailableForArbitrage()
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
    //枚举套利方案：自有资产（金库），闪电贷
    enum StrategyTypes {
        OWN_FUNDS, 
        FLASH_LOAN
    }
    

    function executeStrategy(
        StrategyTypes strategyTypes,
        IArbitrage.ArbitrageParams calldata params
    ) external onlybackCaller nonReentrant {
        if (strategyTypes == StrategyTypes.OWN_FUNDS ) {
            _executedVaultArbitrage(params);
        } else if (strategyTypes == StrategyTypes.FLASH_LOAN) {
            _executedFlashLoanArbitrage(params);
        }
    }


    function _executedVaultArbitrage(
        IArbitrage.ArbitrageParams calldata params
    ) private {
        (
            address asset,
            uint256 amountIn,
            address[] memory swapPath,
            address[] memory dexes,
            uint256 minProfit
        ) = abi.decode(params,(address, uint256, address[], address[], uint256));

        //参数验证
        require(amountIn > 0, "amountIn > 0");
        require(swapPath.length >= 3, "swapPath need 3 at least");
        require(dexes.length == swapPath.length - 1, "dexes = swapPath -1");
        require(swapPath[0] == asset, "swapPath[0] is tokenIn");
        require(asset == swapPath[swapPath.length - 1], "tokenIn = tokenOut");

        //获取vault,查询可用资金
        IArbitrageVault vault = vaults[asset];
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
        uint256 amountOut = spotArbitrage.executeSwaps(
            asset,
            swapPath[swapPath.length - 1],
            amountIn,
            swapPath,
            dexes
        );
        //记录套路后余额
        uint256 balanceAfter = IERC20(asset).balanceOf(address(this));
        
        //计算利润，分成（注意验证minProfit）
        uint256 actProfit = balanceAfter - balanceBefore;
        require(actProfit > minProfit, "Profit below minimum");

        uint256 seviceFee = configManager.profitShareFee();//服务费取值自参数管理合约的配置
        uint256 platFormFee = actProfit * seviceFee / 10000;
        uint256 netProfitToVault = actProfit - platFormFee;

        //将净利润返回vault
        require(balanceAfter >= amountIn + netProfitToVault, "Insufficient  balanceAfter");
        IERC20(asset).transfer(address(vault), amountIn + netProfitToVault);
        IERC20(asset).transfer(platFormWallet, platFormFee);

        //通知金库记录盈利
        vault.recordProfit(netProfitToVault);

        //事件触发VaultArbitrageExecuted
        emit VaultArbitrageExecuted(
            address(vault),
            asset,
            amountIn,
            actProfit,
            platFormFee,
            netProfitToVault,
            block.timestamp
        );
    }

    //闪电贷套利
    function _executedFlashLoanArbitrage() private {
        revert("Flash Loan not implemented yet");
        //验证

        //调用闪电贷套利执行

        //事件触发FlashLoanArbitrageExecuted
    }

    function _authorizeUpgrade(address newImplementation) internal override onlyOwner{
        require(newImplementation != address(0), "New implementation is zero address");
    }

}