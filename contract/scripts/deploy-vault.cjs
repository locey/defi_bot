const hre = require("hardhat");
const fs = require("fs");
const path = require("path");

async function main() {
  console.log("🚀 Starting local deployment...\n");

  // 获取部署账户
  const [deployer] = await hre.ethers.getSigners();
  console.log(`📝 Deploying with account: ${deployer.address}\n`);

  // 1. 部署 MockERC20 (WETH)
  console.log("1️⃣  Deploying MockERC20 (WETH)...");
  const MockERC20 = await hre.ethers.getContractFactory("MockERC20");
  const weth = await MockERC20.deploy("Wrapped ETH", "WETH", 18);
  await weth.deployed();
  const wethAddress = weth.address;
  console.log(`   ✅ WETH deployed at: ${wethAddress}\n`);

  // 2. 部署 MockConfigManager
  console.log("2️⃣  Deploying MockConfigManager...");
  const MockConfigManager = await hre.ethers.getContractFactory("MockConfigManager");
  const configManager = await MockConfigManager.deploy();
  await configManager.deployed();
  const configManagerAddress = configManager.address;
  console.log(`   ✅ ConfigManager deployed at: ${configManagerAddress}\n`);

  // 3. 设置配置管理器的费用
  console.log("3️⃣  Setting ConfigManager fees...");
  await configManager.setDefaultFees(0, 10, 1000); // deposit: 0, withdraw: 0.01%, performance: 10%
  console.log(`   ✅ Fees set: deposit=0, withdraw=0.01%, performance=10%\n`);

  // 4. 部署 ArbitrageVault
  console.log("4️⃣  Deploying ArbitrageVault...");
  const ArbitrageVault = await hre.ethers.getContractFactory("ArbitrageVault");
  const vault = await ArbitrageVault.deploy(
    wethAddress,
    configManagerAddress,
    "Arbitrage WETH",
    "arbWETH"
  );
  await vault.deployed();
  const vaultAddress = vault.address;
  console.log(`   ✅ ArbitrageVault deployed at: ${vaultAddress}\n`);

  // 5. 给测试账户发送初始代币
  console.log("5️⃣  Minting test tokens for deployer...");
  const initialBalance = hre.ethers.utils.parseUnits("10000", 18); // 10000 WETH
  await weth.mint(deployer.address, initialBalance);
  console.log(`   ✅ Minted ${hre.ethers.utils.formatUnits(initialBalance, 18)} WETH\n`);

  // 6. 输出总结
  console.log("════════════════════════════════════════════");
  console.log("✨ Deployment Complete!");
  console.log("════════════════════════════════════════════\n");

  const deploymentInfo = {
    network: hre.network.name,
    timestamp: new Date().toISOString(),
    deployer: deployer.address,
    contracts: {
      weth: {
        name: "MockERC20 (WETH)",
        address: wethAddress,
        symbol: "WETH",
        decimals: 18,
      },
      configManager: {
        name: "MockConfigManager",
        address: configManagerAddress,
        depositFee: 0,
        withdrawFee: 10,
        performanceFee: 1000,
      },
      vault: {
        name: "ArbitrageVault",
        address: vaultAddress,
        symbol: "arbWETH",
        asset: wethAddress,
        configManager: configManagerAddress,
      },
    },
  };

  // 打印到控制台
  console.log(JSON.stringify(deploymentInfo, null, 2));

  // 7. 保存到文件供前端使用
  const outputDir = path.join(__dirname, "../deployments");
  if (!fs.existsSync(outputDir)) {
    fs.mkdirSync(outputDir, { recursive: true });
  }

  const outputFile = path.join(outputDir, "localhost.json");
  fs.writeFileSync(outputFile, JSON.stringify(deploymentInfo, null, 2));
  console.log(`\n📄 Deployment info saved to: ${outputFile}`);

  // 8. 同时保存为前端友好的格式
  const frontendConfig = {
    vault: vaultAddress,
    weth: wethAddress,
    configManager: configManagerAddress,
  };
  const frontendFile = path.join(outputDir, "contract-addresses.json");
  fs.writeFileSync(frontendFile, JSON.stringify(frontendConfig, null, 2));
  console.log(`📱 Frontend config saved to: ${frontendFile}\n`);

  console.log("💡 Tips for frontend integration:");
  console.log(`   1. Copy this vault address: ${vaultAddress}`);
  console.log(`   2. Copy this WETH address: ${wethAddress}`);
  console.log(`   3. Use contract ABIs from artifacts/contracts/\n`);
}

main()
  .then(() => process.exit(0))
  .catch((error) => {
    console.error(error);
    process.exit(1);
  });
