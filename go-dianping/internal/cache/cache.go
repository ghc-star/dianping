// Package cache implements bounded stale-while-revalidate Cache Aside.
package cache

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	mathrand "math/rand/v2"
	"sync"
	"time"

	"github.com/learning/go-dianping/pkg/apperror"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"golang.org/x/sync/singleflight"
)

type Keys struct{ Data, Lock, Version string }
type Loader func(context.Context) ([]byte, error)

type Options struct {
	TTL         time.Duration
	NullTTL     time.Duration
	StaleTTL    time.Duration
	LoadTimeout time.Duration
	Workers     int
}

func DefaultOptions() Options {
	return Options{TTL: 30 * time.Minute, NullTTL: 2 * time.Minute, StaleTTL: 5 * time.Minute, LoadTimeout: 5 * time.Second, Workers: 8}
}

type envelope struct {
	Data       json.RawMessage `json:"data,omitempty"`
	FreshUntil int64           `json:"freshUntil"`
	Missing    bool            `json:"missing,omitempty"`
}

type Client struct {
	rdb     *redis.Client
	log     *zap.Logger
	opt     Options
	root    context.Context
	cancel  context.CancelFunc
	flights singleflight.Group
	sem     chan struct{}
	mu      sync.Mutex
	closed  bool
	wg      sync.WaitGroup
}

func New(rdb *redis.Client, log *zap.Logger, root context.Context, opt Options) *Client {
	if log == nil {
		log = zap.NewNop()
	}
	if opt.TTL <= 0 {
		opt.TTL = 30 * time.Minute
	}
	if opt.NullTTL <= 0 {
		opt.NullTTL = 2 * time.Minute
	}
	if opt.StaleTTL <= 0 {
		opt.StaleTTL = 5 * time.Minute
	}
	if opt.LoadTimeout <= 0 {
		opt.LoadTimeout = 5 * time.Second
	}
	if opt.Workers <= 0 {
		opt.Workers = 8
	}
	ctx, cancel := context.WithCancel(root)
	return &Client{rdb: rdb, log: log, root: ctx, cancel: cancel, opt: opt, sem: make(chan struct{}, opt.Workers)}
}

func (c *Client) read(ctx context.Context, key string) (*envelope, error) {
	value, err := c.rdb.Get(ctx, key).Bytes()
	if err != nil {
		return nil, err
	}
	var item envelope
	if err = json.Unmarshal(value, &item); err != nil || item.FreshUntil == 0 || (!item.Missing && len(item.Data) == 0) {
		// Java cache envelopes and corrupt values are treated as misses on first use.
		return nil, redis.Nil
	}
	return &item, nil
}

func (c *Client) Get(ctx context.Context, keys Keys, load Loader) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	item, err := c.read(ctx, keys.Data)
	if err == nil {
		if item.Missing {
			return nil, apperror.ErrNotFound
		}
		if time.Now().UnixMilli() >= item.FreshUntil {
			c.refresh(keys, load)
		}
		return item.Data, nil
	}
	if !errors.Is(err, redis.Nil) {
		c.log.Warn("cache read failed; use database", zap.Error(err))
	}
	// Do runs in the leader request; unlike an unmanaged goroutine it has the request's deadline.
	value, err, _ := c.flights.Do(keys.Data, func() (any, error) {
		ctx, cancel := context.WithTimeout(ctx, c.opt.LoadTimeout)
		defer cancel()
		if item, e := c.read(ctx, keys.Data); e == nil {
			if item.Missing {
				return nil, apperror.ErrNotFound
			}
			return []byte(item.Data), nil
		}
		return c.loadCold(ctx, keys, load)
	})
	if err != nil {
		return nil, err
	}
	return value.([]byte), nil
}

func (c *Client) loadCold(ctx context.Context, keys Keys, load Loader) ([]byte, error) {
	token := rand.Text()
	for {
		locked, err := c.rdb.SetNX(ctx, keys.Lock, token, 2*c.opt.LoadTimeout).Result()
		if err != nil {
			// Redis is optional for cache reads. The singleflight group still merges local misses.
			return load(ctx)
		}
		if locked {
			defer c.unlock(keys.Lock, token)
			// A different process may have filled the cache just before we obtained the lock.
			if item, e := c.read(ctx, keys.Data); e == nil {
				if item.Missing {
					return nil, apperror.ErrNotFound
				}
				return item.Data, nil
			}
			return c.loadAndStore(ctx, keys, load)
		}
		timer := time.NewTimer(20 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
		if item, e := c.read(ctx, keys.Data); e == nil {
			if item.Missing {
				return nil, apperror.ErrNotFound
			}
			return item.Data, nil
		}
	}
}

var writeIfVersion = redis.NewScript(`
if (redis.call('GET', KEYS[2]) or '') ~= ARGV[1] then return 0 end
redis.call('SET', KEYS[1], ARGV[2], 'PX', ARGV[3])
return 1
`)

var invalidate = redis.NewScript(`
redis.call('INCR', KEYS[2])
redis.call('PEXPIRE', KEYS[2], ARGV[1])
redis.call('DEL', KEYS[1])
return 1
`)

var safeUnlock = redis.NewScript(`
if redis.call('GET', KEYS[1]) == ARGV[1] then return redis.call('DEL', KEYS[1]) end
return 0
`)

func (c *Client) loadAndStore(ctx context.Context, keys Keys, load Loader) ([]byte, error) {
	version, err := c.rdb.Get(ctx, keys.Version).Result()
	if err != nil && !errors.Is(err, redis.Nil) {
		return load(ctx)
	}
	data, loadErr := load(ctx)
	if loadErr != nil && !errors.Is(loadErr, apperror.ErrNotFound) {
		return nil, loadErr
	}
	if loadErr == nil && !json.Valid(data) {
		return nil, fmt.Errorf("cache loader returned invalid JSON")
	}
	ttl := jitter(c.opt.TTL)
	physicalTTL := ttl + c.opt.StaleTTL
	item := envelope{Data: data, FreshUntil: time.Now().Add(ttl).UnixMilli()}
	if errors.Is(loadErr, apperror.ErrNotFound) {
		physicalTTL = c.opt.NullTTL
		item = envelope{Missing: true, FreshUntil: time.Now().Add(physicalTTL).UnixMilli()}
	}
	encoded, err := json.Marshal(item)
	if err != nil {
		return nil, err
	}
	// An update increments Version and deletes Data atomically. A reader that started
	// before that update can finish, but cannot repopulate Redis with its old snapshot.
	if err = writeIfVersion.Run(ctx, c.rdb, []string{keys.Data, keys.Version}, version, encoded, physicalTTL.Milliseconds()).Err(); err != nil {
		c.log.Warn("cache fill failed", zap.Error(err))
	}
	return data, loadErr
}

func jitter(ttl time.Duration) time.Duration {
	spread := int64(ttl / 10)
	if spread <= 0 {
		return ttl
	}
	return ttl + time.Duration(mathrand.Int64N(spread))
}

func (c *Client) refresh(keys Keys, load Loader) {
	c.mu.Lock()
	if c.closed || c.root.Err() != nil {
		c.mu.Unlock()
		return
	}
	select {
	case c.sem <- struct{}{}:
	default:
		c.mu.Unlock()
		return
	}
	c.wg.Add(1)
	c.mu.Unlock()
	go func() {
		defer c.wg.Done()
		defer func() {
			<-c.sem
			if p := recover(); p != nil {
				c.log.Error("cache refresh panic", zap.Any("panic", p))
			}
		}()
		ctx, cancel := context.WithTimeout(c.root, c.opt.LoadTimeout)
		defer cancel()
		token := rand.Text()
		locked, err := c.rdb.SetNX(ctx, keys.Lock, token, 2*c.opt.LoadTimeout).Result()
		if err != nil || !locked {
			return
		}
		defer c.unlock(keys.Lock, token)
		if item, e := c.read(ctx, keys.Data); e == nil && time.Now().UnixMilli() < item.FreshUntil {
			return
		}
		if _, err = c.loadAndStore(ctx, keys, load); err != nil && !errors.Is(err, apperror.ErrNotFound) {
			c.log.Warn("cache refresh failed", zap.Error(err))
		}
	}()
}

func (c *Client) unlock(key, token string) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := safeUnlock.Run(ctx, c.rdb, []string{key}, token).Err(); err != nil {
		c.log.Warn("cache unlock failed", zap.Error(err))
	}
}

func (c *Client) Invalidate(ctx context.Context, keys Keys) error {
	return invalidate.Run(ctx, c.rdb, []string{keys.Data, keys.Version}, (24 * time.Hour).Milliseconds()).Err()
}

func (c *Client) Close() {
	// Add and closed are protected by the same mutex so Add never races with Wait.
	c.mu.Lock()
	c.closed = true
	c.cancel()
	c.mu.Unlock()
	c.wg.Wait()
}
