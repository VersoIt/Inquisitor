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
	"github.com/VersoIt/Inquisitor/internal/config"
	"github.com/VersoIt/Inquisitor/internal/hypothesis"
	"github.com/VersoIt/Inquisitor/internal/paper"
	"github.com/VersoIt/Inquisitor/internal/regime"
	"github.com/VersoIt/Inquisitor/internal/research"
	"github.com/VersoIt/Inquisitor/internal/risk"
	"github.com/VersoIt/Inquisitor/internal/storage/postgres"
	"github.com/VersoIt/Inquisitor/internal/strategy"
	"github.com/VersoIt/Inquisitor/internal/testutil"
)

func TestApprovalCommandPostgresLifecycle(t *testing.T) {
	ctx := context.Background()
	db, scopedDSN := testutil.IsolatedPostgres(t)
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
	validations := postgres.NewPaperValidationRepository(db)
	planned := f.Evidence.Validation
	planned.Status = paper.ValidationStatusPlanned
	planned.StartedAt = time.Time{}
	if _, err := validations.RecordValidation(ctx, planned); err != nil {
		t.Fatal(err)
	}
	if _, err := validations.TransitionValidation(ctx, f.Evidence.Validation, paper.ValidationStatusPlanned); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DATABASE_DSN", scopedDSN)
	deps := dependencies{clock: clock.FixedClock{Time: f.Now}, loadConfig: func(string) (*config.Config, error) {
		c, err := config.Load(filepath.Join("..", "..", "configs", "config.example.yaml"))
		if err != nil {
			return nil, err
		}
		c.Research.MinTrades = 100
		return c, nil
	}}
	var output bytes.Buffer
	wrongScope := append(grantArgs(f), "-symbol", "ETHUSDT")
	if err := run(ctx, wrongScope, &output, deps); err == nil || output.Len() != 0 {
		t.Fatal("out-of-scope command granted permission")
	}
	var count int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM paper_strategy_approvals").Scan(&count); err != nil || count != 0 {
		t.Fatalf("invalid grant wrote records: %d %v", count, err)
	}
	if err := run(ctx, grantArgs(f), &output, deps); err != nil {
		t.Fatal(err)
	}
	var result commandResult
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Stats == nil || result.Stats.Written != 1 || result.Grant == nil || result.Grant.ApprovalID != f.Grant.ApprovalID {
		t.Fatalf("command did not persist real grant: %+v", result)
	}
	repo := postgres.NewStrategyApprovalRepository(db)
	a, err := repo.LoadPaperAuthorization(ctx, f.Grant.ApprovalID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := strategy.ResolvePaperAuthorization(a, f.Policy, risk.ModePaper, f.Grant.Symbol, f.Grant.Interval, regime.RegimeTrendUp, f.Now); err != nil {
		t.Fatal(err)
	}
	deps.loadConfig = func(string) (*config.Config, error) { t.Fatal("show/revoke required research config"); return nil, nil }
	deps.clock = clock.FixedClock{Time: f.Now.Add(time.Hour + 999*time.Nanosecond)}
	revokeArgs := []string{"-action", "revoke", "-approval-id", f.Grant.ApprovalID, "-reviewed-by", "operator", "-reason", "stop reviewed strategy"}
	for i := 0; i < 2; i++ {
		output.Reset()
		if err := run(ctx, revokeArgs, &output, deps); err != nil {
			t.Fatal(err)
		}
	}
	output.Reset()
	if err := run(ctx, []string{"-action", "show", "-approval-id", f.Grant.ApprovalID}, &output, deps); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(output.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Revocation == nil || !result.Revocation.RevokedAt.Equal(f.Now.Add(time.Hour)) || result.Grant == nil || !result.Grant.GrantedAt.Equal(f.Now) {
		t.Fatalf("immutable grant/revocation times lost: %+v", result)
	}
	a, err = repo.LoadPaperAuthorization(ctx, f.Grant.ApprovalID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := strategy.ResolvePaperAuthorization(a, f.Policy, risk.ModePaper, f.Grant.Symbol, f.Grant.Interval, regime.RegimeTrendUp, f.Now.Add(2*time.Hour)); !errors.Is(err, strategy.ErrApprovalUnavailable) {
		t.Fatalf("revoked CLI grant still authorizes: %v", err)
	}
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM paper_strategy_approvals").Scan(&count); err != nil || count != 1 {
		t.Fatalf("duplicate grant rows: %d %v", count, err)
	}
}
