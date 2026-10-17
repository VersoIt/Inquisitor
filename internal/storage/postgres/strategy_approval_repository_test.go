package postgres_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	appstrategy "github.com/VersoIt/Inquisitor/internal/app/strategy"
	"github.com/VersoIt/Inquisitor/internal/clock"
	"github.com/VersoIt/Inquisitor/internal/hypothesis"
	"github.com/VersoIt/Inquisitor/internal/paper"
	"github.com/VersoIt/Inquisitor/internal/regime"
	"github.com/VersoIt/Inquisitor/internal/research"
	"github.com/VersoIt/Inquisitor/internal/risk"
	"github.com/VersoIt/Inquisitor/internal/storage/postgres"
	"github.com/VersoIt/Inquisitor/internal/strategy"
	"github.com/VersoIt/Inquisitor/internal/testutil"
)

func TestStrategyApprovalRepositoryIntegration(t *testing.T) {
	db := openTestPostgres(t)
	ctx := context.Background()
	applyMigrations(t, ctx, db)
	f := testutil.PaperApproval(t)
	cleanup := func() {
		for _, query := range []string{
			"DELETE FROM strategy_signals WHERE hypothesis_id=$1",
			"DELETE FROM paper_strategy_approvals WHERE validation_id IN (SELECT validation_id FROM paper_validation_records WHERE run_id IN (SELECT run_id FROM research_runs WHERE hypothesis_name=$1))",
			"DELETE FROM paper_validation_records WHERE run_id IN (SELECT run_id FROM research_runs WHERE hypothesis_name=$1)",
			"DELETE FROM research_results WHERE run_id IN (SELECT run_id FROM research_runs WHERE hypothesis_name=$1)",
			"DELETE FROM research_runs WHERE hypothesis_name=$1", "DELETE FROM hypotheses WHERE name=$1",
		} {
			if _, err := db.ExecContext(ctx, query, f.Grant.HypothesisName); err != nil {
				t.Error(err)
			}
		}
	}
	cleanup()
	defer cleanup()
	hypotheses := postgres.NewHypothesisRepository(db)
	runs := postgres.NewResearchRunRepository(db)
	validations := postgres.NewPaperValidationRepository(db)
	repo := postgres.NewStrategyApprovalRepository(db)
	if _, err := hypotheses.UpsertHypotheses(ctx, []hypothesis.Record{f.Evidence.Hypothesis}); err != nil {
		t.Fatal(err)
	}
	if _, err := runs.UpsertRuns(ctx, []research.Run{f.Evidence.Run}); err != nil {
		t.Fatal(err)
	}
	if _, err := runs.RecordResult(ctx, f.Evidence.Run, f.Evidence.Result); err != nil {
		t.Fatal(err)
	}
	planned := f.Evidence.Validation
	planned.Status = paper.ValidationStatusPlanned
	planned.StartedAt = time.Time{}
	if _, err := validations.RecordValidation(ctx, planned); err != nil {
		t.Fatal(err)
	}
	if _, err := validations.TransitionValidation(ctx, f.Evidence.Validation, paper.ValidationStatusPlanned); err != nil {
		t.Fatal(err)
	}
	stats, err := repo.RecordPaperApproval(ctx, f.Grant)
	if err != nil || stats.Written != 1 {
		t.Fatalf("grant %+v, %v", stats, err)
	}
	stats, err = repo.RecordPaperApproval(ctx, f.Grant)
	if err != nil || stats.Skipped != 1 {
		t.Fatalf("grant retry %+v, %v", stats, err)
	}
	conflict := f.Grant
	conflict.Reason = "different review"
	if _, err := repo.RecordPaperApproval(ctx, conflict); !errors.Is(err, strategy.ErrApprovalConflict) {
		t.Fatalf("expected conflict, got %v", err)
	}
	service, err := appstrategy.NewApprovalService(repo, clock.FixedClock{Time: f.Now}, f.Policy)
	if err != nil {
		t.Fatal(err)
	}
	market := strategy.MarketContext{SignalID: "approval_test_signal", Now: f.Now, ObservedAt: f.Now.Add(-time.Second), Mode: risk.ModePaper, TradingEnabled: true, KillSwitchActive: true,
		Regime: regime.State{Exchange: "bybit", Category: "linear", Symbol: "BTCUSDT", Interval: "1", Regime: regime.RegimeTrendUp}, Approval: strategy.ApprovalSnapshot{HypothesisID: "forged", Mode: risk.ModeLive, Enabled: true}}
	snapshot, err := service.Resolve(ctx, f.Grant.ApprovalID, market)
	if err != nil || snapshot.HypothesisSHA256 != f.Grant.HypothesisSHA256 || snapshot.ValidationID != f.Grant.ValidationID {
		t.Fatalf("resolve %+v, %v", snapshot, err)
	}
	t.Run("authorized generation journals database provenance", func(t *testing.T) {
		selector, err := strategy.NewSelector(strategy.SelectionPolicy{MinConfidence: 70, MaxDataAge: 3 * time.Second})
		if err != nil {
			t.Fatal(err)
		}
		signals := postgres.NewStrategySignalRepository(db)
		authorized := appstrategy.NewAuthorizedGenerationService(service, appstrategy.NewGenerationService(selector, signals, clock.FixedClock{Time: f.Now}))
		got, err := authorized.GenerateAndRecord(ctx, f.Grant.ApprovalID, market)
		if err != nil || got.Signal.Type != strategy.SignalNoTrade || got.Signal.HypothesisID != f.Grant.HypothesisName {
			t.Fatalf("generation %+v, %v", got, err)
		}
		rows, err := signals.ListSignals(ctx, strategy.SignalQuery{SignalID: market.SignalID})
		if err != nil || len(rows) != 1 || rows[0].Features["approval.id"] != f.Grant.ApprovalID || rows[0].Features["approval.mode"] != "PAPER" {
			t.Fatalf("provenance %+v, %v", rows, err)
		}
	})
	t.Run("changed result invalidates approval", func(t *testing.T) {
		changed := f.Evidence.Result
		changed.Outcome = research.OutcomeRejected
		if _, err := runs.RecordResult(ctx, f.Evidence.Run, changed); err != nil {
			t.Fatal(err)
		}
		if got, err := service.Resolve(ctx, f.Grant.ApprovalID, market); !errors.Is(err, strategy.ErrApprovalUnavailable) || got.Enabled {
			t.Fatalf("changed result allowed %+v, %v", got, err)
		}
		if _, err := runs.RecordResult(ctx, f.Evidence.Run, f.Evidence.Result); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("changed same-version content invalidates approval", func(t *testing.T) {
		h := f.Evidence.Hypothesis
		raw := h.RawYAML + "\n# changed source\n"
		updated, err := hypothesis.NewRecord(h.Spec, h.SourcePath, []byte(raw), h.ImportedAt)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := hypotheses.UpsertHypotheses(ctx, []hypothesis.Record{updated}); err != nil {
			t.Fatal(err)
		}
		if got, err := service.Resolve(ctx, f.Grant.ApprovalID, market); !errors.Is(err, strategy.ErrApprovalUnavailable) || got.Enabled {
			t.Fatalf("changed content allowed %+v, %v", got, err)
		}
		if _, err := hypotheses.UpsertHypotheses(ctx, []hypothesis.Record{h}); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("revocation is persistent and cannot be undone by retry", func(t *testing.T) {
		if _, err := service.Revoke(ctx, f.Grant.ApprovalID, "operator", "stop"); err != nil {
			t.Fatal(err)
		}
		stats, err := service.Revoke(ctx, f.Grant.ApprovalID, "operator", "stop")
		if err != nil || stats.Skipped != 1 {
			t.Fatalf("revoke retry %+v, %v", stats, err)
		}
		if _, err := service.Revoke(ctx, f.Grant.ApprovalID, "other", "new reason"); !errors.Is(err, strategy.ErrApprovalConflict) {
			t.Fatalf("conflicting revoke: %v", err)
		}
		if _, err := repo.RecordPaperApproval(ctx, f.Grant); err != nil {
			t.Fatal(err)
		}
		if got, err := service.Resolve(ctx, f.Grant.ApprovalID, market); !errors.Is(err, strategy.ErrApprovalUnavailable) || got.Enabled {
			t.Fatalf("revoked approval allowed %+v, %v", got, err)
		}
	})
	t.Run("stopped paper validation invalidates another active grant", func(t *testing.T) {
		a := f.Grant
		a.ApprovalID = "approval_test_second"
		if _, err := repo.RecordPaperApproval(ctx, a); err != nil {
			t.Fatal(err)
		}
		v, err := paper.CancelValidation(f.Evidence.Validation, f.Now, "stop validation")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := validations.TransitionValidation(ctx, v, paper.ValidationStatusRunning); err != nil {
			t.Fatal(err)
		}
		if got, err := service.Resolve(ctx, a.ApprovalID, market); !errors.Is(err, strategy.ErrApprovalUnavailable) || got.Enabled {
			t.Fatalf("cancelled paper allowed %+v, %v", got, err)
		}
	})
}

func TestStrategyApprovalRepositoryTransactionsFailClosed(t *testing.T) {
	for _, scenario := range []string{"missing grant", "malformed grant", "missing source", "begin error", "query error"} {
		t.Run(scenario, func(t *testing.T) {
			db, mock := newSQLMock(t)
			defer db.Close()
			repo := postgres.NewStrategyApprovalRepository(db)
			f := testutil.PaperApproval(t)
			if scenario == "begin error" {
				mock.ExpectBegin().WillReturnError(errors.New("unavailable"))
			} else {
				mock.ExpectBegin()
				switch scenario {
				case "missing grant":
					mock.ExpectQuery("SELECT grant_json").WithArgs(f.Grant.ApprovalID).WillReturnRows(sqlmock.NewRows([]string{"grant_json", "revoked_at", "revoked_by", "revocation_reason"}))
				case "query error":
					mock.ExpectQuery("SELECT grant_json").WithArgs(f.Grant.ApprovalID).WillReturnError(context.DeadlineExceeded)
				default:
					data, err := json.Marshal(f.Grant)
					if err != nil {
						t.Fatal(err)
					}
					raw := string(data)
					if scenario == "malformed grant" {
						raw = "{"
					}
					mock.ExpectQuery("SELECT grant_json").WithArgs(f.Grant.ApprovalID).WillReturnRows(sqlmock.NewRows([]string{"grant_json", "revoked_at", "revoked_by", "revocation_reason"}).AddRow(raw, nil, "", ""))
					if scenario == "missing source" {
						mock.ExpectQuery("SELECT validation_id").WithArgs(f.Grant.ValidationID).WillReturnRows(sqlmock.NewRows([]string{"validation_id"}))
					}
				}
				mock.ExpectRollback()
			}
			got, err := repo.LoadPaperAuthorization(context.Background(), f.Grant.ApprovalID)
			if err == nil || got.Grant.ApprovalID != "" {
				t.Fatalf("partial authorization returned %+v, %v", got, err)
			}
			if scenario == "query error" && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("lost cancellation %v", err)
			}
			assertSQLExpectations(t, mock)
		})
	}
}

func TestStrategyApprovalRepositoryGuardsMissingDependenciesAndInputs(t *testing.T) {
	f := testutil.PaperApproval(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, repo := range []*postgres.StrategyApprovalRepository{nil, postgres.NewStrategyApprovalRepository(nil)} {
		if _, err := repo.LoadPaperAuthorization(context.Background(), "id"); err == nil {
			t.Fatal("missing database")
		}
		if _, err := repo.LoadPaperApprovalEvidence(ctx, "id"); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		if _, err := repo.RecordPaperApproval(context.Background(), f.Grant); err == nil {
			t.Fatal("missing database")
		}
		if _, err := repo.RevokePaperApproval(context.Background(), "id", strategy.ApprovalRevocation{}); err == nil {
			t.Fatal("missing database")
		}
	}
	db, mock := newSQLMock(t)
	defer db.Close()
	repo := postgres.NewStrategyApprovalRepository(db)
	if _, err := repo.LoadPaperAuthorization(context.Background(), " padded "); err == nil {
		t.Fatal("bad ID")
	}
	bad := f.Grant
	bad.Reason = ""
	if _, err := repo.RecordPaperApproval(context.Background(), bad); err == nil {
		t.Fatal("bad grant")
	}
	if _, err := repo.RevokePaperApproval(context.Background(), f.Grant.ApprovalID, strategy.ApprovalRevocation{}); err == nil {
		t.Fatal("bad revoke")
	}
	assertSQLExpectations(t, mock)
}
