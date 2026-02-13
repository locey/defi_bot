// internal/api/handlers/vault.go
package handlers

import (
	"math/big"
	"net/http"

	"github.com/defi-bot/backend/internal/vault"
	"github.com/defi-bot/backend/pkg/web3"
	"github.com/ethereum/go-ethereum/common"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// VaultInfoResponse 金库信息响应
type VaultInfoResponse struct {
	Address            string `json:"address"`
	AssetAddress       string `json:"asset_address"`
	TotalAssets        string `json:"total_assets"`
	AvailableAssets    string `json:"available_assets"`
	TotalShares        string `json:"total_shares"`
	SharePrice         string `json:"share_price"`
	TotalProfit        string `json:"total_profit"`
	TotalFees          string `json:"total_fees"`
	LastArbitrageTime  uint64 `json:"last_arbitrage_time"`
}

// UserVaultInfoResponse 用户金库信息响应
type UserVaultInfoResponse struct {
	UserAddress     string `json:"user_address"`
	VaultAddress    string `json:"vault_address"`
	Shares          string `json:"shares"`
	AssetsValue     string `json:"assets_value"`
	DepositedAmount string `json:"deposited_amount"`
	ProfitLoss      string `json:"profit_loss"`
}

// DepositPreviewResponse 存款预览响应
type DepositPreviewResponse struct {
	Assets     string `json:"assets"`
	Shares     string `json:"shares"`
	SharePrice string `json:"share_price"`
}

// RedeemPreviewResponse 赎回预览响应
type RedeemPreviewResponse struct {
	Shares     string `json:"shares"`
	Assets     string `json:"assets"`
	SharePrice string `json:"share_price"`
}

// VaultHandler 金库处理器
type VaultHandler struct {
	db           *gorm.DB
	web3Client   *web3.Client
	vaultAddress common.Address
	vaultManager *vault.VaultManager
}

// NewVaultHandler 创建金库处理器
func NewVaultHandler(db *gorm.DB, web3Client *web3.Client, vaultAddress common.Address) (*VaultHandler, error) {
	vm, err := vault.NewVaultManager(web3Client, vaultAddress)
	if err != nil {
		return nil, err
	}

	return &VaultHandler{
		db:           db,
		web3Client:   web3Client,
		vaultAddress: vaultAddress,
		vaultManager: vm,
	}, nil
}

// GetVaultInfo 获取金库信息
func (h *VaultHandler) GetVaultInfo(c *gin.Context) {
	stats, err := h.vaultManager.GetVaultStats(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	response := VaultInfoResponse{
		Address:           h.vaultAddress.Hex(),
		AssetAddress:      stats.AssetAddress,
		TotalAssets:       stats.TotalAssets.String(),
		AvailableAssets:   stats.AvailableForArb.String(),
		TotalShares:       stats.TotalSupply.String(),
		SharePrice:        stats.SharePrice.String(),
		TotalProfit:       stats.TotalProfit.String(),
		TotalFees:         stats.TotalFees.String(),
		LastArbitrageTime: stats.LastArbitrageTime,
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    response,
	})
}

// GetUserVaultInfo 获取用户金库信息
func (h *VaultHandler) GetUserVaultInfo(c *gin.Context) {
	userAddressHex := c.Param("user_address")
	if !common.IsHexAddress(userAddressHex) {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   "invalid user address",
		})
		return
	}

	userAddress := common.HexToAddress(userAddressHex)
	info, err := h.vaultManager.GetUserInfo(c.Request.Context(), userAddress)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	response := UserVaultInfoResponse{
		UserAddress:     info.Address,
		VaultAddress:    h.vaultAddress.Hex(),
		Shares:          info.Shares.String(),
		AssetsValue:     info.AssetsValue.String(),
		DepositedAmount: info.DepositedAmount.String(),
		ProfitLoss:      info.ProfitLoss.String(),
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    response,
	})
}

// PreviewDeposit 预览存款
func (h *VaultHandler) PreviewDeposit(c *gin.Context) {
	var req struct {
		Assets string `json:"assets" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	assets, ok := new(big.Int).SetString(req.Assets, 10)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   "invalid assets amount",
		})
		return
	}

	preview, err := h.vaultManager.PreviewDeposit(c.Request.Context(), assets)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	response := DepositPreviewResponse{
		Assets:     preview.Assets.String(),
		Shares:     preview.Shares.String(),
		SharePrice: preview.SharePrice.String(),
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    response,
	})
}

// PreviewRedeem 预览赎回
func (h *VaultHandler) PreviewRedeem(c *gin.Context) {
	var req struct {
		Shares string `json:"shares" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	shares, ok := new(big.Int).SetString(req.Shares, 10)
	if !ok {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"error":   "invalid shares amount",
		})
		return
	}

	preview, err := h.vaultManager.PreviewRedeem(c.Request.Context(), shares)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{
			"success": false,
			"error":   err.Error(),
		})
		return
	}

	response := RedeemPreviewResponse{
		Shares:     preview.Shares.String(),
		Assets:     preview.Assets.String(),
		SharePrice: preview.SharePrice.String(),
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    response,
	})
}

// GetVaultInfo 获取金库信息（兼容旧接口）
// 注意：实际金库数据需要从链上读取，这里返回数据库中的统计信息
func GetVaultInfo(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		address := c.Param("address")

		// 这里应该从链上读取金库数据
		// 建议使用 VaultHandler 替代此函数
		vaultInfo := VaultInfoResponse{
			Address:         address,
			TotalAssets:     "0",
			AvailableAssets: "0",
			TotalShares:     "0",
			SharePrice:      "0",
			TotalProfit:     "0",
			TotalFees:       "0",
		}

		c.JSON(http.StatusOK, gin.H{
			"success": true,
			"data":    vaultInfo,
			"note":    "Please use /api/v1/vault/info endpoint for real-time data",
		})
	}
}

