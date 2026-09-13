package cache

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"time"

	"github.com/redis/go-redis/v9"
)

type Keys struct {
	Data    string
	Lock    string
	Version string
}
type Client struct {
	rdb *redis.Client
}

func New(rdb *redis.Client) *Client {
	return &Client{rdb: rdb}
}

var invalidate = redis.NewScript(`
redis.call('INCR', KEYS[2])
redis.call('PEXPIRE', KEYS[2], ARGV[1])
redis.call('DEL', KEYS[1])
return 1
`)

func (c *Client) Invalidate(ctx context.Context, keys Keys) error {
	return invalidate.Run(
		ctx,
		c.rdb,
		[]string{keys.Data, keys.Version},
		(24 * time.Hour).Milliseconds(),
	).Err()
}

var StoreIfVersion = redis.NewScript(`
local current = redis.call('GET', KEYS[2]) or ''

if current ~= ARGV[1] then
    return 0
end

redis.call('SET', KEYS[1], ARGV[2], 'PX', ARGV[3])
return 1
`)

func (c *Client) SetIfVersion(
	ctx context.Context,
	keys Keys,
	version string,
	data []byte,
	ttl time.Duration,
) (bool, error) {
	result, err := StoreIfVersion.Run(
		ctx,
		c.rdb,
		[]string{keys.Data, keys.Version},
		version,
		data,
		ttl.Milliseconds(),
	).Int()

	if err != nil {
		return false, err
	}

	return result == 1, nil
}

func (c *Client) TryLock(
	ctx context.Context,
	key string,
	ttl time.Duration,
) (string, bool, error) {
	var data [16]byte
	if _, err := rand.Read(data[:]); err != nil {
		return "", false, err
	}
	token := hex.EncodeToString(data[:])
	ok, err := c.rdb.SetNX(ctx, key, token, ttl).Result()
	if err != nil {
		return "", false, err
	}
	return token, ok, nil
}
