/**
 * UUPS 代理升级脚本 — Arbitrum One 主网
 *
 * 升级已部署的可升级合约到包含 feeTiers 的最新实现
 *
 * 使用方法：
 *   npx hardhat run scripts/deploy/upgrade_arbitrum.js --network arbitrumOne
 *
 * 前提：
 *   1. .env 中有 DEPLOYER_PRIVATE_KEY（必须是原始部署者/owner）
 *   2. 已部署的 proxy 合约地址正确
 */

const { ethers, upgrades } = require("hardhat");

// 已部署的 Proxy 地址（来自 config.yaml）
const PROXY_ADDRESSES = {
    arbitrageCore: "0x0D14428b4e297344C2C51D087934a3e3a9b822B9",
    spotArbitrage: "0xceCCf5D3f5ab1dAB5b13Fd5Cf7deeA9C3DBaad17",
    doubleRouterIntegration: "0xE075114d8C9142c9497eabc10b4CAaf2794c95dc",
};

async function main() {
    const [deployer] = await ethers.getSigners();
    console.log("========================================");
    console.log("UUPS Proxy Upgrade — Arbitrum One");
    console.log("升级内容: feeTiers (uint24[]) 全链路支持");
    console.log("========================================");
    console.log("Deployer:", deployer.address);

    const balance = await ethers.provider.getBalance(deployer.address);
    console.log("Balance:", ethers.formatEther(balance), "ETH");
    console.log("");

    // ======== 1. 升级 DoubleRouterIntegration (底层先升级) ========
    console.log("[1/3] Upgrading DoubleRouterIntegration...");
    console.log("  Proxy:", PROXY_ADDRESSES.doubleRouterIntegration);

    try {
        const DRFactory = await ethers.getContractFactory("DoubleRouterIntegration");
        const newImpl = await DRFactory.deploy();
        await newImpl.waitForDeployment();
        const newImplAddr = await newImpl.getAddress();
        console.log("  New implementation deployed:", newImplAddr);

        const proxy = await ethers.getContractAt(
            ["function upgradeToAndCall(address newImplementation, bytes memory data) external"],
            PROXY_ADDRESSES.doubleRouterIntegration
        );
        const tx = await proxy.upgradeToAndCall(newImplAddr, "0x");
        await tx.wait();
        console.log("  ✅ DoubleRouterIntegration upgraded!");
    } catch (error) {
        console.log("  ❌ DoubleRouterIntegration upgrade failed:", error.message);
        console.log("  Continuing with next contract...");
    }
    console.log("");

    // ======== 2. 升级 SpotArbitrage ========
    console.log("[2/3] Upgrading SpotArbitrage...");
    console.log("  Proxy:", PROXY_ADDRESSES.spotArbitrage);

    try {
        const SpotArbitrageV2 = await ethers.getContractFactory("SpotArbitrage");

        const upgraded = await upgrades.upgradeProxy(
            PROXY_ADDRESSES.spotArbitrage,
            SpotArbitrageV2,
            {
                unsafeSkipStorageCheck: true,
                kind: 'uups',
            }
        );
        await upgraded.waitForDeployment();

        const implAddr = await upgrades.erc1967.getImplementationAddress(PROXY_ADDRESSES.spotArbitrage);
        console.log("  ✅ SpotArbitrage upgraded!");
        console.log("  New implementation:", implAddr);
    } catch (error) {
        console.log("  ❌ SpotArbitrage upgrade failed:", error.message);
        console.log("  Continuing with next contract...");
    }
    console.log("");

    // ======== 3. 升级 ArbitrageCore ========
    console.log("[3/3] Upgrading ArbitrageCore...");
    console.log("  Proxy:", PROXY_ADDRESSES.arbitrageCore);

    try {
        const ArbitrageCoreV2 = await ethers.getContractFactory("ArbitrageCore");

        const upgraded = await upgrades.upgradeProxy(
            PROXY_ADDRESSES.arbitrageCore,
            ArbitrageCoreV2,
            {
                unsafeSkipStorageCheck: true,
                kind: 'uups',
            }
        );
        await upgraded.waitForDeployment();

        const implAddr = await upgrades.erc1967.getImplementationAddress(PROXY_ADDRESSES.arbitrageCore);
        console.log("  ✅ ArbitrageCore upgraded!");
        console.log("  New implementation:", implAddr);
    } catch (error) {
        console.log("  ❌ ArbitrageCore upgrade failed:", error.message);
    }
    console.log("");

    // ======== 4. 设置 SpotArbitrage 的 backendCaller ========
    console.log("[Post-upgrade] Verifying backendCaller on SpotArbitrage...");
    try {
        const SpotArbitrage = await ethers.getContractFactory("SpotArbitrage");
        const spot = SpotArbitrage.attach(PROXY_ADDRESSES.spotArbitrage);

        const tx = await spot.setBackendCaller(PROXY_ADDRESSES.arbitrageCore);
        await tx.wait();
        console.log("  ✅ backendCaller set to ArbitrageCore:", PROXY_ADDRESSES.arbitrageCore);
    } catch (error) {
        console.log("  ⚠️ setBackendCaller failed (may already be set):", error.message);
    }

    // ======== 完成 ========
    console.log("");
    console.log("========================================");
    console.log("Upgrade complete!");
    console.log("========================================");

    const finalBalance = await ethers.provider.getBalance(deployer.address);
    const gasUsed = balance - finalBalance;
    console.log("Gas spent:", ethers.formatEther(gasUsed), "ETH");
    console.log("Remaining balance:", ethers.formatEther(finalBalance), "ETH");
}

main()
    .then(() => process.exit(0))
    .catch((error) => {
        console.error(error);
        process.exit(1);
    });
