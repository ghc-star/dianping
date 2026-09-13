package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/learning/go-dianping/internal/model"
	"github.com/learning/go-dianping/internal/redisx"
	"github.com/learning/go-dianping/pkg/apperror"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

type VoucherService struct {
	repo voucherStore
	rdb  *redis.Client
	log  *zap.Logger
}

func NewVoucher(repo voucherStore, rdb *redis.Client, log *zap.Logger) *VoucherService {
	return &VoucherService{repo: repo, rdb: rdb, log: log}
}

func (s *VoucherService) Create(ctx context.Context, voucher *model.Voucher, seckill bool) (int64, error) {
	if voucher.ShopID <= 0 || voucher.Title == "" || len([]rune(voucher.Title)) > 255 || voucher.PayValue <= 0 || voucher.ActualValue <= 0 {
		return 0, apperror.BadRequest("商铺、标题、金额不合法；金额单位为分")
	}
	if len([]rune(voucher.SubTitle)) > 255 || len([]rune(voucher.Rules)) > 1024 {
		return 0, apperror.BadRequest("优惠券副标题或规则过长")
	}
	voucher.ID = 0
	voucher.Type = 0
	voucher.Status = 1
	if seckill {
		if voucher.Stock == nil || *voucher.Stock <= 0 || *voucher.Stock > 2147483647 || voucher.BeginTime == nil || voucher.EndTime == nil || !voucher.EndTime.After(*voucher.BeginTime) {
			return 0, apperror.BadRequest("秒杀库存须为正整数，结束时间须晚于开始时间")
		}
		voucher.Type = 1
	}
	if err := s.repo.VoucherCreate(ctx, voucher, seckill); err != nil {
		return 0, fmt.Errorf("create voucher: %w", err)
	}
	if seckill {
		campaign := model.SeckillVoucher{VoucherID: voucher.ID, Stock: *voucher.Stock, BeginTime: *voucher.BeginTime, EndTime: *voucher.EndTime}
		if err := s.initialize(ctx, campaign, true); err != nil {
			return voucher.ID, fmt.Errorf("voucher %d persisted but Redis initialization failed: %w", voucher.ID, err)
		}
	}
	return voucher.ID, nil
}

func (s *VoucherService) List(ctx context.Context, shopID int64) ([]model.Voucher, error) {
	if shopID <= 0 {
		return nil, apperror.BadRequest("商铺ID不合法")
	}
	return s.repo.VoucherList(ctx, shopID)
}

// Warm initializes a cold Redis from committed data and otherwise preserves its
// reservations. Partial loss is an error, never permission to reset live stock.
func (s *VoucherService) Warm(ctx context.Context) error {
	vouchers, err := s.repo.SeckillVouchers(ctx)
	if err != nil {
		return err
	}
	for _, v := range vouchers {
		if err := s.initialize(ctx, v, false); err != nil {
			return fmt.Errorf("warm voucher %d: %w", v.VoucherID, err)
		}
	}
	return nil
}

func (s *VoucherService) initialize(ctx context.Context, v model.SeckillVoucher, newVoucher bool) error {
	mode := "warm"
	if newVoucher {
		mode = "new"
	}
	keys := []string{redisx.Key(redisx.SeckillStockPrefix, v.VoucherID), redisx.Key(redisx.SeckillMetaPrefix, v.VoucherID), redisx.Key(redisx.SeckillBuyersPrefix, v.VoucherID), redisx.OrderStream}
	args := []any{v.Stock, v.BeginTime.UnixMilli(), v.EndTime.UnixMilli(), mode}
	// Persisted buyers seed the one-user-one-order index on a genuinely cold start.
	orders, err := s.repo.VoucherOrders(ctx, v.VoucherID)
	if err != nil {
		return err
	}
	for _, o := range orders {
		args = append(args, o.UserID, o.ID)
	}
	n, err := redisx.InitializeScript.Run(ctx, s.rdb, keys, args...).Int()
	if err != nil {
		return err
	}
	if n < 0 {
		return fmt.Errorf("incomplete Redis seckill state (%d); restore Redis or reconcile accepted stream orders before reopening sales", n)
	}
	return nil
}

func (s *VoucherService) Seckill(ctx context.Context, voucherID, userID int64) (int64, error) {
	if userID <= 0 {
		return 0, apperror.ErrUnauthorized
	}
	if voucherID <= 0 {
		return 0, apperror.BadRequest("优惠券ID不合法")
	}
	orderID, err := redisx.NextOrderID(ctx, s.rdb)
	if err != nil {
		return 0, err
	}
	keys := []string{redisx.Key(redisx.SeckillStockPrefix, voucherID), redisx.Key(redisx.SeckillMetaPrefix, voucherID), redisx.Key(redisx.SeckillBuyersPrefix, voucherID), redisx.OrderStream, redisx.Key(redisx.OrderStatusPrefix, orderID)}
	n, err := redisx.SeckillScript.Run(ctx, s.rdb, keys, userID, voucherID, orderID).Int()
	if err != nil {
		return 0, fmt.Errorf("reserve seckill order: %w", err)
	}
	switch n {
	case 0:
		return orderID, nil
	case 1:
		return 0, apperror.BadRequest("库存不足")
	case 2:
		return 0, apperror.BadRequest("不能重复下单")
	case 3:
		return 0, apperror.BadRequest("秒杀尚未开始")
	case 4:
		return 0, apperror.BadRequest("秒杀已结束")
	case -1:
		if _, lookupErr := s.repo.SeckillVoucher(ctx, voucherID); errors.Is(lookupErr, gorm.ErrRecordNotFound) {
			return 0, apperror.ErrNotFound
		} else if lookupErr != nil {
			return 0, lookupErr
		}
		return 0, fmt.Errorf("seckill Redis campaign %d is incomplete; refusing unsafe stock rebuild", voucherID)
	default:
		return 0, fmt.Errorf("invalid Redis seckill key types or order state: %d", n)
	}
}

func (s *VoucherService) GetOrder(ctx context.Context, id, userID int64) (*model.VoucherOrder, error) {
	if userID <= 0 {
		return nil, apperror.ErrUnauthorized
	}
	if id <= 0 {
		return nil, apperror.BadRequest("订单ID不合法")
	}
	order, err := s.repo.OrderGet(ctx, id, userID)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, apperror.ErrNotFound
	}
	return order, err
}

func (s *VoucherService) GetOrderStatus(ctx context.Context, id, userID int64) (map[string]any, error) {
	if userID <= 0 {
		return nil, apperror.ErrUnauthorized
	}
	if id <= 0 {
		return nil, apperror.BadRequest("订单ID不合法")
	}
	// MySQL is authoritative even if a crash happened between commit and XACK.
	order, err := s.repo.OrderGet(ctx, id, userID)
	if err == nil {
		return map[string]any{"id": strconv.FormatInt(id, 10), "state": "created", "order": order}, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	state, err := s.rdb.HGetAll(ctx, redisx.Key(redisx.OrderStatusPrefix, id)).Result()
	if err != nil {
		return nil, err
	}
	if state["userId"] != strconv.FormatInt(userID, 10) {
		return nil, apperror.ErrNotFound
	}
	result := map[string]any{"id": strconv.FormatInt(id, 10), "state": state["state"]}
	if state["error"] != "" {
		result["error"] = state["error"]
	}
	return result, nil
}

// voucherStore exposes only the persistence operations this consumer needs.
type voucherStore interface {
	VoucherCreate(ctx context.Context, voucher *model.Voucher, seckill bool) error
	VoucherList(ctx context.Context, shopID int64) ([]model.Voucher, error)
	SeckillVoucher(ctx context.Context, id int64) (*model.SeckillVoucher, error)
	SeckillVouchers(ctx context.Context) ([]model.SeckillVoucher, error)
	VoucherOrders(ctx context.Context, voucherID int64) ([]model.VoucherOrder, error)
	OrderGet(ctx context.Context, id, userID int64) (*model.VoucherOrder, error)
}
