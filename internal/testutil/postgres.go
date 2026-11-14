package testutil

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/VersoIt/Inquisitor/internal/config"
	"github.com/VersoIt/Inquisitor/internal/storage/postgres"
)

// IsolatedPostgres gives each command test its own schema, even when packages
// run concurrently. Only this randomly named schema is dropped at cleanup.
func IsolatedPostgres(t testing.TB) (*sql.DB, string) {
	t.Helper()
	dsn := os.Getenv("POSTGRES_TEST_DSN")
	if dsn == "" {
		t.Skip("set POSTGRES_TEST_DSN to an isolated disposable test database")
	}
	root, err := postgres.Open(context.Background(), config.DatabaseConfig{DSN: dsn, MaxOpenConns: 2})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = root.Close() })
	// Extensions are database-wide, not schema-local. Serialize their setup and
	// keep them out of disposable schemas so parallel cleanup cannot remove them.
	tx, err := root.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(context.Background(), "SELECT pg_advisory_xact_lock(82492027)"); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(context.Background(), "CREATE EXTENSION IF NOT EXISTS pgcrypto WITH SCHEMA public"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var suffix [8]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		t.Fatal(err)
	}
	schema := "inquisitor_test_" + hex.EncodeToString(suffix[:])
	if _, err := root.ExecContext(context.Background(), "CREATE SCHEMA "+schema); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := root.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE"); err != nil {
			t.Error(err)
		}
	})
	scoped := dsn
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		u, err := url.Parse(dsn)
		if err != nil {
			t.Fatal(err)
		}
		q := u.Query()
		q.Set("search_path", schema)
		u.RawQuery = q.Encode()
		scoped = u.String()
	} else {
		scoped += " search_path=" + schema
	}
	db, err := postgres.Open(context.Background(), config.DatabaseConfig{DSN: scoped, MaxOpenConns: 2})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db, scoped
}
