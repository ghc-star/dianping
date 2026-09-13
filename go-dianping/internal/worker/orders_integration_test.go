package worker

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/learning/go-dianping/internal/model"
	"github.com/learning/go-dianping/internal/redisx"
	"github.com/learning/go-dianping/internal/repository"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// This opt-in test uses real MySQL transactions and Redis Lua/Stream commands.
// It requires migrated dedicated test services and deletes only its own fixtures.
func TestSeckillIntegration(t *testing.T) {
	dsn := os.Getenv("DIANPING_TEST_MYSQL_DSN")
	addr := os.Getenv("DIANPING_TEST_REDIS_ADDR")
	if dsn == "" || addr == "" {
		t.Skip("set DIANPING_TEST_MYSQL_DSN and DIANPING_TEST_REDIS_ADDR; Redis DB 15 is reserved for integration tests")
	}
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer sqlDB.Close()
	rdb := redis.NewClient(&redis.Options{Addr: addr, DB: 15})
	defer rdb.Close()
	ctx := context.Background()
	if err := rdb.Ping(ctx).Err(); err != nil {
		t.Fatal(err)
	}
	voucher := model.Voucher{ShopID: 1, Title: "integration-seckill-" + strconv.FormatInt(time.Now().UnixNano(), 10), PayValue: 100, ActualValue: 200, Type: 1, Status: 1}
	if err := db.Create(&voucher).Error; err != nil {
		t.Fatal(err)
	}
	campaign := model.SeckillVoucher{VoucherID: voucher.ID, Stock: 7, BeginTime: time.Now().Add(-time.Hour), EndTime: time.Now().Add(time.Hour)}
	if err := db.Create(&campaign).Error; err != nil {
		db.Delete(&voucher)
		t.Fatal(err)
	}
	userBase := time.Now().UnixMilli() * 100
	var mu sync.Mutex
	accepted := map[int64]int64{}
	var allIDs []int64
	defer func() {
		_ = db.Where("voucher_id = ?", voucher.ID).Delete(&model.VoucherOrder{}).Error
		_ = db.Where("voucher_id = ?", voucher.ID).Delete(&model.SeckillVoucher{}).Error
		_ = db.Delete(&voucher).Error
		keys := []string{redisx.Key(redisx.SeckillStockPrefix, voucher.ID), redisx.Key(redisx.SeckillMetaPrefix, voucher.ID), redisx.Key(redisx.SeckillBuyersPrefix, voucher.ID)}
		for _, id := range allIDs {
			keys = append(keys, redisx.Key(redisx.OrderStatusPrefix, id))
		}
		_ = rdb.Del(ctx, keys...).Err()
		for _, stream := range []string{redisx.OrderStream, redisx.OrderDeadStream} {
			messages, _ := rdb.XRange(ctx, stream, "-", "+").Result()
			for _, msg := range messages {
				if fmt.Sprint(msg.Values["voucherId"]) == strconv.FormatInt(voucher.ID, 10) {
					_ = rdb.XAck(ctx, stream, redisx.OrderGroup, msg.ID).Err()
					_ = rdb.XDel(ctx, stream, msg.ID).Err()
				}
			}
		}
	}()
	keys := []string{redisx.Key(redisx.SeckillStockPrefix, voucher.ID), redisx.Key(redisx.SeckillMetaPrefix, voucher.ID), redisx.Key(redisx.SeckillBuyersPrefix, voucher.ID), redisx.OrderStream}
	if err := redisx.InitializeScript.Run(ctx, rdb, keys, campaign.Stock, campaign.BeginTime.UnixMilli(), campaign.EndTime.UnixMilli(), "new").Err(); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := int64(1); i <= 40; i++ {
		for attempt := 0; attempt < 2; attempt++ {
			wg.Add(1)
			go func(userID int64) {
				defer wg.Done()
				orderID, err := redisx.NextOrderID(ctx, rdb)
				if err != nil {
					t.Error(err)
					return
				}
				mu.Lock()
				allIDs = append(allIDs, orderID)
				mu.Unlock()
				orderKeys := append(append([]string{}, keys...), redisx.Key(redisx.OrderStatusPrefix, orderID))
				n, err := redisx.SeckillScript.Run(ctx, rdb, orderKeys, userID, voucher.ID, orderID).Int()
				if err != nil {
					t.Error(err)
					return
				}
				if n == 0 {
					mu.Lock()
					if _, exists := accepted[userID]; exists {
						t.Error("same user accepted twice")
					}
					accepted[userID] = orderID
					mu.Unlock()
				} else if n != 1 && n != 2 {
					t.Errorf("unexpected admission code %d", n)
				}
			}(userBase + i)
		}
	}
	wg.Wait()
	if len(accepted) != 7 {
		t.Fatalf("accepted=%d, want 7", len(accepted))
	}
	if err := rdb.XGroupCreateMkStream(ctx, redisx.OrderStream, redisx.OrderGroup, "0").Err(); err != nil && err.Error() != "BUSYGROUP Consumer Group name already exists" {
		t.Fatal(err)
	}
	if _, err := rdb.XReadGroup(ctx, &redis.XReadGroupArgs{Group: redisx.OrderGroup, Consumer: "integration-crashed", Streams: []string{redisx.OrderStream, ">"}, Count: 1000, Block: time.Second}).Result(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(20 * time.Millisecond)
	w := New(repository.New(db), rdb, zap.NewNop(), Config{Concurrency: 1, ClaimIdle: 10 * time.Millisecond})
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- w.Run(runCtx) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("worker stop timeout")
		}
	}()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		created := 0
		for _, id := range accepted {
			if rdb.HGet(ctx, redisx.Key(redisx.OrderStatusPrefix, id), "state").Val() == "created" {
				created++
			}
		}
		if created == 7 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	var count int64
	if err := db.Model(&model.VoucherOrder{}).Where("voucher_id = ?", voucher.ID).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if count != 7 {
		t.Fatalf("persisted orders=%d, want 7", count)
	}
	if err := db.First(&campaign, "voucher_id = ?", voucher.ID).Error; err != nil {
		t.Fatal(err)
	}
	if campaign.Stock != 0 {
		t.Fatalf("database stock=%d", campaign.Stock)
	}
	for _, id := range accepted {
		if state := rdb.HGet(ctx, redisx.Key(redisx.OrderStatusPrefix, id), "state").Val(); state != "created" {
			t.Fatalf("order %d state=%s", id, state)
		}
	}
	// Re-publish one already ACKed order. A consumer must acknowledge its replay
	// even though database stock is now zero; it must not compensate or reinsert.
	for userID, orderID := range accepted {
		statusKey := redisx.Key(redisx.OrderStatusPrefix, orderID)
		if err := rdb.HSet(ctx, statusKey, "state", "pending").Err(); err != nil {
			t.Fatal(err)
		}
		if err := rdb.XAdd(ctx, &redis.XAddArgs{Stream: redisx.OrderStream, Values: map[string]any{"id": orderID, "userId": userID, "voucherId": voucher.ID}}).Err(); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) && rdb.HGet(ctx, statusKey, "state").Val() != "created" {
			time.Sleep(10 * time.Millisecond)
		}
		if rdb.HGet(ctx, statusKey, "state").Val() != "created" {
			t.Fatal("ACKed order replay was not idempotently accepted")
		}
		break
	}
	if err := db.First(&campaign, "voucher_id = ?", voucher.ID).Error; err != nil {
		t.Fatal(err)
	}
	if campaign.Stock != 0 {
		t.Fatalf("replay changed database stock to %d", campaign.Stock)
	}
	if stock := rdb.Get(ctx, keys[0]).Val(); stock != "0" {
		t.Fatalf("replay incorrectly compensated Redis stock to %s", stock)
	}
	if err := db.Model(&model.VoucherOrder{}).Where("voucher_id = ?", voucher.ID).Count(&count).Error; err != nil || count != 7 {
		t.Fatalf("replay count=%d, err=%v", count, err)
	}
}
