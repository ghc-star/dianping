package cache

import (
	"context"
	"errors"
	"github.com/alicebob/miniredis/v2"
	"github.com/learning/go-dianping/pkg/apperror"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func fixture(t *testing.T) (*Client, *miniredis.Miniredis) {
	t.Helper()
	server := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { rdb.Close() })
	c := New(rdb, zap.NewNop(), context.Background(), DefaultOptions())
	t.Cleanup(c.Close)
	return c, server
}
func TestColdMissSingleflight(t *testing.T) {
	c, _ := fixture(t)
	var calls atomic.Int64
	load := func(context.Context) ([]byte, error) {
		calls.Add(1)
		time.Sleep(20 * time.Millisecond)
		return []byte(`{"id":1}`), nil
	}
	var wg sync.WaitGroup
	for i := 0; i < 30; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			value, err := c.Get(context.Background(), Keys{"data", "lock", "version"}, load)
			if err != nil || string(value) != `{"id":1}` {
				t.Errorf("get=%s,%v", value, err)
			}
		}()
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("loads=%d", calls.Load())
	}
}
func TestNegativeCacheExpires(t *testing.T) {
	c, r := fixture(t)
	calls := 0
	load := func(context.Context) ([]byte, error) { calls++; return nil, apperror.ErrNotFound }
	for i := 0; i < 2; i++ {
		_, err := c.Get(context.Background(), Keys{"missing", "lock", "version"}, load)
		if !errors.Is(err, apperror.ErrNotFound) {
			t.Fatal(err)
		}
	}
	if calls != 1 {
		t.Fatal(calls)
	}
	r.FastForward(3 * time.Minute)
	_, _ = c.Get(context.Background(), Keys{"missing", "lock", "version"}, load)
	if calls != 2 {
		t.Fatal(calls)
	}
}
func TestInvalidationPreventsStaleFill(t *testing.T) {
	c, r := fixture(t)
	keys := Keys{"data", "lock", "version"}
	load := func(ctx context.Context) ([]byte, error) {
		if err := c.Invalidate(ctx, keys); err != nil {
			return nil, err
		}
		return []byte(`{"name":"old"}`), nil
	}
	_, err := c.Get(context.Background(), keys, load)
	if err != nil {
		t.Fatal(err)
	}
	if r.Exists("data") {
		t.Fatal("old query repopulated invalidated data")
	}
}
func TestStaleRefreshAndClose(t *testing.T) {
	c, r := fixture(t)
	r.Set("data", `{"data":{"id":1},"freshUntil":1}`)
	r.SetTTL("data", time.Minute)
	done := make(chan struct{})
	var once sync.Once
	_, err := c.Get(context.Background(), Keys{"data", "lock", "version"}, func(context.Context) ([]byte, error) {
		defer once.Do(func() { close(done) })
		return []byte(`{"id":2}`), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("refresh did not run")
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		v, _ := r.Get("data")
		if strings.Contains(v, `"id":2`) {
			break
		}
		time.Sleep(time.Millisecond)
	}
	c.Close()
	value, e := c.Get(context.Background(), Keys{"data", "lock", "version"}, nil)
	if e != nil || string(value) != `{"id":2}` {
		t.Fatalf("refresh=%s,%v", value, e)
	}
}
