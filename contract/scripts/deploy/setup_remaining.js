const hre = require("hardhat");

async function main() {
    const [deployer] = await hre.ethers.getSigners();
    console.log("Deployer:", deployer.address);

    const WETH = "0x82aF49447D8a07e3bd95BD0d56f35241523fBab1";
    const ADDRS = {
        arbitrageCore: "0x0D14428b4e297344C2C51D087934a3e3a9b822B9",
        spotArbitrage: "0xceCCf5D3f5ab1dAB5b13Fd5Cf7deeA9C3DBaad17",
        vault: "0xBE312B103f89489Df5bFf0A59d327c4C8B19d94F",
        doubleRouter: "0xE075114d8C9142c9497eabc10b4CAaf2794c95dc",
    };

    const ArbitrageCore = await hre.ethers.getContractFactory("ArbitrageCore");
    const core = ArbitrageCore.attach(ADDRS.arbitrageCore);

    const DoubleRouterIntegration = await hre.ethers.getContractFactory("DoubleRouterIntegration");
    const dr = DoubleRouterIntegration.attach(ADDRS.doubleRouter);

    // addVault already done (vault already exists)
    console.log("1. addVault - already done, skipping");

    console.log("2. Setting spotArbitrage on DoubleRouter...");
    tx = await dr.setSpotArbitrage(ADDRS.spotArbitrage);
    await tx.wait();
    console.log("   OK");

    console.log("All setup complete!");
}

main()
    .then(() => process.exit(0))
    .catch((error) => {
        console.error(error);
        process.exit(1);
    });
