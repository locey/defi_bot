const fs = require("fs");
const { ethers } = require("hardhat");

async function main() {
  console.log("Loading deployment addresses...");
  const deploymentData = JSON.parse(fs.readFileSync("./deployments/localhost.json", "utf8"));

  // 获取已部署的合约实例 - 修复地址获取方式
  const configManage = await ethers.getContractAt("ConfigManage", deploymentData.configManage);
  const arbitrageVault = await ethers.getContractAt("ArbitrageVault", deploymentData.arbitrageVault);
  const spotArbitrage = await ethers.getContractAt("SpotArbitrage", deploymentData.spotArbitrage);
  const flashLoanRouter = await ethers.getContractAt("FlashLoanRouter", deploymentData.flashLoanRouter);
  const arbitrageCore = await ethers.getContractAt("ArbitrageCore", deploymentData.arbitrageCore);
  const flashLoanArbitrage = await ethers.getContractAt("FlashLoanArbitrage", deploymentData.flashLoanArbitrage);
  const usdc = await ethers.getContractAt("MockERC20", deploymentData.mockUSDC);
  const weth = await ethers.getContractAt("MockERC20", deploymentData.mockWETH);
  const mockRouter1 = await ethers.getContractAt("MockRouter", deploymentData.mockRouter1);
  const mockRouter2 = await ethers.getContractAt("MockRouter", deploymentData.mockRouter2);

  console.log("All contract instances loaded");

  // 获取部署者账户
  const [deployer] = await ethers.getSigners();

  console.log("\nMinting tokens for testing...");
  // 铸造一些代币用于测试
  await usdc.mint(deployer.address, ethers.parseUnits("100000", 6)); // 100,000 USDC
  await weth.mint(deployer.address, ethers.parseUnits("100", 18)); // 100 WETH
  console.log("Minted 100000.0 USDC to test account");
  console.log("Minted 100.0 WETH to test account");

  // 检查初始余额
  const initialUsdcBalance = await usdc.balanceOf(deployer.address);
  const initialWethBalance = await weth.balanceOf(deployer.address);
  console.log(`Initial USDC balance: ${ethers.formatUnits(initialUsdcBalance, 6)} USDC`);
  console.log(`Initial WETH balance: ${ethers.formatUnits(initialWethBalance, 18)} WETH`);

  console.log("\nSetting up arbitrage opportunity...");
  // 设置套利机会 - 在不同的DEX之间创造价格差异
  // Router1: 1 USDC = 0.00025 WETH (即 1 WETH = 4000 USDC)
  // Router2: 1 USDC = 0.000245 WETH (即 1 WETH = 4081 USDC)
  // 这样可以通过 USDC -> WETH -> USDC 的路径套利
  // Router1: USDC -> WETH 使用 priceTokenA, WETH -> USDC 使用 priceTokenB
  await mockRouter1.setPrice(ethers.parseUnits("1", 6), ethers.parseUnits("0.00025", 18));
  console.log("Set MockRouter1 price: 1 USDC = 0.00025 WETH");

  // Router2: 1 USDC = 0.000245 WETH (即 priceTokenA=1 USDC, priceTokenB=0.000245 WETH)
  await mockRouter2.setPrice(ethers.parseUnits("1", 6), ethers.parseUnits("0.000245", 18));
  console.log("Set MockRouter2 price: 1 USDC = 0.000245 WETH");
  // 验证交换路径：USDC -> WETH -> USDC
  // 第一步：在Router1上 USDC -> WETH
  const usdcAmount = ethers.parseUnits("500", 6);
  const expectedWeth = (usdcAmount * ethers.parseUnits("0.00025", 18)) / ethers.parseUnits("1", 6);
  console.log(`Expected WETH from Router1: ${ethers.formatUnits(expectedWeth, 18)} WETH`);

  // 第二步：在Router2上 WETH -> USDC
  const expectedUsdcBack = (expectedWeth * ethers.parseUnits("4081", 6)) / ethers.parseUnits("1", 18);
  console.log(`Expected USDC from Router2: ${ethers.formatUnits(expectedUsdcBack, 6)} USDC`);
  console.log(`Expected profit: ${ethers.formatUnits(expectedUsdcBack - usdcAmount, 6)} USDC`);

  // 🚨 关键修复：给MockRouter充值代币
  console.log("\n=== Funding routers with tokens ===");
  await usdc.mint(mockRouter1.target, ethers.parseUnits("10000", 6)); // 给Router1充值USDC
  await weth.mint(mockRouter1.target, ethers.parseUnits("10", 18));   // 给Router1充值WETH
  await usdc.mint(mockRouter2.target, ethers.parseUnits("10000", 6)); // 给Router2充值USDC
  await weth.mint(mockRouter2.target, ethers.parseUnits("10", 18));   // 给Router2充值WETH
  console.log("✅ Funded both routers with USDC and WETH");

  // 🚨 关键修复：给MockLendingPool充值USDC用于闪电贷
  console.log("\n=== Funding lending pool ===");
  const lendingPoolAddress = await flashLoanRouter.platFormConfigs(0); // 获取Aave_V2池地址
  await usdc.mint(lendingPoolAddress.lendingPool, ethers.parseUnits("100000", 6)); // 给池子充值USDC
  console.log("✅ Funded lending pool with USDC for flash loans");


  // 配置套利参数
  const lendingPlatform = 0; // 使用Aave V2平台
  const asset = usdc.target; // 借贷资产
  const tokenOut = weth.target; // 输出代币
  const amountIn = ethers.parseUnits("500", 6); // 借500 USDC
  const expectProfit = ethers.parseUnits("1", 6); // 期望利润 1 USDC
  const minProfit = ethers.parseUnits("0.5", 6); // 最小利润 0.5 USDC
  const dexes = [mockRouter1.target, mockRouter2.target]; // DEX地址数组
  const swapPath = [usdc.target, weth.target, usdc.target]; // 交易路径：USDC -> WETH -> USDC

  console.log("\nArbitrage parameters:");
  console.log("- Lending Platform:", lendingPlatform);
  console.log("- Asset:", asset);
  console.log("- Token Out:", tokenOut);
  console.log("- Amount In:", ethers.formatUnits(amountIn, 6), "USDC");
  console.log("- Expect Profit:", ethers.formatUnits(expectProfit, 6), "USDC");
  console.log("- Min Profit:", ethers.formatUnits(minProfit, 6), "USDC");
  console.log("- DEXes:", dexes);
  console.log("- Swap Path:", swapPath);

  console.log("\nTesting arbitrage execution through ArbitrageCore...");

  // 在配置套利参数之后，执行套利之前添加权限检查
  console.log("\n=== Checking permissions ===");
  console.log("Deployer address:", deployer.address);

  try {
    const currentBackCaller = await arbitrageCore.backCaller();
    const contractOwner = await arbitrageCore.owner();
    
    console.log("Current backCaller:", currentBackCaller);
    console.log("Contract owner:", contractOwner);
    
    const isDeployerBackCaller = deployer.address.toLowerCase() === currentBackCaller.toLowerCase();
    const isDeployerOwner = deployer.address.toLowerCase() === contractOwner.toLowerCase();
    
    console.log("Is deployer backCaller?", isDeployerBackCaller);
    console.log("Is deployer owner?", isDeployerOwner);
    
    if (!isDeployerBackCaller && !isDeployerOwner) {
      console.log("❌ Permission denied! Attempting to set deployer as backCaller...");
      
      // 如果部署者是合约拥有者，可以设置backCaller
      if (isDeployerOwner) {
        const tx = await arbitrageCore.setBackCaller(deployer.address);
        await tx.wait();
        console.log("✅ Successfully set deployer as backCaller");
      } else {
        console.log("❌ Deployer is neither backCaller nor owner. Cannot execute flash loan arbitrage.");
        console.log("Please ensure the deployer account has proper permissions.");
        return;
      }
    } else {
      console.log("✅ Deployer has proper permissions");
    }
  } catch (error) {
    console.log("Error checking permissions:", error.message);
  }

  // 方法2: 通过ArbitrageCore执行（使用executeFlashLoanArbitrageWithPlatform函数）
  console.log("\n--- Method 2: Execution via ArbitrageCore with executeFlashLoanArbitrageWithPlatform function ---");
  try {
  console.log("Attempting flash loan arbitrage via ArbitrageCore executeFlashLoanArbitrageWithPlatform function...");
  
  // 检查ArbitrageCore合约的USDC余额
  const arbitrageCoreBalanceBefore = await usdc.balanceOf(arbitrageCore.target);
  console.log(`ArbitrageCore USDC balance before: ${ethers.formatUnits(arbitrageCoreBalanceBefore, 6)}`);

  // 准备参数
  const arbitrageParams = {
    asset: asset,
    tokenOut: tokenOut,
    amountIn: amountIn,
    swapPath: swapPath,
    dexes: dexes,
    expectProfit: expectProfit,
    minProfit: minProfit
  };

  // 通过ArbitrageCore的executeFlashLoanArbitrageWithPlatform函数执行闪电贷套利
  const tx = await arbitrageCore.executeFlashLoanArbitrageWithPlatform(
    lendingPlatform, // 使用定义的lendingPlatform (0 = Aave_V2)
    arbitrageParams  // 套利参数
  );

  console.log("Transaction sent, waiting for confirmation...");
  const receipt = await tx.wait();
  console.log("Transaction confirmed! Gas used:", receipt.gasUsed.toString());

  // 检查ArbitrageCore合约的USDC余额
  const arbitrageCoreBalanceAfter = await usdc.balanceOf(arbitrageCore.target);
  console.log(`ArbitrageCore USDC balance after: ${ethers.formatUnits(arbitrageCoreBalanceAfter, 6)}`);

  console.log("Method 2 completed successfully!");
  } catch (error) {
  console.log("Method 2 failed:", error.message);
  console.log("Error stack:", error.stack);
  }

  // 检查合约余额
  console.log("\nChecking contract balances:");
  const flashLoanArbitrageUsdcBalance = await usdc.balanceOf(flashLoanArbitrage.target);
  const flashLoanArbitrageWethBalance = await weth.balanceOf(flashLoanArbitrage.target);
  console.log("- FlashLoanArbitrage USDC balance:", ethers.formatUnits(flashLoanArbitrageUsdcBalance, 6));
  console.log("- FlashLoanArbitrage WETH balance:", ethers.formatUnits(flashLoanArbitrageWethBalance, 18));

  // 查询执行历史
  try {
    const historyLength = await flashLoanArbitrage.getExecutionHistoryLength();
    console.log(`\nExecution history length: ${historyLength}`);
    
    if (historyLength > 0) {
      for (let i = 0; i < Math.min(historyLength, 5); i++) {
        // 如果合约有公开的历史记录函数，可以在这里查询
        console.log(`Record ${i}: Available`);
      }
    }
  } catch (error) {
    console.log("Could not fetch execution history:", error.message);
  }

  console.log("\nFlash loan arbitrage test completed!");
}

main().catch((error) => {
  console.error(error);
  process.exitCode = 1;
});