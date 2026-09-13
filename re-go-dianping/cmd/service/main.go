package main

import (
	"context"
	"log"
	"os"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/learning/go-dianping/internal/handler"
	"github.com/learning/go-dianping/internal/repository"
	"github.com/learning/go-dianping/internal/service"
	"github.com/redis/go-redis/v9"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
)

func main() {
	dsn := os.Getenv("MYSQL_DSN")
	if dsn == "" {
		log.Fatal("请设置MYSQL_DSN环境变量")
	}
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{})
	if err != nil {
		log.Fatal("连接数据库失败:", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		log.Fatal("获取数据库连接池失败:", err)
	}
	shopRepo := repository.New(db)
	defer sqlDB.Close()

	redisAddr := os.Getenv("REDIS_ADDR")
	if redisAddr == "" {
		redisAddr = "127.0.0.1:6379"
	}
	rdb := redis.NewClient(&redis.Options{
		Addr:     redisAddr,
		Password: os.Getenv("REDIS_PASSWORD"),
		DB:       0,
	})
	defer rdb.Close()
	//启动时检查连接，最多等3秒
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	err = rdb.Ping(ctx).Err()
	cancel()
	if err != nil {
		log.Fatal("连接redis失败:", err)
	}

	shopService := service.NewShopRepository(shopRepo, rdb)
	shopHandler := handler.NewShopHandler(shopService)
	userService := service.NewUservice(shopRepo, rdb)
	userHandler := handler.NewUserHandler(userService)
	r := gin.Default()
	handler.RegisterRoutes(r, shopHandler, userHandler, userService)

	r.Run(":8080")
}
