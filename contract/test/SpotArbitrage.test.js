const { expect } = require("chai");
const { ethers } = require("hardhat");
const { upgrades } = require("hardhat");

describe("SpotArbitrage Comprehensive Tests", function () {
  let spotArbitrage;
  let mockDoubleRouterIntegration;
  let mockUniswapV2Integration;
  let mockArbitrageCore;
  let mockTokenA, mockTokenB, mockTokenC;
  let owner, backend, user, hacker;

  const amountIn = ethers.parseEther("1000");
  const expectProfit = ethers.parseEther("50");
  const minProfit = ethers.parseEther("30");

  // V2 = 0, V3 500bps = 500, V3 3000bps = 3000
  const FEE_V2 = 0;
  const FEE_V3_500 = 500;
  const FEE_V3_3000 = 3000;

  beforeEach(async function () {
    [owner, backend, user, hacker] = await ethers.getSigners();

    const MockERC20 = await ethers.getContractFactory("MockERC20");
    mockTokenA = await MockERC20.deploy("Token A", "TKA", 18);
    mockTokenB = await MockERC20.deploy("Token B", "TKB", 18);
    mockTokenC = await MockERC20.deploy("Token C", "TKC", 18);

    const mintAmount = ethers.parseEther("1000000");
    await mockTokenA.mint(owner.address, mintAmount);
    await mockTokenB.mint(owner.address, mintAmount);
    await mockTokenC.mint(owner.address, mintAmount);

    const MockDoubleRouterIntegration = await ethers.getContractFactory("MockDoubleRouterIntegration");
    mockDoubleRouterIntegration = await MockDoubleRouterIntegration.deploy();

    const MockUniswapV2Integration = await ethers.getContractFactory("MockUniswapV2Integration");
    mockUniswapV2Integration = await MockUniswapV2Integration.deploy();

    const MockArbitrageCore = await ethers.getContractFactory("MockArbitrageCore");
    mockArbitrageCore = await MockArbitrageCore.deploy();

    await mockTokenA.mint(mockArbitrageCore.target, mintAmount);
    await mockTokenB.mint(mockArbitrageCore.target, mintAmount);
    await mockTokenC.mint(mockArbitrageCore.target, mintAmount);

    const SpotArbitrage = await ethers.getContractFactory("SpotArbitrage");
    spotArbitrage = await upgrades.deployProxy(SpotArbitrage, [
      mockDoubleRouterIntegration.target,
      mockUniswapV2Integration.target,
      mockArbitrageCore.target,
      backend.address
    ]);

    const spotAddr = spotArbitrage.target;

    await mockTokenA.transfer(spotAddr, ethers.parseEther("10000"));
    await mockTokenB.transfer(spotAddr, ethers.parseEther("10000"));
    await mockTokenC.transfer(spotAddr, ethers.parseEther("10000"));
  });

  // ==================== 初始化测试 ====================
  describe("初始化测试", function () {
    it("应该正确部署合约并初始化所有参数", async function () {
      expect(await spotArbitrage.owner()).to.equal(owner.address);
      expect(await spotArbitrage.backendCaller()).to.equal(backend.address);
      expect(await spotArbitrage.arbitrageCore()).to.equal(mockArbitrageCore.target);
    });

    it("初始化时传入零地址应该回滚 - doubleRouterIntegration", async function () {
      const SpotArbitrage = await ethers.getContractFactory("SpotArbitrage");
      await expect(
        upgrades.deployProxy(SpotArbitrage, [
          ethers.ZeroAddress, mockUniswapV2Integration.target,
          mockArbitrageCore.target, backend.address
        ])
      ).to.be.revertedWith("Spot: invalid doubleRouterIntegration");
    });

    it("初始化时传入零地址应该回滚 - uniswapV2Integration", async function () {
      const SpotArbitrage = await ethers.getContractFactory("SpotArbitrage");
      await expect(
        upgrades.deployProxy(SpotArbitrage, [
          mockDoubleRouterIntegration.target, ethers.ZeroAddress,
          mockArbitrageCore.target, backend.address
        ])
      ).to.be.revertedWith("Spot: invalid uniswapV2Integration");
    });

    it("初始化时传入零地址应该回滚 - backendCaller", async function () {
      const SpotArbitrage = await ethers.getContractFactory("SpotArbitrage");
      await expect(
        upgrades.deployProxy(SpotArbitrage, [
          mockDoubleRouterIntegration.target, mockUniswapV2Integration.target,
          mockArbitrageCore.target, ethers.ZeroAddress
        ])
      ).to.be.revertedWith("Spot: invalid backendCaller");
    });

    it("不能重复初始化", async function () {
      await expect(
        spotArbitrage.initialize(
          mockDoubleRouterIntegration.target, mockUniswapV2Integration.target,
          mockArbitrageCore.target, backend.address
        )
      ).to.be.revertedWithCustomError(spotArbitrage, "InvalidInitialization");
    });
  });

  // ==================== 权限变更测试 ====================
  describe("权限变更测试", function () {
    it("只有owner可以设置backendCaller", async function () {
      await expect(
        spotArbitrage.connect(user).setBackendCaller(user.address)
      ).to.be.revertedWithCustomError(spotArbitrage, "OwnableUnauthorizedAccount");
    });

    it("owner可以成功设置新的backendCaller", async function () {
      await spotArbitrage.connect(owner).setBackendCaller(user.address);
      expect(await spotArbitrage.backendCaller()).to.equal(user.address);
    });

    it("设置backendCaller为零地址应该回滚", async function () {
      await expect(
        spotArbitrage.connect(owner).setBackendCaller(ethers.ZeroAddress)
      ).to.be.revertedWith("Spot: invalid backendCaller");
    });

    it("只有owner可以设置arbitrageCore", async function () {
      await expect(
        spotArbitrage.connect(user).setArbitrageCore(user.address)
      ).to.be.revertedWithCustomError(spotArbitrage, "OwnableUnauthorizedAccount");
    });

    it("owner可以成功设置新的arbitrageCore", async function () {
      await spotArbitrage.connect(owner).setArbitrageCore(user.address);
      expect(await spotArbitrage.arbitrageCore()).to.equal(user.address);
    });

    it("设置相同的arbitrageCore应该回滚", async function () {
      await expect(
        spotArbitrage.connect(owner).setArbitrageCore(mockArbitrageCore.target)
      ).to.be.revertedWith("Spot: same arbitrageCore");
    });

    it("设置零地址作为arbitrageCore应该回滚", async function () {
      await expect(
        spotArbitrage.connect(owner).setArbitrageCore(ethers.ZeroAddress)
      ).to.be.revertedWith("Spot: invalid arbitrageCore");
    });
  });

  // ==================== 纯DEX套利测试 ====================
  describe("纯DEX套利测试", function () {
    it("应该成功执行纯DEX套利 (V2 fee tier)", async function () {
      const swapPath = [mockTokenA.target, mockTokenB.target, mockTokenA.target];
      const dexes = [mockDoubleRouterIntegration.target, mockDoubleRouterIntegration.target];
      const feeTiers = [FEE_V2, FEE_V2];

      await spotArbitrage.connect(owner).executeSwaps(
        mockTokenA.target, mockTokenA.target, amountIn,
        swapPath, dexes, feeTiers, expectProfit, minProfit, false
      );

      const coreBal = await mockTokenA.balanceOf(mockArbitrageCore.target);
      expect(coreBal).to.be.gt(0);
    });

    it("应该成功执行纯DEX套利 (混合 V3 fee tier)", async function () {
      const swapPath = [mockTokenA.target, mockTokenB.target, mockTokenA.target];
      const dexes = [mockDoubleRouterIntegration.target, mockDoubleRouterIntegration.target];
      const feeTiers = [FEE_V3_500, FEE_V3_3000]; // 第一跳 0.05%, 第二跳 0.3%

      await spotArbitrage.connect(owner).executeSwaps(
        mockTokenA.target, mockTokenA.target, amountIn,
        swapPath, dexes, feeTiers, expectProfit, minProfit, false
      );

      const coreBal = await mockTokenA.balanceOf(mockArbitrageCore.target);
      expect(coreBal).to.be.gt(0);
    });

    it("backendCaller 可以执行套利", async function () {
      await spotArbitrage.connect(backend).executeSwaps(
        mockTokenA.target, mockTokenA.target, amountIn,
        [mockTokenA.target, mockTokenB.target, mockTokenA.target],
        [mockDoubleRouterIntegration.target, mockDoubleRouterIntegration.target],
        [FEE_V2, FEE_V2],
        expectProfit, minProfit, false
      );
    });

    it("user 也不能执行套利", async function () {
      await expect(
        spotArbitrage.connect(user).executeSwaps(
          mockTokenA.target, mockTokenA.target, amountIn,
          [mockTokenA.target, mockTokenB.target, mockTokenA.target],
          [mockDoubleRouterIntegration.target, mockDoubleRouterIntegration.target],
          [FEE_V2, FEE_V2],
          expectProfit, minProfit, false
        )
      ).to.be.revertedWith("Spot: not authorized");
    });
  });

  // ==================== CEX-DEX套利测试 ====================
  describe("CEX-DEX套利测试", function () {
    it("CEX路径太短应该回滚 (swapPath < 3)", async function () {
      await expect(
        spotArbitrage.connect(owner).executeSwaps(
          mockTokenA.target, mockTokenB.target, amountIn,
          [mockTokenA.target, mockTokenB.target],
          [mockDoubleRouterIntegration.target],
          [FEE_V2],
          expectProfit, minProfit, true
        )
      ).to.be.revertedWith("Spot: invalid swapPath for cex");
    });

    it("无效CEX位置应该回滚", async function () {
      await expect(
        spotArbitrage.connect(owner).executeSwaps(
          mockTokenA.target, mockTokenA.target, amountIn,
          [mockTokenA.target, mockTokenB.target, mockTokenA.target],
          [mockDoubleRouterIntegration.target, mockDoubleRouterIntegration.target],
          [FEE_V2, FEE_V2],
          expectProfit, minProfit, true
        )
      ).to.be.revertedWith("Spot: CEX-DEX, invalid cex position");
    });

    it("CEX在第一步应该成功 (swapPath[0] != asset)", async function () {
      const swapPath = [mockTokenB.target, mockTokenC.target, mockTokenA.target];
      const dexes = [mockDoubleRouterIntegration.target, mockDoubleRouterIntegration.target];
      const feeTiers = [FEE_V2, FEE_V3_3000];

      await spotArbitrage.connect(owner).executeSwaps(
        mockTokenA.target, mockTokenA.target, amountIn,
        swapPath, dexes, feeTiers, expectProfit, minProfit, true
      );

      const coreBal = await mockTokenA.balanceOf(mockArbitrageCore.target);
      expect(coreBal).to.be.gt(0);
    });

    it("CEX在最后一步 - tokenOut 余额减少应该回滚", async function () {
      const swapPath = [mockTokenA.target, mockTokenB.target, mockTokenC.target];
      const dexes = [mockDoubleRouterIntegration.target, mockDoubleRouterIntegration.target];
      const feeTiers = [FEE_V3_500, FEE_V2];

      await expect(
        spotArbitrage.connect(owner).executeSwaps(
          mockTokenA.target, mockTokenA.target, amountIn,
          swapPath, dexes, feeTiers, expectProfit, minProfit, true
        )
      ).to.be.revertedWith("CEX last: tokenOut balance decreased");
    });
  });

  // ==================== 边界条件测试 ====================
  describe("边界条件测试", function () {
    it("非CEX模式下swapPath长度为2应该成功", async function () {
      await spotArbitrage.connect(owner).executeSwaps(
        mockTokenA.target, mockTokenB.target, amountIn,
        [mockTokenA.target, mockTokenB.target],
        [mockDoubleRouterIntegration.target],
        [FEE_V2],
        expectProfit, minProfit, false
      );

      const coreBal = await mockTokenB.balanceOf(mockArbitrageCore.target);
      expect(coreBal).to.be.gt(0);
    });

    it("合约未收到代币应该回滚", async function () {
      const MockERC20 = await ethers.getContractFactory("MockERC20");
      const emptyToken = await MockERC20.deploy("Empty", "EMP", 18);

      await expect(
        spotArbitrage.connect(owner).executeSwaps(
          emptyToken.target, emptyToken.target, amountIn,
          [emptyToken.target, mockTokenB.target, emptyToken.target],
          [mockDoubleRouterIntegration.target, mockDoubleRouterIntegration.target],
          [FEE_V2, FEE_V2],
          expectProfit, minProfit, false
        )
      ).to.be.revertedWith("Spot: no received");
    });

    it("传入不存在的代币地址应该 revert", async function () {
      await expect(
        spotArbitrage.connect(owner).executeSwaps(
          ethers.ZeroAddress, mockTokenA.target, amountIn,
          [ethers.ZeroAddress, mockTokenB.target, mockTokenA.target],
          [mockDoubleRouterIntegration.target, mockDoubleRouterIntegration.target],
          [FEE_V2, FEE_V2],
          expectProfit, minProfit, false
        )
      ).to.be.reverted;
    });
  });

  // ==================== 权限控制测试 ====================
  describe("权限控制测试", function () {
    it("非owner不能调用setDoubleRouterIntegration", async function () {
      await expect(
        spotArbitrage.connect(user).setDoubleRouterIntegration(user.address)
      ).to.be.revertedWithCustomError(spotArbitrage, "OwnableUnauthorizedAccount");
    });

    it("非owner不能调用setUniswapV2Integration", async function () {
      await expect(
        spotArbitrage.connect(user).setUniswapV2Integration(user.address)
      ).to.be.revertedWithCustomError(spotArbitrage, "OwnableUnauthorizedAccount");
    });

    it("设置 doubleRouterIntegration 为零地址应该回滚", async function () {
      await expect(
        spotArbitrage.connect(owner).setDoubleRouterIntegration(ethers.ZeroAddress)
      ).to.be.revertedWith("Spot: invalid doubleRouterIntegration");
    });

    it("设置 uniswapV2Integration 为零地址应该回滚", async function () {
      await expect(
        spotArbitrage.connect(owner).setUniswapV2Integration(ethers.ZeroAddress)
      ).to.be.revertedWith("Spot: invalid uniswapV2Integration");
    });
  });

  // ==================== 重入攻击防护测试 ====================
  describe("重入攻击防护测试", function () {
    it("连续调用 executeSwaps 应该都成功 (非并发)", async function () {
      const swapPath = [mockTokenA.target, mockTokenB.target, mockTokenA.target];
      const dexes = [mockDoubleRouterIntegration.target, mockDoubleRouterIntegration.target];
      const feeTiers = [FEE_V2, FEE_V2];

      await spotArbitrage.connect(owner).executeSwaps(
        mockTokenA.target, mockTokenA.target, amountIn,
        swapPath, dexes, feeTiers, expectProfit, minProfit, false
      );

      await spotArbitrage.connect(owner).executeSwaps(
        mockTokenA.target, mockTokenA.target, amountIn,
        swapPath, dexes, feeTiers, expectProfit, minProfit, false
      );
    });
  });

  // ==================== UUPS升级测试 ====================
  describe("UUPS升级测试", function () {
    it("owner可以升级合约", async function () {
      const SpotArbitrageV2 = await ethers.getContractFactory("SpotArbitrage", owner);
      const upgraded = await upgrades.upgradeProxy(spotArbitrage.target, SpotArbitrageV2);
      expect(upgraded.target).to.equal(spotArbitrage.target);
    });

    it("非owner不能升级合约", async function () {
      const SpotArbitrageV2 = await ethers.getContractFactory("SpotArbitrage", user);
      await expect(
        upgrades.upgradeProxy(spotArbitrage.target, SpotArbitrageV2)
      ).to.be.revertedWithCustomError(spotArbitrage, "OwnableUnauthorizedAccount");
    });
  });
});
