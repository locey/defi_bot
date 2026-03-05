const hre = require("hardhat");
async function main() {
  const PROXY = "0xE075114d8C9142c9497eabc10b4CAaf2794c95dc";
  const dr = await hre.ethers.getContractAt(
    ["function slippageTolerance() view returns(uint256)"], PROXY);
  const val = await dr.slippageTolerance();
  console.log("DoubleRouterIntegration slippageTolerance:", val.toString(), "bps");
}
main().catch(console.error);
