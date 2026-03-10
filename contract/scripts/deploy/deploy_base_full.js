/**
 * Base Mainnet — 全量部署脚本
 *
 * 部署所有套利合约到 Base (chainId 8453)
 *
 * 使用前：
 *   1. 在 hardhat.config.cjs 添加 base 网络配置
 *   2. 确保 deployer 有足够 ETH (Base 上 gas 很便宜)
 *
 * 使用方法：
 *   npx hardhat run scripts/deploy/deploy_base_full.js --network base
 */

const hre = require("hardhat");
const fs = require("fs");

async function main() {
    const [deployer] = await hre.ethers.getSigners();
    console.log("========================================");
    console.log("Base — Full Deployment");
    console.log("========================================");
    console.log("Deployer:", deployer.address);

    const balance = await hre.ethers.provider.getBalance(deployer.address);
    console.log("Balance:", hre.ethers.formatEther(balance), "ETH");
    console.log("");

    // ============ Base 主网地址常量 ============
    const WETH = "0x4200000000000000000000000000000000000006";
    const USDC = "0x833589fCD6eDb6E08f4c7C32D4f71b54bdA02913";
    const AAVE_LENDING_POOL = "0xA238Dd80C259a72e81d7e4664a9801593F98d1c5";
    const UNISWAP_V2_ROUTER = hre.ethers.ZeroAddress; // Base 没有标准 V2 router
    const UNISWAP_V3_ROUTER = "0x2626664c2603336E57B271c5C0b26F421741e481"; // SwapRouter02
    const AERODROME_ROUTER = "0xcF77a3Ba9A5CA399B7c97c74d54e5b1Beb874E43";
    const PLATFORM_WALLET = deployer.address;
    const BACK_CALLER = deployer.address;

    const deployed = {};

    // ============ 1. ConfigManage (UUPS) ============
    console.log("[1/7] Deploying ConfigManage...");
    const ConfigManage = await hre.ethers.getContractFactory("ConfigManage");
    const configManage = await hre.upgrades.deployProxy(ConfigManage, [
        AAVE_LENDING_POOL,
        UNISWAP_V3_ROUTER,  // V2 用 V3 router 代替（Base 没有标准 V2）
        UNISWAP_V3_ROUTER,
        AERODROME_ROUTER,
        WETH
    ], { initializer: "initialize", kind: "uups" });
    await configManage.waitForDeployment();
    deployed.configManager = configManage.target;
    console.log("  ✅ ConfigManage:", deployed.configManager);

    // ============ 2. DoubleRouterIntegration (UUPS) ============
    console.log("[2/7] Deploying DoubleRouterIntegration...");
    const DoubleRouterIntegration = await hre.ethers.getContractFactory("DoubleRouterIntegration");
    const doubleRouter = await hre.upgrades.deployProxy(DoubleRouterIntegration, [
        configManage.target
    ], { initializer: "initialize", kind: "uups" });
    await doubleRouter.waitForDeployment();
    deployed.doubleRouterIntegration = doubleRouter.target;
    console.log("  ✅ DoubleRouterIntegration:", deployed.doubleRouterIntegration);

    // 设置 routers（Base: V3 + Aerodrome）
    await doubleRouter.setRouters([UNISWAP_V3_ROUTER, AERODROME_ROUTER]);
    console.log("  ✅ Routers set (UniswapV3, Aerodrome)");

    // ============ 3. UniswapV2Integration (UUPS) ============
    console.log("[3/7] Deploying UniswapV2Integration...");
    const UniswapV2Integration = await hre.ethers.getContractFactory("UniswapV2Integration");
    const v2Integration = await hre.upgrades.deployProxy(UniswapV2Integration, [
        configManage.target
    ], { initializer: "initialize", kind: "uups" });
    await v2Integration.waitForDeployment();
    deployed.uniswapV2Integration = v2Integration.target;
    console.log("  ✅ UniswapV2Integration:", deployed.uniswapV2Integration);

    // ============ 4. FlashLoanRouter ============
    console.log("[4/7] Deploying FlashLoanRouter...");
    const FlashLoanRouter = await hre.ethers.getContractFactory("FlashLoanRouter");
    const flashLoanRouter = await FlashLoanRouter.deploy();
    await flashLoanRouter.waitForDeployment();
    deployed.flashLoanRouter = flashLoanRouter.target;
    console.log("  ✅ FlashLoanRouter:", deployed.flashLoanRouter);

    // 配置 Aave V3 lending pool
    // FlashLoanRouter.LendingPlatForm.Aave_V2 = 0 (enum)
    await flashLoanRouter.setLendingPool(0, AAVE_LENDING_POOL);
    console.log("  ✅ Aave V3 LendingPool configured");

    // ============ 5. SpotArbitrage (UUPS) ============
    console.log("[5/7] Deploying SpotArbitrage...");
    const SpotArbitrage = await hre.ethers.getContractFactory("SpotArbitrage");
    const spotArbitrage = await hre.upgrades.deployProxy(SpotArbitrage, [
        doubleRouter.target,
        v2Integration.target,
        hre.ethers.ZeroAddress,
        BACK_CALLER
    ], { initializer: "initialize", kind: "uups" });
    await spotArbitrage.waitForDeployment();
    deployed.spotArbitrage = spotArbitrage.target;
    console.log("  ✅ SpotArbitrage:", deployed.spotArbitrage);

    // ============ 6. ArbitrageCore (UUPS) ============
    console.log("[6/7] Deploying ArbitrageCore...");
    const ArbitrageCore = await hre.ethers.getContractFactory("ArbitrageCore");
    const arbitrageCore = await hre.upgrades.deployProxy(ArbitrageCore, [
        spotArbitrage.target,
        PLATFORM_WALLET,
        configManage.target,
        BACK_CALLER
    ], { initializer: "initialize", kind: "uups" });
    await arbitrageCore.waitForDeployment();
    deployed.arbitrageCore = arbitrageCore.target;
    console.log("  ✅ ArbitrageCore:", deployed.arbitrageCore);

    // ============ 7. ArbitrageVault + FlashLoanArbitrage ============
    console.log("[7/7] Deploying ArbitrageVault + FlashLoanArbitrage...");
    const ArbitrageVault = await hre.ethers.getContractFactory("ArbitrageVault");
    const vault = await ArbitrageVault.deploy(WETH, PLATFORM_WALLET, "Arbitrage Vault WETH", "avWETH");
    await vault.waitForDeployment();
    deployed.vault = vault.target;
    console.log("  ✅ ArbitrageVault:", deployed.vault);

    const FlashLoanArbitrage = await hre.ethers.getContractFactory("FlashLoanArbitrage");
    const flashLoanArbitrage = await FlashLoanArbitrage.deploy(
        flashLoanRouter.target,
        spotArbitrage.target,
        deployer.address,  // arbitrageCore = keeper (backend 直接调用)
        PLATFORM_WALLET,
        configManage.target
    );
    await flashLoanArbitrage.waitForDeployment();
    deployed.flashLoanArbitrage = flashLoanArbitrage.target;
    console.log("  ✅ FlashLoanArbitrage:", deployed.flashLoanArbitrage);

    // ============ 设置引用关系 ============
    console.log("\n[Setup] Configuring references...");

    let tx = await spotArbitrage.setArbitrageCore(arbitrageCore.target);
    await tx.wait();
    console.log("  ✅ SpotArbitrage.arbitrageCore set");

    tx = await vault.setConfigManager(configManage.target);
    await tx.wait();
    console.log("  ✅ Vault.configManager set");

    tx = await vault.setArbitrageCore(arbitrageCore.target);
    await tx.wait();
    console.log("  ✅ Vault.arbitrageCore set");

    tx = await arbitrageCore.addVault(WETH, vault.target);
    await tx.wait();
    console.log("  ✅ ArbitrageCore.addVault(WETH) set");

    tx = await doubleRouter.setSpotArbitrage(spotArbitrage.target);
    await tx.wait();
    console.log("  ✅ DoubleRouter.spotArbitrage set");

    // ============ 保存部署地址 ============
    const deploymentInfo = {
        network: "base",
        chainId: 8453,
        chainName: "Base",
        deployer: deployer.address,
        timestamp: new Date().toISOString(),
        addresses: {
            weth: WETH,
            configManager: deployed.configManager,
            vault: deployed.vault,
            doubleRouterIntegration: deployed.doubleRouterIntegration,
            uniswapV2Integration: deployed.uniswapV2Integration,
            flashLoanRouter: deployed.flashLoanRouter,
            spotArbitrage: deployed.spotArbitrage,
            arbitrageCore: deployed.arbitrageCore,
            flashLoanArbitrage: deployed.flashLoanArbitrage,
        },
        routers: {
            uniswapV3: UNISWAP_V3_ROUTER,
            aerodrome: AERODROME_ROUTER,
        },
        external: {
            aaveLendingPool: AAVE_LENDING_POOL,
        }
    };

    fs.mkdirSync("./deployments", { recursive: true });
    fs.writeFileSync("./deployments/base-addresses.json", JSON.stringify(deploymentInfo, null, 2));
    console.log("\n📝 Saved to deployments/base-addresses.json");

    const finalBalance = await hre.ethers.provider.getBalance(deployer.address);
    console.log("\n========================================");
    console.log("Base Deployment Complete!");
    console.log("========================================");
    console.log("Gas spent:", hre.ethers.formatEther(balance - finalBalance), "ETH");
    console.log("Remaining:", hre.ethers.formatEther(finalBalance), "ETH");
    console.log("\nUpdate backend/configs/config.base.yaml with these addresses.");
}

main()
    .then(() => process.exit(0))
    .catch((error) => {
        console.error(error);
        process.exit(1);
    });
