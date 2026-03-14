// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

/**
 * @title ISiloV1
 * @notice Silo Finance V1 接口 — 清算所需方法
 *
 * Silo 内置闪电清算：flashLiquidate() 先发抵押品给 liquidator，
 * 然后回调 siloLiquidationCallback()，liquidator 在回调中 swap 后还债。
 * 无需外部闪电贷（Aave/Balancer），合约更简单。
 */

/// @notice Silo 合约接口
interface ISilo {
    /// @notice 闪电清算 — 没收抵押品 → 回调 → 还债
    /// @param _users 要清算的用户列表（支持批量）
    /// @param _flashReceiverData 传给回调的自定义数据
    function flashLiquidate(
        address[] calldata _users,
        bytes calldata _flashReceiverData
    ) external returns (
        address[] memory assets,
        uint256[][] memory receivedCollaterals,
        uint256[][] memory shareAmountsToRepay
    );

    /// @notice 检查用户偿付能力（false = 可清算）
    function isSolvent(address _user) external view returns (bool);

    /// @notice 偿还指定用户的债务
    function repayFor(
        address _asset,
        address _borrower,
        uint256 _amount
    ) external;
}

/// @notice 闪电清算回调接口 — liquidator 合约必须实现
interface IFlashLiquidationReceiver {
    /// @dev Silo 在发送抵押品后调用此函数
    ///      在回调中完成：swap collateral → debt asset → repay
    function siloLiquidationCallback(
        address _user,
        address[] calldata _assets,
        uint256[] calldata _receivedCollaterals,
        uint256[] calldata _shareAmountsToRepay,
        bytes calldata _flashReceiverData
    ) external;
}

/// @notice SiloLens 查询接口
interface ISiloLens {
    function getUserLTV(address _silo, address _user) external view returns (uint256);
    function getUserLiquidationThreshold(address _silo, address _user) external view returns (uint256);
    function inDebt(address _silo, address _user) external view returns (bool);
    function totalBorrowAmount(address _silo, address _asset) external view returns (uint256);
    function totalDeposits(address _silo, address _asset) external view returns (uint256);
}
