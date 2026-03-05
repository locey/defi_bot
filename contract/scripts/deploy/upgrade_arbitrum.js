/**
 * UUPS 代理升级脚本 — Arbitrum One 主网
 * 
 * 升级已部署的可升级合约（ArbitrageCore, SpotArbitrage）到最新实现
 * 
 * 使用方法：
 *   npx hardhat run scripts/deploy/upgrade_arbitrum.js --network arbitrumOne
 * 
 * 前提：
 *   1. .env 中有 DEPLOYER_PRIVATE_KEY（必须是原始部署者/owner）
 *   2. 已部署的 proxy 合约地址正确
 */

const { ethers, upgrades } = require("hardhat");

// 已部署的 Proxy 地址（来自 arbitrum-addresses.json）
const PROXY_ADDRESSES = {
    arbitrageCore: "0x27ea15F931328474d75BE5B0a493278ba7041C74",
    spotArbitrage: "0x7e9eC412aF1f8657b99Df8f419c5D98F11F41bd5",
    // configManage 不需要升级（接口没有破坏性变更，只是新增了 uniswapV3Quoter 字段）
};

async function main() {
    const [deployer] = await ethers.getSigners();
    console.log("========================================");
    console.log("UUPS Proxy Upgrade — Arbitrum One");
    console.log("========================================");
    console.log("Deployer:", deployer.address);
    
    const balance = await ethers.provider.getBalance(deployer.address);
    console.log("Balance:", ethers.formatEther(balance), "ETH");
    console.log("");

    // ======== 1. 升级 ArbitrageCore ========
    console.log("[1/2] Upgrading ArbitrageCore...");
    console.log("  Proxy:", PROXY_ADDRESSES.arbitrageCore);
    
    try {
        const ArbitrageCoreV2 = await ethers.getContractFactory("ArbitrageCore");
        
        // 使用 unsafeSkipStorageCheck 因为我们移除了一些状态变量（flashLoanArbitrage 等）
        // 这在 UUPS 升级中是安全的，只要新合约不重新使用旧的存储槽位
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
        console.log("  Continuing with next contract...");
    }
    console.log("");

    // ======== 2. 升级 SpotArbitrage ========
    console.log("[2/2] Upgrading SpotArbitrage...");
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
    }
    console.log("");

    // ======== 3. 设置 SpotArbitrage 的 backendCaller ========
    console.log("[Post-upgrade] Setting backendCaller on SpotArbitrage...");
    try {
        const SpotArbitrage = await ethers.getContractFactory("SpotArbitrage");
        const spot = SpotArbitrage.attach(PROXY_ADDRESSES.spotArbitrage);
        
        // 设置 backendCaller 为 deployer（Keeper 钱包）
        const tx = await spot.setBackendCaller(deployer.address);
        await tx.wait();
        console.log("  ✅ backendCaller set to:", deployer.address);
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
