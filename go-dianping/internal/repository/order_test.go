package repository

import (
	"context"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/learning/go-dianping/internal/model"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func orderMock(t *testing.T) (*Repository, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	gdb, err := gorm.Open(mysql.New(mysql.Config{Conn: db, SkipInitializeWithVersion: true}), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Error(err)
		}
		_ = db.Close()
	})
	return New(gdb), mock
}

func emptyOrder() *sqlmock.Rows { return sqlmock.NewRows([]string{"id", "user_id", "voucher_id"}) }
func absentPurchase(mock sqlmock.Sqlmock) {
	mock.ExpectQuery("SELECT .* FROM `tb_voucher_order` WHERE id = ").WithArgs(int64(101), 1).WillReturnRows(emptyOrder())
	mock.ExpectQuery("SELECT .* FROM `tb_voucher_order` WHERE user_id = ").WithArgs(int64(42), int64(1), 1).WillReturnRows(emptyOrder())
}

func TestOrderCreateTransactionAndReplay(t *testing.T) {
	r, mock := orderMock(t)
	ctx := context.Background()
	order := &model.VoucherOrder{ID: 101, UserID: 42, VoucherID: 1}
	mock.ExpectBegin()
	absentPurchase(mock)
	mock.ExpectExec(regexp.QuoteMeta("UPDATE `tb_seckill_voucher` SET `stock`=stock - 1 WHERE voucher_id = ? AND stock > 0")).WithArgs(int64(1)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO `tb_voucher_order`").WillReturnResult(sqlmock.NewResult(101, 1))
	mock.ExpectCommit()
	if err := r.OrderCreate(ctx, order); err != nil {
		t.Fatal(err)
	}
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT .* FROM `tb_voucher_order` WHERE id = ").WithArgs(int64(101), 1).WillReturnRows(emptyOrder().AddRow(101, 42, 1))
	mock.ExpectCommit()
	if err := r.OrderCreate(ctx, order); err != nil {
		t.Fatalf("replay: %v", err)
	}
}

func TestOrderInsertFailureRollsBackStock(t *testing.T) {
	r, mock := orderMock(t)
	mock.ExpectBegin()
	absentPurchase(mock)
	mock.ExpectExec("UPDATE `tb_seckill_voucher` SET `stock`=stock - 1 WHERE voucher_id = ").WithArgs(int64(1)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec("INSERT INTO `tb_voucher_order`").WillReturnError(errors.New("connection lost"))
	mock.ExpectRollback()
	err := r.OrderCreate(context.Background(), &model.VoucherOrder{ID: 101, UserID: 42, VoucherID: 1})
	if err == nil {
		t.Fatal("expected insert error")
	}
	var permanent *OrderRejection
	if errors.As(err, &permanent) {
		t.Fatal("transient failure must not compensate reservation")
	}
}

func TestStockRejectionRollsBackAndRechecksConcurrentCommit(t *testing.T) {
	for _, committed := range []bool{false, true} {
		name := "sold out"
		if committed {
			name = "same order committed concurrently"
		}
		t.Run(name, func(t *testing.T) {
			r, mock := orderMock(t)
			mock.ExpectBegin()
			absentPurchase(mock)
			mock.ExpectExec("UPDATE `tb_seckill_voucher` SET `stock`=stock - 1 WHERE voucher_id = ").WithArgs(int64(1)).WillReturnResult(sqlmock.NewResult(0, 0))
			mock.ExpectRollback()
			rows := emptyOrder()
			if committed {
				rows.AddRow(101, 42, 1)
			}
			mock.ExpectQuery("SELECT .* FROM `tb_voucher_order` WHERE id = ").WithArgs(int64(101), 1).WillReturnRows(rows)
			if !committed {
				mock.ExpectQuery("SELECT .* FROM `tb_voucher_order` WHERE user_id = ").WithArgs(int64(42), int64(1), 1).WillReturnRows(emptyOrder())
			}
			err := r.OrderCreate(context.Background(), &model.VoucherOrder{ID: 101, UserID: 42, VoucherID: 1})
			if committed && err != nil {
				t.Fatalf("already committed order must ACK, not compensate: %v", err)
			}
			if !committed && !errors.Is(err, ErrOrderStock) {
				t.Fatalf("expected stock rejection, got %v", err)
			}
		})
	}
}

func TestVoucherProjectionRetainsSeckillFields(t *testing.T) {
	r, mock := orderMock(t)
	now := time.Now().Truncate(time.Second)
	mock.ExpectQuery("SELECT v.\\*, sv.stock, sv.begin_time, sv.end_time FROM tb_voucher AS v").WithArgs(int64(1)).WillReturnRows(sqlmock.NewRows([]string{"id", "shop_id", "title", "stock", "begin_time", "end_time"}).AddRow(1, 1, "demo", 8, now, now.Add(time.Hour)))
	vouchers, err := r.VoucherList(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(vouchers) != 1 || vouchers[0].Stock == nil || *vouchers[0].Stock != 8 || vouchers[0].BeginTime == nil {
		t.Fatalf("projection lost seckill fields: %+v", vouchers)
	}
}
