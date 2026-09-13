local function kind(key) return redis.call('TYPE', key).ok end
if kind(KEYS[1]) ~= 'string' or kind(KEYS[2]) ~= 'hash' or kind(KEYS[3]) ~= 'hash' or kind(KEYS[4]) ~= 'stream' then return redis.error_reply('incomplete reservation; reconciliation required') end
if kind(KEYS[5]) ~= 'none' and kind(KEYS[5]) ~= 'stream' then return redis.error_reply('invalid dead stream type') end
local state = redis.call('HGET', KEYS[3], 'state')
if state == 'failed' or state == 'created' then return redis.call('XACK', KEYS[4], ARGV[1], ARGV[2]) end
local stock = tonumber(redis.call('GET', KEYS[1]))
if not stock or stock ~= math.floor(stock) or stock < 0 then return redis.error_reply('invalid stock') end
if redis.call('HGET', KEYS[2], ARGV[4]) ~= ARGV[3] then return redis.error_reply('reservation owner mismatch') end
redis.call('XADD', KEYS[5], '*', 'messageId', ARGV[2], 'id', ARGV[3], 'userId', ARGV[4], 'voucherId', ARGV[5], 'error', ARGV[6])
redis.call('INCR', KEYS[1])
-- An existing persisted purchase must still prohibit another reservation.
if ARGV[7] ~= '' then redis.call('HSET', KEYS[2], ARGV[4], ARGV[7]) else redis.call('HDEL', KEYS[2], ARGV[4]) end
redis.call('HSET', KEYS[3], 'state', 'failed', 'error', ARGV[6])
return redis.call('XACK', KEYS[4], ARGV[1], ARGV[2])
