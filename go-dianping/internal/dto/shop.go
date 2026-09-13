package dto

import "github.com/learning/go-dianping/internal/model"

type ShopInput struct {
	ID        int64    `json:"id"`
	Name      *string  `json:"name" binding:"omitempty,min=1,max=128"`
	TypeID    *int64   `json:"typeId" binding:"omitempty,gt=0"`
	Images    *string  `json:"images" binding:"omitempty,max=1024"`
	Area      *string  `json:"area" binding:"omitempty,max=128"`
	Address   *string  `json:"address" binding:"omitempty,min=1,max=255"`
	X         *float64 `json:"x" binding:"omitempty,gte=-180,lte=180"`
	Y         *float64 `json:"y" binding:"omitempty,gte=-85.05112878,lte=85.05112878"`
	AvgPrice  *int64   `json:"avgPrice" binding:"omitempty,gte=0"`
	Sold      *int     `json:"sold" binding:"omitempty,gte=0"`
	Comments  *int     `json:"comments" binding:"omitempty,gte=0"`
	Score     *int     `json:"score" binding:"omitempty,gte=0,lte=50"`
	OpenHours *string  `json:"openHours" binding:"omitempty,max=32"`
}

// ApplyTo changes only explicitly supplied fields, preserving legitimate zero values.
func (in ShopInput) ApplyTo(shop *model.Shop) {
	if in.Name != nil {
		shop.Name = *in.Name
	}
	if in.TypeID != nil {
		shop.TypeID = *in.TypeID
	}
	if in.Images != nil {
		shop.Images = *in.Images
	}
	if in.Area != nil {
		shop.Area = *in.Area
	}
	if in.Address != nil {
		shop.Address = *in.Address
	}
	if in.X != nil {
		shop.X = *in.X
	}
	if in.Y != nil {
		shop.Y = *in.Y
	}
	if in.AvgPrice != nil {
		shop.AvgPrice = *in.AvgPrice
	}
	if in.Sold != nil {
		shop.Sold = *in.Sold
	}
	if in.Comments != nil {
		shop.Comments = *in.Comments
	}
	if in.Score != nil {
		shop.Score = *in.Score
	}
	if in.OpenHours != nil {
		shop.OpenHours = *in.OpenHours
	}
}

func (in ShopInput) HasChanges() bool {
	return in.Name != nil || in.TypeID != nil || in.Images != nil || in.Area != nil || in.Address != nil || in.X != nil || in.Y != nil || in.AvgPrice != nil || in.Sold != nil || in.Comments != nil || in.Score != nil || in.OpenHours != nil
}
