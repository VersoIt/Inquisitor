package postgres_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/VersoIt/Inquisitor/internal/storage/postgres"
)

func TestLoadMigrationsTableDriven(t *testing.T) {
	tests := []struct {
		name       string
		files      map[string]string
		wantNames  []string
		wantErrSub string
	}{
		{
			name: "sorts valid migrations and ignores non sql files",
			files: map[string]string{
				"002_second.sql": "SELECT 2;",
				"001_first.sql":  "SELECT 1;",
				"README.md":      "notes",
			},
			wantNames: []string{"001_first.sql", "002_second.sql"},
		},
		{
			name: "rejects invalid file name",
			files: map[string]string{
				"first.sql": "SELECT 1;",
			},
			wantErrSub: "invalid migration file name",
		},
		{
			name: "rejects duplicate version",
			files: map[string]string{
				"001_first.sql": "SELECT 1;",
				"001_other.sql": "SELECT 2;",
			},
			wantErrSub: "duplicate migration version",
		},
		{
			name: "rejects empty migration",
			files: map[string]string{
				"001_empty.sql": "   \n\t",
			},
			wantErrSub: "is empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			for name, contents := range tt.files {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(contents), 0o600); err != nil {
					t.Fatalf("write migration %s: %v", name, err)
				}
			}

			got, err := postgres.LoadMigrations(dir)
			if tt.wantErrSub != "" {
				assertErrorContains(t, err, tt.wantErrSub)
				return
			}
			if err != nil {
				t.Fatalf("load migrations: %v", err)
			}
			if len(got) != len(tt.wantNames) {
				t.Fatalf("expected %d migrations, got %d", len(tt.wantNames), len(got))
			}
			for i, want := range tt.wantNames {
				if got[i].Name != want {
					t.Fatalf("migration[%d] expected %q, got %q", i, want, got[i].Name)
				}
				if got[i].Checksum == "" {
					t.Fatalf("migration[%d] checksum is empty", i)
				}
			}
		})
	}
}

func assertErrorContains(t *testing.T, err error, substring string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error containing %q", substring)
	}
	if !strings.Contains(err.Error(), substring) {
		t.Fatalf("expected error containing %q, got %v", substring, err)
	}
}
