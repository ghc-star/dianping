local function kind(key) return redis.call('TYPE', key).ok end
local stockKind = kind(KEYS[1])
local metaKind = kind(KEYS[2])
local buyerKind = kind(KEYS[3])
if stockKind == 'string' and metaKind == 'hash' and buyerKind == 'hash' then return 0 end
if stockKind ~= 'none' or metaKind ~= 'none' or buyerKind ~= 'none' then return -1 end
-- Only a newly-created voucher may be initialized while a stream already exists.
-- A missing campaign beside an existing stream may still have accepted orders.
if ARGV[4] ~= 'new' and kind(KEYS[4]) ~= 'none' then return -2 end
redis.call('SET', KEYS[1], ARGV[1])
redis.call('HSET', KEYS[2], 'begin', ARGV[2], 'end', ARGV[3])
redis.call('HSET', KEYS[3], '__initialized', '1')
for i = 5, #ARGV, 2 do redis.call('HSET', KEYS[3], ARGV[i], ARGV[i+1]) end
return 1
