// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

import "@openzeppelin/contracts/token/ERC20/IERC20.sol";
import "@openzeppelin/contracts/token/ERC20/utils/SafeERC20.sol";

contract MockArbitrageVault {
    using SafeERC20 for IERC20;

    IERC20 public asset;
    address public arbitrageCore;

    constructor(address _asset, address _arbitrageCore) {
        asset = IERC20(_asset);
        arbitrageCore = _arbitrageCore;
    }

    function setArbitrageCore(address _core) external {
        arbitrageCore = _core;        
    }

    function deposit(uint256 amount) external {
        asset.safeTransferFrom(msg.sender, address(this), amount);
    }

    
    function withdrawProfit(uint256 amount) external {
        require(msg.sender == arbitrageCore, "Only arbitrage core can withdraw profit");
        asset.safeTransfer(arbitrageCore, amount);
    }

    function totalAssets() external view returns (uint256) {
        return asset.balanceOf(address(this));
    }

    function getAvailableForArbitrage() external view returns (uint256) {
        return asset.balanceOf(address(this));
    }

    function approveForArbitrage(uint256 amount) external {
        require(msg.sender == arbitrageCore, "Only arbitrage core can approve");
        asset.approve(arbitrageCore, amount);
    }

    function recordProfit(uint256 profit) external {
        // Mock implementation does nothing
    }
    function recordLoss(uint256 loss) external {
        // Mock implementation does nothing
    }
    function assetAddress() external view returns (address) {
        return address(asset);
    }
    
}