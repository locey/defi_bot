/**
 * Arbitrum One — 部署 SiloLiquidator 合约
 *
 * Silo Finance V1 内置闪电清算（无需外部闪电贷）
 *
 * Silo 合约:
 *   SiloRepository: 0x8658047e48CC09161f4152c79155Dac1d710Ff0a
 *   SiloLens:       0xBDb843c7a7e48Dc543424474d7Aa63b61B5D9536
 *
 * 使用方法：
 *   npx hardhat run scripts/deploy/deploy_silo_liquidator.js --network arbitrumOne
 */

const hre = require("hardhat");
const fs = require("fs");

async function main() {
    const [deployer] = await hre.ethers.getSigners();
    console.log("========================================");
    console.log("Arbitrum One — SiloLiquidator Deployment");
    console.log("========================================");
    console.log("Deployer:", deployer.address);

    const balance = await hre.ethers.provider.getBalance(deployer.address);
    console.log("Balance:", hre.ethers.formatEther(balance), "ETH");
    console.log("");

    // ============ 配置 ============
    const PROFIT_RECEIVER = deployer.address;
    const UNISWAP_V3_ROUTER = "0xE592427A0AEce92De3Edee1F18E0157C05861564";
    const SUSHISWAP_ROUTER = "0x1b02dA8Cb0d097eB8D57A175b88c7D8b47997506";

    // ============ 部署 ============
    console.log("[1/3] Deploying SiloLiquidator...");
    const SiloLiquidator = await hre.ethers.getContractFactory("SiloLiquidator");
    const liquidator = await SiloLiquidator.deploy(PROFIT_RECEIVER);
    await liquidator.waitForDeployment();
    const liquidatorAddress = liquidator.target;
    console.log("  ✅ SiloLiquidator:", liquidatorAddress);

    // ============ 授权 DEX Routers ============
    console.log("\n[2/3] Authorizing DEX routers...");
    let tx = await liquidator.setAuthorizedRouter(UNISWAP_V3_ROUTER, true, true);
    await tx.wait();
    console.log("  ✅ Uniswap V3 Router (V3)");

    tx = await liquidator.setAuthorizedRouter(SUSHISWAP_ROUTER, true, false);
    await tx.wait();
    console.log("  ✅ SushiSwap Router (V2)");

    // ============ 验证 ============
    console.log("\n[3/3] Verifying...");
    const receiver = await liquidator.profitReceiver();
    const isBackendAuth = await liquidator.authorizedBackends(deployer.address);
    console.log("  profitReceiver:", receiver);
    console.log("  deployer authorized:", isBackendAuth);

    // 注意：Silo 地址需要在发现后通过 setAuthorizedSilo() 逐个添加
    console.log("\n⚠️  Silo addresses must be authorized individually via setAuthorizedSilo()");
    console.log("    Backend will discover Silos and admin should authorize them");

    // ============ 更新地址文件 ============
    const addressesPath = "./deployments/arbitrum-addresses.json";
    let deploymentInfo;
    try {
        deploymentInfo = JSON.parse(fs.readFileSync(addressesPath, "utf8"));
    } catch {
        deploymentInfo = { network: "arbitrum", chainId: 42161, addresses: {} };
    }
    deploymentInfo.addresses.siloLiquidator = liquidatorAddress;
    deploymentInfo.siloLiquidatorDeployTimestamp = new Date().toISOString();
    fs.writeFileSync(addressesPath, JSON.stringify(deploymentInfo, null, 2));
    console.log("\n📝 Updated", addressesPath);

    // ============ 完成 ============
    const finalBalance = await hre.ethers.provider.getBalance(deployer.address);
    const gasSpent = balance - finalBalance;

    console.log("\n========================================");
    console.log("Deployment Complete!");
    console.log("========================================");
    console.log("SiloLiquidator:", liquidatorAddress);
    console.log("Gas spent:", hre.ethers.formatEther(gasSpent), "ETH");
    console.log("Remaining:", hre.ethers.formatEther(finalBalance), "ETH");
}

main()
    .then(() => process.exit(0))
    .catch((error) => {
        console.error(error);
        process.exit(1);
    });
