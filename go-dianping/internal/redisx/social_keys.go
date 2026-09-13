package redisx

const FeedReadyPrefix = "feed:ready:"

func BlogLikedKey(id int64) string { return Key(BlogLikedPrefix, id) }
func FollowSetKey(id int64) string { return Key(FollowPrefix, id) }
func FeedInboxKey(id int64) string { return Key(FeedPrefix, id) }
func FeedReadyKey(id int64) string { return Key(FeedReadyPrefix, id) }
