const hre = require("hardhat");

async function main() {
  // 获取部署者账户
  const [deployer] = await hre.ethers.getSigners();
  console.log("🚀 部署 FlashLoanArbitrage 合约...");
  console.log("✅ 部署者地址:", deployer.address);

  // 获取已部署的合约地址
  const fs = require('fs');
  const deploymentFile = './deployments/localhost.json';
  let deployedAddresses = {};
  
  if (fs.existsSync(deploymentFile)) {
    deployedAddresses = JSON.parse(fs.readFileSync(deploymentFile, 'utf8'));
  }

  console.log("📋 已部署的合约地址:");
  console.log("- FlashLoanRouter:", deployedAddresses.flashLoanRouter);
  console.log("- SpotArbitrage:", deployedAddresses.spotArbitrage);
  console.log("- ArbitrageCore:", deployedAddresses.arbitrageCore);
  console.log("- MockUSDC:", deployedAddresses.mockUSDC);
  console.log("- MockWETH:", deployedAddresses.mockWETH);

  // 部署 FlashLoanArbitrage 合约
  const FlashLoanArbitrage = await hre.ethers.getContractFactory("FlashLoanArbitrage");
  
  // 使用已部署的合约地址作为构造函数参数
  const flashLoanArbitrage = await FlashLoanArbitrage.deploy(
    deployedAddresses.flashLoanRouter,    // _flashLoanRouter
    deployedAddresses.spotArbitrage,      // _spotArbitrage
    deployedAddresses.arbitrageCore,      // _arbitrageCore
    deployer.address,                     // _platFormWallet (平台钱包使用部署者地址)
    deployedAddresses.configManager || deployedAddresses.arbitrageCore // _configManager (如果不存在则使用arbitrageCore作为备选)
  );

  console.log("⏳ 等待部署完成...");
  await flashLoanArbitrage.waitForDeployment();
  
  const flashLoanArbitrageAddress = await flashLoanArbitrage.getAddress();
  console.log("✅ FlashLoanArbitrage 部署成功!");
  console.log("🔗 合约地址:", flashLoanArbitrageAddress);

  // 将新部署的合约地址添加到部署文件中
  deployedAddresses.flashLoanArbitrage = flashLoanArbitrageAddress;
  
  // 保存更新后的部署地址
  fs.writeFileSync(deploymentFile, JSON.stringify(deployedAddresses, null, 2));
  console.log("💾 已更新部署地址文件:", deploymentFile);

  // 验证合约是否正确初始化
  try {
    console.log("\n🔍 验证合约初始化...");
    const routerAddress = await flashLoanArbitrage.flashLoanRouter();
    const spotAddress = await flashLoanArbitrage.spotArbitrage();
    const coreAddress = await flashLoanArbitrage.arbitrageCore();
    
    console.log("✅ 初始化验证:");
    console.log(`   - FlashLoanRouter: ${routerAddress} (${routerAddress === deployedAddresses.flashLoanRouter ? '✓' : '✗'})`);
    console.log(`   - SpotArbitrage: ${spotAddress} (${spotAddress === deployedAddresses.spotArbitrage ? '✓' : '✗'})`);
    console.log(`   - ArbitrageCore: ${coreAddress} (${coreAddress === deployedAddresses.arbitrageCore ? '✓' : '✗'})`);
  } catch (error) {
    console.log("⚠️  验证过程中出现问题:", error.message);
  }

  console.log("\n🎉 部署完成!");
}

main()
  .then(() => process.exit(0))
  .catch((error) => {
    console.error("❌ 部署失败:", error);
    process.exit(1);
  });