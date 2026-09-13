package handler

import (
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/learning/go-dianping/internal/dto"
	"github.com/learning/go-dianping/pkg/apperror"
	"math"
	"strconv"
	"time"
)

func positive(c *gin.Context, key string) (int64, error) {
	n, err := strconv.ParseInt(c.Param(key), 10, 64)
	if err != nil || n <= 0 {
		return 0, apperror.BadRequest(key + " 必须是正整数")
	}
	return n, nil
}
func queryInt(c *gin.Context, key string, def, min, max int64) (int64, error) {
	value := c.Query(key)
	if value == "" {
		return def, nil
	}
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil || n < min || n > max {
		return 0, apperror.BadRequest(key + " 参数无效")
	}
	return n, nil
}
func page(c *gin.Context) (int, error) {
	n, err := queryInt(c, "current", 1, 1, 100000)
	return int(n), err
}
func coordinates(c *gin.Context) (*float64, *float64, error) {
	x, y := c.Query("x"), c.Query("y")
	if x == "" && y == "" {
		return nil, nil, nil
	}
	xx, e1 := strconv.ParseFloat(x, 64)
	yy, e2 := strconv.ParseFloat(y, 64)
	if e1 != nil || e2 != nil || math.IsNaN(xx) || math.IsNaN(yy) || math.IsInf(xx, 0) || math.IsInf(yy, 0) || xx < -180 || xx > 180 || yy < -85.05112878 || yy > 85.05112878 {
		return nil, nil, apperror.BadRequest("x/y 必须同时提供有效经纬度")
	}
	return &xx, &yy, nil
}
func parseTime(value string, loc *time.Location) (*time.Time, error) {
	if value == "" {
		return nil, nil
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05", "2006-01-02 15:04:05"} {
		t, err := time.ParseInLocation(layout, value, loc)
		if err == nil {
			return &t, nil
		}
	}
	return nil, apperror.BadRequest(fmt.Sprintf("无效时间 %q", value))
}

// ShopInput is the HTTP binding for the typed shop command.
type ShopInput = dto.ShopInput
