package handler

import (
	"github.com/gin-gonic/gin"
	"github.com/learning/go-dianping/internal/middleware"
	"github.com/learning/go-dianping/internal/service"
)

func RegisterRoutes(
	r *gin.Engine,
	shopHandler *ShopHandler,
	UserHandler *UserHandler,
	userService *service.UserService,
) {

	r.GET("/shop/:id", shopHandler.GetByID)
	r.POST("/user/code", UserHandler.SendCode)
	r.POST("/user/login", UserHandler.Login)
	auth := r.Group("", middleware.RequireLogin(userService))
	auth.GET("/user/me", UserHandler.Me)
	auth.POST("/user/logout", UserHandler.Logout)
	auth.GET("/user/:id", UserHandler.FindUser)
	auth.PUT("/user/info", UserHandler.UpdateUserInfo)
	auth.GET("/user/info/:id", UserHandler.UserInfo)
	auth.PUT("/user/name", UserHandler.SaveUserName)
	auth.POST("/user/password", UserHandler.SetPassword)
	auth.POST("/user/sign", UserHandler.Sign)
	auth.GET("/user/sign/count", UserHandler.SignCount)
	auth.POST("/shop", shopHandler.ShopCreate)
	auth.PUT("/shop", shopHandler.ShopUpdate)
}
