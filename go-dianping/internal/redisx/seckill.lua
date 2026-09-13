-- Validate every key type before the first write: Redis Lua is isolated but
-- does NOT roll back earlier writes after a runtime error.
local function kind(key) return redis.call('TYPE', key).ok end
if kind(KEYS[1]) ~= 'string' or kind(KEYS[2]) ~= 'hash' then return -1 end
if kind(KEYS[3]) ~= 'hash' then return -1 end
if kind(KEYS[4]) ~= 'none' and kind(KEYS[4]) ~= 'stream' then return -2 end
if kind(KEYS[5]) ~= 'none' then return -2 end
local stock = tonumber(redis.call('GET', KEYS[1]))
local begins = tonumber(redis.call('HGET', KEYS[2], 'begin'))
local ends = tonumber(redis.call('HGET', KEYS[2], 'end'))
if not stock or stock < 0 or stock ~= math.floor(stock) or not begins or not ends then return -1 end
local clock = redis.call('TIME')
local now = tonumber(clock[1]) * 1000 + math.floor(tonumber(clock[2]) / 1000)
if now < begins then return 3 end
if now > ends then return 4 end
if redis.call('HEXISTS', KEYS[3], ARGV[1]) == 1 then return 2 end
if stock <= 0 then return 1 end
-- Publish first: an XADD failure must never leave stock reserved without a message.
redis.call('XADD', KEYS[4], '*', 'userId', ARGV[1], 'voucherId', ARGV[2], 'id', ARGV[3])
redis.call('DECR', KEYS[1])
redis.call('HSET', KEYS[3], ARGV[1], ARGV[3])
redis.call('HSET', KEYS[5], 'userId', ARGV[1], 'voucherId', ARGV[2], 'id', ARGV[3], 'state', 'pending')
return 0
