// Package migrations runs explicit, versioned SQL; GORM never alters schemas at startup.
package migrations

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"fmt"
	mysql "github.com/go-sql-driver/mysql"
	"time"
)

//go:embed *.sql
var files embed.FS

func Run(ctx context.Context, dsn string, seed bool) error {
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil {
		return err
	}
	cfg.MultiStatements = true
	db, err := sql.Open("mysql", cfg.FormatDSN())
	if err != nil {
		return err
	}
	defer db.Close()
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	var locked int
	lockName := "go_dianping_migrate_" + cfg.DBName
	if err = conn.QueryRowContext(ctx, "SELECT GET_LOCK(?,30)", lockName).Scan(&locked); err != nil {
		return err
	}
	if locked != 1 {
		return fmt.Errorf("migration lock unavailable")
	}
	defer func() {
		release, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_, _ = conn.ExecContext(release, "SELECT RELEASE_LOCK(?)", lockName)
	}()
	var legacy, ledger int
	if err = conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name='tb_user'").Scan(&legacy); err != nil {
		return err
	}
	if err = conn.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=DATABASE() AND table_name='schema_migrations'").Scan(&ledger); err != nil {
		return err
	}
	if legacy > 0 && ledger == 0 {
		return fmt.Errorf("existing Java database detected; initialize a separate empty database and follow docs/DATABASE.md")
	}
	if _, err = conn.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS schema_migrations (name varchar(128) PRIMARY KEY, checksum varchar(64) NOT NULL, applied_at timestamp NOT NULL DEFAULT CURRENT_TIMESTAMP)"); err != nil {
		return err
	}
	names := []string{"001_schema.sql"}
	if seed {
		names = append(names, "002_seed.sql")
	}
	for _, name := range names {
		body, e := files.ReadFile(name)
		if e != nil {
			return e
		}
		sum := fmt.Sprintf("%x", sha256.Sum256(body))
		var prior string
		e = conn.QueryRowContext(ctx, "SELECT checksum FROM schema_migrations WHERE name=?", name).Scan(&prior)
		if e == nil {
			if prior != sum {
				return fmt.Errorf("migration %s checksum changed", name)
			}
			continue
		}
		if e != sql.ErrNoRows {
			return e
		}
		// MySQL DDL implicitly commits. Each statement is repeatable so interrupted
		// fresh initialization can be retried without claiming a transactional DDL rollback.
		if _, e = conn.ExecContext(ctx, string(body)); e != nil {
			return fmt.Errorf("migration %s: %w", name, e)
		}
		if _, e = conn.ExecContext(ctx, "INSERT INTO schema_migrations(name,checksum) VALUES (?,?)", name, sum); e != nil {
			return e
		}
	}
	return nil
}
