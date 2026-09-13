package config

import (
	"fmt"
	"github.com/spf13/viper"
	"strings"
	"time"
)

type Config struct {
	Server struct {
		Address         string
		Mode            string
		ReadTimeout     time.Duration `mapstructure:"read_timeout"`
		WriteTimeout    time.Duration `mapstructure:"write_timeout"`
		ShutdownTimeout time.Duration `mapstructure:"shutdown_timeout"`
	}
	MySQL struct {
		DSN     string
		MaxOpen int `mapstructure:"max_open"`
		MaxIdle int `mapstructure:"max_idle"`
	}
	Redis struct {
		Address  string
		Password string
		DB       int
	}
	Auth struct {
		CodeTTL      time.Duration `mapstructure:"code_ttl"`
		SessionTTL   time.Duration `mapstructure:"session_ttl"`
		CodeInterval time.Duration `mapstructure:"code_interval"`
		DevCodeLog   bool          `mapstructure:"dev_code_log"`
	}
	Upload struct {
		Directory string
		MaxBytes  int64 `mapstructure:"max_bytes"`
	}
	Worker struct {
		Concurrency int
		ClaimIdle   time.Duration `mapstructure:"claim_idle"`
	}
	Timezone string
}

func Load(path string) (Config, error) {
	v := viper.New()
	v.SetConfigFile(path)
	v.SetEnvPrefix("DIANPING")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()
	defaults := map[string]any{
		"server.address": ":8081", "server.mode": "debug", "server.read_timeout": "10s", "server.write_timeout": "15s", "server.shutdown_timeout": "10s",
		"mysql.dsn": "dianping:dianping_dev@tcp(127.0.0.1:3307)/go_dianping?charset=utf8mb4&parseTime=true&loc=Asia%2FShanghai", "mysql.max_open": 20, "mysql.max_idle": 5,
		"redis.address": "127.0.0.1:6380", "redis.password": "", "redis.db": 0,
		"auth.code_ttl": "2m", "auth.session_ttl": "30m", "auth.code_interval": "60s", "auth.dev_code_log": true,
		"upload.directory": "./data/uploads", "upload.max_bytes": 5 << 20,
		"worker.concurrency": 2, "worker.claim_idle": "30s", "timezone": "Asia/Shanghai",
	}
	for k, val := range defaults {
		v.SetDefault(k, val)
	}
	if err := v.ReadInConfig(); err != nil {
		return Config{}, fmt.Errorf("读取配置 %s: %w", path, err)
	}
	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return cfg, fmt.Errorf("配置解析: %w", err)
	}
	if cfg.Auth.CodeTTL <= 0 || cfg.Auth.SessionTTL <= 0 || cfg.Auth.CodeInterval <= 0 || cfg.Worker.Concurrency < 1 || cfg.Worker.Concurrency > 32 || cfg.Worker.ClaimIdle < time.Second || cfg.Upload.MaxBytes < 1 || cfg.MySQL.MaxOpen < 1 || cfg.MySQL.MaxIdle < 0 || cfg.Server.ReadTimeout <= 0 || cfg.Server.WriteTimeout <= 0 || cfg.Server.ShutdownTimeout <= 0 {
		return cfg, fmt.Errorf("配置参数超出有效范围")
	}
	if cfg.Server.Mode != "debug" && cfg.Server.Mode != "release" && cfg.Server.Mode != "test" {
		return cfg, fmt.Errorf("server.mode 必须为 debug/release/test")
	}
	if cfg.Server.Mode == "release" && cfg.Auth.DevCodeLog {
		return cfg, fmt.Errorf("release 模式必须关闭 auth.dev_code_log")
	}
	if _, err := time.LoadLocation(cfg.Timezone); err != nil {
		return cfg, fmt.Errorf("业务时区: %w", err)
	}
	return cfg, nil
}
