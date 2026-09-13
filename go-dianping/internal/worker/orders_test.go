package worker

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/alicebob/miniredis/v2"
	"github.com/learning/go-dianping/internal/redisx"
	"github.com/learning/go-dianping/internal/repository"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestParseOrderRejectsInvalidMessages(t *testing.T) {
	for _, values := range []map[string]any{{}, {"id": "-1", "userId": "42", "voucherId": "1"}, {"id": "101", "userId": "x", "voucherId": "1"}, {"id": "101", "userId": "42", "voucherId": "0"}} {
		if _, err := parseOrder(values); err == nil {
			t.Fatalf("accepted invalid message: %v", values)
		}
	}
}

func TestRestartRecoversOtherConsumerPendingAndStops(t *testing.T) {
	server := miniredis.RunT(t)
	rdb := redis.NewClient(&redis.Options{Addr: server.Addr()})
	defer rdb.Close()
	ctx := context.Background()
	messageID, err := rdb.XAdd(ctx, &redis.XAddArgs{Stream: redisx.OrderStream, Values: map[string]any{"id": "101", "userId": "42", "voucherId": "1"}}).Result()
	if err != nil {
		t.Fatal(err)
	}
	if err := rdb.XGroupCreate(ctx, redisx.OrderStream, redisx.OrderGroup, "0").Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := rdb.XReadGroup(ctx, &redis.XReadGroupArgs{Group: redisx.OrderGroup, Consumer: "previous-process", Streams: []string{redisx.OrderStream, ">"}, Count: 1}).Result(); err != nil {
		t.Fatal(err)
	}
	rdb.HSet(ctx, redisx.Key(redisx.OrderStatusPrefix, 101), "state", "pending", "userId", "42", "voucherId", "1", "id", "101")
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	gdb, err := gorm.Open(mysql.New(mysql.Config{Conn: db, SkipInitializeWithVersion: true}), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	// This fixture represents a crash after COMMIT and before ACK: no INSERT or
	// UPDATE is expected while the recovered pending message is processed.
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .* FROM `tb_voucher_order` WHERE id = ").WithArgs(int64(101), 1).WillReturnRows(sqlmock.NewRows([]string{"id", "user_id", "voucher_id"}).AddRow(101, 42, 1))
	mock.ExpectCommit()
	worker := New(repository.New(gdb), rdb, zap.NewNop(), Config{Concurrency: 1, ClaimIdle: time.Millisecond, ProcessTimeout: time.Second})
	time.Sleep(3 * time.Millisecond)
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- worker.Run(runCtx) }()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && rdb.HGet(ctx, redisx.Key(redisx.OrderStatusPrefix, 101), "state").Val() != "created" {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("worker did not stop after context cancel")
	}
	if state := rdb.HGet(ctx, redisx.Key(redisx.OrderStatusPrefix, 101), "state").Val(); state != "created" {
		t.Fatalf("message %s not recovered: %s", messageID, state)
	}
	if pending := rdb.XPending(ctx, redisx.OrderStream, redisx.OrderGroup).Val().Count; pending != 0 {
		t.Fatalf("pending=%d", pending)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestWorkerFailureKeepsPendingOrCompensatesByOutcome(t *testing.T) {
	for _, permanent := range []bool{false, true} {
		name := "transient database error"
		if permanent {
			name = "definitive stock rejection"
		}
		t.Run(name, func(t *testing.T) {
			server := miniredis.RunT(t)
			rdb := redis.NewClient(&redis.Options{Addr: server.Addr()})
			defer rdb.Close()
			ctx := context.Background()
			campaignKeys := []string{redisx.Key(redisx.SeckillStockPrefix, 1), redisx.Key(redisx.SeckillMetaPrefix, 1), redisx.Key(redisx.SeckillBuyersPrefix, 1), redisx.OrderStream}
			if err := redisx.InitializeScript.Run(ctx, rdb, campaignKeys, 1, time.Now().Add(-time.Hour).UnixMilli(), time.Now().Add(time.Hour).UnixMilli(), "new").Err(); err != nil {
				t.Fatal(err)
			}
			keys := append(append([]string{}, campaignKeys...), redisx.Key(redisx.OrderStatusPrefix, 101))
			if n, err := redisx.SeckillScript.Run(ctx, rdb, keys, 42, 1, 101).Int(); n != 0 || err != nil {
				t.Fatalf("admission=%d,%v", n, err)
			}
			if err := rdb.XGroupCreate(ctx, redisx.OrderStream, redisx.OrderGroup, "0").Err(); err != nil {
				t.Fatal(err)
			}
			streams, err := rdb.XReadGroup(ctx, &redis.XReadGroupArgs{Group: redisx.OrderGroup, Consumer: "test", Streams: []string{redisx.OrderStream, ">"}, Count: 1}).Result()
			if err != nil {
				t.Fatal(err)
			}
			message := streams[0].Messages[0]
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			gdb, err := gorm.Open(mysql.New(mysql.Config{Conn: db, SkipInitializeWithVersion: true}), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
			if err != nil {
				t.Fatal(err)
			}
			mock.ExpectBegin()
			query := mock.ExpectQuery("SELECT .* FROM `tb_voucher_order` WHERE id = ").WithArgs(int64(101), 1)
			if !permanent {
				query.WillReturnError(errors.New("temporary database outage"))
				mock.ExpectRollback()
			} else {
				empty := func() *sqlmock.Rows { return sqlmock.NewRows([]string{"id", "user_id", "voucher_id"}) }
				query.WillReturnRows(empty())
				mock.ExpectQuery("SELECT .* FROM `tb_voucher_order` WHERE user_id = ").WithArgs(int64(42), int64(1), 1).WillReturnRows(empty())
				mock.ExpectExec("UPDATE `tb_seckill_voucher` SET `stock`=stock - 1 WHERE voucher_id = ").WithArgs(int64(1)).WillReturnResult(sqlmock.NewResult(0, 0))
				mock.ExpectRollback()
				mock.ExpectQuery("SELECT .* FROM `tb_voucher_order` WHERE id = ").WithArgs(int64(101), 1).WillReturnRows(empty())
				mock.ExpectQuery("SELECT .* FROM `tb_voucher_order` WHERE user_id = ").WithArgs(int64(42), int64(1), 1).WillReturnRows(empty())
			}
			w := New(repository.New(gdb), rdb, zap.NewNop(), Config{})
			err = w.process(ctx, message)
			if permanent {
				if err != nil {
					t.Fatal(err)
				}
				if err := w.process(ctx, message); err != nil {
					t.Fatal(err)
				}
				if rdb.Get(ctx, campaignKeys[0]).Val() != "1" || rdb.HGet(ctx, keys[4], "state").Val() != "failed" || rdb.XLen(ctx, redisx.OrderDeadStream).Val() != 1 || rdb.XPending(ctx, redisx.OrderStream, redisx.OrderGroup).Val().Count != 0 {
					t.Fatal("permanent failure must compensate once, record failure and ACK")
				}
			} else {
				if err == nil {
					t.Fatal("expected transient error")
				}
				if rdb.Get(ctx, campaignKeys[0]).Val() != "0" || rdb.HGet(ctx, keys[4], "state").Val() != "pending" || rdb.XPending(ctx, redisx.OrderStream, redisx.OrderGroup).Val().Count != 1 {
					t.Fatal("transient error must retain reservation and Pending")
				}
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
