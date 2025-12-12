// Aave LendingPool 的核心接口（用于调用借款函数）
interface ILendingPool {
    function flashLoanSimple(
        address receiver,   // 借款接收者
        address asset,      // 借款资产
        uint256 amount,     // 借款金额
        bytes calldata params, // 额外参数
        uint16 referralCode // 推广码
    ) external;
}