const hre = require("hardhat");
async function main() {
  const CM = "0x8dD68621209D31D2c3202F894B956DDEdF893697";
  const CORE = "0x0D14428b4e297344C2C51D087934a3e3a9b822B9";
  const tokens = [
    ["USDT","0xFd086bC7CD5C481DCC9C85ebE478A1C0b69FCbb9"],
    ["DAI","0xDA10009cBd5D07dd0CeCc66161FC93D7c9000da1"],
    ["ARB","0x912CE59144191C1204E64559FE8253a0e49E6548"],
  ];
  const core = await hre.ethers.getContractAt(
    ["function addVault(address,address) external","function vaults(address) view returns(address)"], CORE);
  for (const [sym, addr] of tokens) {
    const existing = await core.vaults(addr);
    if (existing !== "0x0000000000000000000000000000000000000000") {
      console.log(`${sym}: already at ${existing}`); continue;
    }
    console.log(`Deploying ${sym} Vault...`);
    const V = await hre.ethers.getContractFactory("ArbitrageVault");
    const v = await V.deploy(addr, CM, `Arbitrage ${sym}`, `arb${sym}`);
    await v.waitForDeployment();
    const va = await v.getAddress();
    console.log(`  Vault: ${va}`);
    await (await v.setArbitrageCore(CORE)).wait();
    console.log(`  setArbitrageCore done`);
    try {
      await (await core.addVault(addr, va)).wait();
      console.log(`  addVault done ✓`);
    } catch(e) { console.log(`  addVault failed: ${e.message.slice(0,80)}`); }
  }
  console.log("\nDone!");
}
main().catch(console.error);
