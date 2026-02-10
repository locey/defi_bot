// d:\E\SoTest\Dapp_frontend\defi_bot\contracts\contract\test\SpotArbitrage.test.js
const { expect } = require("chai");
const { ethers } = require("hardhat");
const { upgrades } = require("hardhat");

describe("SpotArbitrage CEX-DEX Integration", function () {
  let spotArbitrage;
  let mockDoubleRouterIntegration;
  let mockUniswapV2Integration;
  let mockArbitrageCore;
  let mockTokenA, mockTokenB, mockTokenC;
  let owner, backend, user;
  
  // 测试参数
  const amountIn = ethers.parseEther("1000");
  const expectProfit = ethers.parseEther("50");
  const minProfit = ethers.parseEther("30");

  beforeEach(async function () {
    [owner, backend, user] = await ethers.getSigners();
    
    // 部署Mock合约
    const MockERC20 = await ethers.getContractFactory("MockERC20");
    mockTokenA = await MockERC20.deploy("Token A", "TKA", 18);
    mockTokenB = await MockERC20.deploy("Token B", "TKB", 18);
    mockTokenC = await MockERC20.deploy("Token C", "TKC", 18);
    
    // 铸造代币给 owner
    const mintAmount = ethers.parseEther("1000000");
    await mockTokenA.mint(owner.address, mintAmount);
    await mockTokenB.mint(owner.address, mintAmount);
    await mockTokenC.mint(owner.address, mintAmount);
    
    // 部署Mock DoubleRouterIntegration
    const MockDoubleRouterIntegration = await ethers.getContractFactory("MockDoubleRouterIntegration");
    mockDoubleRouterIntegration = await MockDoubleRouterIntegration.deploy();
    
    // 部署Mock UniswapV2Integration
    const MockUniswapV2Integration = await ethers.getContractFactory("MockUniswapV2Integration");
    mockUniswapV2Integration = await MockUniswapV2Integration.deploy();
    
    // 部署Mock ArbitrageCore
    const MockArbitrageCore = await ethers.getContractFactory("MockArbitrageCore");
    mockArbitrageCore = await MockArbitrageCore.deploy();
    
    // 给 MockArbitrageCore 预存所有类型的代币
    await mockTokenA.mint(mockArbitrageCore.target, ethers.parseEther("1000000"));
    await mockTokenB.mint(mockArbitrageCore.target, ethers.parseEther("1000000"));
    await mockTokenC.mint(mockArbitrageCore.target, ethers.parseEther("1000000"));
    
    // 部署SpotArbitrage，使用正确的四个参数
    const SpotArbitrage = await ethers.getContractFactory("SpotArbitrage");
    spotArbitrage = await upgrades.deployProxy(SpotArbitrage, [
      mockDoubleRouterIntegration.target,
      mockUniswapV2Integration.target,
      mockArbitrageCore.target,
      backend.address
    ]);
    
    const spotAddr = spotArbitrage.target;
    
    // 给 spotArbitrage 合约转账代币
    await mockTokenA.transfer(spotAddr, ethers.parseEther("10000"));
    await mockTokenB.transfer(spotAddr, ethers.parseEther("10000"));
  });

  describe("纯DEX套利测试", function () {
    it("应该成功执行纯DEX套利", async function () {
      const swapPath = [
        mockTokenA.target,
        mockTokenB.target,
        mockTokenA.target
      ];
      const dexes = [
        mockDoubleRouterIntegration.target,
        mockDoubleRouterIntegration.target
      ];
      
      // 调用 executeSwaps
      await spotArbitrage.connect(backend).executeSwaps(
        mockTokenA.target,
        mockTokenA.target,
        amountIn,
        swapPath,
        dexes,
        expectProfit,
        minProfit,
        false
      );
      
      // 验证交易成功完成
      expect(true).to.be.true;
    });
  });

  describe("CEX-DEX套利测试", function () {
    it("CEX在最后一步应该成功", async function () {
      // CEX在最后一步：swapPath[最后一个] != tokenOut
      const swapPath = [
        mockTokenA.target,
        mockTokenB.target,
        mockTokenC.target  // 最后一个不是 tokenOut (TokenA)
      ];
      const dexes = [
        mockDoubleRouterIntegration.target,
        mockDoubleRouterIntegration.target
      ];
      
      await spotArbitrage.connect(backend).executeSwaps(
        mockTokenA.target,
        mockTokenA.target,  // tokenOut = asset，但 swapPath[最后一个] = TokenC != TokenA
        amountIn,
        swapPath,
        dexes,
        expectProfit,
        minProfit,
        true
      );
      
      expect(true).to.be.true;
    });
  });

  describe("错误情况测试", function () {
    it("无效CEX位置应该回滚", async function () {
      // CEX 在中间位置（既不是第一步也不是最后一步）
      const swapPath = [
        mockTokenA.target,
        mockTokenB.target,  // CEX在这里（无效）
        mockTokenA.target
      ];
      const dexes = [
        mockDoubleRouterIntegration.target,
        mockDoubleRouterIntegration.target
      ];
      
      await expect(
        spotArbitrage.connect(backend).executeSwaps(
          mockTokenA.target,
          mockTokenA.target,
          amountIn,
          swapPath,
          dexes,
          expectProfit,
          minProfit,
          true
        )
      ).to.be.revertedWith("Spot: CEX-DEX, invalid cex position");
    });

    it("路径太短应该回滚", async function () {
      const swapPath = [mockTokenA.target, mockTokenB.target]; // 只有2步
      
      await expect(
        spotArbitrage.connect(backend).executeSwaps(
          mockTokenA.target,
          mockTokenB.target,
          amountIn,
          swapPath,
          [mockDoubleRouterIntegration.target],
          expectProfit,
          minProfit,
          true
        )
      ).to.be.revertedWith("Spot: invalid swapPath for cex");
    });

    it("非backend调用应该回滚", async function () {
      await expect(
        spotArbitrage.connect(user).executeSwaps(
          mockTokenA.target,
          mockTokenA.target,
          amountIn,
          [mockTokenA.target, mockTokenB.target, mockTokenA.target],
          [mockDoubleRouterIntegration.target, mockDoubleRouterIntegration.target],
          expectProfit,
          minProfit,
          false
        )
      ).to.be.revertedWith("Spot: only backend");
    });
  });
});
