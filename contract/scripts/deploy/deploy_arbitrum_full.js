/**
 * Arbitrum One 主网 — 全量部署脚本
 * 
 * 部署所有合约（使用 master 分支最新代码），设置引用关系
 * 
 * 使用方法：
 *   npx hardhat run scripts/deploy/deploy_arbitrum_full.js --network arbitrumOne
 */

const hre = require("hardhat");
const fs = require("fs");

async function main() {
    const [deployer] = await hre.ethers.getSigners();
    console.log("========================================");
    console.log("Arbitrum One — Full Deployment");
    console.log("========================================");
    console.log("Deployer:", deployer.address);

    const balance = await hre.ethers.provider.getBalance(deployer.address);
    console.log("Balance:", hre.ethers.formatEther(balance), "ETH");
    console.log("");

    // ============ Arbitrum 主网地址常量 ============
    const WETH = "0x82aF49447D8a07e3bd95BD0d56f35241523fBab1";
    const USDC = "0xaf88d065e77c8cC2239327C5EDb3A432268e5831";
    const AAVE_LENDING_POOL = "0x794a61358D6845594F94dc1DB02A252b5b4814aD"; // Aave V3 Pool
    const UNISWAP_V2_ROUTER = "0x4752ba5DBc23f44D87826276BF6Fd6b1C372aD24";
    const UNISWAP_V3_ROUTER = "0xE592427A0AEce92De3Edee1F18E0157C05861564";
    const SUSHISWAP_ROUTER = "0x1b02dA8Cb0d097eB8D57A175b88c7D8b47997506";
    const PLATFORM_WALLET = deployer.address;
    const BACK_CALLER = deployer.address;

    const deployed = {};

    // ============ 1. ConfigManage (UUPS) ============
    console.log("[1/6] Deploying ConfigManage...");
    const ConfigManage = await hre.ethers.getContractFactory("ConfigManage");
    const configManage = await hre.upgrades.deployProxy(ConfigManage, [
        AAVE_LENDING_POOL,
        UNISWAP_V2_ROUTER,
        UNISWAP_V3_ROUTER,
        SUSHISWAP_ROUTER,
        WETH  // arbitrageVault placeholder (will update later)
    ], { initializer: "initialize", kind: "uups" });
    await configManage.waitForDeployment();
    deployed.configManager = configManage.target;
    console.log("  ✅ ConfigManage:", deployed.configManager);

    // ============ 2. DoubleRouterIntegration (UUPS) ============
    console.log("[2/6] Deploying DoubleRouterIntegration...");
    const DoubleRouterIntegration = await hre.ethers.getContractFactory("DoubleRouterIntegration");
    const doubleRouter = await hre.upgrades.deployProxy(DoubleRouterIntegration, [
        configManage.target
    ], { initializer: "initialize", kind: "uups" });
    await doubleRouter.waitForDeployment();
    deployed.doubleRouterIntegration = doubleRouter.target;
    console.log("  ✅ DoubleRouterIntegration:", deployed.doubleRouterIntegration);

    // 设置 routers
    await doubleRouter.setRouters([UNISWAP_V2_ROUTER, UNISWAP_V3_ROUTER, SUSHISWAP_ROUTER]);
    console.log("  ✅ Routers set");

    // ============ 3. UniswapV2Integration (UUPS) ============
    console.log("[3/6] Deploying UniswapV2Integration...");
    const UniswapV2Integration = await hre.ethers.getContractFactory("UniswapV2Integration");
    const v2Integration = await hre.upgrades.deployProxy(UniswapV2Integration, [
        configManage.target
    ], { initializer: "initialize", kind: "uups" });
    await v2Integration.waitForDeployment();
    deployed.uniswapV2Integration = v2Integration.target;
    console.log("  ✅ UniswapV2Integration:", deployed.uniswapV2Integration);

    // ============ 4. SpotArbitrage (UUPS) ============
    console.log("[4/6] Deploying SpotArbitrage...");
    const SpotArbitrage = await hre.ethers.getContractFactory("SpotArbitrage");
    const spotArbitrage = await hre.upgrades.deployProxy(SpotArbitrage, [
        doubleRouter.target,        // _doubleRouterIntegration
        v2Integration.target,       // _uniswapV2Integration
        hre.ethers.ZeroAddress,     // _arbitrageCore (placeholder, set later)
        BACK_CALLER                 // _backendCaller
    ], { initializer: "initialize", kind: "uups" });
    await spotArbitrage.waitForDeployment();
    deployed.spotArbitrage = spotArbitrage.target;
    console.log("  ✅ SpotArbitrage:", deployed.spotArbitrage);

    // ============ 5. ArbitrageCore (UUPS) ============
    console.log("[5/6] Deploying ArbitrageCore...");
    const ArbitrageCore = await hre.ethers.getContractFactory("ArbitrageCore");
    const arbitrageCore = await hre.upgrades.deployProxy(ArbitrageCore, [
        spotArbitrage.target,       // _spotArbitrage
        PLATFORM_WALLET,            // _platFormWallet
        configManage.target,        // _configManager
        BACK_CALLER                 // _backCaller
    ], { initializer: "initialize", kind: "uups" });
    await arbitrageCore.waitForDeployment();
    deployed.arbitrageCore = arbitrageCore.target;
    console.log("  ✅ ArbitrageCore:", deployed.arbitrageCore);

    // ============ 6. ArbitrageVault ============
    console.log("[6/6] Deploying ArbitrageVault...");
    const ArbitrageVault = await hre.ethers.getContractFactory("ArbitrageVault");
    const vault = await ArbitrageVault.deploy(
        WETH,                       // underlying asset
        PLATFORM_WALLET,            // platform wallet
        "Arbitrage Vault WETH",     // name
        "avWETH"                    // symbol
    );
    await vault.waitForDeployment();
    deployed.vault = vault.target;
    console.log("  ✅ ArbitrageVault:", deployed.vault);

    // ============ 设置引用关系 ============
    console.log("\n[Setup] Configuring contract references...");

    // SpotArbitrage → ArbitrageCore
    let tx = await spotArbitrage.setArbitrageCore(arbitrageCore.target);
    await tx.wait();
    console.log("  ✅ SpotArbitrage.arbitrageCore =", arbitrageCore.target);

    // ArbitrageVault → ConfigManager
    tx = await vault.setConfigManager(configManage.target);
    await tx.wait();
    console.log("  ✅ ArbitrageVault.configManager =", configManage.target);

    // ArbitrageVault → ArbitrageCore
    tx = await vault.setArbitrageCore(arbitrageCore.target);
    await tx.wait();
    console.log("  ✅ ArbitrageVault.arbitrageCore =", arbitrageCore.target);

    // ArbitrageCore → addVault(WETH, vault)
    tx = await arbitrageCore.addVault(WETH, vault.target);
    await tx.wait();
    console.log("  ✅ ArbitrageCore.addVault(WETH) =", vault.target);

    // DoubleRouterIntegration → setSpotArbitrage
    tx = await doubleRouter.setSpotArbitrage(spotArbitrage.target);
    await tx.wait();
    console.log("  ✅ DoubleRouterIntegration.spotArbitrage =", spotArbitrage.target);

    // ============ 保存部署地址 ============
    const deploymentInfo = {
        network: "arbitrum",
        chainId: 42161,
        chainName: "Arbitrum One",
        deployer: deployer.address,
        timestamp: new Date().toISOString(),
        addresses: {
            weth: WETH,
            configManager: deployed.configManager,
            vault: deployed.vault,
            doubleRouterIntegration: deployed.doubleRouterIntegration,
            uniswapV2Integration: deployed.uniswapV2Integration,
            spotArbitrage: deployed.spotArbitrage,
            arbitrageCore: deployed.arbitrageCore,
        },
        routers: {
            uniswapV2: UNISWAP_V2_ROUTER,
            uniswapV3: UNISWAP_V3_ROUTER,
            sushiswap: SUSHISWAP_ROUTER,
        },
        external: {
            aaveLendingPool: AAVE_LENDING_POOL,
        }
    };

    // Save to multiple locations
    fs.mkdirSync("./deployments", { recursive: true });
    fs.writeFileSync("./deployments/arbitrum-addresses.json", JSON.stringify(deploymentInfo, null, 2));
    fs.writeFileSync("./contracts/deployments/arbitrum-addresses.json", JSON.stringify(deploymentInfo, null, 2));
    console.log("\n📝 Deployment saved to deployments/arbitrum-addresses.json");

    // ============ 完成 ============
    const finalBalance = await hre.ethers.provider.getBalance(deployer.address);
    const gasSpent = balance - finalBalance;

    console.log("\n========================================");
    console.log("Deployment Complete!");
    console.log("========================================");
    console.log("Gas spent:", hre.ethers.formatEther(gasSpent), "ETH");
    console.log("Remaining:", hre.ethers.formatEther(finalBalance), "ETH");
    console.log("");
    console.log("Contract Addresses:");
    for (const [name, addr] of Object.entries(deployed)) {
        console.log(`  ${name}: ${addr}`);
    }
}

main()
    .then(() => process.exit(0))
    .catch((error) => {
        console.error(error);
        process.exit(1);
    });
