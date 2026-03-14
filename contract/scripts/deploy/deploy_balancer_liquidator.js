/**
 * Arbitrum One — 部署 BalancerLiquidator 合约（使用 Balancer 免费闪电贷）
 *
 * Balancer V2 Vault: 0xBA12222222228d8Ba445958a75a0704d566BF2C8 (所有链相同)
 * Aave V3 Pool:      0x794a61358D6845594F94dc1DB02A252b5b4814aD
 *
 * 使用方法：
 *   npx hardhat run scripts/deploy/deploy_balancer_liquidator.js --network arbitrumOne
 */

const hre = require("hardhat");
const fs = require("fs");

async function main() {
    const [deployer] = await hre.ethers.getSigners();
    console.log("========================================");
    console.log("Arbitrum One — BalancerLiquidator Deployment");
    console.log("========================================");
    console.log("Deployer:", deployer.address);

    const balance = await hre.ethers.provider.getBalance(deployer.address);
    console.log("Balance:", hre.ethers.formatEther(balance), "ETH");
    console.log("");

    // ============ 配置 ============
    const BALANCER_VAULT = "0xBA12222222228d8Ba445958a75a0704d566BF2C8";
    const AAVE_V3_POOL = "0x794a61358D6845594F94dc1DB02A252b5b4814aD";
    const PROFIT_RECEIVER = deployer.address;
    const UNISWAP_V3_ROUTER = "0xE592427A0AEce92De3Edee1F18E0157C05861564";
    const SUSHISWAP_ROUTER = "0x1b02dA8Cb0d097eB8D57A175b88c7D8b47997506";

    console.log("Config:");
    console.log("  Balancer Vault:", BALANCER_VAULT);
    console.log("  Aave V3 Pool:", AAVE_V3_POOL);
    console.log("  Profit Receiver:", PROFIT_RECEIVER);
    console.log("");

    // ============ 部署 BalancerLiquidator ============
    console.log("[1/3] Deploying BalancerLiquidator...");
    const BalancerLiquidator = await hre.ethers.getContractFactory("BalancerLiquidator");
    const liquidator = await BalancerLiquidator.deploy(
        BALANCER_VAULT,
        AAVE_V3_POOL,
        PROFIT_RECEIVER
    );
    await liquidator.waitForDeployment();
    const liquidatorAddress = liquidator.target;
    console.log("  ✅ BalancerLiquidator:", liquidatorAddress);

    // ============ 配置 DEX Router 白名单 ============
    console.log("\n[2/3] Setting up authorized routers...");

    // Uniswap V3 Router (V3)
    let tx = await liquidator.setAuthorizedRouter(UNISWAP_V3_ROUTER, true, true);
    await tx.wait();
    console.log("  ✅ Uniswap V3 Router authorized (V3)");

    // SushiSwap Router (V2)
    tx = await liquidator.setAuthorizedRouter(SUSHISWAP_ROUTER, true, false);
    await tx.wait();
    console.log("  ✅ SushiSwap Router authorized (V2)");

    // ============ 验证 ============
    console.log("\n[3/3] Checking contract state...");
    const vault = await liquidator.balancerVault();
    const pool = await liquidator.aavePool();
    const receiver = await liquidator.profitReceiver();
    const isBackendAuth = await liquidator.authorizedBackends(deployer.address);
    const isUniV3Auth = await liquidator.authorizedRouters(UNISWAP_V3_ROUTER);
    console.log("  balancerVault:", vault);
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
    deploymentInfo.addresses.balancerLiquidator = liquidatorAddress;
    deploymentInfo.balancerLiquidatorDeployTimestamp = new Date().toISOString();
    fs.writeFileSync(addressesPath, JSON.stringify(deploymentInfo, null, 2));
    console.log("\n📝 Updated", addressesPath);

    // ============ 完成 ============
    const finalBalance = await hre.ethers.provider.getBalance(deployer.address);
    const gasSpent = balance - finalBalance;

    console.log("\n========================================");
    console.log("Deployment Complete!");
    console.log("========================================");
    console.log("BalancerLiquidator:", liquidatorAddress);
    console.log("Gas spent:", hre.ethers.formatEther(gasSpent), "ETH");
    console.log("Remaining:", hre.ethers.formatEther(finalBalance), "ETH");
    console.log("");
    console.log("Next steps:");
    console.log("  1. Update backend/configs/config.yaml:");
    console.log(`     balancer_liquidator: "${liquidatorAddress}"`);
    console.log("  2. Update backend config to use BalancerLiquidator as primary");
    console.log("  3. Keep FlashLoanLiquidator as fallback (Aave flash loan)");
}

main()
    .then(() => process.exit(0))
    .catch((error) => {
        console.error(error);
        process.exit(1);
    });
