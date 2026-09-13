package main

import (
	"context"
	"flag"
	"github.com/learning/go-dianping/internal/config"
	"github.com/learning/go-dianping/migrations"
	"log"
	"time"
)

func main() {
	path := flag.String("config", "configs/config.yaml", "configuration path")
	seed := flag.Bool("seed", false, "insert development fixtures")
	flag.Parse()
	cfg, err := config.Load(*path)
	if err != nil {
		log.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err = migrations.Run(ctx, cfg.MySQL.DSN, *seed); err != nil {
		log.Fatal(err)
	}
	log.Print("database migrations complete")
}
