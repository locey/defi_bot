/**
 * Arbitrum One — 部署 CompoundV3Liquidator 合约
 *
 * 使用 Balancer 免费闪电贷执行 Compound V3 (Comet) 清算
 *
 * Comet 市场:
 *   USDC:   0x9c4ec768c28520B50860ea7a15bd7213a9fF58bf
 *   USDC.e: 0xA5EDBDD9646f8dFF606d7448e414884C7d905dCA
 *   USDT:   0xd98Be00b5D27fc98112BdE293e487f8D4cA57d07
 *   WETH:   0x6f7D514bbD4aFf3BcD1140B7344b32f063dEe486
 *
 * 使用方法：
 *   npx hardhat run scripts/deploy/deploy_compound_v3_liquidator.js --network arbitrumOne
 */

const hre = require("hardhat");
const fs = require("fs");

async function main() {
    const [deployer] = await hre.ethers.getSigners();
    console.log("========================================");
    console.log("Arbitrum One — CompoundV3Liquidator Deployment");
    console.log("========================================");
    console.log("Deployer:", deployer.address);

    const balance = await hre.ethers.provider.getBalance(deployer.address);
    console.log("Balance:", hre.ethers.formatEther(balance), "ETH");
    console.log("");

    // ============ 配置 ============
    const BALANCER_VAULT = "0xBA12222222228d8Ba445958a75a0704d566BF2C8";
    const PROFIT_RECEIVER = deployer.address;
    const UNISWAP_V3_ROUTER = "0xE592427A0AEce92De3Edee1F18E0157C05861564";
    const SUSHISWAP_ROUTER = "0x1b02dA8Cb0d097eB8D57A175b88c7D8b47997506";

    // Compound V3 Comet 市场
    const COMET_MARKETS = [
        { name: "USDC", address: "0x9c4ec768c28520B50860ea7a15bd7213a9fF58bf" },
        { name: "USDC.e", address: "0xA5EDBDD9646f8dFF606d7448e414884C7d905dCA" },
        { name: "USDT", address: "0xd98Be00b5D27fc98112BdE293e487f8D4cA57d07" },
        { name: "WETH", address: "0x6f7D514bbD4aFf3BcD1140B7344b32f063dEe486" },
    ];

    // ============ 部署 ============
    console.log("[1/4] Deploying CompoundV3Liquidator...");
    const CompoundV3Liquidator = await hre.ethers.getContractFactory("CompoundV3Liquidator");
    const liquidator = await CompoundV3Liquidator.deploy(
        BALANCER_VAULT,
        PROFIT_RECEIVER
    );
    await liquidator.waitForDeployment();
    const liquidatorAddress = liquidator.target;
    console.log("  ✅ CompoundV3Liquidator:", liquidatorAddress);

    // ============ 授权 Comet 市场 ============
    console.log("\n[2/4] Authorizing Comet markets...");
    for (const market of COMET_MARKETS) {
        const tx = await liquidator.setAuthorizedComet(market.address, true);
        await tx.wait();
        console.log(`  ✅ ${market.name}: ${market.address}`);
    }

    // ============ 授权 DEX Routers ============
    console.log("\n[3/4] Authorizing DEX routers...");
    let tx = await liquidator.setAuthorizedRouter(UNISWAP_V3_ROUTER, true, true);
    await tx.wait();
    console.log("  ✅ Uniswap V3 Router (V3)");

    tx = await liquidator.setAuthorizedRouter(SUSHISWAP_ROUTER, true, false);
    await tx.wait();
    console.log("  ✅ SushiSwap Router (V2)");

    // ============ 验证 ============
    console.log("\n[4/4] Verifying...");
    const vault = await liquidator.balancerVault();
    const receiver = await liquidator.profitReceiver();
    console.log("  balancerVault:", vault);
    console.log("  profitReceiver:", receiver);
    for (const market of COMET_MARKETS) {
        const auth = await liquidator.authorizedComets(market.address);
        console.log(`  ${market.name} authorized:`, auth);
    }

    // ============ 更新地址文件 ============
    const addressesPath = "./deployments/arbitrum-addresses.json";
    let deploymentInfo;
    try {
        deploymentInfo = JSON.parse(fs.readFileSync(addressesPath, "utf8"));
    } catch {
        deploymentInfo = { network: "arbitrum", chainId: 42161, addresses: {} };
    }
    deploymentInfo.addresses.compoundV3Liquidator = liquidatorAddress;
    deploymentInfo.compoundV3LiquidatorDeployTimestamp = new Date().toISOString();
    fs.writeFileSync(addressesPath, JSON.stringify(deploymentInfo, null, 2));
    console.log("\n📝 Updated", addressesPath);

    // ============ 完成 ============
    const finalBalance = await hre.ethers.provider.getBalance(deployer.address);
    const gasSpent = balance - finalBalance;

    console.log("\n========================================");
    console.log("Deployment Complete!");
    console.log("========================================");
    console.log("CompoundV3Liquidator:", liquidatorAddress);
    console.log("Gas spent:", hre.ethers.formatEther(gasSpent), "ETH");
    console.log("Remaining:", hre.ethers.formatEther(finalBalance), "ETH");
}

main()
    .then(() => process.exit(0))
    .catch((error) => {
        console.error(error);
        process.exit(1);
    });
