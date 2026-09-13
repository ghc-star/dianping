package repository

import (
	"context"
	"errors"
	"github.com/learning/go-dianping/internal/model"
	"github.com/learning/go-dianping/pkg/apperror"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func (r *Repository) UserByPhone(ctx context.Context, phone string) (*model.User, error) {
	var user model.User
	err := r.DB.WithContext(ctx).Where("phone = ?", phone).First(&user).Error
	return &user, err
}
func (r *Repository) FindOrCreateUser(ctx context.Context, phone, nickname string) (*model.User, error) {
	user, err := r.UserByPhone(ctx, phone)
	if err == nil {
		return user, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	u := model.User{Phone: phone, NickName: nickname}
	// 手机号唯一索引解决并发注册；随后重新读，取得竞争胜出的一行。
	if err := r.DB.WithContext(ctx).Clauses(clause.OnConflict{DoNothing: true}).Create(&u).Error; err != nil {
		return nil, err
	}
	return r.UserByPhone(ctx, phone)
}
func (r *Repository) UserByID(ctx context.Context, id int64) (*model.User, error) {
	var user model.User
	if err := r.DB.WithContext(ctx).First(&user, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperror.ErrNotFound
		}
		return nil, err
	}
	return &user, nil
}
func (r *Repository) UserInfo(ctx context.Context, id int64) (*model.UserInfo, error) {
	var info model.UserInfo
	if err := r.DB.WithContext(ctx).Where("user_id = ?", id).First(&info).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &info, nil
}
func (r *Repository) SetPassword(ctx context.Context, id int64, password string) error {
	return r.DB.WithContext(ctx).Model(&model.User{}).Where("id = ?", id).Update("password", password).Error
}
