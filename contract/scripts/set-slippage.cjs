const hre = require("hardhat");
async function main() {
  const CM = "0x8dD68621209D31D2c3202F894B956DDEdF893697";
  const config = await hre.ethers.getContractAt(
    ["function setSlipageTolerance(uint256) external","function slippageTolerance() view returns(uint256)"], CM);
  const current = await config.slippageTolerance();
  console.log("Current slippage:", current.toString(), "bps");
  console.log("Setting to 1000 bps (10%)...");
  await (await config.setSlipageTolerance(1000)).wait();
  const newVal = await config.slippageTolerance();
  console.log("New slippage:", newVal.toString(), "bps ✓");
}
main().catch(console.error);
