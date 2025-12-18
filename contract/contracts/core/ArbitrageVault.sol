// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

import "../interfaces/IConfigManager.sol";
import "../interfaces/IArbitrageVault.sol";

import "@openzeppelin/contracts/token/ERC20/ERC20.sol";
import "@openzeppelin/contracts/token/ERC20/IERC20.sol";
import "@openzeppelin/contracts/token/ERC20/utils/SafeERC20.sol";
import "@openzeppelin/contracts/utils/ReentrancyGuard.sol";
import "@openzeppelin/contracts/access/Ownable.sol";

/**
 * @title ArbitrageVault
 * @dev 基于ERC4626标准的套利金库
 * 
 * 核心机制：
 * - 用户存入资产（如WETH）→ 获得份额代币（如arbWETH）
 * - 平台使用池中资金进行套利
 * - 套利利润自动反映在份额价值上
 * - 用户赎回时获得本金 + 收益
 */
contract ArbitrageVault is IArbitrageVault, ERC20, ReentrancyGuard, Ownable {
    using SafeERC20 for IERC20;

    // ========== 状态变量 ==========
    IERC20 public immutable asset;           // 底层资产（如WETH）
    address public arbitrageCore;             // 套利调度合约
    
    // 费用设置  改为可插拔，在接口合约中配置
    uint256 public depositFee;
    uint256 public withdrawFee;
    uint256 public performanceFee;
    uint256 public constant DEFAULT_DEPOSITFEE = 0;            // 存款费（basis points）
    uint256 public constant DEFAULT_WITHDRAWFEE = 1;           // 提款费（basis points） 暂定0.01%
    uint256 public constant DEFAULT_PERFORMANCEFEE = 1000;     // 业绩费10%（从利润中收取）
    IConfigManager public configManager;
    
    // 统计数据
    uint256 public totalProfitGenerated;      // 总产生利润
    uint256 public totalFeesCollected;        // 总收取费用
    uint256 public lastArbitrageTime;         // 上次套利时间
    
    // 安全限制
    uint256 public maxTotalAssets;            // 最大总资产
    uint256 public minDepositAmount;          // 最小存款金额
    bool public paused;                       // 暂停状态
    
    //用户地址=>存款记录数组
    mapping(address => IArbitrageVault.DepositRecord[]) public userDeposits;
    //用户存入本金总计
    mapping(address => uint256) public userTotalDeposited;
    //用户提现总计
    mapping(address => uint256) public userTotalWithdraw;

    // ========== 事件 ==========
    
    event Deposit(
        address indexed sender,
        address indexed owner,
        uint256 assets,
        uint256 shares
    );
    
    event Withdraw(
        address indexed sender,
        address indexed receiver,
        address indexed owner,
        uint256 assets,
        uint256 shares
    );
    
    event ArbitrageProfit(
        uint256 profit,
        uint256 fee,
        uint256 timestamp
    );
    
    event ArbitrageLoss(
        uint256 loss,
        uint256 timestamp
    );
    
    // ========== 修饰符 ==========
    
    modifier whenNotPaused() {
        require(!paused, "Vault is paused");
        _;
    }
    
    modifier onlyArbitrageCore() {
        require(msg.sender == arbitrageCore, "Only ArbitrageCore");
        _;
    }
    
    // ========== 构造函数 ==========
    
    /**
     * @param _asset 底层资产地址（如WETH）
     * @param _name 份额代币名称（如"Arbitrage WETH"）
     * @param _symbol 份额代币符号（如"arbWETH"）
     */
    constructor(
        address _asset,
        address _configManager,
        string memory _name,
        string memory _symbol
    ) ERC20(_name, _symbol) Ownable(msg.sender) ReentrancyGuard() {
        require(_asset != address(0), "Invalid asset");
        require(_configManager != address(0), "Invalid config Manager");
        asset = IERC20(_asset);
        maxTotalAssets = type(uint256).max;
        minDepositAmount = 0;
        configManager = IConfigManager(_configManager);
    }
    

    //可插拔关键 支持替换配置
    function setConfigManager(address _newConfigManager) external onlyOwner {
        require(_newConfigManager != address(0), "Invalid new config Manager");
        configManager = IConfigManager(_newConfigManager);
    }


    // ========== ERC4626 核心函数 ==========
    
    /**
     * @dev 返回金库管理的底层资产地址
     */
    function assetAddress() public view override returns (address) {
        return address(asset);
    }
    
    /**
     * @dev 返回金库中的总资产数量
     * 
     * 这是ERC4626的核心函数！
     * 份额价值 = totalAssets() / totalSupply()
     */
    function totalAssets() public view override returns (uint256) {
        return asset.balanceOf(address(this));
    }
    
    /**
     * @dev 将资产数量转换为份额数量
     * 
     * 公式：shares = assets * totalSupply / totalAssets
     * 
     * 示例：
     * - 金库有100 ETH，总份额100
     * - 存入10 ETH
     * - 获得份额 = 10 * 100 / 100 = 10 shares
     */
    function convertToShares(uint256 assets) public view returns (uint256) {
        uint256 supply = totalSupply();
        if (supply == 0) {
            return assets; // 1:1 初始比例
        }
        return (assets * supply) / totalAssets();
    }
    
    /**
     * @dev 将份额数量转换为资产数量
     * 
     * 公式：assets = shares * totalAssets / totalSupply
     * 
     * 示例：
     * - 金库有110 ETH，总份额100（套利赚了10 ETH）
     * - 持有10 shares
     * - 可赎回 = 10 * 110 / 100 = 11 ETH
     */
    function convertToAssets(uint256 shares) public view returns (uint256) {
        uint256 supply = totalSupply();
        if (supply == 0) {
            return shares;
        }
        return (shares * totalAssets()) / supply;
    }
    
    /**
     * @dev 预览存款将获得的份额
     */
    function previewDeposit(uint256 assets) public view returns (uint256) {
        uint256 dePositFee = configManager.getDepositFee(address(this));
        uint256 fee = (assets * dePositFee) / 10000;
        return convertToShares(assets - fee);
    }
    
    /**
     * @dev 预览赎回份额将获得的资产
     */
    function previewRedeem(uint256 shares) public view returns (uint256) {
        uint256 assets = convertToAssets(shares);
        uint256 withDrawFee = configManager.getWithDrawFee(address(this));
        uint256 fee = (assets * withDrawFee) / 10000;
        return assets - fee;
    }
    
    /**
     * @dev 存款：用户存入资产，获得份额
     * 
     * @param assets 存入的资产数量
     * @param receiver 份额接收者
     * @return shares 获得的份额数量
     */
    function deposit(
        uint256 assets,
        address receiver
    ) public nonReentrant whenNotPaused returns (uint256 shares) {
        require(assets > 0, "Cannot deposit 0");
        require(assets >= minDepositAmount, "Below minimum deposit");
        require(
            totalAssets() + assets <= maxTotalAssets,
            "Exceeds max total assets"
        );
        
        // 计算份额（扣除存款费后）
        uint256 dePositFee = configManager.getDepositFee(address(this));
        uint256 fee = (assets * dePositFee) / 10000;
        uint256 assetsAfterFee = assets - fee;
        shares = convertToShares(assetsAfterFee);
        
        //记录用户存金
        userDeposits[receiver].push(IArbitrageVault.DepositRecord({
            amount: assets,
            shares: shares,
            sharesPrice: sharePrice(),
            timestamp: block.timestamp
        }));

        userTotalDeposited[receiver] += assets;
        
        require(shares > 0, "Zero shares");
        
        // 转入资产
        asset.safeTransferFrom(msg.sender, address(this), assets);

        
        // 铸造份额代币给接收者
        _mint(receiver, shares);
        
        // 记录费用
        if (fee > 0) {
            totalFeesCollected += fee;
        }
        
        emit Deposit(msg.sender, receiver, assets, shares);
        
        return shares;
    }
    
    /**
     * @dev 赎回：用户销毁份额，取回资产
     * 
     * @param shares 要赎回的份额数量
     * @param receiver 资产接收者
     * @param owner 份额所有者
     * @return assets 获得的资产数量
     */
    function redeem(
        uint256 shares,
        address receiver,
        address owner
    ) public nonReentrant returns (uint256 assets) {
        require(shares > 0, "Cannot redeem 0");
        require(balanceOf(owner) >= shares, "Insufficient shares");
        
        // 如果调用者不是owner，检查授权
        if (msg.sender != owner) {
            uint256 allowed = allowance(owner, msg.sender);
            require(allowed >= shares, "Insufficient allowance");
            _approve(owner, msg.sender, allowed - shares);
        }
        
        // 计算可获得的资产
        assets = convertToAssets(shares);
        
        // 扣除提款费
        uint256 withDrawFee = configManager.getWithDrawFee(address(this));
        uint256 fee = (assets * withDrawFee) / 10000;
        uint256 assetsAfterFee = assets - fee;
        
        require(assetsAfterFee > 0, "Zero assets");
        require(
            asset.balanceOf(address(this)) >= assetsAfterFee,
            "Insufficient vault balance"
        );
        
        //记录用户提现
        userTotalWithdraw[owner] += assetsAfterFee;

        // 销毁份额代币
        _burn(owner, shares);
        
        // 转出资产
        asset.safeTransfer(receiver, assetsAfterFee);
        
        // 记录费用
        if (fee > 0) {
            totalFeesCollected += fee;
        }
        
        emit Withdraw(msg.sender, receiver, owner, assetsAfterFee, shares);
        
        return assetsAfterFee;
    }
    
    /**
     * @dev 便捷函数：按资产数量提款
     */
    function withdraw(
        uint256 assets,
        address receiver,
        address owner
    ) public returns (uint256 shares) {
        shares = convertToShares(assets);
        redeem(shares, receiver, owner);
        return shares;
    }
    
    // ========== 套利相关函数 ==========
    
    /**
     * @dev 获取可用于套利的资金
     * 
     * 注意：可以设置保留比例，不把全部资金用于套利
     */
    function getAvailableForArbitrage() public view override returns (uint256) {
        uint256 total = totalAssets();
        // 保留10%作为流动性，90%可用于套利
        return (total * 9000) / 10000;
    }
    
    /**
     * @dev 为套利授权资金（由ArbitrageCore调用）
     * 
     * @param amount 授权金额
     */
    function approveForArbitrage(uint256 amount) external onlyArbitrageCore override {
        require(amount <= getAvailableForArbitrage(), "Exceeds available");
        asset.approve(arbitrageCore, amount);
    }

    /**
     * @dev 为套利转账资金（由ArbitrageCore调用）
     * 
     * @param amount 转账金额
     */
    function transferForArbitrage(uint256 amount) external onlyArbitrageCore override {
        require(amount <= getAvailableForArbitrage(), "Exceeds available");
        asset.safeTransfer(arbitrageCore, amount);
    }
    
    /**
     * @dev 记录套利利润（由ArbitrageCore调用）
     * 
     * 利润自动反映在totalAssets()中
     * 份额持有者自动享受增值
     * 
     * @param profit 本次套利利润
     */
    function recordProfit(uint256 profit) external onlyArbitrageCore override {
        // 收取业绩费
        uint256 perFormanceFee = configManager.getPlatFormFee(address(this));
        uint256 fee = (profit * perFormanceFee) / 10000;
        
        totalProfitGenerated += profit;
        totalFeesCollected += fee;
        lastArbitrageTime = block.timestamp;
        
        // 将费用转给平台
        if (fee > 0) {
            asset.safeTransfer(owner(), fee);
        }
        
        emit ArbitrageProfit(profit, fee, block.timestamp);
    }
    
    /**
     * @dev 记录套利亏损（理论上不应该发生，有minProfit保护）
     */
    function recordLoss(uint256 loss) external onlyArbitrageCore override {
        emit ArbitrageLoss(loss, block.timestamp);
    }
    
    // ========== 查询函数 ==========

     /**
     * @dev 1211新增：获取用户本金
     */
    function getUserBalance(address user) public view returns (uint256) {
        uint256 totalDeposited = userTotalDeposited[user];
        uint256 totalWithdrawn = userTotalWithdraw[user];   

        //防止溢出
        if (totalWithdrawn >= totalDeposited) {
            return 0;
        }

        return totalDeposited - totalWithdrawn;
    }

    function getMyBalance() public view override returns (uint256) {
        return getUserBalance(msg.sender);
    }
    
    /**
     * @dev 获取用户的份额余额
     */
    function sharesOf(address user) public view returns (uint256) {
        return balanceOf(user);
    }
    
    /**
     * @dev 获取用户份额对应的资产价值
     */
    function assetsOf(address user) public view returns (uint256) {
        return convertToAssets(balanceOf(user));
    }
    
    /**
     * @dev 获取当前份额价格（每份额值多少资产）
     */
    function sharePrice() public view returns (uint256) {
        uint256 supply = totalSupply();
        if (supply == 0) {
            return 1e18; // 初始价格1:1
        }
        return (totalAssets() * 1e18) / supply;
    }
    
    /**
     * @dev 获取金库统计信息
     */
    function getVaultStats() public view returns (
        uint256 _totalAssets,
        uint256 _totalSupply,
        uint256 _sharePrice,
        uint256 _totalProfit,
        uint256 _totalFees,
        uint256 _lastArbitrageTime
    ) {
        return (
            totalAssets(),
            totalSupply(),
            sharePrice(),
            totalProfitGenerated,
            totalFeesCollected,
            lastArbitrageTime
        );
    }
    
    // ========== 管理函数 ==========
    
    function setArbitrageCore(address _arbitrageCore) external onlyOwner {
        require(_arbitrageCore != address(0), "Invalid address");
        arbitrageCore = _arbitrageCore;
    }
    
    function setFees(
        uint256 _depositFee,
        uint256 _withdrawFee,
        uint256 _performanceFee
    ) external onlyOwner {
        require(_depositFee <= 500, "Deposit fee too high");      // 最高5%
        require(_withdrawFee <= 500, "Withdraw fee too high");    // 最高5%
        require(_performanceFee <= 5000, "Performance fee too high"); // 最高50%
        
        depositFee = _depositFee;
        withdrawFee = _withdrawFee;
        performanceFee = _performanceFee;
    }
    
    function setLimits(
        uint256 _maxTotalAssets,
        uint256 _minDepositAmount
    ) external onlyOwner {
        maxTotalAssets = _maxTotalAssets;
        minDepositAmount = _minDepositAmount;
    }
    
    function setPaused(bool _paused) external onlyOwner {
        paused = _paused;
    }
    
    /**
     * @dev 紧急提取（仅owner，仅限紧急情况）
     */
    function emergencyWithdraw(address token) external onlyOwner {
        if (token == address(asset)) {
            // 只能提取超出用户份额的部分
            revert("Cannot withdraw user assets");
        }
        
        uint256 balance = IERC20(token).balanceOf(address(this));
        IERC20(token).safeTransfer(owner(), balance);
    }
}