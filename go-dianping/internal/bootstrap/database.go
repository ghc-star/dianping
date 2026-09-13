package bootstrap

import (
	"context"
	"fmt"
	"github.com/learning/go-dianping/internal/config"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"time"
)

func Database(ctx context.Context, cfg config.Config) (*gorm.DB, error) {
	loc, err := time.LoadLocation(cfg.Timezone)
	if err != nil {
		return nil, err
	}
	db, err := gorm.Open(mysql.Open(cfg.MySQL.DSN), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent), NowFunc: func() time.Time { return time.Now().In(loc) }})
	if err != nil {
		return nil, fmt.Errorf("connect mysql: %w", err)
	}
	pool, err := db.DB()
	if err != nil {
		return nil, err
	}
	pool.SetMaxOpenConns(cfg.MySQL.MaxOpen)
	pool.SetMaxIdleConns(cfg.MySQL.MaxIdle)
	pool.SetConnMaxLifetime(30 * time.Minute)
	if err = pool.PingContext(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return db, nil
}
