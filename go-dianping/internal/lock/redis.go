package lock

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

var ErrNotOwner = errors.New("lock expired or belongs to another owner")
var unlock = redis.NewScript(`if redis.call('GET',KEYS[1]) == ARGV[1] then return redis.call('DEL',KEYS[1]) end return 0`)

type Lease struct {
	rdb        *redis.Client
	key, token string
}

// TryAcquire is deliberately non-reentrant and has no hidden watchdog goroutine.
// A lease reduces duplicate work; database constraints remain the correctness boundary.
func TryAcquire(ctx context.Context, rdb *redis.Client, key string, ttl time.Duration) (*Lease, bool, error) {
	if ttl <= 0 {
		return nil, false, fmt.Errorf("lock TTL must be positive")
	}
	var token [24]byte
	if _, err := rand.Read(token[:]); err != nil {
		return nil, false, err
	}
	lease := &Lease{rdb: rdb, key: key, token: hex.EncodeToString(token[:])}
	ok, err := rdb.SetNX(ctx, key, lease.token, ttl).Result()
	if err != nil || !ok {
		return nil, false, err
	}
	return lease, true, nil
}

func (l *Lease) Release(ctx context.Context) error {
	n, err := unlock.Run(ctx, l.rdb, []string{l.key}, l.token).Int()
	if err != nil {
		return fmt.Errorf("release lock: %w", err)
	}
	if n == 0 {
		return ErrNotOwner
	}
	return nil
}
