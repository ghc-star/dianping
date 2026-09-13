package middleware

import (
	"log"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/learning/go-dianping/internal/dto"
	"github.com/learning/go-dianping/internal/service"
)

const userContextKey = "currentUser"

func RequireLogin(users *service.UserService) gin.HandlerFunc {
	return func(c *gin.Context) {
		//与原项目保持一致：请求头直接传token
		token := c.GetHeader("authorization")
		user, err := users.GetSession(c.Request.Context(), token)
		if err != nil {
			log.Printf("读取登录会话失败:%v", err)
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{
				"success":  false,
				"errorMsg": "登录状态查询失败，请稍后重试",
			})
			return
		}
		if user == nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"success":  false,
				"errorMsg": "请先登录",
			})
			return
		}
		c.Set(userContextKey, user)
		c.Next()
	}
}

func CurrentUser(c *gin.Context) *dto.UserDTO {
	value, exists := c.Get(userContextKey)
	if !exists {
		return nil
	}
	user, ok := value.(*dto.UserDTO)
	if !ok {
		return nil
	}
	return user
}
