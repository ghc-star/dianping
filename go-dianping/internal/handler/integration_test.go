package handler

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/learning/go-dianping/internal/bootstrap"
	"github.com/learning/go-dianping/internal/config"
	"github.com/learning/go-dianping/internal/model"
	"github.com/learning/go-dianping/internal/redisx"
	"github.com/learning/go-dianping/internal/repository"
	"github.com/learning/go-dianping/internal/service"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"mime/multipart"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"
	"time"
)

func TestHTTPIntegration(t *testing.T) {
	dsn, addr := os.Getenv("DIANPING_TEST_MYSQL_DSN"), os.Getenv("DIANPING_TEST_REDIS_ADDR")
	if dsn == "" || addr == "" {
		t.Skip("requires migrated test MySQL and Redis DB 14")
	}
	ctx := context.Background()
	var cfg config.Config
	cfg.MySQL.DSN = dsn
	cfg.MySQL.MaxOpen = 10
	cfg.MySQL.MaxIdle = 2
	cfg.Timezone = "Asia/Shanghai"
	cfg.Server.Mode = "test"
	cfg.Auth.CodeTTL = 2 * time.Minute
	cfg.Auth.CodeInterval = time.Minute
	cfg.Auth.SessionTTL = 30 * time.Minute
	cfg.Auth.DevCodeLog = true
	cfg.Upload.Directory = t.TempDir()
	cfg.Upload.MaxBytes = 1 << 20
	db, err := bootstrap.Database(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	pool, _ := db.DB()
	defer pool.Close()
	rdb := redis.NewClient(&redis.Options{Addr: addr, DB: 14})
	defer rdb.Close()
	repo := repository.New(db)
	log := zap.NewNop()
	users := service.NewUser(repo, rdb, log, cfg)
	shops := service.NewShop(repo, rdb, log, ctx)
	defer shops.Close()
	router := New(API{Users: users, Shops: shops, Social: service.NewSocial(repo, rdb, log), Vouchers: service.NewVoucher(repo, rdb, log), Repo: repo, Redis: rdb, Config: cfg, Log: log})
	token := ""
	call := func(method, path string, body any) map[string]any {
		t.Helper()
		var buf bytes.Buffer
		if body != nil {
			json.NewEncoder(&buf).Encode(body)
		}
		req := httptest.NewRequest(method, path, &buf)
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("authorization", token)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		var result map[string]any
		if e := json.Unmarshal(w.Body.Bytes(), &result); e != nil {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body)
		}
		return result
	}
	ok := func(result map[string]any) any {
		t.Helper()
		if result["success"] != true {
			t.Fatalf("API error: %v", result)
		}
		return result["data"]
	}
	phone := "139" + fmt.Sprintf("%08d", time.Now().UnixNano()%100000000)
	ok(call("POST", "/user/code?phone="+phone, nil))
	code := rdb.Get(ctx, redisx.LoginCodePrefix+phone).Val()
	token = ok(call("POST", "/user/login", map[string]string{"phone": phone, "code": code})).(string)
	me := ok(call("GET", "/user/me", nil)).(map[string]any)
	uid := int64(me["id"].(float64))
	defer func() {
		db.Delete(&model.User{}, uid)
		rdb.Del(ctx, redisx.LoginCodePrefix+phone, redisx.LoginThrottlePrefix+phone, redisx.LoginAttemptsPrefix+phone, redisx.LoginTokenPrefix+token, redisx.Key(redisx.SignPrefix, uid)+time.Now().In(time.FixedZone("CST", 8*3600)).Format(":200601"))
	}()
	if call("POST", "/user/login", map[string]string{"phone": phone, "code": code})["success"] != false {
		t.Fatal("OTP replay")
	}
	ok(call("POST", "/user/sign", nil))
	if n := ok(call("GET", "/user/sign/count", nil)); n != float64(1) {
		t.Fatalf("sign count=%v", n)
	}
	ok(call("PUT", "/user/password", map[string]string{"password": "learning-go-2026"}))
	ok(call("POST", "/user/logout", nil))
	if call("GET", "/user/me", nil)["success"] != false {
		t.Fatal("revoked token remains usable")
	}
	token = ok(call("POST", "/user/login", map[string]string{"phone": phone, "password": "learning-go-2026"})).(string)
	ok(call("GET", "/shop/1", nil))
	ok(call("GET", "/shop-type/list", nil))
	ok(call("GET", "/shop/of/type?typeId=1&x=120.149192&y=30.316078", nil))
	shopID := int64(ok(call("POST", "/shop", map[string]any{"name": "integration-shop", "typeId": 1, "address": "test", "x": 120.149192, "y": 30.316078, "score": 20})).(float64))
	defer func() {
		db.Delete(&model.Shop{}, shopID)
		rdb.Del(ctx, redisx.Key(redisx.ShopCachePrefix, shopID), redisx.Key(redisx.ShopVersionPrefix, shopID))
		rdb.ZRem(ctx, redisx.Key(redisx.ShopGeoPrefix, 1), strconv.FormatInt(shopID, 10))
	}()
	ok(call("GET", fmt.Sprintf("/shop/%d", shopID), nil))
	ok(call("PUT", "/shop", map[string]any{"id": shopID, "score": 0, "name": "updated"}))
	shop := ok(call("GET", fmt.Sprintf("/shop/%d", shopID), nil)).(map[string]any)
	if shop["name"] != "updated" || shop["score"] != float64(0) {
		t.Fatalf("cache/zero update: %v", shop)
	}
	var imageBody bytes.Buffer
	writer := multipart.NewWriter(&imageBody)
	file, _ := writer.CreateFormFile("file", "test.png")
	png, _ := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+/ZioAAAAASUVORK5CYII=")
	file.Write(png)
	writer.Close()
	req := httptest.NewRequest("POST", "/upload/blog", &imageBody)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	req.Header.Set("authorization", token)
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	var uploaded map[string]any
	json.Unmarshal(w.Body.Bytes(), &uploaded)
	name := ok(uploaded).(string)
	ok(call("DELETE", "/upload/blog/delete?name="+name, nil))
}
