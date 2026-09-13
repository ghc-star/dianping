package repository

import (
	"context"
	"errors"
	"fmt"

	mysqlDriver "github.com/go-sql-driver/mysql"
	"github.com/learning/go-dianping/internal/model"
	"gorm.io/gorm"
)

var (
	ErrOrderStock        = errors.New("database stock exhausted")
	ErrOrderIdentity     = errors.New("order ID belongs to another purchase")
	ErrDuplicatePurchase = errors.New("user already purchased this voucher")
)

// OrderRejection is a definitive business outcome. Network errors, deadlocks,
// canceled contexts and unknown commit outcomes must stay pending for retry.
type OrderRejection struct {
	Cause           error
	ExistingOrderID int64
}

func (e *OrderRejection) Error() string { return e.Cause.Error() }
func (e *OrderRejection) Unwrap() error { return e.Cause }

func (r *Repository) OrderGet(ctx context.Context, id, userID int64) (*model.VoucherOrder, error) {
	var order model.VoucherOrder
	err := r.DB.WithContext(ctx).Where("id = ? AND user_id = ?", id, userID).First(&order).Error
	return &order, err
}

// OrderCreate atomically decrements stock and inserts an order. Duplicate
// delivery succeeds only when ID, user and voucher refer to the same purchase.
func (r *Repository) OrderCreate(ctx context.Context, order *model.VoucherOrder) error {
	err := r.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var existing model.VoucherOrder
		err := tx.First(&existing, "id = ?", order.ID).Error
		if err == nil {
			if existing.UserID == order.UserID && existing.VoucherID == order.VoucherID {
				return nil
			}
			return &OrderRejection{Cause: ErrOrderIdentity}
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		err = tx.Where("user_id = ? AND voucher_id = ?", order.UserID, order.VoucherID).First(&existing).Error
		if err == nil {
			return &OrderRejection{Cause: ErrDuplicatePurchase, ExistingOrderID: existing.ID}
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		// The affected-row check is the final protection against negative stock.
		result := tx.Model(&model.SeckillVoucher{}).Where("voucher_id = ? AND stock > 0", order.VoucherID).UpdateColumn("stock", gorm.Expr("stock - 1"))
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected != 1 {
			return &OrderRejection{Cause: ErrOrderStock}
		}
		if order.PayType == 0 {
			order.PayType = 1
		}
		if order.Status == 0 {
			order.Status = 1
		}
		return tx.Create(order).Error
	})
	if err == nil {
		return nil
	}
	var driverError *mysqlDriver.MySQLError
	stockRejected := errors.Is(err, ErrOrderStock)
	if !stockRejected && !errors.Is(err, gorm.ErrDuplicatedKey) && !(errors.As(err, &driverError) && driverError.Number == 1062) {
		return err
	}
	// The insert may race a different consumer after our read. Its duplicate
	// error rolled back the stock update; inspect in a NEW transaction snapshot.
	var existing model.VoucherOrder
	lookup := r.DB.WithContext(ctx).First(&existing, "id = ?", order.ID).Error
	if lookup == nil {
		if existing.UserID == order.UserID && existing.VoucherID == order.VoucherID {
			return nil
		}
		return &OrderRejection{Cause: ErrOrderIdentity}
	}
	if !errors.Is(lookup, gorm.ErrRecordNotFound) {
		return fmt.Errorf("resolve duplicate order: %w", lookup)
	}
	lookup = r.DB.WithContext(ctx).Where("user_id = ? AND voucher_id = ?", order.UserID, order.VoucherID).First(&existing).Error
	if lookup == nil {
		return &OrderRejection{Cause: ErrDuplicatePurchase, ExistingOrderID: existing.ID}
	}
	if stockRejected && errors.Is(lookup, gorm.ErrRecordNotFound) {
		return err
	}
	return fmt.Errorf("resolve duplicate purchase: %w", lookup)
}
