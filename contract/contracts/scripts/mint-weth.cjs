const { ethers } = require("hardhat");

async function main() {
  const [deployer] = await ethers.getSigners();
  // 部署时的 Mock WETH 地址
  const wethAddress = "0x5FbDB2315678afecb367f032d93F642f64180aa3";
  
  const MockERC20 = await ethers.getContractFactory("MockERC20");
  const weth = MockERC20.attach(wethAddress);

  const amount = ethers.utils.parseUnits("10000", 18);
  console.log(`正在为 ${deployer.address} 铸造 ${ethers.utils.formatUnits(amount, 18)} WETH...`);
  
  const tx = await weth.mint(deployer.address, amount);
  await tx.wait();
  
  const balance = await weth.balanceOf(deployer.address);
  console.log(`铸造完成。当前余额: ${ethers.utils.formatUnits(balance, 18)} WETH`);
}

main()
  .then(() => process.exit(0))
  .catch((error) => {
    console.error(error);
    process.exit(1);
  });
