package db

import (
	"database/sql"
	"embed"
	"fmt"

	_ "github.com/jackc/pgx/v5/stdlib" // database/sql driver for goose
	"github.com/pressly/goose/v3"
)

// Migrate applies every .sql migration in fsys (a service's embedded
// migrations directory) to the database at dsn. Services call this once on
// startup so `docker compose up` alone brings the schema up to date — no
// separate migration step to remember.
func Migrate(dsn string, fsys embed.FS, dir string) error {
	sqlDB, err := sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("open sql.DB for migrations: %w", err)
	}
	defer sqlDB.Close()

	goose.SetBaseFS(fsys)
	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("set goose dialect: %w", err)
	}
	if err := goose.Up(sqlDB, dir); err != nil {
		return fmt.Errorf("run migrations: %w", err)
	}
	return nil
}
