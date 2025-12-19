// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;
import "../interfaces/IConfigManager.sol";
import "@openzeppelin/contracts/access/Ownable.sol";

contract MockConfigManager is IConfigManager, Ownable {
    uint256 private _platShareFee = 100;
    uint256 public depositFee = 0;
    uint256 public withDrawFee = 10;
    uint256 public platFormFee = 100;

    constructor() Ownable(msg.sender) {
        // Ownable constructor sets the deployer as the owner
    }

    
    function setProfitShareFee(uint256 feeBps) external onlyOwner {
        require(feeBps < 10000, "feeBps too high");
        _platShareFee = feeBps;
    }

    function setDefaultFees(uint256 _deposit, uint256 _withdraw, uint256 _performance) external onlyOwner {
        require(_performance < 10000, "performance too high");
        depositFee = _deposit;
        withDrawFee = _withdraw;
        _platShareFee = _performance;
    }

    function profitShareFee() external view override returns(uint256) {
        return _platShareFee;
    }

    function getDepositFee(address /* vault */) external view returns(uint256){
        return depositFee;
    } //金库合约 获取存入手续费

    function getWithDrawFee(address /* vault */) external view returns(uint256){
        return withDrawFee;
    } //金库合约 获取提现手续费
    
    function getPlatFormFee(address /* vault */) external view returns(uint256){
        return platFormFee;
    } //金库合约 获取平台服务费
}