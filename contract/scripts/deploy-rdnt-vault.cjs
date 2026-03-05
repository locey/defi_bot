const hre = require("hardhat");
async function main() {
  const [deployer] = await hre.ethers.getSigners();
  console.log("Deploying RDNT Vault with:", deployer.address);

  const RDNT = "0x3082CC23568eA640225c2467653dB90e9250AaA0";
  const CONFIG_MANAGER = "0x8dD68621209D31D2c3202F894B956DDEdF893697";
  const ARBITRAGE_CORE = "0x0D14428b4e297344C2C51D087934a3e3a9b822B9";

  console.log("\n1. Deploying RDNT Vault...");
  const Vault = await hre.ethers.getContractFactory("ArbitrageVault");
  const vault = await Vault.deploy(RDNT, CONFIG_MANAGER, "Arbitrage RDNT", "arbRDNT");
  await vault.waitForDeployment();
  const addr = await vault.getAddress();
  console.log("   Vault:", addr);

  console.log("2. Setting arbitrageCore...");
  await (await vault.setArbitrageCore(ARBITRAGE_CORE)).wait();

  console.log("3. Registering in ArbitrageCore...");
  const core = await hre.ethers.getContractAt(
    ["function addVault(address,address) external"], ARBITRAGE_CORE);
  await (await core.addVault(RDNT, addr)).wait();

  console.log("\n  RDNT Vault deployed:", addr);
}
main().catch(console.error);
