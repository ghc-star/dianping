package service

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/learning/go-dianping/internal/dto"
	"github.com/learning/go-dianping/internal/model"
	"github.com/learning/go-dianping/internal/redisx"
	"github.com/learning/go-dianping/pkg/apperror"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"golang.org/x/sync/singleflight"
)

const socialFeedPageSize = 2

type SocialService struct {
	repo    socialStore
	rdb     *redis.Client
	log     *zap.Logger
	inboxes singleflight.Group
}

func NewSocial(repo socialStore, rdb *redis.Client, log *zap.Logger) *SocialService {
	if log == nil {
		log = zap.NewNop()
	}
	return &SocialService{repo: repo, rdb: rdb, log: log}
}

func (s *SocialService) CreateBlog(ctx context.Context, userID int64, blog *model.Blog) (int64, error) {
	if userID <= 0 {
		return 0, apperror.ErrUnauthorized
	}
	if err := validateSocialBlog(blog); err != nil {
		return 0, err
	}
	// Only the client-editable fields cross the persistence boundary.
	now := time.Now().Truncate(time.Millisecond)
	row := &model.Blog{ShopID: blog.ShopID, UserID: userID, Title: strings.TrimSpace(blog.Title), Images: blog.Images, Content: blog.Content, CreateTime: now, UpdateTime: now}
	if err := s.repo.CreateSocialBlog(ctx, row); err != nil {
		return 0, fmt.Errorf("create blog with feed outbox: %w", err)
	}
	// This attempt gives the normal path immediate delivery. Failed deliveries
	// remain in the committed outbox for Run; they do not undo a published blog.
	if _, err := s.deliverFeedBatch(ctx); err != nil {
		s.log.Warn("blog saved; feed delivery will retry", zap.Int64("blog_id", row.ID), zap.Error(err))
	}
	return row.ID, nil
}

func validateSocialBlog(blog *model.Blog) error {
	if blog == nil || blog.ShopID <= 0 {
		return apperror.BadRequest("商铺 id 必须为正整数")
	}
	if strings.TrimSpace(blog.Title) == "" || utf8.RuneCountInString(blog.Title) > 255 {
		return apperror.BadRequest("笔记标题须为 1 至 255 个字符")
	}
	if strings.TrimSpace(blog.Content) == "" || utf8.RuneCountInString(blog.Content) > 2048 {
		return apperror.BadRequest("笔记内容须为 1 至 2048 个字符")
	}
	images := strings.Split(blog.Images, ",")
	if len(images) > 9 || utf8.RuneCountInString(blog.Images) > 2048 {
		return apperror.BadRequest("图片最多 9 张，总长度不超过 2048 个字符")
	}
	for _, item := range images {
		if strings.TrimSpace(item) == "" {
			return apperror.BadRequest("请上传至少一张图片，图片地址不能为空")
		}
	}
	return nil
}

func validSocialPage(current int) error {
	if current < 1 || current > 100000 {
		return apperror.BadRequest("current 须在 1 至 100000 之间")
	}
	return nil
}

func (s *SocialService) HotBlogs(ctx context.Context, current int, userID int64) ([]model.Blog, error) {
	if err := validSocialPage(current); err != nil {
		return nil, err
	}
	blogs, err := s.repo.SocialBlogs(ctx, 0, current, true)
	if err != nil {
		return nil, err
	}
	return blogs, s.enrichBlogs(ctx, blogs, userID)
}

func (s *SocialService) BlogByID(ctx context.Context, id, userID int64) (*model.Blog, error) {
	if id <= 0 {
		return nil, apperror.BadRequest("笔记 id 必须为正整数")
	}
	blog, err := s.repo.SocialBlogByID(ctx, id)
	if err != nil {
		return nil, err
	}
	blogs := []model.Blog{*blog}
	if err := s.enrichBlogs(ctx, blogs, userID); err != nil {
		return nil, err
	}
	return &blogs[0], nil
}

func (s *SocialService) BlogsByUser(ctx context.Context, id int64, current int) ([]model.Blog, error) {
	if id <= 0 {
		return nil, apperror.BadRequest("用户 id 必须为正整数")
	}
	if err := validSocialPage(current); err != nil {
		return nil, err
	}
	return s.repo.SocialBlogs(ctx, id, current, false)
}

func (s *SocialService) enrichBlogs(ctx context.Context, blogs []model.Blog, viewerID int64) error {
	if len(blogs) == 0 {
		return nil
	}
	userIDs := make([]int64, 0, len(blogs))
	blogIDs := make([]int64, 0, len(blogs))
	seen := make(map[int64]bool)
	for _, blog := range blogs {
		blogIDs = append(blogIDs, blog.ID)
		if !seen[blog.UserID] {
			userIDs = append(userIDs, blog.UserID)
			seen[blog.UserID] = true
		}
	}
	users, err := s.repo.SocialUsers(ctx, userIDs)
	if err != nil {
		return err
	}
	userByID := make(map[int64]model.User, len(users))
	for _, user := range users {
		userByID[user.ID] = user
	}
	likedIDs, err := s.repo.SocialLikedIDs(ctx, viewerID, blogIDs)
	if err != nil {
		return err
	}
	liked := make(map[int64]bool, len(likedIDs))
	for _, id := range likedIDs {
		liked[id] = true
	}
	for i := range blogs {
		user := userByID[blogs[i].UserID]
		blogs[i].Name, blogs[i].Icon, blogs[i].IsLike = user.NickName, user.Icon, liked[blogs[i].ID]
	}
	return nil
}

func (s *SocialService) LikeBlog(ctx context.Context, userID, blogID int64) error {
	if userID <= 0 {
		return apperror.ErrUnauthorized
	}
	if blogID <= 0 {
		return apperror.BadRequest("笔记 id 必须为正整数")
	}
	if err := s.repo.ToggleSocialLike(ctx, userID, blogID); err != nil {
		return err
	}
	if _, err := s.likeRanking(ctx, blogID); err != nil {
		s.log.Warn("like saved; Redis ranking will rebuild on read", zap.Int64("blog_id", blogID), zap.Error(err))
	}
	return nil
}

var replaceSocialZSet = redis.NewScript(`
redis.call('DEL', KEYS[1])
for i = 1, #ARGV, 2 do
  redis.call('ZADD', KEYS[1], ARGV[i], ARGV[i + 1])
end
return redis.call('ZRANGE', KEYS[1], 0, 4)
`)

// Rebuild from durable likes under the same DB row lock used by the toggle.
// Redis is a ranking projection: restart/outage must not erase who liked a blog.
func (s *SocialService) likeRanking(ctx context.Context, blogID int64) ([]int64, error) {
	ids := make([]int64, 0, 5)
	err := s.repo.WithSocialLikes(ctx, blogID, func(likes []model.BlogLike) error {
		args := make([]interface{}, 0, len(likes)*2)
		// Redis resolves equal scores lexicographically by member, not numerically.
		sort.Slice(likes, func(i, j int) bool {
			a, b := likes[i].CreateTime.UnixMilli(), likes[j].CreateTime.UnixMilli()
			if a == b {
				return strconv.FormatInt(likes[i].UserID, 10) < strconv.FormatInt(likes[j].UserID, 10)
			}
			return a < b
		})
		for i, like := range likes {
			args = append(args, like.CreateTime.UnixMilli(), strconv.FormatInt(like.UserID, 10))
			if i < 5 {
				ids = append(ids, like.UserID)
			}
		}
		redisCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		members, err := replaceSocialZSet.Run(redisCtx, s.rdb, []string{redisx.BlogLikedKey(blogID)}, args...).StringSlice()
		if err != nil {
			s.log.Warn("using persisted like ranking", zap.Int64("blog_id", blogID), zap.Error(err))
			return nil
		}
		ids, err = parseSocialIDs(members)
		return err
	})
	return ids, err
}

func (s *SocialService) BlogLikes(ctx context.Context, id int64) ([]dto.User, error) {
	if id <= 0 {
		return nil, apperror.BadRequest("笔记 id 必须为正整数")
	}
	ids, err := s.likeRanking(ctx, id)
	if err != nil {
		return nil, err
	}
	return s.orderedUsers(ctx, ids)
}

func (s *SocialService) orderedUsers(ctx context.Context, ids []int64) ([]dto.User, error) {
	result := make([]dto.User, 0, len(ids))
	users, err := s.repo.SocialUsers(ctx, ids)
	if err != nil {
		return nil, err
	}
	byID := make(map[int64]model.User, len(users))
	for _, user := range users {
		byID[user.ID] = user
	}
	for _, id := range ids {
		if user, exists := byID[id]; exists {
			result = append(result, dto.User{ID: user.ID, NickName: user.NickName, Icon: user.Icon})
		}
	}
	return result, nil
}

var replaceSocialSet = redis.NewScript(`
redis.call('DEL', KEYS[1])
for i = 1, #ARGV do redis.call('SADD', KEYS[1], ARGV[i]) end
return 1
`)

func (s *SocialService) Follow(ctx context.Context, userID, targetID int64, follow bool) error {
	if userID <= 0 {
		return apperror.ErrUnauthorized
	}
	if targetID <= 0 || userID == targetID {
		return apperror.BadRequest("关注目标必须存在，且不能是自己")
	}
	if err := s.repo.SetSocialFollow(ctx, userID, targetID, follow); err != nil {
		return err
	}
	if err := s.repo.WithSocialFollows(ctx, []int64{userID}, func(values map[int64][]int64) error {
		return s.mirrorFollows(ctx, values)
	}); err != nil {
		s.log.Warn("follow saved; Redis sets will rebuild on read", zap.Int64("user_id", userID), zap.Error(err))
	}
	return nil
}

func (s *SocialService) IsFollow(ctx context.Context, userID, targetID int64) (bool, error) {
	if userID <= 0 {
		return false, apperror.ErrUnauthorized
	}
	if targetID <= 0 {
		return false, apperror.BadRequest("用户 id 必须为正整数")
	}
	return s.repo.SocialIsFollow(ctx, userID, targetID)
}

func (s *SocialService) mirrorFollows(ctx context.Context, values map[int64][]int64) error {
	redisCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	for userID, ids := range values {
		args := make([]interface{}, 0, len(ids))
		for _, id := range ids {
			args = append(args, strconv.FormatInt(id, 10))
		}
		if err := replaceSocialSet.Run(redisCtx, s.rdb, []string{redisx.FollowSetKey(userID)}, args...).Err(); err != nil {
			return err
		}
	}
	return nil
}

func (s *SocialService) CommonFollows(ctx context.Context, userID, targetID int64) ([]dto.User, error) {
	if userID <= 0 {
		return nil, apperror.ErrUnauthorized
	}
	if targetID <= 0 {
		return nil, apperror.BadRequest("用户 id 必须为正整数")
	}
	ids := make([]int64, 0)
	err := s.repo.WithSocialFollows(ctx, []int64{userID, targetID}, func(values map[int64][]int64) error {
		ids = socialIntersection(values[userID], values[targetID])
		if err := s.mirrorFollows(ctx, values); err != nil {
			s.log.Warn("using persisted common follows", zap.Error(err))
			return nil
		}
		redisCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		members, err := s.rdb.SInter(redisCtx, redisx.FollowSetKey(userID), redisx.FollowSetKey(targetID)).Result()
		if err != nil {
			s.log.Warn("using persisted common follows", zap.Error(err))
			return nil
		}
		ids, err = parseSocialIDs(members)
		return err
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return s.orderedUsers(ctx, ids)
}

func socialIntersection(a, b []int64) []int64 {
	set := make(map[int64]bool, len(a))
	for _, id := range a {
		set[id] = true
	}
	result := make([]int64, 0)
	for _, id := range b {
		if set[id] {
			result = append(result, id)
			delete(set, id)
		}
	}
	return result
}

func parseSocialIDs(members []string) ([]int64, error) {
	ids := make([]int64, 0, len(members))
	for _, member := range members {
		id, err := parseSocialID(member)
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, nil
}

func (s *SocialService) Feed(ctx context.Context, userID, max int64, offset int) (*dto.ScrollResult, error) {
	if userID <= 0 {
		return nil, apperror.ErrUnauthorized
	}
	if max < 0 || offset < 0 || offset > 100000 {
		return nil, apperror.BadRequest("lastId 不能为负数，offset 须在 0 至 100000 之间")
	}
	if err := s.ensureInbox(ctx, userID); err != nil {
		return nil, err
	}
	tuples, err := s.rdb.ZRevRangeByScoreWithScores(ctx, redisx.FeedInboxKey(userID), &redis.ZRangeBy{Min: "0", Max: strconv.FormatInt(max, 10), Offset: int64(offset), Count: socialFeedPageSize}).Result()
	if err != nil {
		return nil, err
	}
	if len(tuples) == 0 {
		// Java returns Result.ok() with no data when the inbox is exhausted.
		return nil, nil
	}
	ids := make([]int64, 0, len(tuples))
	for _, tuple := range tuples {
		id, err := parseSocialID(fmt.Sprint(tuple.Member))
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	blogs, err := s.repo.SocialBlogsByIDs(ctx, ids)
	if err != nil {
		return nil, err
	}
	byID := make(map[int64]model.Blog, len(blogs))
	for _, blog := range blogs {
		byID[blog.ID] = blog
	}
	ordered := make([]model.Blog, 0, len(blogs))
	for _, id := range ids {
		if blog, exists := byID[id]; exists {
			ordered = append(ordered, blog)
		}
	}
	if err := s.enrichBlogs(ctx, ordered, userID); err != nil {
		return nil, err
	}
	minTime, nextOffset := nextSocialCursor(tuples, max, offset)
	return &dto.ScrollResult{List: ordered, MinTime: minTime, Offset: nextOffset}, nil
}

func nextSocialCursor(tuples []redis.Z, max int64, offset int) (int64, int) {
	if len(tuples) == 0 {
		return max, offset
	}
	minTime := int64(tuples[len(tuples)-1].Score)
	nextOffset := 0
	for i := len(tuples) - 1; i >= 0 && int64(tuples[i].Score) == minTime; i-- {
		nextOffset++
	}
	// If three pages have the same millisecond, the already-consumed offset
	// must accumulate. Resetting it to this page's count repeats page two.
	if minTime == max {
		nextOffset += offset
	}
	return minTime, nextOffset
}

func (s *SocialService) ensureInbox(ctx context.Context, userID int64) error {
	// The marker may survive eviction/deletion of the actual ZSet. Check both;
	// an empty inbox simply performs one empty, indexed history query on reads.
	ready, err := s.rdb.Exists(ctx, redisx.FeedReadyKey(userID), redisx.FeedInboxKey(userID)).Result()
	if err != nil || ready == 2 {
		return err
	}
	_, err, _ = s.inboxes.Do(strconv.FormatInt(userID, 10), func() (interface{}, error) {
		// Rebuild stays in the HTTP goroutine, with request cancellation and a
		// deadline. Concurrent callers share this attempt and may retry on cancel.
		rebuildCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		var afterID int64
		for {
			rows, err := s.repo.SocialFeedHistory(rebuildCtx, userID, afterID, 500)
			if err != nil {
				return nil, err
			}
			if len(rows) == 0 {
				break
			}
			members := make([]redis.Z, 0, len(rows))
			for _, row := range rows {
				members = append(members, redis.Z{Score: float64(row.Score), Member: strconv.FormatInt(row.BlogID, 10)})
				afterID = row.ID
			}
			if err := s.rdb.ZAdd(rebuildCtx, redisx.FeedInboxKey(userID), members...).Err(); err != nil {
				return nil, err
			}
		}
		return nil, s.rdb.Set(rebuildCtx, redisx.FeedReadyKey(userID), "1", 24*time.Hour).Err()
	})
	return err
}

func (s *SocialService) deliverFeedBatch(ctx context.Context) (int, error) {
	rows, err := s.repo.PendingSocialFeed(ctx, 100)
	if err != nil || len(rows) == 0 {
		return 0, err
	}
	pipe := s.rdb.Pipeline()
	ids := make([]int64, 0, len(rows))
	for _, row := range rows {
		pipe.ZAdd(ctx, redisx.FeedInboxKey(row.UserID), redis.Z{Score: float64(row.Score), Member: strconv.FormatInt(row.BlogID, 10)})
		ids = append(ids, row.ID)
	}
	if _, err := pipe.Exec(ctx); err != nil {
		return 0, err
	}
	// Mark only after Redis succeeds. A crash between ZADD and this update
	// repeats ZADD with the same member/score and therefore creates no duplicate.
	if err := s.repo.SocialFeedDelivered(ctx, ids); err != nil {
		return 0, err
	}
	return len(rows), nil
}

// Run blocks until shutdown; the application owns its goroutine and WaitGroup.
func (s *SocialService) Run(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		count, err := s.safeDeliverFeed(ctx)
		if err != nil && ctx.Err() == nil {
			s.log.Error("feed outbox delivery failed; will retry", zap.Error(err))
		}
		if err == nil && count == 100 {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *SocialService) safeDeliverFeed(ctx context.Context) (count int, err error) {
	defer func() {
		if value := recover(); value != nil {
			err = fmt.Errorf("feed worker panic: %v", value)
		}
	}()
	return s.deliverFeedBatch(ctx)
}
func parseSocialID(member string) (int64, error) {
	id, err := strconv.ParseInt(member, 10, 64)
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("invalid social Redis member %q", member)
	}
	return id, nil
}

// socialStore exposes only the persistence operations this consumer needs.
type socialStore interface {
	CreateSocialBlog(ctx context.Context, blog *model.Blog) error
	SocialBlogByID(ctx context.Context, id int64) (*model.Blog, error)
	SocialBlogs(ctx context.Context, userID int64, current int, hot bool) ([]model.Blog, error)
	SocialBlogsByIDs(ctx context.Context, ids []int64) ([]model.Blog, error)
	SocialUsers(ctx context.Context, ids []int64) ([]model.User, error)
	SocialLikedIDs(ctx context.Context, userID int64, blogIDs []int64) ([]int64, error)
	ToggleSocialLike(ctx context.Context, userID, blogID int64) error
	WithSocialLikes(ctx context.Context, blogID int64, fn func([]model.BlogLike) error) error
	SetSocialFollow(ctx context.Context, userID, targetID int64, follow bool) error
	SocialIsFollow(ctx context.Context, userID, targetID int64) (bool, error)
	WithSocialFollows(ctx context.Context, userIDs []int64, fn func(map[int64][]int64) error) error
	PendingSocialFeed(ctx context.Context, limit int) ([]model.FeedOutbox, error)
	SocialFeedDelivered(ctx context.Context, ids []int64) error
	SocialFeedHistory(ctx context.Context, userID, afterID int64, limit int) ([]model.FeedOutbox, error)
}
