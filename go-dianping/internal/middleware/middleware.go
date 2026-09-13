package middleware

import (
	"github.com/gin-gonic/gin"
	"github.com/learning/go-dianping/internal/dto"
	"github.com/learning/go-dianping/internal/service"
	"github.com/learning/go-dianping/pkg/apperror"
	"github.com/learning/go-dianping/pkg/response"
	"go.uber.org/zap"
	"net/http"
	"runtime/debug"
	"strings"
	"time"
)

const UserKey = "user"
const TokenKey = "token"

func User(c *gin.Context) *dto.User {
	value, exists := c.Get(UserKey)
	if !exists {
		return nil
	}
	u, _ := value.(*dto.User)
	return u
}
func UserID(c *gin.Context) int64 {
	if u := User(c); u != nil {
		return u.ID
	}
	return 0
}
func Token(c *gin.Context) string { return c.GetString(TokenKey) }
func Auth(users *service.UserService) gin.HandlerFunc {
	return func(c *gin.Context) {
		token := strings.TrimSpace(c.GetHeader("authorization"))
		if len(token) > 7 && strings.EqualFold(token[:7], "bearer ") {
			token = strings.TrimSpace(token[7:])
		}
		if token != "" {
			u, err := users.Session(c.Request.Context(), token)
			if err != nil {
				response.Fail(c, err)
				return
			}
			if u != nil {
				c.Set(UserKey, u)
				c.Set(TokenKey, token)
			}
		}
		c.Next()
	}
}
func RequireLogin() gin.HandlerFunc {
	return func(c *gin.Context) {
		if User(c) == nil {
			response.Fail(c, apperror.ErrUnauthorized)
			return
		}
		c.Next()
	}
}
func AccessLog(log *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		fields := []zap.Field{zap.String("method", c.Request.Method), zap.String("path", c.Request.URL.Path), zap.Int("status", c.Writer.Status()), zap.Duration("latency", time.Since(start))}
		if len(c.Errors) > 0 && c.Writer.Status() >= 500 {
			log.Error("http request failed", append(fields, zap.String("error", c.Errors.String()))...)
		} else {
			log.Info("http request", fields...)
		}
	}
}
func Recovery(log *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if recovered := recover(); recovered != nil {
				log.Error("handler panic", zap.Any("panic", recovered), zap.ByteString("stack", debug.Stack()))
				c.AbortWithStatusJSON(http.StatusInternalServerError, response.Result{Success: false, ErrorMsg: "服务器异常"})
			}
		}()
		c.Next()
	}
}
