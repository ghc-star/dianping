package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/learning/go-dianping/internal/model"
	"github.com/learning/go-dianping/internal/redisx"
	"github.com/learning/go-dianping/internal/repository"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

type Config struct {
	Concurrency    int
	ClaimIdle      time.Duration
	ProcessTimeout time.Duration
}

type OrderWorker struct {
	repo           orderStore
	rdb            *redis.Client
	log            *zap.Logger
	cfg            Config
	consumerPrefix string
}

var workerSequence atomic.Uint64

func New(repo orderStore, rdb *redis.Client, log *zap.Logger, cfg Config) *OrderWorker {
	if cfg.Concurrency <= 0 {
		cfg.Concurrency = 2
	}
	if cfg.ClaimIdle <= 0 {
		cfg.ClaimIdle = 30 * time.Second
	}
	if cfg.ProcessTimeout <= 0 {
		cfg.ProcessTimeout = 10 * time.Second
	}
	if log == nil {
		log = zap.NewNop()
	}
	host, _ := os.Hostname()
	identity := fmt.Sprintf("%s-%d-%d-%d", host, os.Getpid(), time.Now().UnixNano(), workerSequence.Add(1))
	return &OrderWorker{repo: repo, rdb: rdb, log: log, cfg: cfg, consumerPrefix: identity}
}

// Run owns every consumer goroutine and waits for them when ctx is canceled.
// Group creation starts at 0 so pre-start accepted messages are never skipped.
func (w *OrderWorker) Run(ctx context.Context) error {
	err := w.rdb.XGroupCreateMkStream(ctx, redisx.OrderStream, redisx.OrderGroup, "0").Err()
	if err != nil && !strings.HasPrefix(err.Error(), "BUSYGROUP") {
		return fmt.Errorf("create order consumer group: %w", err)
	}
	var wg sync.WaitGroup
	for i := 0; i < w.cfg.Concurrency; i++ {
		wg.Add(1)
		go func(index int) { defer wg.Done(); w.consume(ctx, fmt.Sprintf("%s-%d", w.consumerPrefix, index)) }(i)
	}
	wg.Wait()
	return nil
}

func (w *OrderWorker) consume(ctx context.Context, consumer string) {
	claimCursor := "0-0"
	for ctx.Err() == nil {
		// XAUTOCLAIM also recovers messages belonging to a process that no longer
		// exists. Re-reading only this consumer's own pending entries would miss them.
		messages, next, err := w.rdb.XAutoClaim(ctx, &redis.XAutoClaimArgs{Stream: redisx.OrderStream, Group: redisx.OrderGroup, Consumer: consumer, MinIdle: w.cfg.ClaimIdle, Start: claimCursor, Count: 16}).Result()
		if err != nil && !errors.Is(err, redis.Nil) {
			if ctx.Err() != nil {
				return
			}
			w.log.Error("claim pending orders", zap.Error(err))
			if !wait(ctx, time.Second) {
				return
			}
			continue
		}
		claimCursor = next
		if claimCursor == "" {
			claimCursor = "0-0"
		}
		for _, message := range messages {
			w.handle(ctx, message)
		}
		streams, err := w.rdb.XReadGroup(ctx, &redis.XReadGroupArgs{Group: redisx.OrderGroup, Consumer: consumer, Streams: []string{redisx.OrderStream, ">"}, Count: 16, Block: time.Second}).Result()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			if errors.Is(err, redis.Nil) {
				continue
			}
			w.log.Error("read order stream", zap.Error(err))
			if !wait(ctx, time.Second) {
				return
			}
			continue
		}
		for _, stream := range streams {
			for _, message := range stream.Messages {
				w.handle(ctx, message)
			}
		}
	}
}

func wait(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}

func (w *OrderWorker) handle(parent context.Context, message redis.XMessage) {
	ctx, cancel := context.WithTimeout(parent, w.cfg.ProcessTimeout)
	defer cancel()
	if err := w.process(ctx, message); err != nil && parent.Err() == nil {
		w.log.Error("order remains pending for retry", zap.String("messageID", message.ID), zap.Error(err))
	}
}

func parseOrder(values map[string]any) (*model.VoucherOrder, error) {
	order := &model.VoucherOrder{}
	for name, dest := range map[string]*int64{"id": &order.ID, "userId": &order.UserID, "voucherId": &order.VoucherID} {
		value, ok := values[name]
		if !ok {
			return nil, fmt.Errorf("missing message field %s", name)
		}
		n, err := strconv.ParseInt(fmt.Sprint(value), 10, 64)
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("invalid message field %s", name)
		}
		*dest = n
	}
	return order, nil
}

func (w *OrderWorker) process(ctx context.Context, message redis.XMessage) (err error) {
	// Panic isolation belongs at the worker boundary; business failures use error.
	defer func() {
		if recovered := recover(); recovered != nil {
			w.log.Error("order worker panic", zap.Any("panic", recovered), zap.ByteString("stack", debug.Stack()))
			err = fmt.Errorf("order worker panic: %v", recovered)
		}
	}()
	order, err := parseOrder(message.Values)
	if err != nil {
		payload, _ := json.Marshal(message.Values)
		return redisx.QuarantineScript.Run(ctx, w.rdb, []string{redisx.OrderStream, redisx.OrderDeadStream}, redisx.OrderGroup, message.ID, err.Error(), string(payload)).Err()
	}
	statusKey := redisx.Key(redisx.OrderStatusPrefix, order.ID)
	state, stateErr := w.rdb.HGet(ctx, statusKey, "state").Result()
	if stateErr != nil && !errors.Is(stateErr, redis.Nil) {
		return stateErr
	}
	if state == "failed" {
		return w.rdb.XAck(ctx, redisx.OrderStream, redisx.OrderGroup, message.ID).Err()
	}
	err = w.repo.OrderCreate(ctx, order)
	if err == nil {
		// Redis failure after COMMIT leaves a pending entry. Its retry sees the
		// existing matching order and ACKs without a second stock deduction.
		return redisx.FinishScript.Run(ctx, w.rdb, []string{statusKey, redisx.OrderStream}, redisx.OrderGroup, message.ID, order.ID, order.UserID, order.VoucherID).Err()
	}
	var rejected *repository.OrderRejection
	if !errors.As(err, &rejected) {
		return err
	}
	existing := ""
	if rejected.ExistingOrderID > 0 {
		existing = strconv.FormatInt(rejected.ExistingOrderID, 10)
	}
	keys := []string{redisx.Key(redisx.SeckillStockPrefix, order.VoucherID), redisx.Key(redisx.SeckillBuyersPrefix, order.VoucherID), statusKey, redisx.OrderStream, redisx.OrderDeadStream}
	if err := redisx.RejectScript.Run(ctx, w.rdb, keys, redisx.OrderGroup, message.ID, order.ID, order.UserID, order.VoucherID, rejected.Error(), existing).Err(); err != nil {
		return err
	}
	w.log.Warn("order rejected and reservation compensated", zap.Int64("orderID", order.ID), zap.Error(rejected))
	return nil
}

// orderStore exposes only the persistence operations this consumer needs.
type orderStore interface {
	OrderCreate(ctx context.Context, order *model.VoucherOrder) error
}
