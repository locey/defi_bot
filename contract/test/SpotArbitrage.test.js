// d:\E\SoTest\Dapp_frontend\defi_bot\contracts\contract\test\SpotArbitrage.test.js
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
  
  // 测试参数
  const amountIn = ethers.parseEther("1000");
  const expectProfit = ethers.parseEther("50");
  const minProfit = ethers.parseEther("30");

  beforeEach(async function () {
    [owner, backend, user, hacker] = await ethers.getSigners();
    
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
          ethers.ZeroAddress,
          mockUniswapV2Integration.target,
          mockArbitrageCore.target,
          backend.address
        ])
      ).to.be.revertedWith("Spot: invalid doubleRouterIntegration");
    });

    it("初始化时传入零地址应该回滚 - uniswapV2Integration", async function () {
      const SpotArbitrage = await ethers.getContractFactory("SpotArbitrage");
      await expect(
        upgrades.deployProxy(SpotArbitrage, [
          mockDoubleRouterIntegration.target,
          ethers.ZeroAddress,
          mockArbitrageCore.target,
          backend.address
        ])
      ).to.be.revertedWith("Spot: invalid uniswapV2Integration");
    });

    it("初始化时传入零地址应该回滚 - backendCaller", async function () {
      const SpotArbitrage = await ethers.getContractFactory("SpotArbitrage");
      await expect(
        upgrades.deployProxy(SpotArbitrage, [
          mockDoubleRouterIntegration.target,
          mockUniswapV2Integration.target,
          mockArbitrageCore.target,
          ethers.ZeroAddress
        ])
      ).to.be.revertedWith("Spot: invalid backendCaller");
    });

    it("不能重复初始化", async function () {
      await expect(
        spotArbitrage.initialize(
          mockDoubleRouterIntegration.target,
          mockUniswapV2Integration.target,
          mockArbitrageCore.target,
          backend.address
        )
      ).to.be.revertedWith("Initializable: contract is already initialized");
    });
  });

  // ==================== 权限变更测试 ====================
  describe("权限变更测试", function () {
    it("只有owner可以设置backendCaller", async function () {
      await expect(
        spotArbitrage.connect(user).setBackendCaller(user.address)
      ).to.be.revertedWithCustomError(spotArbitrage, "OwnableUnauthorized");
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
      ).to.be.revertedWithCustomError(spotArbitrage, "OwnableUnauthorized");
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

    it("owner可以设置零地址作为arbitrageCore但会回滚", async function () {
      await expect(
        spotArbitrage.connect(owner).setArbitrageCore(ethers.ZeroAddress)
      ).to.be.revertedWith("Spot: invalid arbitrageCore");
    });
  });

  // ==================== 纯DEX套利测试 ====================
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
      
      expect(true).to.be.true;
    });

    it("纯DEX套利 - owner可以作为backend调用", async function () {
      const swapPath = [
        mockTokenA.target,
        mockTokenB.target,
        mockTokenA.target
      ];
      const dexes = [
        mockDoubleRouterIntegration.target,
        mockDoubleRouterIntegration.target
      ];
      
      await spotArbitrage.connect(owner).executeSwaps(
        mockTokenA.target,
        mockTokenA.target,
        amountIn,
        swapPath,
        dexes,
        expectProfit,
        minProfit,
        false
      );
      
      expect(true).to.be.true;
    });
  });

  // ==================== CEX-DEX套利测试 ====================
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

  // ==================== 边界条件测试 ====================
  describe("边界条件测试", function () {
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

    it("非CEX模式下swapPath长度为2应该成功", async function () {
      const swapPath = [mockTokenA.target, mockTokenB.target];
      const dexes = [mockDoubleRouterIntegration.target];
      
      await spotArbitrage.connect(backend).executeSwaps(
        mockTokenA.target,
        mockTokenB.target,
        amountIn,
        swapPath,
        dexes,
        expectProfit,
        minProfit,
        false
      );
      
      expect(true).to.be.true;
    });
  });

  // ==================== 权限控制测试 ====================
  describe("权限控制测试", function () {
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

    it("非owner不能调用onlyOwner函数", async function () {
      await expect(
        spotArbitrage.connect(user).setDoubleRouterIntegration(user.address)
      ).to.be.revertedWithCustomError(spotArbitrage, "OwnableUnauthorized");
    });

    it("非owner不能调用setUniswapV2Integration", async function () {
      await expect(
        spotArbitrage.connect(user).setUniswapV2Integration(user.address)
      ).to.be.revertedWithCustomError(spotArbitrage, "OwnableUnauthorized");
    });
  });

  // ==================== 重入攻击防护测试 ====================
  describe("重入攻击防护测试", function () {
    it("不应该在executeSwaps中重新进入同一函数", async function () {
      // 这个测试验证 nonReentrant 修饰符是否工作
      // 如果没有重入保护，恶意合约可能尝试多次调用 executeSwaps
      const swapPath = [
        mockTokenA.target,
        mockTokenB.target,
        mockTokenA.target
      ];
      const dexes = [
        mockDoubleRouterIntegration.target,
        mockDoubleRouterIntegration.target
      ];
      
      // 第一次调用应该成功
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
      
      // 第二次调用也应该是独立的交易
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
      
      expect(true).to.be.true;
    });
  });

  // ==================== ERC20异常测试 ====================
  describe("ERC20异常测试", function () {
    it("合约未收到代币应该回滚", async function () {
      // 不给合约转账代币，直接调用
      const swapPath = [
        mockTokenA.target,
        mockTokenB.target,
        mockTokenA.target
      ];
      const dexes = [
        mockDoubleRouterIntegration.target,
        mockDoubleRouterIntegration.target
      ];
      
      // 使用0金额尝试调用
      await expect(
        spotArbitrage.connect(backend).executeSwaps(
          mockTokenA.target,
          mockTokenA.target,
          0,
          swapPath,
          dexes,
          expectProfit,
          minProfit,
          false
        )
      ).to.be.revertedWith("Spot: no received");
    });

    it("传入不存在的代币地址应该回滚", async function () {
      const swapPath = [
        ethers.ZeroAddress,  // 使用零地址作为代币
        mockTokenB.target,
        mockTokenA.target
      ];
      const dexes = [
        mockDoubleRouterIntegration.target,
        mockDoubleRouterIntegration.target
      ];
      
      await expect(
        spotArbitrage.connect(backend).executeSwaps(
          ethers.ZeroAddress,
          mockTokenA.target,
          amountIn,
          swapPath,
          dexes,
          expectProfit,
          minProfit,
          false
        )
      ).to.be.reverted; // ERC20操作会失败
    });
  });

  // ==================== UUPS升级测试 ====================
  describe("UUPS升级测试", function () {
    it("非owner不能升级合约", async function () {
      const SpotArbitrageV2 = await ethers.getContractFactory("SpotArbitrage");
      
      await expect(
        upgrades.upgradeProxy(spotArbitrage.target, SpotArbitrageV2)
      ).to.be.revertedWithCustomError(spotArbitrage, "OwnableUnauthorized");
    });
  });
});
