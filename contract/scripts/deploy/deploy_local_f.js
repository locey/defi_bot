const { ethers, upgrades } = require("hardhat");

async function main() {
  const [deployer] = await ethers.getSigners();

  console.log("Deploying contracts with the account:", deployer.address);
  console.log("Account balance:", (await deployer.getBalance()).toString());

  // 1. 部署模拟代币
  console.log("\n1. Deploying Mock Tokens...");
  const MockERC20 = await ethers.getContractFactory("MockERC20");
  
  const usdc = await MockERC20.deploy("USD Coin", "USDC", ethers.parseUnits("10000000", 6), 6);
  await usdc.waitForDeployment();
  console.log("USDC Token deployed to:", usdc.target);

  const weth = await MockERC20.deploy("Wrapped Ether", "WETH", ethers.parseUnits("10000", 18), 18);
  await weth.waitForDeployment();
  console.log("WETH Token deployed to:", weth.target);

  // 2. 部署模拟借贷池
  console.log("\n2. Deploying Mock Lending Pool...");
  const MockLendingPool = await ethers.getContractFactory("MockLendingPool");
  const mockLendingPool = await MockLendingPool.deploy();
  await mockLendingPool.waitForDeployment();
  console.log("Mock Lending Pool deployed to:", mockLendingPool.target);

  // 3. 部署 ConfigManage 可升级合约
  console.log("\n3. Deploying ConfigManage...");
  const ConfigManage = await ethers.getContractFactory("ConfigManage");
  const configManage = await upgrades.deployProxy(
    ConfigManage,
    [
      mockLendingPool.target, // lendingPool
      "0x7a250d5630B4cF539739dF2C5dAcb4c659F2488D", // uniswapV2Router
      "0xE592427A0AEce92De3Edee1F18E0157C05861564", // uniswapV3Router
      "0xd9e1cE17f2641f24aE83637ab66a2cca9C227EF", // sushiSwapRouter
      "0x0000000000000000000000000000000000000000"  // arbitrageVault - 将在后面更新
    ],
    { initializer: "initialize" }
  );
  await configManage.waitForDeployment();
  console.log("ConfigManage deployed to:", configManage.target);

  // 4. 部署 ArbitrageVault 可升级合约
  console.log("\n4. Deploying ArbitrageVault...");
  const ArbitrageVault = await ethers.getContractFactory("ArbitrageVault");
  const arbitrageVault = await upgrades.deployProxy(
    ArbitrageVault,
    [usdc.target, weth.target, deployer.address], // underlyingAssets, owner
    { initializer: "initialize" }
  );
  await arbitrageVault.waitForDeployment();
  console.log("ArbitrageVault deployed to:", arbitrageVault.target);

  // 更新ConfigManage中的arbitrageVault地址
  await configManage.updateArbitrageVault(arbitrageVault.target);
  console.log("Updated ArbitrageVault address in ConfigManage");

  // 5. 部署 DEX Router 模拟
  console.log("\n5. Deploying Mock DEX Routers...");
  const MockRouter = await ethers.getContractFactory("MockRouter");
  const router1 = await MockRouter.deploy(); // Uniswap V2模拟
  await router1.waitForDeployment();
  console.log("Mock Router 1 (Uniswap V2) deployed to:", router1.target);

  const router2 = await MockRouter.deploy(); // Sushiswap模拟
  await router2.waitForDeployment();
  console.log("Mock Router 2 (Sushiswap) deployed to:", router2.target);

  // 更新ConfigManage中的DEX路由器地址
  await configManage.updateUniswapV2Router(router1.target);
  await configManage.updateSushiSwapRouter(router2.target);
  console.log("Updated DEX router addresses in ConfigManage");

  // 6. 部署 SpotArbitrage
  console.log("\n6. Deploying SpotArbitrage...");
  const SpotArbitrage = await ethers.getContractFactory("SpotArbitrage");
  const spotArbitrage = await SpotArbitrage.deploy(
    arbitrageVault.target,
    configManage.target
  );
  await spotArbitrage.waitForDeployment();
  console.log("SpotArbitrage deployed to:", spotArbitrage.target);

  // 7. 部署 FlashLoanRouter
  console.log("\n7. Deploying FlashLoanRouter...");
  const FlashLoanRouter = await ethers.getContractFactory("FlashLoanRouter");
  const flashLoanRouter = await FlashLoanRouter.deploy(configManage.target);
  await flashLoanRouter.waitForDeployment();
  console.log("FlashLoanRouter deployed to:", flashLoanRouter.target);

  // 8. 部署 ArbitrageCore 可升级合约
  console.log("\n8. Deploying ArbitrageCore...");
  const ArbitrageCore = await ethers.getContractFactory("ArbitrageCore");
  const arbitrageCore = await upgrades.deployProxy(
    ArbitrageCore,
    [
      spotArbitrage.target,
      flashLoanRouter.target,
      arbitrageVault.target,
      configManage.target,
      deployer.address // platform wallet
    ],
    { initializer: "initialize" }
  );
  await arbitrageCore.waitForDeployment();
  console.log("ArbitrageCore deployed to:", arbitrageCore.target);

  // 9. 部署 FlashLoanArbitrage
  console.log("\n9. Deploying FlashLoanArbitrage...");
  const FlashLoanArbitrage = await ethers.getContractFactory("FlashLoanArbitrage");
  const flashLoanArbitrage = await FlashLoanArbitrage.deploy(
    flashLoanRouter.target,
    spotArbitrage.target,
    arbitrageCore.target,
    deployer.address, // platform wallet
    configManage.target
  );
  await flashLoanArbitrage.waitForDeployment();
  console.log("FlashLoanArbitrage deployed to:", flashLoanArbitrage.target);

  // 10. 设置合约间的关系
  console.log("\n10. Setting up contract relationships...");
  
  // 设置ArbitrageCore中的FlashLoanArbitrage地址
  await arbitrageCore.setFlashLoanArbitrage(flashLoanArbitrage.target);
  console.log("Set FlashLoanArbitrage address in ArbitrageCore");

  // 设置ConfigManage中的ArbitrageCore地址
  await configManage.updateArbitrageCore(arbitrageCore.target);
  console.log("Updated ArbitrageCore address in ConfigManage");

  // 铸币并授权给相关合约
  console.log("\n11. Minting tokens and setting allowances...");
  
  // 铸造一些USDC代币给部署者
  await usdc.mint(deployer.address, ethers.parseUnits("1000000", 6));
  console.log("Minted USDC to deployer");

  // 授权给ArbitrageVault
  await usdc.approve(arbitrageVault.target, ethers.MaxUint256);
  await weth.approve(arbitrageVault.target, ethers.MaxUint256);
  console.log("Approved tokens for ArbitrageVault");

  // 授权给FlashLoanRouter
  await usdc.approve(flashLoanRouter.target, ethers.MaxUint256);
  await weth.approve(flashLoanRouter.target, ethers.MaxUint256);
  console.log("Approved tokens for FlashLoanRouter");

  // 12. 配置MockRouter的价格
  console.log("\n12. Configuring MockRouter prices...");
  await router1.setPrice(usdc.target, weth.target, ethers.parseEther("0.00025")); // 1 WETH = 4000 USDC
  await router1.setPrice(weth.target, usdc.target, ethers.parseEther("4000"));
  await router2.setPrice(usdc.target, weth.target, ethers.parseEther("0.000245")); // 1 WETH = 4081 USDC (稍高的价格以产生套利机会)
  await router2.setPrice(weth.target, usdc.target, ethers.parseEther("4081.63"));
  console.log("Set prices in MockRouters for arbitrage opportunity");

  // 13. 保存部署地址到文件
  console.log("\n13. Saving deployment addresses...");
  const fs = require("fs");
  const deploymentData = {
    tokens: {
      USDC: usdc.target,
      WETH: weth.target,
    },
    contracts: {
      mockLendingPool: mockLendingPool.target,
      configManage: configManage.target,
      arbitrageVault: arbitrageVault.target,
      spotArbitrage: spotArbitrage.target,
      flashLoanRouter: flashLoanRouter.target,
      arbitrageCore: arbitrageCore.target,
      flashLoanArbitrage: flashLoanArbitrage.target,
      routers: {
        router1: router1.target, // Uniswap V2模拟
        router2: router2.target, // Sushiswap模拟
      }
    },
    deployer: deployer.address,
    timestamp: Date.now(),
  };

  fs.writeFileSync("./deployments/localhost.json", JSON.stringify(deploymentData, null, 2));
  console.log("Deployment addresses saved to ./deployments/localhost.json");

  console.log("\nAll contracts deployed successfully!");
}

main()
  .then(() => process.exit(0))
  .catch((error) => {
    console.error(error);
    process.exit(1);
  });