// Package redisx 集中声明 Redis 键；业务代码只组合键，不复制前缀。
package redisx

import "strconv"

const (
	LoginCodePrefix     = "login:code:"
	LoginTokenPrefix    = "login:token:"
	LoginThrottlePrefix = "login:throttle:"
	LoginAttemptsPrefix = "login:attempts:"
	ShopCachePrefix     = "cache:shop:"
	ShopLockPrefix      = "lock:shop:"
	ShopTypeKey         = "shop_type:"
	BlogLikedPrefix     = "blog:liked:"
	FollowPrefix        = "follows:"
	FeedPrefix          = "feed:"
	ShopGeoPrefix       = "shop:geo:"
	SignPrefix          = "sign:"
)

func Key(prefix string, id int64) string { return prefix + strconv.FormatInt(id, 10) }
