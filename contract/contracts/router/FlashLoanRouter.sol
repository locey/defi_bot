// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

import "../interfaces/IFlashLoanLendingPool.sol";
import "../core/ConfigManage.sol";

import "@openzeppelin/contracts/token/ERC20/IERC20.sol";

contract FlashLoanRouter {
    ConfigManage public configManage;
    enum LendingPlatForm {Aave_V2, Aave_V3, DYDX, UNISWAP_V3}

    struct PlatFormConfig {
        address lendingPool;// LendingPool 地址
        uint16 referralCode; // 推广码（通常为 0）
        uint256 maxLoanRatio; // 最大借款比例（防止借光）
    }

    mapping(LendingPlatForm => PlatFormConfig) public platFormConfigs;

    address public immutable admin;

    //初始化构造Aave_V2等平台的地址配置
    constructor(address _configManage) {
        admin = msg.sender;
        configManage = ConfigManage(_configManage);
        platFormConfigs[LendingPlatForm.Aave_V2] = PlatFormConfig({
            // lendingPool: 0x7d2768dE32b0b80b7a3454c06BdAc94A69DDc7,
            lendingPool: configManage.lendingPool(),
            referralCode: 0,
            maxLoanRatio: 5000 //此处设置最多借出借款池的50%
        });
    }

    modifier onlyAdmin() {
        require(msg.sender == admin, "no admin no right");
        _;
    }

    //更新配置接口 可新增
    function setConfig(
        LendingPlatForm platform,
        address lendingPool,
        uint16 referralCode,
        uint256 maxLoanRatio
        ) external onlyAdmin {
            require(lendingPool != address(0), "lendingPool not invalid");
            require(maxLoanRatio <= 10000, "maxLoanRatio must <= 100%");
            
            platFormConfigs[platform] = PlatFormConfig({
                lendingPool: lendingPool,
                referralCode: referralCode,
                maxLoanRatio: maxLoanRatio
            });
    }

    function requsetFlashLoan(
        //1.平台2.业务合约地址3.借款资产（如USDC的地址）4.借款额度（申请金额）
        LendingPlatForm platform,
        address receiver,
        address asset,
        uint256 amount,
        bytes calldata params
    )external {
        //1.校验平台配置
        PlatFormConfig memory config = platFormConfigs[platform];
        require(config.lendingPool != address(0), "platform not invalid");

        //2.校验借款额度
        uint256 poolBalance = IERC20(asset).balanceOf(config.lendingPool);
        uint256 maxAllowedAmount = poolBalance * config.maxLoanRatio / 10000;
        require(amount <= maxAllowedAmount, "Amount over a lot");

        //3.转发请求到lendingPool
        ILendingPool(config.lendingPool).flashLoanSimple(
            receiver,    // 借款接收者
            asset,       // 借款资产
            amount,      // 借款金额
            params,   // 额外参数
            config.referralCode // 推广码
        );
    }
}