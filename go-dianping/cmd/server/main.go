package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"github.com/learning/go-dianping/internal/bootstrap"
	"github.com/learning/go-dianping/internal/config"
	"github.com/learning/go-dianping/internal/handler"
	"github.com/learning/go-dianping/internal/repository"
	"github.com/learning/go-dianping/internal/service"
	"github.com/learning/go-dianping/internal/worker"
	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
	_ "time/tzdata"
)

func main() {
	path := flag.String("config", "configs/config.yaml", "configuration path")
	flag.Parse()
	if err := run(*path); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run(path string) error {
	cfg, err := config.Load(path)
	if err != nil {
		return err
	}
	log, err := zap.NewProduction()
	if err != nil {
		return err
	}
	defer log.Sync()
	root, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithCancel(root)
	defer cancel()
	startup, done := context.WithTimeout(ctx, 30*time.Second)
	defer done()
	db, err := bootstrap.Database(startup, cfg)
	if err != nil {
		return err
	}
	pool, _ := db.DB()
	defer pool.Close()
	if !db.WithContext(startup).Migrator().HasTable("schema_migrations") {
		return fmt.Errorf("database not initialized; run go run ./cmd/migrate -seed")
	}
	rdb := redis.NewClient(&redis.Options{Addr: cfg.Redis.Address, Password: cfg.Redis.Password, DB: cfg.Redis.DB, DialTimeout: 3 * time.Second, ReadTimeout: 3 * time.Second, WriteTimeout: 3 * time.Second, ContextTimeoutEnabled: true})
	defer rdb.Close()
	if err = rdb.Ping(startup).Err(); err != nil {
		return fmt.Errorf("connect redis: %w", err)
	}
	repo := repository.New(db)
	users := service.NewUser(repo, rdb, log, cfg)
	shops := service.NewShop(repo, rdb, log, ctx)
	defer shops.Close()
	social := service.NewSocial(repo, rdb, log)
	vouchers := service.NewVoucher(repo, rdb, log)
	if err = shops.WarmGeo(startup); err != nil {
		return fmt.Errorf("initialize GEO: %w", err)
	}
	if err = vouchers.Warm(startup); err != nil {
		return fmt.Errorf("initialize seckill: %w", err)
	}
	orders := worker.New(repo, rdb, log, worker.Config{Concurrency: cfg.Worker.Concurrency, ClaimIdle: cfg.Worker.ClaimIdle})
	router := handler.New(handler.API{Users: users, Shops: shops, Social: social, Vouchers: vouchers, Repo: repo, Redis: rdb, Config: cfg, Log: log})
	server := &http.Server{Addr: cfg.Server.Address, Handler: router, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: cfg.Server.ReadTimeout, WriteTimeout: cfg.Server.WriteTimeout, IdleTimeout: 60 * time.Second, BaseContext: func(net.Listener) context.Context { return ctx }}
	listener, err := net.Listen("tcp", cfg.Server.Address)
	if err != nil {
		return err
	}
	group, workersCtx := errgroup.WithContext(ctx)
	group.Go(func() error { return orders.Run(workersCtx) })
	group.Go(func() error { social.Run(workersCtx); return nil })
	group.Go(func() error {
		log.Info("Go Dianping started", zap.String("address", cfg.Server.Address))
		err := server.Serve(listener)
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	})
	group.Go(func() error {
		<-workersCtx.Done()
		shutdown, finish := context.WithTimeout(context.Background(), cfg.Server.ShutdownTimeout)
		defer finish()
		if err := server.Shutdown(shutdown); err != nil {
			_ = server.Close()
			return err
		}
		return nil
	})
	err = group.Wait()
	cancel()
	return err
}
