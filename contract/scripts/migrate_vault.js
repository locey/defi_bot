const { ethers } = require("ethers");

const provider = new ethers.JsonRpcProvider(
  "https://warmhearted-wild-road.arbitrum-mainnet.quiknode.pro/1ed461039e0b8a3008160cbc130479deb34798c9/"
);

const OLD_VAULT = "0xBE312B103f89489Df5bFf0A59d327c4C8B19d94F";
const NEW_VAULT = "0xB7e37Fb429795E10A8D25752fFC69Fbab71df677";
const WETH = "0x82aF49447D8a07e3bd95BD0d56f35241523fBab1";
const OLD_CONFIG_MANAGER = "0x8dD68621209D31D2c3202F894B956DDEdF893697";

async function main() {
  let pk = process.env.KEEPER_PK;
  if (!pk) throw new Error("Set KEEPER_PK env var");
  if (pk.substring(0, 2) !== "0x") pk = "0x" + pk;
  const wallet = new ethers.Wallet(pk, provider);
  console.log("Keeper:", wallet.address);

  // ========== Diagnose old ConfigManager ==========
  const code = await provider.getCode(OLD_CONFIG_MANAGER);
  console.log("Old ConfigManager bytecode:", code.length > 4 ? (code.length / 2) + " bytes" : "EMPTY");

  const cfgAbi = ["function getWithDrawFee(address) view returns (uint256)"];
  const cfg = new ethers.Contract(OLD_CONFIG_MANAGER, cfgAbi, provider);
  try {
    const fee = await cfg.getWithDrawFee(OLD_VAULT);
    console.log("getWithDrawFee:", fee.toString(), "bps");
  } catch (e) {
    console.log("getWithDrawFee failed:", e.message.substring(0, 100));
  }

  // ========== Test redeem staticCall ==========
  const vaultAbi = [
    "function redeem(uint256, address, address) returns (uint256)",
    "function balanceOf(address) view returns (uint256)",
    "function totalAssets() view returns (uint256)",
  ];
  const erc20Abi = [
    "function balanceOf(address) view returns (uint256)",
    "function approve(address, uint256) returns (bool)",
  ];

  const oldV = new ethers.Contract(OLD_VAULT, vaultAbi, wallet);
  const newV = new ethers.Contract(NEW_VAULT, [
    "function deposit(uint256, address) returns (uint256)",
    "function totalAssets() view returns (uint256)",
  ], wallet);
  const wethContract = new ethers.Contract(WETH, erc20Abi, wallet);

  const shares = await oldV.balanceOf(wallet.address);
  console.log("Shares in old vault:", ethers.formatEther(shares));

  // Test redeem
  try {
    const result = await oldV.redeem.staticCall(shares, wallet.address, wallet.address);
    console.log("redeem staticCall OK, would get:", ethers.formatEther(result), "WETH");
  } catch (e) {
    console.log("redeem staticCall failed:", e.message.substring(0, 200));
    console.log("\nCannot redeem from old vault via normal path.");
    console.log("Need to use emergencyWithdraw or owner rescue.");

    // Try WETH transfer directly (if vault has a rescue function)
    const rescueAbi = [
      "function rescueToken(address token, uint256 amount)",
      "function emergencyWithdraw()",
      "function emergencyWithdraw(address)",
    ];
    const rescueV = new ethers.Contract(OLD_VAULT, rescueAbi, wallet);
    const wethBal = await wethContract.balanceOf(OLD_VAULT);
    console.log("WETH in old vault:", ethers.formatEther(wethBal));

    for (const fn of ["rescueToken", "emergencyWithdraw"]) {
      try {
        if (fn === "rescueToken") {
          await rescueV.rescueToken.staticCall(WETH, wethBal);
          console.log("rescueToken staticCall OK!");
        }
      } catch (e2) {
        console.log(fn + " failed:", e2.message.substring(0, 80));
      }
    }
    return;
  }

  // ========== Execute migration ==========
  console.log("\n[1/4] Redeeming from old vault...");
  const tx1 = await oldV.redeem(shares, wallet.address, wallet.address);
  const r1 = await tx1.wait();
  console.log("  tx:", r1.hash);

  const wethBal = await wethContract.balanceOf(wallet.address);
  console.log("  Keeper WETH:", ethers.formatEther(wethBal));

  console.log("\n[2/4] Approving new vault...");
  const tx2 = await wethContract.approve(NEW_VAULT, wethBal);
  await tx2.wait();
  console.log("  done");

  console.log("\n[3/4] Depositing to new vault...");
  const tx3 = await newV.deposit(wethBal, wallet.address);
  const r3 = await tx3.wait();
  console.log("  tx:", r3.hash);

  console.log("\n[4/4] Verifying...");
  const oldAssets = await oldV.totalAssets();
  const newAssets = await newV.totalAssets();
  console.log("  Old vault:", ethers.formatEther(oldAssets), "WETH");
  console.log("  New vault:", ethers.formatEther(newAssets), "WETH");
  console.log("\nMigration complete!");
}

main().catch(console.error);
