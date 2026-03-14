const { expect } = require("chai");
const { ethers } = require("hardhat");

describe("ArbitrageVault", function () {
  let vault;
  let weth;
  let configManager;
  let owner, user1, user2, arbitrageCore;

  const INITIAL_BALANCE = ethers.parseUnits("1000", 18);
  const DEPOSIT_AMOUNT = ethers.parseUnits("100", 18);

  beforeEach(async function () {
    [owner, user1, user2, arbitrageCore] = await ethers.getSigners();

    // 1. 部署 Mock WETH
    const MockERC20 = await ethers.getContractFactory("MockERC20");
    weth = await MockERC20.deploy("Wrapped ETH", "WETH", 18);

    // 2. 部署 Mock ConfigManager (确保它有 getDepositFee 等函数)
    const MockConfigManager = await ethers.getContractFactory("MockConfigManager");
    configManager = await MockConfigManager.deploy();

    // 3. 部署 ArbitrageVault (注意：根据你的代码，这是个普通合约，不是代理)
    const ArbitrageVault = await ethers.getContractFactory("ArbitrageVault");
    vault = await ArbitrageVault.deploy(
      await weth.getAddress(),
      await configManager.getAddress(),
      "Arbitrage WETH",
      "arbWETH"
    );

    // 4. 配置 ArbitrageCore
    await vault.setArbitrageCore(await arbitrageCore.getAddress());

    // 5. 给用户发钱
    await weth.mint(await user1.getAddress(), INITIAL_BALANCE);
    await weth.mint(await user2.getAddress(), INITIAL_BALANCE);

    // 6. 初始化 ConfigManager 费率为 0
    await configManager.setDefaultFees(0, 0, 1000); // 10% 业绩费
  });

  describe("存款联调测试 (前端常用接口)", function () {
    it("用户存款后余额和总资产应正确更新", async function () {
      await weth.connect(user1).approve(await vault.getAddress(), DEPOSIT_AMOUNT);
      await vault.connect(user1).deposit(DEPOSIT_AMOUNT, await user1.getAddress());

      // 检查本金余额
      expect(await vault.getUserBalance(await user1.getAddress())).to.equal(DEPOSIT_AMOUNT);
      // 检查份额
      expect(await vault.sharesOf(await user1.getAddress())).to.equal(DEPOSIT_AMOUNT);
      // 检查总资产
      expect(await vault.totalAssets()).to.equal(DEPOSIT_AMOUNT);
    });

    it("sharePrice 初始应为 1e18", async function () {
      const price = await vault.sharePrice();
      expect(price).to.equal(ethers.parseUnits("1", 18));
    });

    it("getVaultStats 应返回完整的统计信息", async function () {
      await weth.connect(user1).approve(await vault.getAddress(), DEPOSIT_AMOUNT);
      await vault.connect(user1).deposit(DEPOSIT_AMOUNT, await user1.getAddress());

      const stats = await vault.getVaultStats();
      expect(stats._totalAssets).to.equal(DEPOSIT_AMOUNT);
      expect(stats._totalSupply).to.equal(DEPOSIT_AMOUNT);
      expect(stats._sharePrice).to.equal(ethers.parseUnits("1", 18));
    });
  });

  describe("取款测试", function () {
    beforeEach(async function () {
      await weth.connect(user1).approve(await vault.getAddress(), DEPOSIT_AMOUNT);
      await vault.connect(user1).deposit(DEPOSIT_AMOUNT, await user1.getAddress());
    });

    it("赎回份额应能取回资产", async function () {
      const shares = await vault.sharesOf(await user1.getAddress());
      await vault.connect(user1).redeem(shares, await user1.getAddress(), await user1.getAddress());

      expect(await weth.balanceOf(await user1.getAddress())).to.equal(INITIAL_BALANCE);
      expect(await vault.sharesOf(await user1.getAddress())).to.equal(0n);
    });
  });

  describe("套利管理接口", function () {
    beforeEach(async function () {
      // 先存入资金，否则 approveForArbitrage 会因余额不足 revert
      await weth.connect(user1).approve(await vault.getAddress(), DEPOSIT_AMOUNT);
      await vault.connect(user1).deposit(DEPOSIT_AMOUNT, await user1.getAddress());
    });

    it("只有 ArbitrageCore 可以调用 approveForArbitrage", async function () {
      const amount = ethers.parseUnits("10", 18);
      try {
        await vault.connect(user1).approveForArbitrage(amount);
        throw new Error("Expected revert but transaction succeeded");
      } catch (error) {
        expect(error.message).to.include("Only ArbitrageCore");
      }

      await vault.connect(arbitrageCore).approveForArbitrage(amount);
      const allowance = await weth.allowance(await vault.getAddress(), await arbitrageCore.getAddress());
      expect(allowance).to.equal(amount);
    });
  });
});
