package lock

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func TestExpiredOwnerCannotUnlockNewOwner(t *testing.T) {
	server := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: server.Addr()})
	defer rdb.Close()
	ctx := context.Background()
	first, ok, err := TryAcquire(ctx, rdb, "lock:test", time.Second)
	if err != nil || !ok {
		t.Fatalf("first acquire: %v, %v", ok, err)
	}
	if _, ok, err := TryAcquire(ctx, rdb, "lock:test", time.Second); err != nil || ok {
		t.Fatalf("contended acquire: %v, %v", ok, err)
	}
	server.FastForward(2 * time.Second)
	second, ok, err := TryAcquire(ctx, rdb, "lock:test", time.Second)
	if err != nil || !ok {
		t.Fatalf("second acquire: %v, %v", ok, err)
	}
	if err := first.Release(ctx); !errors.Is(err, ErrNotOwner) {
		t.Fatalf("stale release = %v", err)
	}
	if !server.Exists("lock:test") {
		t.Fatal("stale owner deleted new owner's lock")
	}
	if err := second.Release(ctx); err != nil {
		t.Fatal(err)
	}
}
