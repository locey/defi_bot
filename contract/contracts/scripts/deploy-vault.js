const { ethers, upgrades, network } = require("hardhat");
const fs = require("fs");
const path = require("path");

async function main() {
  const [deployer, user1] = await ethers.getSigners();
  console.log("===================================================");
  console.log("开始部署金库合约...");
  console.log("当前网络:", network.name);
  console.log("部署账户:", deployer.address);
  console.log("===================================================\n");

  let assetAddress;
  let configManagerAddress;
  let isLocal = network.name === "hardhat" || network.name === "localhost";

  // 1. 处理依赖合约
  if (isLocal) {
    console.log("检测到本地网络，部署 Mock 合约...");
    // 部署 Mock WETH
    const MockERC20 = await ethers.getContractFactory("MockERC20");
    const weth = await MockERC20.deploy("Wrapped ETH", "WETH", 18);
    await weth.waitForDeployment();
    assetAddress = await weth.getAddress();
    console.log("Mock WETH 部署于:", assetAddress);

    // 部署 Mock ConfigManager
    const MockConfig = await ethers.getContractFactory("MockConfigManager");
    const configManager = await MockConfig.deploy();
    await configManager.waitForDeployment();
    configManagerAddress = await configManager.getAddress();
    console.log("Mock ConfigManager 部署于:", configManagerAddress);
    
    // 给测试账户铸造代币
    await weth.mint(user1.address, ethers.parseUnits("10000", 18));
    console.log("已为测试账户 user1 铸造 10000 WETH");
  } else {
    // 这里填入你测试网或主网已有的地址
    assetAddress = "0x..."; // 比如测试网 WETH 地址
    configManagerAddress = "0x..."; // 你之前部署的 ConfigManage 代理地址
  }

  // 2. 部署 ArbitrageVault (普通 UUPS 合约部署用 deploy，非 Proxy)
  // 注意：如果你的 Vault 也是可升级的，请告知我，目前看你给的代码是构造函数初始化
  console.log("正在部署 ArbitrageVault...");
  const ArbitrageVault = await ethers.getContractFactory("ArbitrageVault");
  const vault = await ArbitrageVault.deploy(
    assetAddress,
    configManagerAddress,
    "Arbitrage WETH",
    "arbWETH"
  );
  await vault.waitForDeployment();
  const vaultAddress = await vault.getAddress();
  console.log("ArbitrageVault 部署成功:", vaultAddress);

  // 3. 初始配置 (可选)
  console.log("执行初始配置...");
  // 暂时将 core 设置为部署者，方便前端手动操作测试
  await vault.setArbitrageCore(deployer.address);
  console.log("ArbitrageCore 暂时设置为部署者，用于手动测试接口");

  // 4. 保存部署信息供前端联调
  const deploymentInfo = {
    network: network.name,
    chainId: network.config.chainId,
    contracts: {
      vault: vaultAddress,
      asset: assetAddress,
      configManager: configManagerAddress
    },
    vaultInfo: {
      name: "Arbitrage WETH",
      symbol: "arbWETH"
    }
  };

  const deploymentsDir = path.join(__dirname, "../deployments");
  if (!fs.existsSync(deploymentsDir)) {
    fs.mkdirSync(deploymentsDir);
  }

  fs.writeFileSync(
    path.join(deploymentsDir, `${network.name}-frontend.json`),
    JSON.stringify(deploymentInfo, null, 2)
  );
  console.log(`\n✅ 部署信息已保存至: deployments/${network.name}-frontend.json`);
  console.log("前端开发者可以使用这些地址进行联调。");
}

main()
  .then(() => process.exit(0))
  .catch((error) => {
    console.error(error);
    process.exit(1);
  });