// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

import "../interfaces/IConfigManager.sol";

contract MockFlashLoan {
    IConfigManager public configManager;

    constructor(address _configManager) {
        configManager = IConfigManager(_configManager);
    }

    function getPlatFormFee(address vault) external view returns (uint256) {
        return configManager.getPlatFormFee(vault);
    }
}