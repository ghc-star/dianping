package dto

import "github.com/learning/go-dianping/internal/model"

type ShopInput struct {
	ID        int64    `json:"id"`
	Name      *string  `json:"name"`
	TypeID    *int64   `json:"typeId"`
	Images    *string  `json:"images"`
	Area      *string  `json:"area"`
	Address   *string  `json:"address"`
	X         *float64 `json:"x"`
	Y         *float64 `json:"y"`
	AvgPrice  *int64   `json:"avgPrice"`
	Sold      *int     `json:"sold"`
	Comments  *int     `json:"comments"`
	Score     *int     `json:"score"`
	OpenHours *string  `json:"openHours"`
}

func (in ShopInput) ApplyTo(shop *model.Shop) {
	if in.Name != nil {
		shop.Name = *in.Name
	}

	if in.TypeID != nil {
		shop.TypeID = *in.TypeID
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

	if in.Images != nil {
		shop.Images = *in.Images
	}
	if in.Area != nil {
		shop.Area = *in.Area
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

// HasChanges 报告请求是否携带了至少一个待更新字段。
func (in ShopInput) HasChanges() bool {
	return in.Name != nil ||
		in.TypeID != nil ||
		in.Images != nil ||
		in.Area != nil ||
		in.Address != nil ||
		in.X != nil ||
		in.Y != nil ||
		in.AvgPrice != nil ||
		in.Sold != nil ||
		in.Comments != nil ||
		in.Score != nil ||
		in.OpenHours != nil
}
