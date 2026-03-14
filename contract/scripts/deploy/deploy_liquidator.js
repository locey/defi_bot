/**
 * Arbitrum One — 部署 FlashLoanLiquidator 合约
 *
 * Aave V3 Pool: 0x794a61358D6845594F94dc1DB02A252b5b4814aD
 *
 * 使用方法：
 *   npx hardhat run scripts/deploy/deploy_liquidator.js --network arbitrumOne
 */

const hre = require("hardhat");
const fs = require("fs");

async function main() {
    const [deployer] = await hre.ethers.getSigners();
    console.log("========================================");
    console.log("Arbitrum One — FlashLoanLiquidator Deployment");
    console.log("========================================");
    console.log("Deployer:", deployer.address);

    const balance = await hre.ethers.provider.getBalance(deployer.address);
    console.log("Balance:", hre.ethers.formatEther(balance), "ETH");
    console.log("");

    // ============ 配置 ============
    const AAVE_V3_POOL = "0x794a61358D6845594F94dc1DB02A252b5b4814aD";
    const PROFIT_RECEIVER = deployer.address; // 利润接收地址 = 部署者
    const UNISWAP_V3_ROUTER = "0xE592427A0AEce92De3Edee1F18E0157C05861564";
    const SUSHISWAP_ROUTER = "0x1b02dA8Cb0d097eB8D57A175b88c7D8b47997506";

    console.log("Config:");
    console.log("  Aave V3 Pool:", AAVE_V3_POOL);
    console.log("  Profit Receiver:", PROFIT_RECEIVER);
    console.log("");

    // ============ 部署 FlashLoanLiquidator ============
    console.log("[1/3] Deploying FlashLoanLiquidator...");
    const FlashLoanLiquidator = await hre.ethers.getContractFactory("FlashLoanLiquidator");
    const liquidator = await FlashLoanLiquidator.deploy(
        AAVE_V3_POOL,
        PROFIT_RECEIVER
    );
    await liquidator.waitForDeployment();
    const liquidatorAddress = liquidator.target;
    console.log("  ✅ FlashLoanLiquidator:", liquidatorAddress);

    // ============ 配置 Backend 权限 ============
    console.log("\n[2/3] Authorizing backend (deployer) as authorized caller...");
    // 部署者已在构造函数中自动授权

    // ============ 配置 DEX Router 白名单 ============
    console.log("[3/3] Setting up authorized routers...");

    // Uniswap V3 Router (V3)
    let tx = await liquidator.setAuthorizedRouter(UNISWAP_V3_ROUTER, true, true);
    await tx.wait();
    console.log("  ✅ Uniswap V3 Router authorized (V3)");

    // SushiSwap Router (V2)
    tx = await liquidator.setAuthorizedRouter(SUSHISWAP_ROUTER, true, false);
    await tx.wait();
    console.log("  ✅ SushiSwap Router authorized (V2)");

    // ============ 验证 ============
    console.log("\n[Verify] Checking contract state...");
    const pool = await liquidator.aavePool();
    const receiver = await liquidator.profitReceiver();
    const isBackendAuth = await liquidator.authorizedBackends(deployer.address);
    const isUniV3Auth = await liquidator.authorizedRouters(UNISWAP_V3_ROUTER);
    console.log("  aavePool:", pool);
    console.log("  profitReceiver:", receiver);
    console.log("  deployer authorized:", isBackendAuth);
    console.log("  UniV3 router authorized:", isUniV3Auth);

    // ============ 更新部署地址文件 ============
    const addressesPath = "./deployments/arbitrum-addresses.json";
    let deploymentInfo;
    try {
        deploymentInfo = JSON.parse(fs.readFileSync(addressesPath, "utf8"));
    } catch {
        deploymentInfo = { network: "arbitrum", chainId: 42161, addresses: {} };
    }
    deploymentInfo.addresses.flashLoanLiquidator = liquidatorAddress;
    deploymentInfo.liquidatorDeployTimestamp = new Date().toISOString();
    fs.writeFileSync(addressesPath, JSON.stringify(deploymentInfo, null, 2));
    console.log("\n📝 Updated", addressesPath);

    // ============ 完成 ============
    const finalBalance = await hre.ethers.provider.getBalance(deployer.address);
    const gasSpent = balance - finalBalance;

    console.log("\n========================================");
    console.log("Deployment Complete!");
    console.log("========================================");
    console.log("FlashLoanLiquidator:", liquidatorAddress);
    console.log("Gas spent:", hre.ethers.formatEther(gasSpent), "ETH");
    console.log("Remaining:", hre.ethers.formatEther(finalBalance), "ETH");
    console.log("");
    console.log("Next steps:");
    console.log("  1. Update backend/configs/config.yaml:");
    console.log(`     flash_loan_liquidator: "${liquidatorAddress}"`);
    console.log("  2. Restart backend to enable liquidation bot");
}

main()
    .then(() => process.exit(0))
    .catch((error) => {
        console.error(error);
        process.exit(1);
    });
