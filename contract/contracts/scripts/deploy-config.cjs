const { ethers, upgrades, network } = require("hardhat");
const fs = require("fs");
const path = require("path");

async function main() {
  const [deployer] = await ethers.getSigners();

  console.log("正在部署 ConfigManage 代理...");
  const ConfigManage = await ethers.getContractFactory("ConfigManage");

  // 初始化参数
  const ZERO_ADDRESS = "0x0000000000000000000000000000000000000000";
  const args = [
    deployer.address, // lendingPool
    deployer.address, // uniswapV2Router
    deployer.address, // uniswapV3Router
    deployer.address, // sushiSwapRouter
    ZERO_ADDRESS // arbitrageVault (部署后再设置)
  ];

  const configManage = await upgrades.deployProxy(ConfigManage, args, {
    initializer: "initialize",
    kind: "uups",
  });

  await configManage.deployed();
  const proxyAddress = configManage.address;
  
  console.log("ConfigManage Proxy 部署于:", proxyAddress);

  // 保存地址
  const deploymentInfo = {
    configManager: proxyAddress
  };
  
  const deploymentsDir = path.join(__dirname, "../deployments");
  if (!fs.existsSync(deploymentsDir)) fs.mkdirSync(deploymentsDir);
  
  fs.writeFileSync(
    path.join(deploymentsDir, `${network.name}-config.json`),
    JSON.stringify(deploymentInfo, null, 2)
  );
}

main().catch((error) => {
  console.error(error);
  process.exit(1);
});
