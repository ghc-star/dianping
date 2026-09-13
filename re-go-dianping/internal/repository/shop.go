package repository

import (
	"context"

	"github.com/learning/go-dianping/internal/dto"
	"github.com/learning/go-dianping/internal/model"
)

//保存数据库连接，供查询方法使用

// 根据商铺ID查询数据库
func (r *Repository) FindById(ctx context.Context, id int64) (model.Shop, error) {
	var result model.Shop
	err := r.DB.WithContext(ctx).First(&result, id).Error
	return result, err
}

func (r *Repository) ShopCreate(ctx context.Context, shop *model.Shop) error {
	return r.DB.WithContext(ctx).Create(shop).Error
}

func (r *Repository) ShopTypeExists(
	ctx context.Context,
	id int64,
) (bool, error) {
	var count int64

	err := r.DB.WithContext(ctx).
		Model(&model.ShopType{}).
		Where("id = ?", id).
		Count(&count).
		Error

	return count > 0, err
}

// ShopChange 只更新请求里显式提供的字段。
// 用 map 而不是 struct：GORM 的 struct 更新会跳过零值字段，
// 显式传 sold:0 就会被吞掉；map 里有什么就更新什么。
func (r *Repository) ShopChange(ctx context.Context, in dto.ShopInput) error {
	updates := make(map[string]any)
	if in.Name != nil {
		updates["name"] = *in.Name
	}
	if in.TypeID != nil {
		updates["type_id"] = *in.TypeID
	}
	if in.Images != nil {
		updates["images"] = *in.Images
	}
	if in.Area != nil {
		updates["area"] = *in.Area
	}
	if in.Address != nil {
		updates["address"] = *in.Address
	}
	if in.X != nil {
		updates["x"] = *in.X
	}
	if in.Y != nil {
		updates["y"] = *in.Y
	}
	if in.AvgPrice != nil {
		updates["avg_price"] = *in.AvgPrice
	}
	if in.Sold != nil {
		updates["sold"] = *in.Sold
	}
	if in.Comments != nil {
		updates["comments"] = *in.Comments
	}
	if in.Score != nil {
		updates["score"] = *in.Score
	}
	if in.OpenHours != nil {
		updates["open_hours"] = *in.OpenHours
	}
	result := r.DB.WithContext(ctx).
		Model(&model.Shop{}).
		Where("id = ?", in.ID).
		Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	// MySQL 的 RowsAffected 是"发生变化的行数"，新旧值完全相同时
	// 也是 0；再查一次区分"商铺不存在"和"内容没有变化"。
	if result.RowsAffected == 0 {
		_, err := r.FindById(ctx, in.ID)
		return err
	}
	return nil
}
