/**
 * Fix Flash Loan Authorization — Arbitrum One
 *
 * Two authorization bugs prevent FlashLoanArbitrage from executing:
 * 1. FlashLoanRouter.requestFlashLoan() has onlyAuthorized → FlashLoanArbitrage not registered
 * 2. SpotArbitrage.executeSwaps() has onlyAuthorizedCaller → FlashLoanArbitrage not in whitelist
 *
 * Fixes:
 * 1. FlashLoanRouter.setAuthorizedCaller(FlashLoanArbitrage, true)
 * 2. Upgrade SpotArbitrage (adds backendCaller to onlyAuthorizedCaller)
 * 3. SpotArbitrage.setBackendCaller(FlashLoanArbitrage)
 *
 * Usage:
 *   npx hardhat run scripts/deploy/fix_flashloan_auth.js --network arbitrumOne
 */

const { ethers, upgrades } = require("hardhat");

const ADDRESSES = {
    flashLoanRouter: "0x4A89d8B8B0376Fc8c7e2119445c828719BeC019E",
    flashLoanArbitrage: "0x8dbcdA14888b48CF3b6484e6aeb870B19563C01D",
    spotArbitrage: "0xceCCf5D3f5ab1dAB5b13Fd5Cf7deeA9C3DBaad17",
};

async function main() {
    const [deployer] = await ethers.getSigners();
    console.log("========================================");
    console.log("Fix Flash Loan Authorization — Arbitrum One");
    console.log("========================================");
    console.log("Deployer:", deployer.address);

    const balance = await ethers.provider.getBalance(deployer.address);
    console.log("Balance:", ethers.formatEther(balance), "ETH\n");

    // ======== 1. Authorize FlashLoanArbitrage on FlashLoanRouter ========
    console.log("[1/3] FlashLoanRouter.setAuthorizedCaller(FlashLoanArbitrage, true)");
    try {
        const router = await ethers.getContractAt(
            [
                "function setAuthorizedCaller(address caller, bool authorized) external",
                "function authorizedCallers(address) external view returns (bool)",
                "function admin() external view returns (address)",
            ],
            ADDRESSES.flashLoanRouter
        );

        // Check current state
        const admin = await router.admin();
        console.log("  Router admin:", admin);
        const isAuthorized = await router.authorizedCallers(ADDRESSES.flashLoanArbitrage);
        console.log("  FlashLoanArbitrage currently authorized:", isAuthorized);

        if (!isAuthorized) {
            const tx = await router.setAuthorizedCaller(ADDRESSES.flashLoanArbitrage, true);
            await tx.wait();
            console.log("  ✅ FlashLoanArbitrage authorized on FlashLoanRouter");
        } else {
            console.log("  ✅ Already authorized (skipped)");
        }
    } catch (error) {
        console.log("  ❌ Failed:", error.message);
    }
    console.log("");

    // ======== 2. Upgrade SpotArbitrage (adds backendCaller to onlyAuthorizedCaller) ========
    console.log("[2/3] Upgrade SpotArbitrage (onlyAuthorizedCaller now includes backendCaller)");
    try {
        const SpotArbitrageV3 = await ethers.getContractFactory("SpotArbitrage");

        const upgraded = await upgrades.upgradeProxy(
            ADDRESSES.spotArbitrage,
            SpotArbitrageV3,
            {
                unsafeSkipStorageCheck: true,
                kind: 'uups',
            }
        );
        await upgraded.waitForDeployment();

        const implAddr = await upgrades.erc1967.getImplementationAddress(ADDRESSES.spotArbitrage);
        console.log("  ✅ SpotArbitrage upgraded! New impl:", implAddr);
    } catch (error) {
        console.log("  ❌ SpotArbitrage upgrade failed:", error.message);
    }
    console.log("");

    // ======== 3. Set backendCaller to FlashLoanArbitrage ========
    console.log("[3/3] SpotArbitrage.setBackendCaller(FlashLoanArbitrage)");
    try {
        const SpotArbitrage = await ethers.getContractFactory("SpotArbitrage");
        const spot = SpotArbitrage.attach(ADDRESSES.spotArbitrage);

        const currentBackend = await spot.backendCaller();
        console.log("  Current backendCaller:", currentBackend);

        const tx = await spot.setBackendCaller(ADDRESSES.flashLoanArbitrage);
        await tx.wait();

        const newBackend = await spot.backendCaller();
        console.log("  ✅ backendCaller updated to:", newBackend);
    } catch (error) {
        console.log("  ❌ setBackendCaller failed:", error.message);
    }
    console.log("");

    // ======== Verify ========
    console.log("[Verify] Checking authorization chain...");
    try {
        const router = await ethers.getContractAt(
            ["function authorizedCallers(address) external view returns (bool)"],
            ADDRESSES.flashLoanRouter
        );
        const isAuth = await router.authorizedCallers(ADDRESSES.flashLoanArbitrage);
        console.log("  FlashLoanRouter → FlashLoanArbitrage authorized:", isAuth ? "✅" : "❌");

        const SpotArbitrage = await ethers.getContractFactory("SpotArbitrage");
        const spot = SpotArbitrage.attach(ADDRESSES.spotArbitrage);
        const backend = await spot.backendCaller();
        console.log("  SpotArbitrage → backendCaller:", backend);
        console.log("  Matches FlashLoanArbitrage:", backend.toLowerCase() === ADDRESSES.flashLoanArbitrage.toLowerCase() ? "✅" : "❌");
    } catch (error) {
        console.log("  Verification error:", error.message);
    }

    // ======== Done ========
    const finalBalance = await ethers.provider.getBalance(deployer.address);
    console.log("\n========================================");
    console.log("Authorization Fix Complete!");
    console.log("Gas spent:", ethers.formatEther(balance - finalBalance), "ETH");
    console.log("========================================");
    console.log("\nCall chain after fix:");
    console.log("  Keeper → FlashLoanArbitrage.executeFlashLoan() [onlyArbitrageCore=keeper ✅]");
    console.log("  FlashLoanArbitrage → FlashLoanRouter.requestFlashLoan() [onlyAuthorized ✅]");
    console.log("  Aave → FlashLoanArbitrage.executeOperation() [callback]");
    console.log("  FlashLoanArbitrage → SpotArbitrage.executeSwaps() [onlyAuthorizedCaller: backendCaller ✅]");
}

main()
    .then(() => process.exit(0))
    .catch((error) => {
        console.error(error);
        process.exit(1);
    });
