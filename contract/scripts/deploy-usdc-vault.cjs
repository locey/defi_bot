// scripts/deploy-usdc-vault.cjs
// 在 Arbitrum 主网上为 USDC 部署 ArbitrageVault 并注册到 ArbitrageCore
const hre = require("hardhat");

async function main() {
  const [deployer] = await hre.ethers.getSigners();
  console.log("Deploying with:", deployer.address);

  const balance = await hre.ethers.provider.getBalance(deployer.address);
  console.log("ETH balance:", hre.ethers.formatEther(balance));

  // Arbitrum 上的合约地址
  const USDC = "0xaf88d065e77c8cC2239327C5EDb3A432268e5831";
  const CONFIG_MANAGER = "0x8dD68621209D31D2c3202F894B956DDEdF893697";
  const ARBITRAGE_CORE = "0x0D14428b4e297344C2C51D087934a3e3a9b822B9";

  // 1. 部署 USDC Vault
  console.log("\n1. Deploying USDC Vault...");
  const ArbitrageVault = await hre.ethers.getContractFactory("ArbitrageVault");
  const vault = await ArbitrageVault.deploy(
    USDC,
    CONFIG_MANAGER,
    "Arbitrage USDC",
    "arbUSDC"
  );
  await vault.waitForDeployment();
  const vaultAddr = await vault.getAddress();
  console.log("   USDC Vault deployed:", vaultAddr);

  // 2. 设置 ArbitrageCore 为 Vault 的 arbitrageCore
  console.log("\n2. Setting arbitrageCore on Vault...");
  const setTx = await vault.setArbitrageCore(ARBITRAGE_CORE);
  await setTx.wait();
  console.log("   Done");

  // 3. 在 ArbitrageCore 中注册 Vault
  console.log("\n3. Registering Vault in ArbitrageCore...");
  const core = await hre.ethers.getContractAt(
    ["function addVault(address asset, address vault) external"],
    ARBITRAGE_CORE
  );
  const addTx = await core.addVault(USDC, vaultAddr);
  await addTx.wait();
  console.log("   Done");

  console.log("\n========================================");
  console.log("  USDC Vault 部署完成！");
  console.log("  Vault 地址:", vaultAddr);
  console.log("  Asset: USDC (" + USDC + ")");
  console.log("========================================");
  console.log("\n下一步: 存入 USDC 到 Vault 进行测试");
}

main().catch(console.error);
