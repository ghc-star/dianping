package handler

import (
	"bytes"
	"encoding/json"
	"github.com/gin-gonic/gin"
	"github.com/learning/go-dianping/internal/config"
	"go.uber.org/zap"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOrderIDStringCompatibility(t *testing.T) {
	const id int64 = 633000000000000001
	for _, tc := range []struct {
		query, data string
	}{
		{"?idAsString=true", `"633000000000000001"`},
		{"", `633000000000000001`},
	} {
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest(http.MethodPost, "/seckill"+tc.query, nil)
		respondOrder(c, id, nil)
		if w.Header().Get("X-Order-ID") != "633000000000000001" || !bytes.Contains(w.Body.Bytes(), []byte(tc.data)) {
			t.Fatalf("query=%q headers=%v body=%s", tc.query, w.Header(), w.Body.String())
		}
	}
}

func testRouter(t *testing.T) *gin.Engine {
	var cfg config.Config
	cfg.Server.Mode = "test"
	cfg.Upload.Directory = t.TempDir()
	return New(API{Config: cfg, Log: zap.NewNop()})
}
func TestRouterValidationAndAuthentication(t *testing.T) {
	r := testRouter(t)
	for _, tc := range []struct {
		method, path, body string
		status             int
	}{{"GET", "/user/me", "", 401}, {"POST", "/shop", "{}", 401}, {"POST", "/user/login", "{", 200}, {"GET", "/shop/zero", "", 200}, {"GET", "/shop/of/type?typeId=1&x=120", "", 200}, {"GET", "/shop/of/type?typeId=-1", "", 200}, {"GET", "/not-a-route", "", 404}} {
		t.Run(tc.path, func(t *testing.T) {
			w := httptest.NewRecorder()
			req := httptest.NewRequest(tc.method, tc.path, bytes.NewBufferString(tc.body))
			req.Header.Set("Content-Type", "application/json")
			r.ServeHTTP(w, req)
			if w.Code != tc.status {
				t.Fatalf("HTTP %d: %s", w.Code, w.Body)
			}
			var body struct {
				Success  bool
				ErrorMsg string
			}
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body.Success || body.ErrorMsg == "" {
				t.Fatalf("body=%s err=%v", w.Body, err)
			}
		})
	}
}
func TestHealthLive(t *testing.T) {
	r := testRouter(t)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/health/live", nil))
	if w.Code != 200 {
		t.Fatal(w.Code)
	}
}

func TestJavaRouteContractIsPresent(t *testing.T) {
	routes := map[string]bool{}
	for _, route := range testRouter(t).Routes() {
		routes[route.Method+" "+route.Path] = true
	}
	required := []string{
		"POST /user/code", "POST /user/login", "POST /user/logout", "GET /user/me",
		"GET /user/info/:id", "GET /user/:id", "POST /user/sign", "GET /user/sign/count",
		"GET /shop/:id", "POST /shop", "PUT /shop", "GET /shop/of/type", "GET /shop/of/name",
		"GET /shop-type/list", "POST /blog", "PUT /blog/like/:id", "GET /blog/of/me",
		"GET /blog/hot", "GET /blog/:id", "GET /blog/likes/:id", "GET /blog/of/user",
		"GET /blog/of/follow", "PUT /follow/:id/:isFollow", "GET /follow/or/not/:id",
		"GET /follow/common/:id", "POST /voucher", "POST /voucher/seckill",
		"GET /voucher/list/:shopId", "POST /voucher-order/seckill/:id",
		"POST /upload/blog", "DELETE /upload/blog/delete",
	}
	for _, route := range required {
		if !routes[route] {
			t.Errorf("missing Java-compatible route %s", route)
		}
	}
}

func TestImagePathRejectsTraversal(t *testing.T) {
	for _, name := range []string{"/blogs/1/../../x.png", "/blogs/1/test.svg", "/blogs/1/../2/image.jpg", "C:/private.png", "/blogs/1/file.png"} {
		if imageNamePattern.MatchString(name) {
			t.Fatal(name)
		}
	}
}
