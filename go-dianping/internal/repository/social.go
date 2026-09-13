package repository

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/learning/go-dianping/internal/model"
	"github.com/learning/go-dianping/pkg/apperror"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// CreateSocialBlog commits the blog and every fan's delivery intent together.
// Redis outages after COMMIT cannot erase the intent to deliver a feed item.
func (r *Repository) CreateSocialBlog(ctx context.Context, blog *model.Blog) error {
	return r.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var count int64
		if err := tx.Model(&model.Shop{}).Where("id = ?", blog.ShopID).Count(&count).Error; err != nil {
			return err
		}
		if count == 0 {
			return apperror.ErrNotFound
		}
		if err := tx.Create(blog).Error; err != nil {
			return err
		}
		var fans []int64
		if err := tx.Model(&model.Follow{}).Where("follow_user_id = ?", blog.UserID).Distinct().Pluck("user_id", &fans).Error; err != nil {
			return err
		}
		outbox := make([]model.FeedOutbox, 0, len(fans))
		for _, fanID := range fans {
			outbox = append(outbox, model.FeedOutbox{UserID: fanID, BlogID: blog.ID, Score: blog.CreateTime.UnixMilli(), CreateTime: blog.CreateTime})
		}
		if len(outbox) > 0 {
			return tx.CreateInBatches(outbox, 500).Error
		}
		return nil
	})
}

func (r *Repository) SocialBlogByID(ctx context.Context, id int64) (*model.Blog, error) {
	var blog model.Blog
	err := r.DB.WithContext(ctx).First(&blog, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, apperror.ErrNotFound
	}
	return &blog, err
}

func (r *Repository) SocialBlogs(ctx context.Context, userID int64, current int, hot bool) ([]model.Blog, error) {
	blogs := make([]model.Blog, 0)
	q := r.DB.WithContext(ctx)
	if hot {
		q = q.Order("liked DESC, id DESC")
	} else {
		q = q.Where("user_id = ?", userID).Order("id DESC")
	}
	err := q.Offset((current - 1) * 10).Limit(10).Find(&blogs).Error
	return blogs, err
}

func (r *Repository) SocialBlogsByIDs(ctx context.Context, ids []int64) ([]model.Blog, error) {
	blogs := make([]model.Blog, 0, len(ids))
	if len(ids) == 0 {
		return blogs, nil
	}
	err := r.DB.WithContext(ctx).Where("id IN ?", ids).Find(&blogs).Error
	return blogs, err
}

func (r *Repository) SocialUsers(ctx context.Context, ids []int64) ([]model.User, error) {
	users := make([]model.User, 0, len(ids))
	if len(ids) == 0 {
		return users, nil
	}
	err := r.DB.WithContext(ctx).Where("id IN ?", ids).Find(&users).Error
	return users, err
}

func (r *Repository) SocialLikedIDs(ctx context.Context, userID int64, blogIDs []int64) ([]int64, error) {
	ids := make([]int64, 0)
	if userID == 0 || len(blogIDs) == 0 {
		return ids, nil
	}
	err := r.DB.WithContext(ctx).Model(&model.BlogLike{}).Where("user_id = ? AND blog_id IN ?", userID, blogIDs).Pluck("blog_id", &ids).Error
	return ids, err
}

func (r *Repository) ToggleSocialLike(ctx context.Context, userID, blogID int64) error {
	return r.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var blog model.Blog
		// Different API instances serialize on the same DB row. A Redis lease alone
		// cannot guarantee this once its TTL expires during a slow request.
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&blog, blogID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return apperror.ErrNotFound
			}
			return err
		}
		var count int64
		if err := tx.Model(&model.BlogLike{}).Where("blog_id = ? AND user_id = ?", blogID, userID).Count(&count).Error; err != nil {
			return err
		}
		if count == 0 {
			like := model.BlogLike{BlogID: blogID, UserID: userID, CreateTime: time.Now()}
			if err := tx.Create(&like).Error; err != nil {
				return err
			}
			return tx.Model(&blog).UpdateColumn("liked", gorm.Expr("COALESCE(liked, 0) + 1")).Error
		}
		if err := tx.Where("blog_id = ? AND user_id = ?", blogID, userID).Delete(&model.BlogLike{}).Error; err != nil {
			return err
		}
		return tx.Model(&blog).UpdateColumn("liked", gorm.Expr("CASE WHEN liked > 0 THEN liked - 1 ELSE 0 END")).Error
	})
}

// WithSocialLikes keeps the projection snapshot ordered with concurrent toggles.
// The callback must use a short context deadline because it holds a DB row lock.
func (r *Repository) WithSocialLikes(ctx context.Context, blogID int64, fn func([]model.BlogLike) error) error {
	return r.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var blog model.Blog
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&blog, blogID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return apperror.ErrNotFound
			}
			return err
		}
		likes := make([]model.BlogLike, 0)
		if err := tx.Where("blog_id = ?", blogID).Order("create_time ASC, user_id ASC").Find(&likes).Error; err != nil {
			return err
		}
		return fn(likes)
	})
}

func (r *Repository) SetSocialFollow(ctx context.Context, userID, targetID int64, follow bool) error {
	return r.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var user model.User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&user, userID).Error; err != nil {
			return err
		}
		var count int64
		if err := tx.Model(&model.User{}).Where("id = ?", targetID).Count(&count).Error; err != nil {
			return err
		}
		if count == 0 {
			return apperror.ErrNotFound
		}
		if follow {
			row := model.Follow{UserID: userID, FollowUserID: targetID, CreateTime: time.Now()}
			return tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&row).Error
		}
		return tx.Where("user_id = ? AND follow_user_id = ?", userID, targetID).Delete(&model.Follow{}).Error
	})
}

func (r *Repository) SocialIsFollow(ctx context.Context, userID, targetID int64) (bool, error) {
	var count int64
	err := r.DB.WithContext(ctx).Model(&model.Follow{}).Where("user_id = ? AND follow_user_id = ?", userID, targetID).Count(&count).Error
	return count > 0, err
}

func (r *Repository) WithSocialFollows(ctx context.Context, userIDs []int64, fn func(map[int64][]int64) error) error {
	ids := append([]int64(nil), userIDs...)
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return r.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		result := make(map[int64][]int64, len(ids))
		// Always lock users in the same order to avoid A/B versus B/A deadlocks.
		for _, id := range ids {
			if _, seen := result[id]; seen {
				continue
			}
			var user model.User
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&user, id).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return apperror.ErrNotFound
				}
				return err
			}
			result[id] = nil
		}
		// Acquire every user lock before the first consistent read. Under MySQL
		// REPEATABLE READ, an earlier snapshot could otherwise predate a second
		// user's committed mutation and overwrite its newer Redis projection.
		for id := range result {
			followIDs := make([]int64, 0)
			if err := tx.Model(&model.Follow{}).Where("user_id = ?", id).Order("follow_user_id ASC").Pluck("follow_user_id", &followIDs).Error; err != nil {
				return err
			}
			result[id] = followIDs
		}
		return fn(result)
	})
}

func (r *Repository) PendingSocialFeed(ctx context.Context, limit int) ([]model.FeedOutbox, error) {
	rows := make([]model.FeedOutbox, 0)
	err := r.DB.WithContext(ctx).Where("delivered_at IS NULL").Order("id ASC").Limit(limit).Find(&rows).Error
	return rows, err
}

func (r *Repository) SocialFeedDelivered(ctx context.Context, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	return r.DB.WithContext(ctx).Model(&model.FeedOutbox{}).Where("id IN ? AND delivered_at IS NULL", ids).Update("delivered_at", time.Now()).Error
}

// Delivered rows are retained as inbox history, so a lost Redis inbox can be rebuilt.
func (r *Repository) SocialFeedHistory(ctx context.Context, userID, afterID int64, limit int) ([]model.FeedOutbox, error) {
	rows := make([]model.FeedOutbox, 0)
	err := r.DB.WithContext(ctx).Where("user_id = ? AND id > ?", userID, afterID).Order("id ASC").Limit(limit).Find(&rows).Error
	return rows, err
}
