package repository

import (
	"context"
	"errors"
	"time"

	"github.com/learning/go-dianping/internal/dto"
	"github.com/learning/go-dianping/internal/model"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (r *Repository) UserByPhone(ctx context.Context, phone string) (*model.User, error) {
	var user model.User
	err := r.DB.WithContext(ctx).Where("phone=?", phone).First(&user).Error
	return &user, err
}

func (r *Repository) FindOrCreateUser(
	ctx context.Context,
	phone string,
	nickname string,
) (*model.User, error) {
	user, err := r.UserByPhone(ctx, phone)
	if err == nil {
		return user, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	user = &model.User{
		Phone:    phone,
		NickName: nickname,
	}
	err = r.DB.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(user).Error
	if err != nil {
		return nil, err
	}
	return r.UserByPhone(ctx, phone)
}

func (r *Repository) FindUser(
	ctx context.Context,
	id int64,
) (*dto.UserDTO, error) {
	var user dto.UserDTO
	result := r.DB.
		WithContext(ctx).
		Table("tb_user").
		Select("id", "nick_name", "icon").
		Where("id=?", id).
		Scan(&user)
	if result.Error != nil {
		return nil, result.Error
	}
	if result.RowsAffected == 0 {
		return nil, gorm.ErrRecordNotFound
	}
	return &user, nil
}

func (r *Repository) SaveUserInfo(
	ctx context.Context,
	id int64,
	updates map[string]any,
) error {
	values := map[string]any{
		"user_id":     id,
		"create_time": time.Now(),
		"update_time": time.Now(),
	}
	for key, value := range updates {
		values[key] = value
	}
	// 记录已存在时，只更新本次提交的字段。
	changes := make(map[string]any, len(updates)+1)
	for key, value := range updates {
		changes[key] = value
	}
	changes["update_time"] = time.Now()

	return r.DB.WithContext(ctx).
		Model(&model.UserInfo{}).
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "user_id"}},
			DoUpdates: clause.Assignments(changes),
		}).
		Create(values).Error
}

func (r *Repository) UserInfo(ctx context.Context, id int64) (*model.UserInfo, error) {
	var info model.UserInfo
	if err := r.DB.WithContext(ctx).Where("user_id=?", id).First(&info).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &info, nil
}

func (r *Repository) SaveUserName(ctx context.Context, id int64, updates map[string]any) error {
	if len(updates) == 0 {
		return nil
	}
	return r.DB.WithContext(ctx).
		Model(&model.User{}).
		Where("id = ?", id).
		Updates(updates).Error
}

func (r *Repository) SetPassword(
	ctx context.Context,
	id int64,
	passwordHash string,
) error {
	return r.DB.WithContext(ctx).
		Model(&model.User{}).
		Where("id = ?", id).
		Update("password", passwordHash).
		Error
}
