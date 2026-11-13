package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/VersoIt/Inquisitor/internal/clock"
	"github.com/VersoIt/Inquisitor/internal/hypothesis"
	"github.com/VersoIt/Inquisitor/internal/paper"
	"github.com/VersoIt/Inquisitor/internal/research"
	"github.com/VersoIt/Inquisitor/internal/storage/postgres"
	"github.com/VersoIt/Inquisitor/internal/testutil"
)

func TestPaperAccountCommandPostgresLifecycle(t *testing.T) {
	db, dsn := testutil.IsolatedPostgres(t)
	ctx := context.Background()
	if _, err := postgres.ApplyMigrations(ctx, db, filepath.Join("..", "..", "migrations")); err != nil {
		t.Fatal(err)
	}
	f := testutil.PaperApproval(t)
	if _, err := postgres.NewHypothesisRepository(db).UpsertHypotheses(ctx, []hypothesis.Record{f.Evidence.Hypothesis}); err != nil {
		t.Fatal(err)
	}
	runs := postgres.NewResearchRunRepository(db)
	if _, err := runs.UpsertRuns(ctx, []research.Run{f.Evidence.Run}); err != nil {
		t.Fatal(err)
	}
	if _, err := runs.RecordResult(ctx, f.Evidence.Run, f.Evidence.Result); err != nil {
		t.Fatal(err)
	}
	repo := postgres.NewPaperValidationRepository(db)
	planned := f.Evidence.Validation
	planned.Status = paper.ValidationStatusPlanned
	planned.StartedAt = time.Time{}
	if _, err := repo.RecordValidation(ctx, planned); err != nil {
		t.Fatal(err)
	}
	deps := dependencies{clock: clock.FixedClock{Time: f.Now}, lookupEnv: func(string) (string, bool) { return dsn, true }}
	args := []string{"-validation-id", planned.ValidationID, "-require-flat"}
	inspect := func(blocked bool) {
		t.Helper()
		var output bytes.Buffer
		err := run(ctx, args, &output, deps)
		if blocked {
			if !errors.Is(err, errLedgerNotFlat) {
				t.Fatalf("expected blocked inspection: %v", err)
			}
		} else if err != nil {
			t.Fatal(err)
		}
		var artifact struct {
			Snapshot paper.AccountSnapshot
			Warning  string
		}
		if err := json.Unmarshal(output.Bytes(), &artifact); err != nil {
			t.Fatal(err)
		}
		if artifact.Snapshot.FlatLedgerReady == blocked || (artifact.Snapshot.Account == nil) != blocked || !artifact.Snapshot.RealizedEquity.Equal(f.Evidence.Validation.InitialBalance) {
			t.Fatalf("CLI report %+v", artifact)
		}
	}
	inspect(true)
	if _, err := repo.TransitionValidation(ctx, f.Evidence.Validation, paper.ValidationStatusPlanned); err != nil {
		t.Fatal(err)
	}
	inspect(false)
	cancelled, err := paper.CancelValidation(f.Evidence.Validation, f.Now, "operator stop")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.TransitionValidation(ctx, cancelled, paper.ValidationStatusRunning); err != nil {
		t.Fatal(err)
	}
	inspect(true)
	var count int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM paper_equity_events").Scan(&count); err != nil || count != 0 {
		t.Fatal("read-only command modified accounting", err)
	}
}
