// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

import "../interfaces/IArbitrage.sol";
import "../interfaces/IArbitrageVault.sol";
import "../interfaces/ISpotArbitrage.sol";
import "../interfaces/IConfigManager.sol";

import "@openzeppelin/contracts-upgradeable/proxy/utils/Initializable.sol";
import "@openzeppelin/contracts-upgradeable/access/OwnableUpgradeable.sol";
import "@openzeppelin/contracts-upgradeable/token/ERC20/ERC20Upgradeable.sol";
import "@openzeppelin/contracts-upgradeable/token/ERC20/utils/SafeERC20Upgradeable.sol";
import "@openzeppelin/contracts-upgradeable/utils/ReentrancyGuardUpgradeable.sol";
import "@openzeppelin/contracts-upgradeable/proxy/utils/UUPSUpgradeable.sol";

contract ArbitrageCore is Initializable, UUPSUpgradeable, OwnableUpgradeable, ReentrancyGuardUpgradeable {
    using SafeERC20Upgradeable for ERC20Upgradeable;

    /**
    *套利调度核心合约
    *调用套利策略执行
    *金库资金套利机器人的验证获利
    *分润：平台收取利润的10%作为服务费，在configManager中进行管理
    */

    

    ISpotArbitrage public spotArbitrage;
    IConfigManager public configManager;    //参数管理   平台收取利润的10%作为服务费等
          
    address public spotArbImp;              // 实现套利(实现IArbitrage.sol)
    address public platFormWallet;
    address public backCaller;              //后端
    address[] public supportAssets;
    bool public paused;

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

    event VaultAdd(address indexed asset, address indexed vault);

    modifier onlybackCaller() {
        require(msg.sender == backCaller || msg.sender == owner(), "Only back or owner can call");
        _;
    }

    modifier whenNotPaused() {
        require(!paused, "Arbitrage Contract is paused");
        _;
    }

    function initialize(
        address _spotArbitrage,
        address _platFormWallet,
        address _configManager,
        address _backCaller
    ) public initializer {
        __Ownable_init(msg.sender);
        __ReentrancyGuard_init();
        __UUPSUpgradeable_init();

        require(_spotArbitrage != address(0), "Invalid spot Arbitrage");
        require(_platFormWallet != address(0), "Invalid platForm Wallet");
        require(_configManager != address(0), "Invalid config Manager");
        require(_backCaller != address(0), "Invalid backend Caller");

        spotArbitrage = ISpotArbitrage(_spotArbitrage);
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

    function setPaused(bool _paused) external onlyOwner {
        paused = _paused;
    }

    function setBackCaller(address _backCaller) external onlyOwner {
        require(_backCaller != address(0), "Invalid backend Caller");
        backCaller = _backCaller;
    }

    function setSpotArbitrage(address _spotArbitrage) external onlyOwner {
        require(_spotArbitrage != address(0), "Invalid spot Arbitrage");
        spotArbitrage = ISpotArbitrage(_spotArbitrage);
    }

    function setConfigManager(address _configManager) external onlyOwner {
        require(_configManager != address(0), "Invalid config Manager");
        configManager = IConfigManager(_configManager);
    }

    function setPlatFormWallet(address _platFormWallet) external onlyOwner {
        require(_platFormWallet != address(0), "Invalid platForm Wallet");
        platFormWallet = _platFormWallet;
    }

    /**
    *入参：
    *原代币(输入)，
    *兑换代币（输出），注意输入的代币要和输出的代币相同，以便比较收益，路径如：ETH-USDT-ETH
    *购买数量（输入数量），
    *交易路径，
    *dex地址：在dex集成路由中
    */

    function executeStrategy(
        IArbitrage.ArbitrageParams calldata params
    ) external nonReentrant whenNotPaused onlybackCaller {
        _executedVaultArbitrage(params);
    }


    function _executedVaultArbitrage(
        IArbitrage.ArbitrageParams calldata params
    ) private {
        address asset = params.asset;
        address tokenOut = params.tokenOut;
        uint256 amountIn = params.amountIn;
        address[] calldata swapPath = params.swapPath;
        address[] calldata dexes = params.dexes;
        uint256 expectProfit = params.expectProfit;
        uint256 minProfit = params.minProfit;
        bool isCex = params.isCex;

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

        //获取vault资金转账
        vault.transferForArbitrage(amountIn);

        //记录套利前余额
        uint256 balanceBefore = ERC20Upgradeable(asset).balanceOf(address(this));
        //授权给套利实现合约
        ERC20Upgradeable(asset).safeTransfer(address(spotArbitrage), amountIn);
        //执行套利策略
        spotArbitrage.executeSwaps(
            asset,
            tokenOut,
            amountIn,
            swapPath,
            dexes,
            expectProfit,
            minProfit,
            isCex
        );
        //记录套利后余额
        uint256 balanceAfter = ERC20Upgradeable(asset).balanceOf(address(this));
        
        //计算利润，分成（注意验证minProfit）
        require(balanceAfter >= balanceBefore, "ArbitrageCore: arbitrage resulted in loss");
        uint256 actProfit = balanceAfter - balanceBefore;
        require(actProfit >= minProfit, "Profit below minimum");

        //计算分润 分成比例由configManager管理
        uint256 platFormFee = actProfit * configManager.profitShareFee() / 10000;
        uint256 netProfitToVault = actProfit - platFormFee;

        //将净利润返回vault
        require(balanceAfter >= amountIn + netProfitToVault, "Insufficient  balanceAfter");
        ERC20Upgradeable(asset).safeTransfer(address(vault), amountIn + netProfitToVault);
        //转账平台服务费  平台收益
        ERC20Upgradeable(asset).safeTransfer(platFormWallet, platFormFee);

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

    function _authorizeUpgrade(address newImplementation) internal view override onlyOwner{
        require(newImplementation != address(0), "New implementation is zero address");
    }
}
