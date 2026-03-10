/**
 * Arbitrum One — 部署 FlashLoanArbitrage 合约
 *
 * FlashLoanRouter 已部署在 0x4A89d8B8B0376Fc8c7e2119445c828719BeC019E
 * 本脚本仅部署 FlashLoanArbitrage 并配置引用关系
 *
 * 使用方法：
 *   npx hardhat run scripts/deploy/deploy_flashloan_arbitrage.js --network arbitrumOne
 */

const hre = require("hardhat");
const fs = require("fs");

async function main() {
    const [deployer] = await hre.ethers.getSigners();
    console.log("========================================");
    console.log("Arbitrum One — FlashLoanArbitrage Deployment");
    console.log("========================================");
    console.log("Deployer:", deployer.address);

    const balance = await hre.ethers.provider.getBalance(deployer.address);
    console.log("Balance:", hre.ethers.formatEther(balance), "ETH");
    console.log("");

    // ============ 已部署的合约地址（2026-02-16 重新部署） ============
    const FLASH_LOAN_ROUTER = "0x4A89d8B8B0376Fc8c7e2119445c828719BeC019E";
    const SPOT_ARBITRAGE = "0xceCCf5D3f5ab1dAB5b13Fd5Cf7deeA9C3DBaad17";
    const CONFIG_MANAGER = "0x8dD68621209D31D2c3202F894B956DDEdF893697";
    const PLATFORM_WALLET = deployer.address;

    // Flash Loan 由 backend 直接调用，所以 arbitrageCore = keeper/deployer 地址
    // 这样 onlyArbitrageCore 修饰器允许 backend keeper 调用 executeFlashLoan
    const ARBITRAGE_CORE_FOR_FLASH = deployer.address;

    console.log("References:");
    console.log("  FlashLoanRouter:", FLASH_LOAN_ROUTER);
    console.log("  SpotArbitrage:", SPOT_ARBITRAGE);
    console.log("  ConfigManager:", CONFIG_MANAGER);
    console.log("  ArbitrageCore (caller):", ARBITRAGE_CORE_FOR_FLASH);
    console.log("");

    // ============ 部署 FlashLoanArbitrage ============
    console.log("[1/1] Deploying FlashLoanArbitrage...");
    const FlashLoanArbitrage = await hre.ethers.getContractFactory("FlashLoanArbitrage");
    const flashLoanArbitrage = await FlashLoanArbitrage.deploy(
        FLASH_LOAN_ROUTER,          // _flashLoanRouter
        SPOT_ARBITRAGE,             // _spotArbitrage
        ARBITRAGE_CORE_FOR_FLASH,   // _arbitrageCore (= keeper, so backend can call)
        PLATFORM_WALLET,            // _platFormWallet
        CONFIG_MANAGER              // _configManager
    );
    await flashLoanArbitrage.waitForDeployment();
    const flashLoanAddress = flashLoanArbitrage.target;
    console.log("  ✅ FlashLoanArbitrage:", flashLoanAddress);

    // ============ 验证合约配置 ============
    console.log("\n[Verify] Checking contract references...");
    const router = await flashLoanArbitrage.flashLoanRouter();
    const spot = await flashLoanArbitrage.spotArbitrage();
    const core = await flashLoanArbitrage.arbitrageCore();
    console.log("  flashLoanRouter:", router);
    console.log("  spotArbitrage:", spot);
    console.log("  arbitrageCore:", core);

    // ============ 更新部署地址文件 ============
    const addressesPath = "./deployments/arbitrum-addresses.json";
    let deploymentInfo;
    try {
        deploymentInfo = JSON.parse(fs.readFileSync(addressesPath, "utf8"));
    } catch {
        deploymentInfo = { network: "arbitrum", chainId: 42161, addresses: {} };
    }
    deploymentInfo.addresses.flashLoanArbitrage = flashLoanAddress;
    deploymentInfo.flashLoanDeployTimestamp = new Date().toISOString();
    fs.writeFileSync(addressesPath, JSON.stringify(deploymentInfo, null, 2));
    console.log("\n📝 Updated", addressesPath);

    // ============ 完成 ============
    const finalBalance = await hre.ethers.provider.getBalance(deployer.address);
    const gasSpent = balance - finalBalance;

    console.log("\n========================================");
    console.log("Deployment Complete!");
    console.log("========================================");
    console.log("FlashLoanArbitrage:", flashLoanAddress);
    console.log("Gas spent:", hre.ethers.formatEther(gasSpent), "ETH");
    console.log("Remaining:", hre.ethers.formatEther(finalBalance), "ETH");
    console.log("");
    console.log("Next steps:");
    console.log("  1. Update backend/configs/config.yaml:");
    console.log(`     flash_loan_arbitrage: "${flashLoanAddress}"`);
    console.log("  2. Restart backend to enable flash loan path");
}

main()
    .then(() => process.exit(0))
    .catch((error) => {
        console.error(error);
        process.exit(1);
    });
