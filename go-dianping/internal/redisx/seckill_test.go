package redisx

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
)

func seckillClient(t *testing.T) *redis.Client {
	t.Helper()
	server := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = rdb.Close() })
	return rdb
}

func initCampaign(t *testing.T, rdb *redis.Client, stock int) {
	t.Helper()
	ctx := context.Background()
	now := time.Now()
	n, err := InitializeScript.Run(ctx, rdb, []string{Key(SeckillStockPrefix, 1), Key(SeckillMetaPrefix, 1), Key(SeckillBuyersPrefix, 1), OrderStream}, stock, now.Add(-time.Hour).UnixMilli(), now.Add(time.Hour).UnixMilli(), "new").Int()
	if err != nil || n != 1 {
		t.Fatalf("initialize = %d, %v", n, err)
	}
}

func admission(ctx context.Context, rdb *redis.Client, userID, orderID int64) (int, error) {
	return SeckillScript.Run(ctx, rdb, []string{Key(SeckillStockPrefix, 1), Key(SeckillMetaPrefix, 1), Key(SeckillBuyersPrefix, 1), OrderStream, Key(OrderStatusPrefix, orderID)}, userID, 1, orderID).Int()
}

func TestConcurrentSeckillNeverOversells(t *testing.T) {
	rdb := seckillClient(t)
	initCampaign(t, rdb, 10)
	ctx := context.Background()
	var won atomic.Int64
	var wg sync.WaitGroup
	for user := int64(1); user <= 100; user++ {
		wg.Add(1)
		go func(userID int64) {
			defer wg.Done()
			result, err := admission(ctx, rdb, userID, 1000+userID)
			if err != nil {
				t.Errorf("admission: %v", err)
				return
			}
			switch result {
			case 0:
				won.Add(1)
			case 1:
			default:
				t.Errorf("unexpected result %d", result)
			}
		}(user)
	}
	wg.Wait()
	if won.Load() != 10 {
		t.Fatalf("accepted %d orders, want 10", won.Load())
	}
	if stock := rdb.Get(ctx, Key(SeckillStockPrefix, 1)).Val(); stock != "0" {
		t.Fatalf("stock = %s", stock)
	}
	if n := rdb.XLen(ctx, OrderStream).Val(); n != 10 {
		t.Fatalf("stream length = %d", n)
	}
}

func TestSeckillDuplicateAndWrongStreamDoNotReserve(t *testing.T) {
	t.Run("duplicate", func(t *testing.T) {
		rdb := seckillClient(t)
		initCampaign(t, rdb, 2)
		ctx := context.Background()
		if n, err := admission(ctx, rdb, 42, 101); n != 0 || err != nil {
			t.Fatalf("first = %d, %v", n, err)
		}
		if n, err := admission(ctx, rdb, 42, 102); n != 2 || err != nil {
			t.Fatalf("duplicate = %d, %v", n, err)
		}
		if rdb.Get(ctx, Key(SeckillStockPrefix, 1)).Val() != "1" || rdb.XLen(ctx, OrderStream).Val() != 1 {
			t.Fatal("duplicate changed stock or stream")
		}
	})
	t.Run("wrong stream type", func(t *testing.T) {
		rdb := seckillClient(t)
		initCampaign(t, rdb, 2)
		ctx := context.Background()
		rdb.Set(ctx, OrderStream, "wrong", 0)
		if n, err := admission(ctx, rdb, 42, 101); n != -2 || err != nil {
			t.Fatalf("admission = %d, %v", n, err)
		}
		if rdb.Get(ctx, Key(SeckillStockPrefix, 1)).Val() != "2" || rdb.HExists(ctx, Key(SeckillBuyersPrefix, 1), "42").Val() {
			t.Fatal("script partially reserved stock")
		}
	})
}

func TestWindowAndLostBuyerStateFailClosed(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(context.Context, *redis.Client)
		want   int
	}{
		{"before", func(ctx context.Context, rdb *redis.Client) {
			rdb.HSet(ctx, Key(SeckillMetaPrefix, 1), "begin", time.Now().Add(time.Hour).UnixMilli())
		}, 3},
		{"after", func(ctx context.Context, rdb *redis.Client) {
			rdb.HSet(ctx, Key(SeckillMetaPrefix, 1), "end", time.Now().Add(-time.Hour).UnixMilli())
		}, 4},
		{"lost buyer hash", func(ctx context.Context, rdb *redis.Client) { rdb.Del(ctx, Key(SeckillBuyersPrefix, 1)) }, -1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rdb := seckillClient(t)
			initCampaign(t, rdb, 2)
			ctx := context.Background()
			tc.change(ctx, rdb)
			if n, err := admission(ctx, rdb, 42, 101); n != tc.want || err != nil {
				t.Fatalf("got %d, %v", n, err)
			}
			if rdb.Get(ctx, Key(SeckillStockPrefix, 1)).Val() != "2" {
				t.Fatal("rejected admission changed stock")
			}
		})
	}
}

func TestPermanentRejectionCompensatesExactlyOnce(t *testing.T) {
	rdb := seckillClient(t)
	initCampaign(t, rdb, 2)
	ctx := context.Background()
	if n, err := admission(ctx, rdb, 42, 101); n != 0 || err != nil {
		t.Fatalf("admission = %d, %v", n, err)
	}
	if err := rdb.XGroupCreate(ctx, OrderStream, OrderGroup, "0").Err(); err != nil {
		t.Fatal(err)
	}
	streams, err := rdb.XReadGroup(ctx, &redis.XReadGroupArgs{Group: OrderGroup, Consumer: "dead-consumer", Streams: []string{OrderStream, ">"}, Count: 1}).Result()
	if err != nil {
		t.Fatal(err)
	}
	messageID := streams[0].Messages[0].ID
	keys := []string{Key(SeckillStockPrefix, 1), Key(SeckillBuyersPrefix, 1), Key(OrderStatusPrefix, 101), OrderStream, OrderDeadStream}
	for attempt := 0; attempt < 2; attempt++ {
		if err := RejectScript.Run(ctx, rdb, keys, OrderGroup, messageID, 101, 42, 1, "database stock exhausted", "").Err(); err != nil {
			t.Fatal(err)
		}
	}
	if stock := rdb.Get(ctx, keys[0]).Val(); stock != "2" {
		t.Fatalf("stock after replay = %s", stock)
	}
	if n := rdb.XLen(ctx, OrderDeadStream).Val(); n != 1 {
		t.Fatalf("dead messages = %d", n)
	}
	if state := rdb.HGet(ctx, keys[2], "state").Val(); state != "failed" {
		t.Fatalf("state = %s", state)
	}
	if n := rdb.XPending(ctx, OrderStream, OrderGroup).Val().Count; n != 0 {
		t.Fatalf("pending = %d", n)
	}
}

func TestInitializationNeverResetsExistingReservations(t *testing.T) {
	rdb := seckillClient(t)
	initCampaign(t, rdb, 2)
	ctx := context.Background()
	_, _ = admission(ctx, rdb, 42, 101)
	keys := []string{Key(SeckillStockPrefix, 1), Key(SeckillMetaPrefix, 1), Key(SeckillBuyersPrefix, 1), OrderStream}
	n, err := InitializeScript.Run(ctx, rdb, keys, 999, 0, time.Now().Add(time.Hour).UnixMilli(), "warm").Int()
	if err != nil || n != 0 {
		t.Fatalf("warm=%d,%v", n, err)
	}
	if rdb.Get(ctx, keys[0]).Val() != "1" {
		t.Fatal("warm overwrote reserved stock")
	}
	rdb.Del(ctx, keys[0])
	n, err = InitializeScript.Run(ctx, rdb, keys, 999, 0, 0, "warm").Int()
	if err != nil || n != -1 {
		t.Fatalf("partial recovery=%d,%v", n, err)
	}
	if rdb.Exists(ctx, keys[0]).Val() != 0 {
		t.Fatal("partial recovery silently rebuilt stock")
	}
}

func TestOrderIDsAreUnique(t *testing.T) {
	rdb := seckillClient(t)
	seen := map[int64]bool{}
	for i := 0; i < 100; i++ {
		id, err := NextOrderID(context.Background(), rdb)
		if err != nil {
			t.Fatal(err)
		}
		if id <= 0 || seen[id] {
			t.Fatal(fmt.Sprintf("invalid/repeated ID %d", id))
		}
		seen[id] = true
	}
}
