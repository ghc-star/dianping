package handler

import (
	"github.com/gin-gonic/gin"
	"github.com/learning/go-dianping/internal/config"
	"github.com/learning/go-dianping/internal/dto"
	"github.com/learning/go-dianping/internal/middleware"
	"github.com/learning/go-dianping/internal/model"
	"github.com/learning/go-dianping/internal/repository"
	"github.com/learning/go-dianping/internal/service"
	"github.com/learning/go-dianping/pkg/apperror"
	"github.com/learning/go-dianping/pkg/response"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"net/http"
	"strconv"
	"time"
)

type API struct {
	Users    *service.UserService
	Shops    *service.ShopService
	Social   *service.SocialService
	Vouchers *service.VoucherService
	Repo     *repository.Repository
	Redis    *redis.Client
	Config   config.Config
	Log      *zap.Logger
}

func New(api API) *gin.Engine {
	gin.SetMode(api.Config.Server.Mode)
	r := gin.New()
	_ = r.SetTrustedProxies(nil)
	r.Use(middleware.AccessLog(api.Log), middleware.Recovery(api.Log))
	r.GET("/health/live", func(c *gin.Context) { response.OK(c, gin.H{"status": "up"}) })
	r.GET("/health/ready", func(c *gin.Context) {
		db, e := api.Repo.DB.DB()
		if e == nil {
			e = db.PingContext(c.Request.Context())
		}
		if e == nil {
			e = api.Redis.Ping(c.Request.Context()).Err()
		}
		if e != nil {
			response.Fail(c, apperror.Unavailable("依赖服务不可用"))
			return
		}
		response.OK(c, gin.H{"status": "ready"})
	})
	r.StaticFile("/api/openapi.yaml", "./api/openapi.yaml")
	r.StaticFile("/docs", "./api/index.html")
	r.Static("/imgs", api.Config.Upload.Directory)
	r.Use(middleware.Auth(api.Users))
	r.POST("/user/code", func(c *gin.Context) { respond(c, nil, api.Users.SendCode(c.Request.Context(), c.Query("phone"))) })
	r.POST("/user/login", func(c *gin.Context) {
		var in dto.Login
		if !bind(c, &in) {
			return
		}
		data, err := api.Users.Login(c.Request.Context(), in)
		respond(c, data, err)
	})
	r.GET("/shop/of/type", func(c *gin.Context) {
		id, err := queryInt(c, "typeId", 0, 1, 1<<62)
		if err != nil || id == 0 {
			response.Fail(c, apperror.BadRequest("typeId 必填正整数"))
			return
		}
		p, err := page(c)
		if err != nil {
			response.Fail(c, err)
			return
		}
		x, y, err := coordinates(c)
		if err != nil {
			response.Fail(c, err)
			return
		}
		data, err := api.Shops.ListByType(c.Request.Context(), id, p, x, y)
		respond(c, data, err)
	})
	r.GET("/shop/of/name", func(c *gin.Context) {
		p, err := page(c)
		if err != nil {
			response.Fail(c, err)
			return
		}
		data, err := api.Shops.ListByName(c.Request.Context(), c.Query("name"), p)
		respond(c, data, err)
	})
	r.GET("/shop/:id", func(c *gin.Context) {
		id, err := positive(c, "id")
		if err != nil {
			response.Fail(c, err)
			return
		}
		data, err := api.Shops.GetByID(c.Request.Context(), id)
		respond(c, data, err)
	})
	r.GET("/shop-type/list", func(c *gin.Context) { data, err := api.Shops.Types(c.Request.Context()); respond(c, data, err) })
	r.GET("/blog/hot", func(c *gin.Context) {
		p, err := page(c)
		if err != nil {
			response.Fail(c, err)
			return
		}
		data, err := api.Social.HotBlogs(c.Request.Context(), p, middleware.UserID(c))
		respond(c, data, err)
	})
	r.GET("/voucher/list/:shopId", func(c *gin.Context) {
		id, err := positive(c, "shopId")
		if err != nil {
			response.Fail(c, err)
			return
		}
		data, err := api.Vouchers.List(c.Request.Context(), id)
		respond(c, data, err)
	})
	a := r.Group("", middleware.RequireLogin())
	a.POST("/user/logout", func(c *gin.Context) { respond(c, nil, api.Users.Logout(c.Request.Context(), middleware.Token(c))) })
	a.POST("/user/refresh", func(c *gin.Context) { response.OK(c, gin.H{"ttlSeconds": int64(api.Config.Auth.SessionTTL.Seconds())}) })
	a.GET("/user/me", func(c *gin.Context) { response.OK(c, middleware.User(c)) })
	a.GET("/user/info/:id", func(c *gin.Context) {
		id, err := positive(c, "id")
		if err != nil {
			response.Fail(c, err)
			return
		}
		data, err := api.Users.Info(c.Request.Context(), id)
		if data == nil {
			respond(c, nil, err)
			return
		}
		respond(c, data, err)
	})
	a.GET("/user/:id", func(c *gin.Context) {
		id, err := positive(c, "id")
		if err != nil {
			response.Fail(c, err)
			return
		}
		data, err := api.Users.ByID(c.Request.Context(), id)
		if data == nil {
			respond(c, nil, err)
			return
		}
		respond(c, data, err)
	})
	a.PUT("/user/password", func(c *gin.Context) {
		var in struct {
			Password string `json:"password" binding:"required,min=8,max=72"`
		}
		if !bind(c, &in) {
			return
		}
		respond(c, nil, api.Users.SetPassword(c.Request.Context(), middleware.UserID(c), in.Password))
	})
	a.POST("/user/sign", func(c *gin.Context) { respond(c, nil, api.Users.Sign(c.Request.Context(), middleware.UserID(c))) })
	a.GET("/user/sign/count", func(c *gin.Context) {
		data, err := api.Users.SignCount(c.Request.Context(), middleware.UserID(c))
		respond(c, data, err)
	})
	a.POST("/shop", func(c *gin.Context) {
		var in ShopInput
		if !bind(c, &in) {
			return
		}
		if in.Name == nil || in.TypeID == nil || in.Address == nil || in.X == nil || in.Y == nil {
			response.Fail(c, apperror.BadRequest("name/typeId/address/x/y 必填"))
			return
		}
		var shop model.Shop
		in.ApplyTo(&shop)
		shop.ID = 0
		data, err := api.Shops.Create(c.Request.Context(), &shop)
		respond(c, data, err)
	})
	a.PUT("/shop", func(c *gin.Context) {
		var in ShopInput
		if !bind(c, &in) {
			return
		}
		if in.ID <= 0 || !in.HasChanges() {
			response.Fail(c, apperror.BadRequest("id 和至少一个更新字段必填"))
			return
		}
		respond(c, nil, api.Shops.Update(c.Request.Context(), in.ID, in))
	})
	a.POST("/blog", func(c *gin.Context) {
		var in struct {
			ShopID  int64  `json:"shopId" binding:"required,gt=0"`
			Title   string `json:"title" binding:"required,max=255"`
			Images  string `json:"images" binding:"max=2048"`
			Content string `json:"content" binding:"required,max=2048"`
		}
		if !bind(c, &in) {
			return
		}
		data, err := api.Social.CreateBlog(c.Request.Context(), middleware.UserID(c), &model.Blog{ShopID: in.ShopID, Title: in.Title, Images: in.Images, Content: in.Content})
		respond(c, data, err)
	})
	a.PUT("/blog/like/:id", func(c *gin.Context) {
		id, err := positive(c, "id")
		if err != nil {
			response.Fail(c, err)
			return
		}
		respond(c, nil, api.Social.LikeBlog(c.Request.Context(), middleware.UserID(c), id))
	})
	a.GET("/blog/of/me", func(c *gin.Context) {
		p, err := page(c)
		if err != nil {
			response.Fail(c, err)
			return
		}
		data, err := api.Social.BlogsByUser(c.Request.Context(), middleware.UserID(c), p)
		respond(c, data, err)
	})
	a.GET("/blog/of/user", func(c *gin.Context) {
		id, err := queryInt(c, "id", 0, 1, 1<<62)
		if err != nil || id == 0 {
			response.Fail(c, apperror.BadRequest("id 必填正整数"))
			return
		}
		p, err := page(c)
		if err != nil {
			response.Fail(c, err)
			return
		}
		data, err := api.Social.BlogsByUser(c.Request.Context(), id, p)
		respond(c, data, err)
	})
	a.GET("/blog/of/follow", func(c *gin.Context) {
		max, err := queryInt(c, "lastId", time.Now().UnixMilli(), 0, 1<<53-1)
		if err != nil {
			response.Fail(c, err)
			return
		}
		off, err := queryInt(c, "offset", 0, 0, 1000000)
		if err != nil {
			response.Fail(c, err)
			return
		}
		data, err := api.Social.Feed(c.Request.Context(), middleware.UserID(c), max, int(off))
		respond(c, data, err)
	})
	a.GET("/blog/likes/:id", func(c *gin.Context) {
		id, err := positive(c, "id")
		if err != nil {
			response.Fail(c, err)
			return
		}
		data, err := api.Social.BlogLikes(c.Request.Context(), id)
		respond(c, data, err)
	})
	a.GET("/blog/:id", func(c *gin.Context) {
		id, err := positive(c, "id")
		if err != nil {
			response.Fail(c, err)
			return
		}
		data, err := api.Social.BlogByID(c.Request.Context(), id, middleware.UserID(c))
		respond(c, data, err)
	})
	a.PUT("/follow/:id/:isFollow", func(c *gin.Context) {
		id, err := positive(c, "id")
		if err != nil {
			response.Fail(c, err)
			return
		}
		raw := c.Param("isFollow")
		if raw != "true" && raw != "false" {
			response.Fail(c, apperror.BadRequest("isFollow 须为 true/false"))
			return
		}
		yes, _ := strconv.ParseBool(raw)
		respond(c, nil, api.Social.Follow(c.Request.Context(), middleware.UserID(c), id, yes))
	})
	a.GET("/follow/or/not/:id", func(c *gin.Context) {
		id, err := positive(c, "id")
		if err != nil {
			response.Fail(c, err)
			return
		}
		data, err := api.Social.IsFollow(c.Request.Context(), middleware.UserID(c), id)
		respond(c, data, err)
	})
	a.GET("/follow/common/:id", func(c *gin.Context) {
		id, err := positive(c, "id")
		if err != nil {
			response.Fail(c, err)
			return
		}
		data, err := api.Social.CommonFollows(c.Request.Context(), middleware.UserID(c), id)
		respond(c, data, err)
	})
	a.POST("/voucher", api.createVoucher(false))
	a.POST("/voucher/seckill", api.createVoucher(true))
	a.POST("/voucher-order/seckill/:id", func(c *gin.Context) {
		id, err := positive(c, "id")
		if err != nil {
			response.Fail(c, err)
			return
		}
		data, err := api.Vouchers.Seckill(c.Request.Context(), id, middleware.UserID(c))
		respondOrder(c, data, err)
	})
	a.GET("/voucher-order/:id", func(c *gin.Context) {
		id, err := positive(c, "id")
		if err != nil {
			response.Fail(c, err)
			return
		}
		data, err := api.Vouchers.GetOrder(c.Request.Context(), id, middleware.UserID(c))
		respond(c, data, err)
	})
	a.GET("/voucher-order/status/:id", func(c *gin.Context) {
		id, err := positive(c, "id")
		if err != nil {
			response.Fail(c, err)
			return
		}
		data, err := api.Vouchers.GetOrderStatus(c.Request.Context(), id, middleware.UserID(c))
		respond(c, data, err)
	})
	a.POST("/upload/blog", api.upload)
	a.DELETE("/upload/blog/delete", api.deleteUpload)
	r.NoRoute(func(c *gin.Context) {
		c.JSON(http.StatusNotFound, response.Result{Success: false, ErrorMsg: "接口不存在"})
	})
	return r
}
func bind(c *gin.Context, target any) bool {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20)
	if err := c.ShouldBindJSON(target); err != nil {
		response.Fail(c, apperror.BadRequest("请求 JSON 或参数格式错误"))
		return false
	}
	return true
}
func respond(c *gin.Context, data any, err error) {
	if err != nil {
		response.Fail(c, err)
		return
	}
	response.OK(c, data)
}

func respondOrder(c *gin.Context, id int64, err error) {
	if err != nil {
		response.Fail(c, err)
		return
	}
	orderID := strconv.FormatInt(id, 10)
	c.Header("X-Order-ID", orderID)
	// The default number preserves the Java response. New browser clients can
	// request a string because JavaScript cannot exactly represent every int64.
	if c.Query("idAsString") == "true" {
		response.OK(c, orderID)
		return
	}
	response.OK(c, id)
}
func (api API) createVoucher(seckill bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		var in struct {
			ShopID      int64  `json:"shopId" binding:"required,gt=0"`
			Title       string `json:"title" binding:"required,max=255"`
			SubTitle    string `json:"subTitle" binding:"max=255"`
			Rules       string `json:"rules" binding:"max=1024"`
			PayValue    int64  `json:"payValue" binding:"gte=0"`
			ActualValue int64  `json:"actualValue" binding:"required,gt=0"`
			Stock       *int   `json:"stock" binding:"omitempty,gte=0"`
			BeginTime   string `json:"beginTime"`
			EndTime     string `json:"endTime"`
		}
		if !bind(c, &in) {
			return
		}
		loc, _ := time.LoadLocation(api.Config.Timezone)
		begin, err := parseTime(in.BeginTime, loc)
		if err != nil {
			response.Fail(c, err)
			return
		}
		end, err := parseTime(in.EndTime, loc)
		if err != nil {
			response.Fail(c, err)
			return
		}
		v := &model.Voucher{ShopID: in.ShopID, Title: in.Title, SubTitle: in.SubTitle, Rules: in.Rules, PayValue: in.PayValue, ActualValue: in.ActualValue, Status: 1, Stock: in.Stock, BeginTime: begin, EndTime: end}
		data, err := api.Vouchers.Create(c.Request.Context(), v, seckill)
		respondVoucher(c, data, err)
	}
}

// A persisted voucher with failed initialization needs its own response contract.
func respondVoucher(c *gin.Context, id int64, err error) {
	if err != nil && id > 0 {
		_ = c.Error(err)
		c.AbortWithStatusJSON(http.StatusServiceUnavailable, response.Result{Success: false, ErrorMsg: "优惠券已保存，初始化未完成，请勿重复创建", Data: gin.H{"voucherId": id, "state": "initialization_failed"}})
		return
	}
	respond(c, id, err)
}
