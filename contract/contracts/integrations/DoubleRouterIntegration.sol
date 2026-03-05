// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

import "../core/ConfigManage.sol";
import "../router/IUniswapV2Router02.sol";
import "../router/IUniswapV3Router.sol";
import "../interfaces/IDoubleRouterIntegration.sol";

import "@openzeppelin/contracts/token/ERC20/utils/SafeERC20.sol";
import "@openzeppelin/contracts-upgradeable/proxy/utils/Initializable.sol";
import "@openzeppelin/contracts-upgradeable/proxy/utils/UUPSUpgradeable.sol";
import "@openzeppelin/contracts-upgradeable/access/OwnableUpgradeable.sol";

contract DoubleRouterIntegration is IDoubleRouterIntegration, Initializable, UUPSUpgradeable, OwnableUpgradeable {

    using SafeERC20 for IERC20;

    ConfigManage public configManage;
    uint256 public slippageTolerance;
    address[] public mrouters;

    // V3 Router 白名单（保留用于 arbCheck，swap 时以 feeTiers 为准）
    mapping(address => bool) public isV3Router;
    mapping(address => uint24) public v3RouterFee; // 仅用于 arbCheck 的默认 fee

    /// @custom:oz-upgrades-unsafe-allow constructor
    constructor() {
        _disableInitializers();
    }

    function initialize(address _configManage) public initializer {
        __Ownable_init(msg.sender);
        __UUPSUpgradeable_init();
        configManage = ConfigManage(_configManage);
        slippageTolerance = configManage.slippageTolerance();
    }

    event DoubleRouterSwap(
        address indexed sender,
        address indexed routerA,
        address indexed routerB,
        uint256 amountIn,
        uint256 profit
    );

    event DoubleRouterSwap2(
        address indexed routerAddr,
        address indexed fromToken,
        address indexed toToken,
        uint256 currentAmount,
        uint256 outAmount
    );

    function setRouters(address[] calldata _routers) external onlyOwner {
        require(_routers.length > 0 , "Empty routers");
        mrouters = _routers;
    }

    function getRouters() external view returns(address[] memory) {
        return mrouters;
    }

    // 设置 V3 Router 白名单和默认费率（用于 arbCheck）
    function setV3Router(address router, bool enabled, uint24 fee) external onlyOwner {
        isV3Router[router] = enabled;
        v3RouterFee[router] = fee;
    }

    function doubleRouterArbCheck(
        uint amountIn,
        address[] calldata path,
        address[] calldata routers
    ) external view returns(
        bool profitable,
        uint finalAmount,
        int profit
    ) {
        require(path.length >= 2, "Invalid path");
        require(path[0] == path[path.length - 1], "Must return to start");
        require(routers.length == path.length - 1, "Router count mismatch");

        uint256 currentAmount = amountIn;

        for (uint i = 0; i < routers.length; i++) {
            address router = routers[i];
            address from = path[i];
            address to = path[i + 1];

            address[] memory stepPath = new address[](2);
            stepPath[0] = from;
            stepPath[1] = to;

            IUniswapV2Router02 swapRouter = IUniswapV2Router02(router);

            uint256[] memory amounts;
            try swapRouter.getAmountsOut(currentAmount, stepPath) returns (uint256[] memory result) {
                amounts = result;
            } catch {
                return (false, 0, -1);
            }

            currentAmount = amounts[1];
        }

        uint finalOut = currentAmount;

        bool isProfit = finalOut > amountIn;
        int profitAmount = int(finalOut) - int(amountIn);

        return (isProfit, finalOut, profitAmount);
    }

    /**
     * @dev 执行多跳交换
     * @param feeTiers 每一跳的 V3 fee tier (500/3000/10000)；V2 跳传 0
     */
    function doubleRouterSwap(
        address spot,
        address tokenIn,
        address tokenOut,
        uint256 amountIn,
        address[] calldata swapPath,
        address[] calldata dexes,
        uint24[] calldata feeTiers,
        uint256 expectProfit,
        uint256 minProfit
    ) external returns(uint256 amountOut) {
        require(swapPath.length >= 2, "invalid swapPath");
        require(dexes.length == swapPath.length - 1, "dexes length mismatch");
        require(feeTiers.length == dexes.length, "feeTiers length mismatch");
        require(swapPath[swapPath.length - 1] == tokenOut, "tokenOut mismatch with swapPath");
        require(IERC20(tokenIn).balanceOf(spot) >= amountIn, "insufficient tokenIn balance");

        // 将代币从 spot 拉到本合约（V3 Router 需要从 msg.sender 拉取）
        IERC20(tokenIn).safeTransferFrom(spot, address(this), amountIn);

        uint256 currentAmount = amountIn;
        uint256 deadline = block.timestamp + 120;
        for (uint i = 0; i < dexes.length; i++) {
            currentAmount = _executeSingleSwap(
                dexes[i],
                swapPath[i],
                swapPath[i + 1],
                currentAmount,
                deadline,
                feeTiers[i]
            );
        }
        require(currentAmount >= amountIn + minProfit, "DoubleRouter: insufficient profit");

        // 将最终代币转回 spot（SpotArbitrage）
        IERC20(tokenOut).safeTransfer(spot, currentAmount);

        amountOut = currentAmount;
    }

    /**
     * @dev 执行单跳交换
     * @param feeTier V3 fee tier (500/3000/10000)；0 表示 V2 路由
     */
    function _executeSingleSwap(
        address routerAddr,
        address fromToken,
        address toToken,
        uint256 currentAmount,
        uint256 deadline,
        uint24 feeTier
    ) internal returns (uint256 outAmount) {
        IERC20(fromToken).approve(routerAddr, currentAmount);

        if (feeTier > 0 || isV3Router[routerAddr]) {
            // V3 Router: 使用 exactInputSingle
            // feeTier 优先；为 0 时回退到 v3RouterFee 映射或默认 3000
            uint24 fee = feeTier > 0 ? feeTier : v3RouterFee[routerAddr];
            if (fee == 0) fee = 3000;

            outAmount = IUniswapV3Router(routerAddr).exactInputSingle(
                IUniswapV3Router.ExactInputSingleParams({
                    tokenIn: fromToken,
                    tokenOut: toToken,
                    fee: fee,
                    recipient: address(this),
                    deadline: deadline,
                    amountIn: currentAmount,
                    amountOutMinimum: 1,
                    sqrtPriceLimitX96: 0
                })
            );
        } else {
            // V2 Router
            address[] memory path = new address[](2);
            path[0] = fromToken;
            path[1] = toToken;

            uint[] memory amounts = IUniswapV2Router02(routerAddr).getAmountsOut(currentAmount, path);
            uint256 expectedOut = amounts[amounts.length - 1];
            uint256 minOut = (expectedOut * (10000 - slippageTolerance)) / 10000;
            minOut = minOut == 0 ? 1 : minOut;

            outAmount = IUniswapV2Router02(routerAddr).swapExactTokensForTokens(
                currentAmount,
                minOut,
                path,
                address(this),
                deadline
            )[1];
        }

        emit DoubleRouterSwap2(routerAddr, fromToken, toToken, currentAmount, outAmount);
    }

    function _authorizeUpgrade(address newImplementation) internal override onlyOwner {}
}
