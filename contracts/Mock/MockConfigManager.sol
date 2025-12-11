// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;
import "../interfaces/IConfigManager.sol";
import "@openzeppelin/contracts-upgradeable/access/OwnableUpgradeable.sol";
import "@openzeppelin/contracts-upgradeable/proxy/utils/UUPSUpgradeable.sol";

contract MockConfigManager is IConfigManager, OwnableUpgradeable, UUPSUpgradeable {
    uint256 private _platShareFee = 100;
    uint256 public depositFee = 0;
    uint256 public withDrawFee = 10;
    uint256 public platFormFee = 100;

    constructor() {
        _disableInitializers();
    }

    function initialize() public initializer {
        __Ownable_init_(msg.sender);
        __UUPSUpgradeable_init();
    }

    
    function setProfitShareFee(uint256 feeBps) external onlyOwner {
        require(feeBps < 10000, "feeBps too high");
        _platShareFee = feeBps;
    }

    function profitShareFee() external view override returns(uint256) {
        return _platShareFee;
    }

    function getDepositFee(address vault) external view returns(uint256){
        return depositFee;
    } //金库合约 获取存入手续费

    function getWithDrawFee(address vault) external view returns(uint256){
        return withDrawFee;
    } //金库合约 获取提现手续费
    
    function getPlatFormFee(address vault) external view returns(uint256){
        return platFormFee;
    } //金库合约 获取平台服务费

    function _authorizeUpgrade(address newImplementation) internal override onlyOwner {
        require(newImplementation != address(0), "New implementation is zero address");
    }
}