package service

import (
	"context"
	"github.com/alicebob/miniredis/v2"
	"github.com/learning/go-dianping/internal/config"
	"github.com/learning/go-dianping/internal/redisx"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"testing"
	"time"
)

func userFixture(t *testing.T) (*UserService, *redis.Client, *miniredis.Miniredis) {
	t.Helper()
	mr := miniredis.RunT(t)
	r := redis.NewClient(&redis.Options{Addr: mr.Addr()})
	t.Cleanup(func() { r.Close() })
	var cfg config.Config
	cfg.Auth.CodeTTL = 2 * time.Minute
	cfg.Auth.SessionTTL = 30 * time.Minute
	cfg.Auth.CodeInterval = time.Minute
	cfg.Auth.DevCodeLog = true
	cfg.Timezone = "Asia/Shanghai"
	return NewUser(nil, r, zap.NewNop(), cfg), r, mr
}
func TestCodeThrottleAndSingleUse(t *testing.T) {
	s, r, _ := userFixture(t)
	ctx := context.Background()
	phone := "13900000001"
	if err := s.SendCode(ctx, phone); err != nil {
		t.Fatal(err)
	}
	if err := s.SendCode(ctx, phone); err == nil {
		t.Fatal("resend allowed")
	}
	code, e := r.Get(ctx, redisx.LoginCodePrefix+phone).Result()
	if e != nil || len(code) != 6 {
		t.Fatalf("code=%q %v", code, e)
	}
	keys := []string{redisx.LoginCodePrefix + phone, redisx.LoginAttemptsPrefix + phone}
	for _, want := range []int{1, 0} {
		got, e := verifyCodeScript.Run(ctx, r, keys, code, 120000).Int()
		if e != nil || got != want {
			t.Fatalf("verification=%d %v", got, e)
		}
	}
}
func TestSessionRefreshAndLogout(t *testing.T) {
	s, r, mr := userFixture(t)
	ctx := context.Background()
	key := redisx.LoginTokenPrefix + "token"
	r.HSet(ctx, key, "id", 1, "nickName", "learner", "icon", "")
	r.Expire(ctx, key, time.Minute)
	u, err := s.Session(ctx, "token")
	if err != nil || u == nil || u.ID != 1 {
		t.Fatalf("session=%v %v", u, err)
	}
	if mr.TTL(key) != 30*time.Minute {
		t.Fatal(mr.TTL(key))
	}
	if err = s.Logout(ctx, "token"); err != nil {
		t.Fatal(err)
	}
	u, err = s.Session(ctx, "token")
	if err != nil || u != nil {
		t.Fatal("logout did not revoke")
	}
}
func TestSignMonthAndTimezone(t *testing.T) {
	s, r, _ := userFixture(t)
	ctx := context.Background()
	s.now = func() time.Time { return time.Date(2026, 9, 1, 16, 1, 0, 0, time.UTC) }
	if e := s.Sign(ctx, 1); e != nil {
		t.Fatal(e)
	}
	bit, e := r.GetBit(ctx, "sign:1:202609", 1).Result()
	if e != nil || bit != 1 {
		t.Fatalf("Shanghai Sept 2 bit=%d %v", bit, e)
	}
	bit, _ = r.GetBit(ctx, "sign:1:202609", 0).Result()
	if bit != 0 {
		t.Fatal("wrong day was signed")
	}
}
func TestPhoneAndTrailingBits(t *testing.T) {
	if !ValidPhone("13900000001") || ValidPhone("10000000000") || ValidPhone("1390000000x") {
		t.Fatal("phone validation")
	}
	for bits, want := range map[uint64]int{0: 0, 1: 1, 7: 3, 11: 2, 14: 0} {
		if got := TrailingOnes(bits); got != want {
			t.Fatalf("%d=%d", bits, got)
		}
	}
}
