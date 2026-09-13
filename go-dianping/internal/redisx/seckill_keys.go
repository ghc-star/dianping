package redisx

// All keys touched by one Lua script share a Redis Cluster hash slot.
const (
	SeckillStockPrefix  = "seckill:{seckill}:stock:"
	SeckillBuyersPrefix = "seckill:{seckill}:buyers:"
	SeckillMetaPrefix   = "seckill:{seckill}:meta:"
	OrderStatusPrefix   = "seckill:{seckill}:status:"
	OrderStream         = "stream:{seckill}:orders"
	OrderDeadStream     = "stream:{seckill}:dead"
	OrderGroup          = "orders"
	OrderIDSequence     = "seckill:{seckill}:id:"
)
