const hre = require("hardhat");
async function main() {
  const RDNT = "0x3082CC23568eA640225c2467653dB90e9250AaA0";
  const VAULT = "0x9620eEfD5E464BeEc9Ada47E62a0772294F7009B";
  const CORE = "0x0D14428b4e297344C2C51D087934a3e3a9b822B9";
  
  const core = await hre.ethers.getContractAt(
    ["function addVault(address,address) external"], CORE);
  console.log("Registering RDNT Vault...");
  const tx = await core.addVault(RDNT, VAULT);
  await tx.wait();
  console.log("Done! TX:", tx.hash);
}
main().catch(console.error);
