const hre = require("hardhat");

async function main() {
  const [deployer] = await hre.ethers.getSigners();
  console.log("Deploying all missing vaults with:", deployer.address);
  const bal = await hre.ethers.provider.getBalance(deployer.address);
  console.log("ETH balance:", hre.ethers.formatEther(bal));

  const CONFIG_MANAGER = "0x8dD68621209D31D2c3202F894B956DDEdF893697";
  const ARBITRAGE_CORE = "0x0D14428b4e297344C2C51D087934a3e3a9b822B9";

  const tokens = [
    { symbol: "USDCe", address: "0xFF970A61A04b1cA14834A43f5dE4533eBDDB5CC8" },
    { symbol: "USDT",  address: "0xFd086bC7CD5C481DCC9C85ebE478A1C0b69FCbb9" },
    { symbol: "DAI",   address: "0xDA10009cBd5D07dd0CeCc66161FC93D7c9000da1" },
    { symbol: "WBTC",  address: "0x2f2a2543B76A4166549F7aaB2e75Bef0aefC5B0f" },
    { symbol: "ARB",   address: "0x912CE59144191C1204E64559FE8253a0e49E6548" },
  ];

  const core = await hre.ethers.getContractAt(
    [
      "function addVault(address,address) external",
      "function vaults(address) view returns (address)"
    ],
    ARBITRAGE_CORE
  );

  for (const token of tokens) {
    // 检查是否已注册
    const existing = await core.vaults(token.address);
    if (existing !== "0x0000000000000000000000000000000000000000") {
      console.log(`  ${token.symbol}: already registered at ${existing}`);
      continue;
    }

    console.log(`\n  Deploying ${token.symbol} Vault...`);
    const Vault = await hre.ethers.getContractFactory("ArbitrageVault");
    const vault = await Vault.deploy(token.address, CONFIG_MANAGER, `Arbitrage ${token.symbol}`, `arb${token.symbol}`);
    await vault.waitForDeployment();
    const addr = await vault.getAddress();
    console.log(`    Vault: ${addr}`);

    await (await vault.setArbitrageCore(ARBITRAGE_CORE)).wait();
    await (await core.addVault(token.address, addr)).wait();
    console.log(`    Registered ✓`);
  }

  console.log("\n✅ All vaults deployed!");
}
main().catch(console.error);
