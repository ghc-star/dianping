local deadType = redis.call('TYPE', KEYS[2]).ok
if deadType ~= 'none' and deadType ~= 'stream' then return redis.error_reply('invalid dead stream type') end
if redis.call('TYPE', KEYS[1]).ok ~= 'stream' then return redis.error_reply('missing order stream') end
local pending = redis.call('XPENDING', KEYS[1], ARGV[1], ARGV[2], ARGV[2], 1)
if #pending == 0 then return 0 end
redis.call('XADD', KEYS[2], '*', 'messageId', ARGV[2], 'error', ARGV[3], 'payload', ARGV[4])
return redis.call('XACK', KEYS[1], ARGV[1], ARGV[2])
