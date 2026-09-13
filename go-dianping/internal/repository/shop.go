package repository

import (
	"context"
	"errors"

	"github.com/learning/go-dianping/internal/dto"
	"github.com/learning/go-dianping/internal/model"
	"github.com/learning/go-dianping/pkg/apperror"
	"gorm.io/gorm"
)

func (r *Repository) ShopGet(ctx context.Context, id int64) (*model.Shop, error) {
	var shop model.Shop
	if err := r.DB.WithContext(ctx).First(&shop, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperror.ErrNotFound
		}
		return nil, err
	}
	return &shop, nil
}

func (r *Repository) ShopCreate(ctx context.Context, shop *model.Shop) error {
	return r.DB.WithContext(ctx).Create(shop).Error
}

func (r *Repository) ShopUpdate(ctx context.Context, id int64, input dto.ShopInput) error {
	// Column mapping belongs at the persistence boundary.
	updates := make(map[string]any)
	if input.Name != nil {
		updates["name"] = *input.Name
	}
	if input.TypeID != nil {
		updates["type_id"] = *input.TypeID
	}
	if input.Images != nil {
		updates["images"] = *input.Images
	}
	if input.Area != nil {
		updates["area"] = *input.Area
	}
	if input.Address != nil {
		updates["address"] = *input.Address
	}
	if input.X != nil {
		updates["x"] = *input.X
	}
	if input.Y != nil {
		updates["y"] = *input.Y
	}
	if input.AvgPrice != nil {
		updates["avg_price"] = *input.AvgPrice
	}
	if input.Sold != nil {
		updates["sold"] = *input.Sold
	}
	if input.Comments != nil {
		updates["comments"] = *input.Comments
	}
	if input.Score != nil {
		updates["score"] = *input.Score
	}
	if input.OpenHours != nil {
		updates["open_hours"] = *input.OpenHours
	}
	result := r.DB.WithContext(ctx).Model(&model.Shop{}).Where("id = ?", id).Updates(updates)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		_, err := r.ShopGet(ctx, id)
		return err
	}
	return nil
}

func (r *Repository) ShopListByType(ctx context.Context, typeID int64, page, size int) ([]model.Shop, error) {
	shops := make([]model.Shop, 0)
	err := r.DB.WithContext(ctx).Where("type_id = ?", typeID).Order("id ASC").Limit(size).Offset((page - 1) * size).Find(&shops).Error
	return shops, err
}

func (r *Repository) ShopListByName(ctx context.Context, name string, page, size int) ([]model.Shop, error) {
	shops := make([]model.Shop, 0)
	query := r.DB.WithContext(ctx)
	if name != "" {
		query = query.Where("name LIKE ?", "%"+name+"%")
	}
	err := query.Order("id ASC").Limit(size).Offset((page - 1) * size).Find(&shops).Error
	return shops, err
}

func (r *Repository) ShopListByIDs(ctx context.Context, ids []int64) ([]model.Shop, error) {
	shops := make([]model.Shop, 0)
	if len(ids) == 0 {
		return shops, nil
	}
	err := r.DB.WithContext(ctx).Where("id IN ?", ids).Find(&shops).Error
	return shops, err
}

func (r *Repository) ShopAll(ctx context.Context) ([]model.Shop, error) {
	shops := make([]model.Shop, 0)
	err := r.DB.WithContext(ctx).Order("id ASC").Find(&shops).Error
	return shops, err
}

func (r *Repository) ShopTypes(ctx context.Context) ([]model.ShopType, error) {
	types := make([]model.ShopType, 0)
	err := r.DB.WithContext(ctx).Order("sort ASC, id ASC").Find(&types).Error
	return types, err
}

func (r *Repository) ShopTypeExists(ctx context.Context, id int64) (bool, error) {
	var count int64
	err := r.DB.WithContext(ctx).Model(&model.ShopType{}).Where("id = ?", id).Count(&count).Error
	return count > 0, err
}
