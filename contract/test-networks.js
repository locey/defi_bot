const { config } = require("hardhat/config");

console.log("=== 已配置的网络列表 ===");
console.log(Object.keys(config.networks));
console.log("=== Sepolia网络配置详情 ===");
console.log(config.networks.sepolia);