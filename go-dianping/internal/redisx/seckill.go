package redisx

import (
	"context"
	_ "embed"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

//go:embed seckill.lua
var seckillLua string

//go:embed initialize.lua
var initializeLua string

//go:embed finish.lua
var finishLua string

//go:embed reject.lua
var rejectLua string

//go:embed quarantine.lua
var quarantineLua string

var (
	SeckillScript    = redis.NewScript(seckillLua)
	InitializeScript = redis.NewScript(initializeLua)
	FinishScript     = redis.NewScript(finishLua)
	RejectScript     = redis.NewScript(rejectLua)
	QuarantineScript = redis.NewScript(quarantineLua)
)

// NextOrderID uses a UTC timestamp and an atomic daily sequence. IDs are int64;
// JavaScript clients should retain the string ID exposed by the status endpoint.
func NextOrderID(ctx context.Context, rdb *redis.Client) (int64, error) {
	now := time.Now().UTC()
	key := OrderIDSequence + now.Format("2006:01:02")
	n, err := rdb.Incr(ctx, key).Result()
	if err != nil {
		return 0, fmt.Errorf("generate order sequence: %w", err)
	}
	if n == 1 {
		if err := rdb.Expire(ctx, key, 72*time.Hour).Err(); err != nil {
			return 0, err
		}
	}
	seconds := now.Unix() - 1640995200
	if n <= 0 || n >= 1<<32 || seconds < 0 || seconds >= 1<<31 {
		return 0, fmt.Errorf("order ID timestamp or sequence exhausted")
	}
	return seconds<<32 | n, nil
}
