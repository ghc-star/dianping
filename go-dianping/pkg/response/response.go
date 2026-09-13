package response

import (
	"github.com/gin-gonic/gin"
	"github.com/learning/go-dianping/pkg/apperror"
	"net/http"
	"reflect"
)

// Result 保留原 Spring Jackson NON_NULL 的返回格式。
type Result struct {
	Success  bool   `json:"success"`
	ErrorMsg string `json:"errorMsg,omitempty"`
	Data     any    `json:"data,omitempty"`
	Total    *int64 `json:"total,omitempty"`
}

func OK(c *gin.Context, data any) {
	// A typed nil pointer inside any is non-nil; normalize it to match Java NON_NULL.
	if data != nil {
		v := reflect.ValueOf(data)
		if v.Kind() == reflect.Pointer && v.IsNil() {
			data = nil
		}
	}
	c.JSON(http.StatusOK, Result{Success: true, Data: data})
}
func Fail(c *gin.Context, err error) {
	status, message := http.StatusInternalServerError, "服务器异常"
	if app, ok := apperror.As(err); ok {
		message = app.Message
		switch app.Kind {
		case "unauthorized":
			status = http.StatusUnauthorized
		case "unavailable":
			status = http.StatusServiceUnavailable
		default:
			status = http.StatusOK // 原前端通过 success 判断业务失败。
		}
	}
	_ = c.Error(err)
	c.AbortWithStatusJSON(status, Result{Success: false, ErrorMsg: message})
}
