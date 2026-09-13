package repository

import (
	"context"
	"time"

	"github.com/learning/go-dianping/internal/model"
	"gorm.io/gorm"
)

func (r *Repository) VoucherCreate(ctx context.Context, voucher *model.Voucher, seckill bool) error {
	return r.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(voucher).Error; err != nil {
			return err
		}
		if !seckill {
			return nil
		}
		campaign := model.SeckillVoucher{VoucherID: voucher.ID, Stock: *voucher.Stock, BeginTime: *voucher.BeginTime, EndTime: *voucher.EndTime}
		return tx.Create(&campaign).Error
	})
}

func (r *Repository) VoucherList(ctx context.Context, shopID int64) ([]model.Voucher, error) {
	// Scan into a dedicated projection: GORM intentionally ignores the virtual
	// stock/time fields on Voucher during inserts and ordinary model scans.
	type view struct {
		model.Voucher
		Stock              *int
		BeginTime, EndTime *time.Time
	}
	var rows []view
	err := r.DB.WithContext(ctx).Table("tb_voucher AS v").Select("v.*, sv.stock, sv.begin_time, sv.end_time").Joins("LEFT JOIN tb_seckill_voucher AS sv ON sv.voucher_id = v.id").Where("v.shop_id = ? AND v.status = 1", shopID).Order("v.id").Scan(&rows).Error
	vouchers := make([]model.Voucher, 0, len(rows))
	for _, row := range rows {
		row.Voucher.Stock = row.Stock
		row.Voucher.BeginTime = row.BeginTime
		row.Voucher.EndTime = row.EndTime
		vouchers = append(vouchers, row.Voucher)
	}
	return vouchers, err
}

func (r *Repository) SeckillVoucher(ctx context.Context, id int64) (*model.SeckillVoucher, error) {
	var voucher model.SeckillVoucher
	err := r.DB.WithContext(ctx).First(&voucher, "voucher_id = ?", id).Error
	return &voucher, err
}

func (r *Repository) SeckillVouchers(ctx context.Context) ([]model.SeckillVoucher, error) {
	var vouchers []model.SeckillVoucher
	err := r.DB.WithContext(ctx).Find(&vouchers).Error
	return vouchers, err
}

func (r *Repository) VoucherOrders(ctx context.Context, voucherID int64) ([]model.VoucherOrder, error) {
	var orders []model.VoucherOrder
	err := r.DB.WithContext(ctx).Select("id", "user_id", "voucher_id").Where("voucher_id = ?", voucherID).Find(&orders).Error
	return orders, err
}
