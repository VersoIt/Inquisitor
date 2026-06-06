package postgres

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

var migrationNamePattern = regexp.MustCompile(`^([0-9]+)_(.+)\.sql$`)

type Migration struct {
	Version  string
	Name     string
	Path     string
	SQL      string
	Checksum string
}

type MigrationResult struct {
	Applied int
	Skipped int
}

func LoadMigrations(dir string) ([]Migration, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read migrations dir %q: %w", dir, err)
	}

	var migrations []Migration
	seenVersions := map[string]string{}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}

		matches := migrationNamePattern.FindStringSubmatch(entry.Name())
		if matches == nil {
			return nil, fmt.Errorf("invalid migration file name %q; expected NNN_name.sql", entry.Name())
		}

		version := matches[1]
		if existingName, exists := seenVersions[version]; exists {
			return nil, fmt.Errorf("duplicate migration version %s in %q and %q", version, existingName, entry.Name())
		}
		seenVersions[version] = entry.Name()

		path := filepath.Join(dir, entry.Name())
		sqlBytes, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read migration %q: %w", entry.Name(), err)
		}
		sqlText := strings.TrimSpace(string(sqlBytes))
		if sqlText == "" {
			return nil, fmt.Errorf("migration %q is empty", entry.Name())
		}

		sum := sha256.Sum256([]byte(sqlText))
		migrations = append(migrations, Migration{
			Version:  version,
			Name:     entry.Name(),
			Path:     path,
			SQL:      sqlText,
			Checksum: hex.EncodeToString(sum[:]),
		})
	}

	sort.Slice(migrations, func(i, j int) bool {
		return migrations[i].Version < migrations[j].Version
	})

	return migrations, nil
}

func ApplyMigrations(ctx context.Context, db *sql.DB, dir string) (MigrationResult, error) {
	migrations, err := LoadMigrations(dir)
	if err != nil {
		return MigrationResult{}, err
	}
	if err := ensureSchemaMigrations(ctx, db); err != nil {
		return MigrationResult{}, err
	}

	var result MigrationResult
	for _, migration := range migrations {
		applied, err := getAppliedMigration(ctx, db, migration.Version)
		if err != nil {
			return result, err
		}
		if applied != nil {
			if applied.Checksum != migration.Checksum {
				return result, fmt.Errorf("migration %s checksum mismatch: database=%s file=%s", migration.Version, applied.Checksum, migration.Checksum)
			}
			result.Skipped++
			continue
		}

		if err := applyMigration(ctx, db, migration); err != nil {
			return result, err
		}
		result.Applied++
	}

	return result, nil
}

func ensureSchemaMigrations(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			checksum_sha256 TEXT NOT NULL,
			applied_at TIMESTAMPTZ NOT NULL
		)
	`)
	if err != nil {
		return fmt.Errorf("ensure schema_migrations table: %w", err)
	}
	return nil
}

type appliedMigration struct {
	Version  string
	Checksum string
}

func getAppliedMigration(ctx context.Context, db *sql.DB, version string) (*appliedMigration, error) {
	var applied appliedMigration
	err := db.QueryRowContext(ctx, `
		SELECT version, checksum_sha256
		FROM schema_migrations
		WHERE version = $1
	`, version).Scan(&applied.Version, &applied.Checksum)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("load applied migration %s: %w", version, err)
	}
	return &applied, nil
}

func applyMigration(ctx context.Context, db *sql.DB, migration Migration) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration %s: %w", migration.Version, err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	if _, err := tx.ExecContext(ctx, migration.SQL); err != nil {
		return fmt.Errorf("apply migration %s %s: %w", migration.Version, migration.Name, err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO schema_migrations (version, name, checksum_sha256, applied_at)
		VALUES ($1, $2, $3, $4)
	`, migration.Version, migration.Name, migration.Checksum, time.Now().UTC()); err != nil {
		return fmt.Errorf("record migration %s: %w", migration.Version, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migration %s: %w", migration.Version, err)
	}
	committed = true

	return nil
}
