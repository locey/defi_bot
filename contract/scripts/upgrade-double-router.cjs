const hre = require("hardhat");

async function main() {
  const [deployer] = await hre.ethers.getSigners();
  console.log("Upgrading with:", deployer.address);

  // 当前代理地址
  const PROXY = "0xE075114d8C9142c9497eabc10b4CAaf2794c95dc";
  // Uniswap V3 Router on Arbitrum
  const V3_ROUTER = "0xE592427A0AEce92De3Edee1F18E0157C05861564";

  // 1. 部署新的实现合约
  console.log("\n1. Deploying new implementation...");
  const Factory = await hre.ethers.getContractFactory("DoubleRouterIntegration");
  const newImpl = await Factory.deploy();
  await newImpl.waitForDeployment();
  const newImplAddr = await newImpl.getAddress();
  console.log("   New impl:", newImplAddr);

  // 2. 通过代理升级
  console.log("\n2. Upgrading proxy...");
  const proxy = await hre.ethers.getContractAt(
    ["function upgradeToAndCall(address newImplementation, bytes memory data) external"],
    PROXY
  );
  const upgradeTx = await proxy.upgradeToAndCall(newImplAddr, "0x");
  await upgradeTx.wait();
  console.log("   Upgrade done");

  // 3. 配置 V3 Router
  console.log("\n3. Configuring V3 Router...");
  const doubleRouter = await hre.ethers.getContractAt(
    ["function setV3Router(address router, bool enabled, uint24 fee) external"],
    PROXY
  );

  // Uniswap V3 0.05% fee
  let tx = await doubleRouter.setV3Router(V3_ROUTER, true, 500);
  await tx.wait();
  console.log("   V3 Router (0.05%) configured");

  // 也注册 0.3% 和 1% 的 fee tier（用同一个 Router 地址，fee 在 swap 时指定）
  // 默认设为 3000 (0.3%)，因为这是最常见的
  tx = await doubleRouter.setV3Router(V3_ROUTER, true, 3000);
  await tx.wait();
  console.log("   V3 Router default fee set to 0.3%");

  console.log("\n========================================");
  console.log("  DoubleRouterIntegration 升级完成！");
  console.log("  现在支持 V2 + V3 混合路径");
  console.log("========================================");
}

main().catch(console.error);
