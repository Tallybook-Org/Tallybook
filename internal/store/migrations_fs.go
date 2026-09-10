package store

import (
	"embed"
	"fmt"
	"io/fs"
)

// migrationsFS embeds every numbered migration file into the binary, so a
// service never depends on the migrations directory being present on disk
// at runtime.
//
//go:embed migrations/*.sql
var migrationsFS embed.FS

// Migrations returns every numbered migration in internal/store/migrations,
// sorted by version. Callers pass the result to Migrate.
func Migrations() ([]Migration, error) {
	sub, err := fs.Sub(migrationsFS, "migrations")
	if err != nil {
		return nil, fmt.Errorf("store: open embedded migrations: %w", err)
	}
	return ParseMigrations(sub)
}
