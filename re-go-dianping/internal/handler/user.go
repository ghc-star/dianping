package handler

import (
	"errors"
	"log"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/learning/go-dianping/internal/dto"
	"github.com/learning/go-dianping/internal/middleware"
	"github.com/learning/go-dianping/internal/service"
	"gorm.io/gorm"
)

type UserHandler struct {
	users *service.UserService
}

func NewUserHandler(users *service.UserService) *UserHandler {
	return &UserHandler{users: users}
}

func (h *UserHandler) SendCode(c *gin.Context) {
	phone := c.Query("phone")
	err := h.users.SendCode(c.Request.Context(), phone)
	if errors.Is(err, service.ErrInvalidPhone) {
		c.JSON(http.StatusBadRequest, gin.H{
			"success":  false,
			"errorMsg": "手机号格式错误",
		})
		return
	}
	if errors.Is(err, service.ErrCodeTooFrequent) {
		c.JSON(http.StatusTooManyRequests, gin.H{
			"success":  false,
			"errorMsg": "验证码获取频繁，请稍后重试",
		})
		return
	}
	if err != nil {
		log.Printf("获取验证码失败：%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"success":  false,
			"errorMsg": "获取验证码失败",
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
	})
}

func (h *UserHandler) Login(c *gin.Context) {
	var in dto.LoginRequest

	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success":  false,
			"errorMsg": "请提交手机号和六位数字验证码",
		})
		return
	}
	token, err := h.users.Login(c.Request.Context(), in)
	if errors.Is(err, service.ErrInvalidPhone) ||
		errors.Is(err, service.ErrInvalidCode) {
		c.JSON(http.StatusBadRequest, gin.H{
			"success":  false,
			"errorMsg": err.Error(),
		})
		return
	}
	if err != nil {
		log.Printf("登录失败：%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"success":  false,
			"errorMsg": "登录失败，请稍后重试",
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    token,
	})
}

func (h *UserHandler) Me(c *gin.Context) {
	user := middleware.CurrentUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success":  false,
			"errorMsg": "请先登录",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    user,
	})
}

func (h *UserHandler) Logout(c *gin.Context) {
	token := c.GetHeader("authorization")
	if err := h.users.Logout(c.Request.Context(), token); err != nil {
		log.Printf("退出登录失败:%v", err)
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"success":  false,
			"errorMsg": "退出登录失败：请稍后重试",
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
	})
}

func (h *UserHandler) FindUser(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"success":  false,
			"errorMsg": "id必须是正整数",
		})
		return
	}
	user, err := h.users.FindUser(c.Request.Context(), id)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusNotFound, gin.H{
			"success":  false,
			"errorMsg": "用户不存在",
		})
		return
	}
	if err != nil {
		log.Printf("查询用户失败，id=%d：%v", id, err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"success":  false,
			"errorMsg": "查询用户失败",
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    user,
	})
}
func (h *UserHandler) UpdateUserInfo(c *gin.Context) {
	user := middleware.CurrentUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success":  false,
			"errorMsg": "请先登录",
		})
		return
	}
	var in dto.UpdateUserInfoRequest
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success":  false,
			"errorMsg": "请求JSON格式错误",
		})
		return
	}
	err := h.users.UpdateUserInfo(
		c.Request.Context(),
		user.ID,
		in,
	)
	if errors.Is(err, service.ErrInvalidUserInfo) {
		c.JSON(http.StatusBadRequest, gin.H{
			"success":  false,
			"errorMsg": err.Error(),
		})
		return
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusNotFound, gin.H{
			"success":  false,
			"errorMsg": "用户不存在",
		})
		return
	}
	if err != nil {
		log.Printf("保存用户资料失败：%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"success":  false,
			"errorMsg": "保存用户资料失败",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
	})

}

func (h *UserHandler) UserInfo(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"success":  false,
			"errorMsg": "id必须是正整数",
		})
		return
	}
	user, err := h.users.UserInfo(c.Request.Context(), id)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusNotFound, gin.H{
			"success":  false,
			"errorMsg": "用户不存在",
		})
		return
	}
	if err != nil {
		log.Printf("查询用户失败，id=%d：%v", id, err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"success":  false,
			"errorMsg": "查询用户失败",
		})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    user,
	})
}

func (h *UserHandler) SaveUserName(c *gin.Context) {
	user := middleware.CurrentUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success":  false,
			"errorMsg": "请先登录",
		})
		return
	}
	var in dto.UserDTO
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success":  false,
			"errorMsg": "请求JSON格式错误",
		})
		return
	}
	err := h.users.UpdateUserName(
		c.Request.Context(),
		user.ID,
		in,
	)
	if errors.Is(err, service.ErrInvalidUserInfo) {
		c.JSON(http.StatusBadRequest, gin.H{
			"success":  false,
			"errorMsg": err.Error(),
		})
		return
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusNotFound, gin.H{
			"success":  false,
			"errorMsg": "用户不存在",
		})
		return
	}
	if err != nil {
		log.Printf("保存用户资料失败：%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"success":  false,
			"errorMsg": "保存用户资料失败",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
	})

}

func (h *UserHandler) SetPassword(c *gin.Context) {
	user := middleware.CurrentUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success":  false,
			"errorMsg": "请先登录",
		})
		return
	}
	var in dto.Password
	if err := c.ShouldBindJSON(&in); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success":  false,
			"errorMsg": "请求JSON格式错误",
		})
		return
	}
	err := h.users.SetPassword(c.Request.Context(), user.ID, in)
	if errors.Is(err, service.ErrInvalidUserInfo) {
		c.JSON(http.StatusBadRequest, gin.H{
			"success":  false,
			"errorMsg": err.Error(),
		})
		return
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		c.JSON(http.StatusNotFound, gin.H{
			"success":  false,
			"errorMsg": "用户不存在",
		})
		return
	}
	if err != nil {
		log.Printf("修改密码失败：%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"success":  false,
			"errorMsg": "修改密码失败",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
	})
}

func (h *UserHandler) Sign(c *gin.Context) {
	user := middleware.CurrentUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success":  false,
			"errorMsg": "请先登录",
		})
		return
	}

	if err := h.users.Sign(c.Request.Context(), user.ID); err != nil {
		log.Printf("签到失败：%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"success":  false,
			"errorMsg": "签到失败",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
	})
}
func (h *UserHandler) SignCount(c *gin.Context) {
	user := middleware.CurrentUser(c)
	if user == nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"success":  false,
			"errorMsg": "请先登录",
		})
		return
	}

	count, err := h.users.SignCount(c.Request.Context(), user.ID)
	if err != nil {
		log.Printf("查询连续签到失败：%v", err)
		c.JSON(http.StatusInternalServerError, gin.H{
			"success":  false,
			"errorMsg": "查询连续签到失败",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"data":    count,
	})
}
