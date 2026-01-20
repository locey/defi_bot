// scripts/deploy/deploy_all.js
const hre = require("hardhat");
const fs = require('fs');

// 确保 upgrades 可用
// 必须在 hardhat.config.js 中配置: require("@openzeppelin/hardhat-upgrades");

async function main() {
  const [deployer] = await hre.ethers.getSigners();
  console.log("Deploying contracts with the account:", deployer.address);
  console.log("Account balance:", hre.ethers.formatEther(await hre.ethers.provider.getBalance(deployer.address)), "ETH"); // v6 语法：formatEther 替代 utils.formatEther

  // ----------------------------
  // 0. 配置常量（替换为 Sepolia 测试网真实地址！）
  // ----------------------------
  const UNDERLYING_ASSET = "0x57Ab1ec28D129707052df4dF418D58a2D46d5f51"; // Sepolia USDC 官方地址
  const UNISWAP_V2_ROUTER = "0xC532a74256D3Db42D0Bf7a0400fEFDbad7694008"; // Sepolia Uniswap V2 Router
  const UNISWAP_V3_ROUTER = "0xE592427A0AEce92De3Edee1F18E0157C05861564"; // Sepolia Uniswap V3 Router
  const SUSHISWAP_ROUTER = "0x1b02da8cb0d097eb8d57a175b88c7d8b47997506"; // Sepolia SushiSwap Router
  const PLATFORM_WALLET = deployer.address; // 平台钱包地址
  const BACK_CALLER = deployer.address; // 后端调用者地址

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
  await arbitrageVault.waitForDeployment(); // v6 等待部署完成（替代 v5 的 deployed()）
  console.log("✅ ArbitrageVault deployed to:", arbitrageVault.target); // v6 用 target 替代 address

  // ----------------------------
  // 2. 部署 ConfigManage (UUPS 可升级合约)
  // ----------------------------
  const ConfigManage = await hre.ethers.getContractFactory("ConfigManage");
  const configManage = await hre.upgrades.deployProxy(ConfigManage, [
    arbitrageVault.target,
    UNISWAP_V2_ROUTER,
    UNISWAP_V3_ROUTER,
    SUSHISWAP_ROUTER,
    UNDERLYING_ASSET
  ], {
    initializer: "initialize",
    kind: "uups"
  });
  await configManage.waitForDeployment(); // 可升级合约也用 waitForDeployment
  console.log("✅ ConfigManager (UUPS) deployed to:", configManage.target);

  // 更新 ArbitrageVault 中的 configManager 地址
  await arbitrageVault.setConfigManager(configManage.target);
  console.log("✅ ArbitrageVault configManager set to:", configManage.target);

  // ----------------------------
  // 3. 部署 DoubleRouterIntegration (UUPS 可升级合约)
  // ----------------------------
  const DoubleRouterIntegration = await hre.ethers.getContractFactory("DoubleRouterIntegration");
  const doubleRouterIntegration = await hre.upgrades.deployProxy(DoubleRouterIntegration, [
    configManage.target
  ], {
    initializer: "initialize",
    kind: "uups"
  });
  await doubleRouterIntegration.waitForDeployment();
  console.log("✅ DoubleRouterIntegration (UUPS) deployed to:", doubleRouterIntegration.target);

  // ----------------------------
  // 4. 部署 UniswapV2Integration (UUPS 可升级合约)
  // ----------------------------
  const UniswapV2Integration = await hre.ethers.getContractFactory("UniswapV2Integration");
  const uniswapV2Integration = await hre.upgrades.deployProxy(UniswapV2Integration, [
    configManage.target
  ], {
    initializer: "initialize",
    kind: "uups"
  });
  await uniswapV2Integration.waitForDeployment();
  console.log("✅ UniswapV2Integration (UUPS) deployed to:", uniswapV2Integration.target);

  // ----------------------------
  // 5. 部署 FlashLoanRouter (普通合约)
  // ----------------------------
  const FlashLoanRouter = await hre.ethers.getContractFactory("FlashLoanRouter");
  const flashLoanRouter = await FlashLoanRouter.deploy(configManage.target);
  await flashLoanRouter.waitForDeployment();
  console.log("✅ FlashLoanRouter deployed to:", flashLoanRouter.target);

  // ----------------------------
  // 6. 部署 SpotArbitrage (UUPS 可升级合约)
  // ----------------------------
  const ZERO_ADDRESS = hre.ethers.ZeroAddress;
  const SpotArbitrage = await hre.ethers.getContractFactory("SpotArbitrage");
  const spotArbitrage = await hre.upgrades.deployProxy(SpotArbitrage, [
    doubleRouterIntegration.target,
    uniswapV2Integration.target,
    ZERO_ADDRESS // 临时用零地址，后续会更新
  ], {
    initializer: "initialize",
    kind: "uups"
  });
  await spotArbitrage.waitForDeployment();
  console.log("✅ SpotArbitrage (UUPS) deployed to:", spotArbitrage.target);

  // ----------------------------
  // 7. 部署 ArbitrageCore (UUPS 可升级合约)
  // ----------------------------
  const ArbitrageCore = await hre.ethers.getContractFactory("ArbitrageCore");
  const arbitrageCore = await hre.upgrades.deployProxy(ArbitrageCore, [
    spotArbitrage.target,       // _spotArbitrage
    flashLoanRouter.target,     // _flashLoanArbitrage
    PLATFORM_WALLET,            // _platFormWallet
    configManage.target,       // _configManager
    BACK_CALLER                 // _backCaller
  ], {
    initializer: "initialize",
    kind: "uups"
  });
  await arbitrageCore.waitForDeployment();
  console.log("✅ ArbitrageCore (UUPS) deployed to:", arbitrageCore.target);

  // ----------------------------
  // 8. 关键步骤：补全 SpotArbitrage 中的 ArbitrageCore 地址
  // ----------------------------
  await spotArbitrage.setArbitrageCore(arbitrageCore.target);
  console.log("✅ SpotArbitrage ArbitrageCore set to:", arbitrageCore.target);

  // 8.2 添加 Vault 到 ArbitrageCore
  await arbitrageCore.addVault(
    UNDERLYING_ASSET,
    arbitrageVault.target
  );
  console.log("✅ ArbitrageCore vault added.");

  // 8.3 设置 ArbitrageCore 中的 backCaller（后端钱包地址）
  await arbitrageCore.setBackCaller(BACK_CALLER);
  console.log("✅ ArbitrageCore backCaller set to:", BACK_CALLER);

  // ----------------------------
  // 9. 部署 MockRouter (用于本地测试，可控套利)
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
  // 10. 保存部署信息
  // ----------------------------
  const deployments = {
    network: hre.network.name,
    arbitratorVault: arbitrageVault.target,
    configManage: configManage.target,
    doubleRouterIntegration: doubleRouterIntegration.target,
    uniswapV2Integration: uniswapV2Integration.target,
    spotArbitrage: spotArbitrage.target,
    flashLoanRouter: flashLoanRouter.target,
    arbitrageCore: arbitrageCore.target,
    mockRouter1: mockRouter1.target,
    mockRouter2: mockRouter2.target
  };

  fs.mkdirSync('./deployments', { recursive: true });
  fs.writeFileSync(`./deployments/${hre.network.name}.json`, JSON.stringify(deployments, null, 2));
  console.log(`📝 Deployment info saved to ./deployments/${hre.network.name}.json`);

  console.log("\n🎉 ALL CONTRACTS DEPLOYED SUCCESSFULLY!");
}

main()
  .then(() => process.exit(0))
  .catch((error) => {
    console.error(error);
    process.exit(1);
  });