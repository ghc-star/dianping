package handler

import (
	"errors"
	"log"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/learning/go-dianping/internal/dto"
	"github.com/learning/go-dianping/internal/model"
	"github.com/learning/go-dianping/internal/service"
	"gorm.io/gorm"
)

type ShopHandler struct {
	shop *service.ShopService
}

func NewShopHandler(shop *service.ShopService) *ShopHandler {
	return &ShopHandler{shop: shop}
}

func (h *ShopHandler) GetByID(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{
			"success":  false,
			"errorMsg": "id必须是正整数",
		})
		return
	}
	result, err := h.shop.GetByID(c.Request.Context(), id)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusNotFound, gin.H{
			"success":  false,
			"errorMsg": "商铺不存在",
		})
		return
	}
	if err != nil {
		log.Printf("查询商铺失败，id=%d：%v", id, err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"success":  false,
			"errorMsg": "查询商铺失败",
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    result,
	})
}

func (h *ShopHandler) ShopCreate(c *gin.Context) {
	var in dto.ShopInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success":  false,
			"errorMsg": "商铺请求参数错误",
		})
		return
	}

	// 创建商铺时，这些字段必须提供。
	if in.Name == nil ||
		in.TypeID == nil ||
		in.Address == nil ||
		in.X == nil ||
		in.Y == nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success":  false,
			"errorMsg": "name、typeId、address、x、y必填",
		})
		return
	}
	var shop model.Shop
	in.ApplyTo(&shop)
	id, err := h.shop.ShopCreate(c.Request.Context(), &shop)
	if errors.Is(err, service.ErrInvalidShopCreat) {
		c.JSON(http.StatusBadRequest, gin.H{
			"success":  false,
			"errorMsg": err.Error(),
		})
		return
	}
	if err != nil {
		log.Printf("创建商铺失败：%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"success":  false,
			"errorMsg": "创建商铺失败",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    id,
	})
}

func (h *ShopHandler) ShopUpdate(c *gin.Context) {
	var in dto.ShopInput
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success":  false,
			"errorMsg": "商铺请求参数错误",
		})
		return
	}
	err := h.shop.ShopUpdate(c.Request.Context(), in)
	if err != nil {
		log.Printf("更新商铺失败：%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"success":  false,
			"errorMsg": "更新商铺失败",
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
	})
}
