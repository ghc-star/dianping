local stateType = redis.call('TYPE', KEYS[1]).ok
if stateType ~= 'none' and stateType ~= 'hash' then return redis.error_reply('invalid order status type') end
if redis.call('TYPE', KEYS[2]).ok ~= 'stream' then return redis.error_reply('missing order stream') end
redis.call('HSET', KEYS[1], 'id', ARGV[3], 'userId', ARGV[4], 'voucherId', ARGV[5], 'state', 'created')
redis.call('HDEL', KEYS[1], 'error')
return redis.call('XACK', KEYS[2], ARGV[1], ARGV[2])
