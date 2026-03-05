const hre = require("hardhat");
async function main() {
  const PROXY = "0xE075114d8C9142c9497eabc10b4CAaf2794c95dc";
  const V3_ROUTER = "0xE592427A0AEce92De3Edee1F18E0157C05861564";

  console.log("1. Deploying new implementation...");
  const F = await hre.ethers.getContractFactory("DoubleRouterIntegration");
  const impl = await F.deploy();
  await impl.waitForDeployment();
  console.log("   Impl:", await impl.getAddress());

  console.log("2. Upgrading proxy...");
  const proxy = await hre.ethers.getContractAt(
    ["function upgradeToAndCall(address,bytes) external"], PROXY);
  await (await proxy.upgradeToAndCall(await impl.getAddress(), "0x")).wait();
  console.log("   Done");

  console.log("3. Setting V3 Router...");
  const dr = await hre.ethers.getContractAt(
    ["function setV3Router(address,bool,uint24) external"], PROXY);
  await (await dr.setV3Router(V3_ROUTER, true, 3000)).wait();
  console.log("   V3 Router set (0.3% default)");

  console.log("\n✅ Upgrade complete! STF bug fixed.");
}
main().catch(console.error);
