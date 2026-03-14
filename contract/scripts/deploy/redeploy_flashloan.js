/**
 * Redeploy Flash Loan contracts — Arbitrum One
 *
 * Root cause: Old FlashLoanRouter checked IERC20(asset).balanceOf(Pool) but
 * Aave V3 holds tokens in aTokens, not the Pool — so balance always = 0 → revert.
 *
 * This script:
 * 1. Deploys new FlashLoanRouter (removed broken balance check)
 * 2. Authorizes FlashLoanArbitrage as caller on new router
 * 3. Deploys new FlashLoanArbitrage with new router
 * 4. Sets SpotArbitrage.backendCaller = new FlashLoanArbitrage
 *
 * Usage:
 *   npx hardhat run scripts/deploy/redeploy_flashloan.js --network arbitrumOne
 */

const { ethers } = require("hardhat");
const fs = require("fs");

const EXISTING = {
    configManager: "0x8dD68621209D31D2c3202F894B956DDEdF893697",
    spotArbitrage: "0xceCCf5D3f5ab1dAB5b13Fd5Cf7deeA9C3DBaad17",
};

async function main() {
    const [deployer] = await ethers.getSigners();
    console.log("========================================");
    console.log("Redeploy Flash Loan — Arbitrum One");
    console.log("========================================");
    console.log("Deployer:", deployer.address);

    const balance = await ethers.provider.getBalance(deployer.address);
    console.log("Balance:", ethers.formatEther(balance), "ETH\n");

    // ======== 1. Deploy new FlashLoanRouter ========
    console.log("[1/4] Deploying FlashLoanRouter (fixed: removed broken balance check)...");
    const FlashLoanRouter = await ethers.getContractFactory("FlashLoanRouter");
    const newRouter = await FlashLoanRouter.deploy(EXISTING.configManager);
    await newRouter.waitForDeployment();
    const newRouterAddr = newRouter.target;
    console.log("  ✅ FlashLoanRouter:", newRouterAddr);

    // Verify lending pool
    const lendingPool = await newRouter.getLendingPool(0); // Aave_V2 enum
    console.log("  LendingPool:", lendingPool);

    // ======== 2. Deploy new FlashLoanArbitrage ========
    console.log("\n[2/4] Deploying FlashLoanArbitrage...");
    const PLATFORM_WALLET = deployer.address;
    const ARBITRAGE_CORE = deployer.address; // keeper = arbitrageCore

    const FlashLoanArbitrage = await ethers.getContractFactory("FlashLoanArbitrage");
    const newFLA = await FlashLoanArbitrage.deploy(
        newRouterAddr,              // _flashLoanRouter (new one)
        EXISTING.spotArbitrage,     // _spotArbitrage
        ARBITRAGE_CORE,             // _arbitrageCore = keeper
        PLATFORM_WALLET,            // _platFormWallet
        EXISTING.configManager      // _configManager
    );
    await newFLA.waitForDeployment();
    const newFLAAddr = newFLA.target;
    console.log("  ✅ FlashLoanArbitrage:", newFLAAddr);

    // ======== 3. Authorize FlashLoanArbitrage on FlashLoanRouter ========
    console.log("\n[3/4] Authorizing FlashLoanArbitrage on FlashLoanRouter...");
    const authTx = await newRouter.setAuthorizedCaller(newFLAAddr, true);
    await authTx.wait();
    const isAuth = await newRouter.authorizedCallers(newFLAAddr);
    console.log("  ✅ FlashLoanArbitrage authorized:", isAuth);

    // ======== 4. Set SpotArbitrage.backendCaller = new FlashLoanArbitrage ========
    console.log("\n[4/4] Setting SpotArbitrage.backendCaller = FlashLoanArbitrage...");
    const SpotArbitrage = await ethers.getContractFactory("SpotArbitrage");
    const spot = SpotArbitrage.attach(EXISTING.spotArbitrage);
    const backendTx = await spot.setBackendCaller(newFLAAddr);
    await backendTx.wait();
    const newBackend = await spot.backendCaller();
    console.log("  ✅ backendCaller:", newBackend);

    // ======== Verify the full chain ========
    console.log("\n[Verify] Checking authorization chain...");
    const flaCore = await newFLA.arbitrageCore();
    const flaPaused = await newFLA.paused();
    const flaRouter = await newFLA.flashLoanRouter();
    const flaSpot = await newFLA.spotArbitrage();
    console.log("  FlashLoanArbitrage.arbitrageCore:", flaCore, flaCore === deployer.address ? "✅" : "❌");
    console.log("  FlashLoanArbitrage.paused:", flaPaused, !flaPaused ? "✅" : "❌");
    console.log("  FlashLoanArbitrage.flashLoanRouter:", flaRouter, flaRouter === newRouterAddr ? "✅" : "❌");
    console.log("  FlashLoanArbitrage.spotArbitrage:", flaSpot, flaSpot === EXISTING.spotArbitrage ? "✅" : "❌");
    console.log("  FlashLoanRouter.authorized(FLA):", isAuth ? "✅" : "❌");
    console.log("  SpotArbitrage.backendCaller:", newBackend === newFLAAddr ? "✅" : "❌");

    // ======== Update deployment file ========
    const addressesPath = "./deployments/arbitrum-addresses.json";
    let deploymentInfo;
    try {
        deploymentInfo = JSON.parse(fs.readFileSync(addressesPath, "utf8"));
    } catch {
        deploymentInfo = { network: "arbitrum", chainId: 42161, addresses: {} };
    }
    deploymentInfo.addresses.flashLoanRouter = newRouterAddr;
    deploymentInfo.addresses.flashLoanArbitrage = newFLAAddr;
    deploymentInfo.flashLoanRedeployTimestamp = new Date().toISOString();
    fs.writeFileSync(addressesPath, JSON.stringify(deploymentInfo, null, 2));

    // ======== Done ========
    const finalBalance = await ethers.provider.getBalance(deployer.address);
    console.log("\n========================================");
    console.log("Redeployment Complete!");
    console.log("========================================");
    console.log("FlashLoanRouter:", newRouterAddr);
    console.log("FlashLoanArbitrage:", newFLAAddr);
    console.log("Gas spent:", ethers.formatEther(balance - finalBalance), "ETH");
    console.log("Remaining:", ethers.formatEther(finalBalance), "ETH");
    console.log("\n⚠️  Update backend config:");
    console.log(`  flash_loan_arbitrage: "${newFLAAddr}"`);
    console.log("\nCall chain:");
    console.log("  Keeper → FLA.executeFlashLoan() [onlyArbitrageCore=keeper]");
    console.log("  FLA → Router.requestFlashLoan() [onlyAuthorized=FLA]");
    console.log("  Router → AavePool.flashLoanSimple()");
    console.log("  AavePool → FLA.executeOperation() [caller=LendingPool]");
    console.log("  FLA → SpotArbitrage.executeSwaps() [onlyAuthorizedCaller: backendCaller=FLA]");
}

main()
    .then(() => process.exit(0))
    .catch((error) => {
        console.error(error);
        process.exit(1);
    });
