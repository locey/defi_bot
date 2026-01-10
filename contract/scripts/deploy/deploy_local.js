// scripts/deploy/deploy_local.js
const hre = require("hardhat");
const fs = require('fs');

async function main() {
  const [deployer] = await hre.ethers.getSigners();
  console.log("Deploying contracts with the account:", deployer.address);
  console.log("Account balance:", hre.ethers.formatEther(await hre.ethers.provider.getBalance(deployer.address)), "ETH");

  // ----------------------------
  // 0. 配置常量（本地测试使用 Mock 代币）
  // ----------------------------
  // 部署 Mock USDC 和 WETH
  const MockERC20 = await hre.ethers.getContractFactory("MockERC20");
  const mockUSDC = await MockERC20.deploy("USD Coin", "USDC", 6);
  await mockUSDC.waitForDeployment();
  const mockWETH = await MockERC20.deploy("Wrapped ETH", "WETH", 18);
  await mockWETH.waitForDeployment();

  const UNDERLYING_ASSET = mockUSDC.target; // 本地测试使用 Mock USDC
  const WETH_ADDRESS = mockWETH.target;    // 本地测试使用 Mock WETH
  const PLATFORM_WALLET = deployer.address;
  const BACK_CALLER = deployer.address;

  // ----------------------------
  // 1. 部署 ArbitrageVault (普通合约)
  // ----------------------------
  const ArbitrageVault = await hre.ethers.getContractFactory("ArbitrageVault");
  const arbitrageVault = await ArbitrageVault.deploy(
    UNDERLYING_ASSET,
    PLATFORM_WALLET, // 临时用 deployer 地址，后续会更新为 ConfigManager
    "Arbitrage Vault",
    "ARBV"
  );
  await arbitrageVault.waitForDeployment();
  console.log("✅ ArbitrageVault deployed to:", arbitrageVault.target);

  // ----------------------------
  // 2. 部署 ConfigManager (UUPS 可升级合约)
  // ----------------------------
  const ConfigManage = await hre.ethers.getContractFactory("ConfigManage");
  const configManage = await hre.upgrades.deployProxy(ConfigManage, [
    arbitrageVault.target,
    deployer.address, // 本地测试用 deployer 作为占位
    deployer.address, // 同上
    deployer.address, // 同上
    UNDERLYING_ASSET
  ], {
    initializer: "initialize",
    kind: "uups"
  });
  await configManage.waitForDeployment();
  console.log("✅ ConfigManager (UUPS) deployed to:", configManage.target);

  // 更新 ArbitrageVault 中的 configManager 地址
  await arbitrageVault.setConfigManager(configManage.target);
  console.log("✅ ArbitrageVault configManager set to:", configManage.target);

  // ----------------------------
  // 3. 部署 DoubleRouterIntegration (普通合约)
  // ----------------------------
  const DoubleRouterIntegration = await hre.ethers.getContractFactory("DoubleRouterIntegration");
  const doubleRouterIntegration = await DoubleRouterIntegration.deploy(configManage.target);
  await doubleRouterIntegration.waitForDeployment();
  console.log("✅ DoubleRouterIntegration deployed to:", doubleRouterIntegration.target);

  // ----------------------------
  // 4. 部署 UniswapV2Integration (普通合约)
  // ----------------------------
  const UniswapV2Integration = await hre.ethers.getContractFactory("UniswapV2Integration");
  const uniswapV2Integration = await UniswapV2Integration.deploy(configManage.target);
  await uniswapV2Integration.waitForDeployment();
  console.log("✅ UniswapV2Integration deployed to:", uniswapV2Integration.target);

  // ----------------------------
  // 5. 部署 FlashLoanRouter (普通合约)
  // ----------------------------
  const FlashLoanRouter = await hre.ethers.getContractFactory("FlashLoanRouter");
  const flashLoanRouter = await FlashLoanRouter.deploy(configManage.target);
  await flashLoanRouter.waitForDeployment();
  console.log("✅ FlashLoanRouter deployed to:", flashLoanRouter.target);

  // ----------------------------
  // 6. 部署 SpotArbitrage (普通合约)
  // ----------------------------
  const SpotArbitrage = await hre.ethers.getContractFactory("SpotArbitrage");
  const spotArbitrage = await SpotArbitrage.deploy(
    doubleRouterIntegration.target,
    uniswapV2Integration.target,
    hre.ethers.ZeroAddress // 临时传入零地址，后续通过 setter 设置
  );
  await spotArbitrage.waitForDeployment();
  console.log("✅ SpotArbitrage deployed to:", spotArbitrage.target);

  // ----------------------------
  // 7. 部署 ArbitrageCore (UUPS 可升级合约)
  // ----------------------------
  const ArbitrageCore = await hre.ethers.getContractFactory("ArbitrageCore");
  const arbitrageCore = await hre.upgrades.deployProxy(ArbitrageCore, [
    spotArbitrage.target,       // _spotArbitrage
    flashLoanRouter.target,     // _flashLoanArbitrage
    PLATFORM_WALLET,            // _platFormWallet
    configManage.target,        // _configManager
    BACK_CALLER                 // _backCaller
  ], {
    initializer: "initialize",
    kind: "uups"
  });
  await arbitrageCore.waitForDeployment();
  console.log("✅ ArbitrageCore (UUPS) deployed to:", arbitrageCore.target);

  // ----------------------------
  // 8. 解决循环依赖：设置 SpotArbitrage 的 ArbitrageCore 地址
  // ----------------------------
  await spotArbitrage.setArbitrageCore(arbitrageCore.target);
  console.log("✅ SpotArbitrage ArbitrageCore set to:", arbitrageCore.target);

  // ----------------------------
  // 9. 其他关联配置
  // ----------------------------
  // 9.1 添加 Vault 到 ArbitrageCore
  await arbitrageCore.addVault(
    mockUSDC.target,
    arbitrageVault.target
  );
  console.log("✅ ArbitrageCore vault added mockUSDC .");

  // 9.2 设置 ArbitrageCore 中的 backCaller
  await arbitrageCore.setBackCaller(BACK_CALLER);
  console.log("✅ ArbitrageCore backCaller set to:", BACK_CALLER);

  // ----------------------------
  // 10. 部署 MockRouter (用于本地测试，可控套利)
  // ----------------------------
  const MockRouter = await hre.ethers.getContractFactory("MockRouter");
  const mockRouter1 = await MockRouter.deploy();
  await mockRouter1.waitForDeployment();
  const mockRouter2 = await MockRouter.deploy();
  await mockRouter2.waitForDeployment();
  console.log("✅ MockRouter1 deployed to:", mockRouter1.target);
  console.log("✅ MockRouter2 deployed to:", mockRouter2.target);

  // 将 MockRouter 注册到 DoubleRouterIntegration
  await doubleRouterIntegration.setRouters([mockRouter1.target, mockRouter2.target]);
  console.log("✅ DoubleRouterIntegration routers set.");

  // ----------------------------
  // 11. 给测试账户铸造测试代币
  // ----------------------------
  // 给 deployer 铸造 10000 USDC
  await mockUSDC.mint(deployer.address, hre.ethers.parseUnits("10000", 6));
  console.log("✅ 铸造 10000 USDC 给测试账户");

  // ----------------------------
  // 12. 保存部署信息
  // ----------------------------
  const deployments = {
    network: hre.network.name,
    arbitrageVault: arbitrageVault.target,
    configManage: configManage.target,
    doubleRouterIntegration: doubleRouterIntegration.target,
    uniswapV2Integration: uniswapV2Integration.target,
    spotArbitrage: spotArbitrage.target,
    flashLoanRouter: flashLoanRouter.target,
    arbitrageCore: arbitrageCore.target,
    mockRouter1: mockRouter1.target,
    mockRouter2: mockRouter2.target,
    mockUSDC: mockUSDC.target,
    mockWETH: mockWETH.target
  };

  fs.mkdirSync('./deployments', { recursive: true });
  fs.writeFileSync(`./deployments/localhost.json`, JSON.stringify(deployments, null, 2));
  console.log(`📝 Deployment info saved to ./deployments/localhost.json`);

  console.log("\n🎉 ALL CONTRACTS DEPLOYED SUCCESSFULLY ON LOCALHOST!");
}

main()
  .then(() => process.exit(0))
  .catch((error) => {
    console.error(error);
    process.exit(1);
  });