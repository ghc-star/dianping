package service

import (
	"context"
	"crypto/rand"
	"fmt"
	"math/big"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"
	"github.com/learning/go-dianping/internal/model"
	"github.com/learning/go-dianping/internal/redisx"
	"github.com/learning/go-dianping/internal/repository"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// These tests need the migrated, dedicated MySQL test database and Redis DB 12.
// They never FLUSHDB and remove only the users/shops/blogs created by this test.
func TestSocialIntegration(t *testing.T) {
	dsn, addr := os.Getenv("DIANPING_TEST_MYSQL_DSN"), os.Getenv("DIANPING_TEST_REDIS_ADDR")
	if dsn == "" || addr == "" {
		t.Skip("set DIANPING_TEST_MYSQL_DSN and DIANPING_TEST_REDIS_ADDR for real MySQL/Redis tests")
	}
	parsed, err := mysqldriver.ParseDSN(dsn)
	if err != nil || !strings.Contains(strings.ToLower(parsed.DBName), "test") {
		t.Fatal("integration DSN must name a dedicated database containing 'test'")
	}
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(12)
	t.Cleanup(func() { _ = sqlDB.Close() })
	rdb := redis.NewClient(&redis.Options{Addr: addr, Password: os.Getenv("DIANPING_TEST_REDIS_PASSWORD"), DB: 12})
	t.Cleanup(func() { _ = rdb.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	t.Cleanup(cancel)
	if err := rdb.Ping(ctx).Err(); err != nil {
		t.Fatal(err)
	}
	users := make([]model.User, 3)
	for i := range users {
		number, err := rand.Int(rand.Reader, big.NewInt(1000000000))
		if err != nil {
			t.Fatal(err)
		}
		users[i] = model.User{Phone: fmt.Sprintf("19%09d", number.Int64()), NickName: fmt.Sprintf("social_test_%d", i)}
		if err := db.WithContext(ctx).Create(&users[i]).Error; err != nil {
			t.Fatal(err)
		}
	}
	userIDs := []int64{users[0].ID, users[1].ID, users[2].ID}
	shop := model.Shop{Name: "Social integration fixture", TypeID: 1, Images: "/test.png", Address: "test only", X: 120, Y: 30}
	if err := db.WithContext(ctx).Create(&shop).Error; err != nil {
		t.Fatal(err)
	}
	blogIDs := make([]int64, 0)
	t.Cleanup(func() {
		cleanCtx, cleanCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cleanCancel()
		cleanDB := db.WithContext(cleanCtx)
		for _, operation := range []func() error{
			func() error { return cleanDB.Where("user_id IN ?", userIDs).Delete(&model.BlogLike{}).Error },
			func() error { return cleanDB.Where("user_id IN ?", userIDs).Delete(&model.FeedOutbox{}).Error },
			func() error {
				return cleanDB.Where("user_id IN ? OR follow_user_id IN ?", userIDs, userIDs).Delete(&model.Follow{}).Error
			},
			func() error { return cleanDB.Where("user_id IN ?", userIDs).Delete(&model.Blog{}).Error },
			func() error { return cleanDB.Delete(&model.Shop{}, shop.ID).Error },
			func() error { return cleanDB.Where("id IN ?", userIDs).Delete(&model.User{}).Error },
		} {
			if err := operation(); err != nil {
				t.Errorf("fixture cleanup: %v", err)
			}
		}
		keys := make([]string, 0)
		for _, id := range userIDs {
			keys = append(keys, redisx.FollowSetKey(id), redisx.FeedInboxKey(id), redisx.FeedReadyKey(id))
		}
		for _, id := range blogIDs {
			keys = append(keys, redisx.BlogLikedKey(id))
		}
		if err := rdb.Del(cleanCtx, keys...).Err(); err != nil {
			t.Errorf("Redis fixture cleanup: %v", err)
		}
	})
	svc := NewSocial(repository.New(db), rdb, zap.NewNop())
	newBlog := func() *model.Blog {
		return &model.Blog{ShopID: shop.ID, Title: "事务测试", Images: "/test.png", Content: "用于验证真实 MySQL 和 Redis"}
	}
	blogID, err := svc.CreateBlog(ctx, users[0].ID, newBlog())
	if err != nil {
		t.Fatal(err)
	}
	blogIDs = append(blogIDs, blogID)

	t.Run("concurrent toggles preserve count and unique relationship", func(t *testing.T) {
		var wg sync.WaitGroup
		errs := make(chan error, 12)
		for i := 0; i < 12; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				errs <- svc.LikeBlog(ctx, users[1].ID, blogID)
			}()
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
		var blog model.Blog
		if err := db.WithContext(ctx).First(&blog, blogID).Error; err != nil {
			t.Fatal(err)
		}
		var count int64
		if err := db.WithContext(ctx).Model(&model.BlogLike{}).Where("blog_id = ?", blogID).Count(&count).Error; err != nil {
			t.Fatal(err)
		}
		if blog.Liked != 0 || count != 0 {
			t.Fatalf("12 toggles must net to zero; liked=%d details=%d", blog.Liked, count)
		}
		if err := svc.LikeBlog(ctx, users[1].ID, blogID); err != nil {
			t.Fatal(err)
		}
		if err := rdb.Del(ctx, redisx.BlogLikedKey(blogID)).Err(); err != nil {
			t.Fatal(err)
		}
		likes, err := svc.BlogLikes(ctx, blogID)
		if err != nil || len(likes) != 1 || likes[0].ID != users[1].ID {
			t.Fatalf("lost ranking was not rebuilt: likes=%v err=%v", likes, err)
		}
	})

	t.Run("idempotent follows and restored Set intersection", func(t *testing.T) {
		for i := 0; i < 3; i++ {
			if err := svc.Follow(ctx, users[1].ID, users[0].ID, true); err != nil {
				t.Fatal(err)
			}
		}
		if err := svc.Follow(ctx, users[2].ID, users[0].ID, true); err != nil {
			t.Fatal(err)
		}
		var count int64
		if err := db.WithContext(ctx).Model(&model.Follow{}).Where("user_id = ? AND follow_user_id = ?", users[1].ID, users[0].ID).Count(&count).Error; err != nil {
			t.Fatal(err)
		}
		if count != 1 {
			t.Fatalf("repeated follow created %d rows", count)
		}
		if err := rdb.Del(ctx, redisx.FollowSetKey(users[1].ID), redisx.FollowSetKey(users[2].ID)).Err(); err != nil {
			t.Fatal(err)
		}
		common, err := svc.CommonFollows(ctx, users[1].ID, users[2].ID)
		if err != nil || len(common) != 1 || common[0].ID != users[0].ID {
			t.Fatalf("lost follow Set was not restored: common=%v err=%v", common, err)
		}
	})

	t.Run("Redis outage preserves outbox and worker recovers", func(t *testing.T) {
		unavailable := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1", DialTimeout: 20 * time.Millisecond, MaxRetries: -1})
		defer unavailable.Close()
		outageService := NewSocial(repository.New(db), unavailable, zap.NewNop())
		id, err := outageService.CreateBlog(ctx, users[0].ID, newBlog())
		if err != nil {
			t.Fatalf("committed blog must survive Redis outage: %v", err)
		}
		blogIDs = append(blogIDs, id)
		var pending int64
		if err := db.WithContext(ctx).Model(&model.FeedOutbox{}).Where("blog_id = ? AND delivered_at IS NULL", id).Count(&pending).Error; err != nil {
			t.Fatal(err)
		}
		if pending != 2 {
			t.Fatalf("want two persisted fan deliveries, got %d", pending)
		}
		workerCtx, stopWorker := context.WithCancel(ctx)
		done := make(chan struct{})
		go func() { defer close(done); svc.Run(workerCtx) }()
		defer func() {
			stopWorker()
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Error("outbox worker did not stop")
			}
		}()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if err := db.WithContext(ctx).Model(&model.FeedOutbox{}).Where("blog_id = ? AND delivered_at IS NULL", id).Count(&pending).Error; err != nil {
				t.Fatal(err)
			}
			if pending == 0 {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		if pending != 0 {
			t.Fatalf("worker left %d deliveries pending", pending)
		}
		max := time.Now().Add(time.Second).UnixMilli()
		feed, err := svc.Feed(ctx, users[1].ID, max, 0)
		if err != nil || feed == nil || len(feed.List) != 1 || feed.List[0].ID != id {
			t.Fatalf("delivered blog missing: feed=%v err=%v", feed, err)
		}
		// Reproduce partial eviction: the ready marker survives but the ZSet does not.
		if err := rdb.Del(ctx, redisx.FeedInboxKey(users[1].ID)).Err(); err != nil {
			t.Fatal(err)
		}
		feed, err = svc.Feed(ctx, users[1].ID, max, 0)
		if err != nil || feed == nil || len(feed.List) != 1 || feed.List[0].ID != id {
			t.Fatalf("inbox was not restored with surviving marker: feed=%v err=%v", feed, err)
		}
		// Simulate a crash after ZADD but before marking delivered, then redeliver.
		if err := db.WithContext(ctx).Model(&model.FeedOutbox{}).Where("blog_id = ?", id).Update("delivered_at", nil).Error; err != nil {
			t.Fatal(err)
		}
		if _, err := svc.deliverFeedBatch(ctx); err != nil {
			t.Fatal(err)
		}
		if size, err := rdb.ZCard(ctx, redisx.FeedInboxKey(users[1].ID)).Result(); err != nil || size != 1 {
			t.Fatalf("redelivery duplicated inbox: size=%d err=%v", size, err)
		}
		if _, err := rdb.ZScore(ctx, redisx.FeedInboxKey(users[1].ID), strconv.FormatInt(id, 10)).Result(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("seven same-millisecond items scroll without duplication", func(t *testing.T) {
		stamp := time.Now().Add(time.Hour).Truncate(time.Millisecond)
		want := make(map[int64]bool)
		for i := 0; i < 7; i++ {
			blog := newBlog()
			blog.UserID, blog.CreateTime, blog.UpdateTime = users[0].ID, stamp, stamp
			if err := svc.repo.CreateSocialBlog(ctx, blog); err != nil {
				t.Fatal(err)
			}
			blogIDs = append(blogIDs, blog.ID)
			want[blog.ID] = true
		}
		if _, err := svc.deliverFeedBatch(ctx); err != nil {
			t.Fatal(err)
		}
		seen := make(map[int64]bool)
		max, offset := stamp.UnixMilli(), 0
		for page := 0; page < 4; page++ {
			feed, err := svc.Feed(ctx, users[1].ID, max, offset)
			if err != nil || feed == nil {
				t.Fatalf("page %d missing: feed=%v err=%v", page, feed, err)
			}
			for _, blog := range feed.List {
				if seen[blog.ID] {
					t.Fatalf("page %d repeated Blog %d", page, blog.ID)
				}
				seen[blog.ID] = true
			}
			max, offset = feed.MinTime, feed.Offset
		}
		for id := range want {
			if !seen[id] {
				t.Errorf("same-millisecond Blog %d missing after four pages", id)
			}
		}
	})
}
