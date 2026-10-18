package postgres_test

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	appstrategy "github.com/VersoIt/Inquisitor/internal/app/strategy"
	"github.com/VersoIt/Inquisitor/internal/clock"
	"github.com/VersoIt/Inquisitor/internal/features"
	"github.com/VersoIt/Inquisitor/internal/hypothesis"
	"github.com/VersoIt/Inquisitor/internal/marketdata"
	"github.com/VersoIt/Inquisitor/internal/paper"
	"github.com/VersoIt/Inquisitor/internal/research"
	"github.com/VersoIt/Inquisitor/internal/risk"
	"github.com/VersoIt/Inquisitor/internal/storage/postgres"
	"github.com/VersoIt/Inquisitor/internal/strategy"
	"github.com/VersoIt/Inquisitor/internal/testutil"
	"github.com/shopspring/decimal"
)

var paperRiskColumns = []string{"signal_id", "approval_id", "context_json", "decision_id", "intent_id", "mode", "hypothesis_id", "strategy_name", "symbol", "side", "entry_price", "leverage", "confidence", "intent_reason", "intent_created_at", "approved", "final_quantity", "max_loss", "stop_loss", "take_profit", "reason", "checks_json", "created_at", "recorded_at"}

func paperRiskContextJSON(t *testing.T, e strategy.PaperRiskEvaluation) string {
	t.Helper()
	raw, err := json.Marshal(e.Context)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
func paperRiskRows(t *testing.T, e strategy.PaperRiskEvaluation, raw string) *sqlmock.Rows {
	t.Helper()
	return sqlmock.NewRows(paperRiskColumns).AddRow(append([]driver.Value{e.SignalID, e.ApprovalID, raw}, riskDecisionSQLDriverArgs(t, e.Audit)...)...)
}

func TestPaperSignalRiskRepositoryTransactionBoundaries(t *testing.T) {
	failure := errors.New("database unavailable")
	for _, scenario := range []string{"insert", "retry", "conflict", "begin error", "lock error", "read error", "audit error", "link error", "count error", "unexpected count", "commit error"} {
		t.Run(scenario, func(t *testing.T) {
			db, mock := newSQLMock(t)
			defer db.Close()
			e := testutil.PaperRisk(t).Evaluation(t)
			raw := paperRiskContextJSON(t, e)
			if scenario == "begin error" {
				mock.ExpectBegin().WillReturnError(failure)
			} else {
				mock.ExpectBegin()
				lock := mock.ExpectExec("SELECT pg_advisory_xact_lock").WithArgs("paper_signal_risk:" + e.SignalID)
				if scenario == "lock error" {
					lock.WillReturnError(failure)
				} else {
					lock.WillReturnResult(sqlmock.NewResult(0, 1))
					read := mock.ExpectQuery("SELECT e.signal_id").WithArgs(e.SignalID)
					switch scenario {
					case "read error":
						read.WillReturnError(failure)
					case "retry", "conflict":
						stored := e
						if scenario == "conflict" {
							stored.Context.Input.Account.OpenPositions++
						}
						read.WillReturnRows(paperRiskRows(t, stored, paperRiskContextJSON(t, stored)))
					default:
						read.WillReturnRows(sqlmock.NewRows(paperRiskColumns))
						audit := mock.ExpectExec("INSERT INTO risk_decisions").WithArgs(riskDecisionSQLDriverArgs(t, e.Audit)...)
						if scenario == "audit error" {
							audit.WillReturnError(failure)
						} else {
							audit.WillReturnResult(sqlmock.NewResult(1, 1))
							link := mock.ExpectExec("INSERT INTO paper_signal_risk_evaluations").WithArgs(e.SignalID, e.ApprovalID, e.Audit.DecisionID, raw)
							switch scenario {
							case "link error":
								link.WillReturnError(failure)
							case "count error":
								link.WillReturnResult(sqlmock.NewErrorResult(failure))
							case "unexpected count":
								link.WillReturnResult(sqlmock.NewResult(0, 0))
							default:
								link.WillReturnResult(sqlmock.NewResult(1, 1))
							}
						}
					}
				}
				switch scenario {
				case "insert", "retry":
					mock.ExpectCommit()
				case "commit error":
					mock.ExpectCommit().WillReturnError(failure)
				default:
					mock.ExpectRollback()
				}
			}
			got, stats, err := postgres.NewPaperSignalRiskRepository(db).SavePaperRiskEvaluation(context.Background(), e)
			if scenario == "insert" || scenario == "retry" {
				if err != nil || !strategy.EquivalentPaperRiskEvaluation(got, e) || stats.Total() != 1 || (scenario == "retry") != (stats.Skipped == 1) {
					t.Fatalf("write: %+v %+v %v", got, stats, err)
				}
			} else if err == nil || got.SignalID != "" || stats.Total() != 0 {
				t.Fatalf("failed open: %+v %+v %v", got, stats, err)
			}
			if scenario == "conflict" && !errors.Is(err, strategy.ErrPaperRiskConflict) {
				t.Fatal(err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPaperSignalRiskRepositoryLoadValidation(t *testing.T) {
	for _, scenario := range []string{"valid", "missing", "read error", "invalid JSON", "unknown field", "null context", "trailing JSON", "mismatched audit", "wrong ID"} {
		t.Run(scenario, func(t *testing.T) {
			db, mock := newSQLMock(t)
			defer db.Close()
			e := testutil.PaperRisk(t).Evaluation(t)
			raw := paperRiskContextJSON(t, e)
			switch scenario {
			case "invalid JSON":
				raw = "{"
			case "unknown field":
				raw = `{"Unknown":1}`
			case "null context":
				raw = "null"
			case "trailing JSON":
				raw += " {}"
			case "mismatched audit":
				e.Audit.Confidence--
			case "wrong ID":
				e.SignalID = "other"
			}
			q := mock.ExpectQuery("SELECT e.signal_id").WithArgs("paper_risk_test_signal")
			if scenario == "missing" {
				q.WillReturnRows(sqlmock.NewRows(paperRiskColumns))
			} else if scenario == "read error" {
				q.WillReturnError(context.DeadlineExceeded)
			} else {
				q.WillReturnRows(paperRiskRows(t, e, raw))
			}
			got, err := postgres.NewPaperSignalRiskRepository(db).LoadPaperRiskEvaluation(context.Background(), "paper_risk_test_signal")
			if scenario == "valid" {
				if err != nil || !strategy.EquivalentPaperRiskEvaluation(got, e) {
					t.Fatalf("read %+v %v", got, err)
				}
			} else if err == nil || got.SignalID != "" {
				t.Fatalf("bad row accepted %+v %v", got, err)
			}
			if scenario == "missing" && !errors.Is(err, strategy.ErrPaperRiskNotFound) {
				t.Fatal(err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPaperSignalRiskRepositoryRejectsInvalidCallsBeforeIO(t *testing.T) {
	db, mock := newSQLMock(t)
	defer db.Close()
	repo := postgres.NewPaperSignalRiskRepository(db)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	e := testutil.PaperRisk(t).Evaluation(t)
	if _, err := repo.LoadPaperRiskEvaluation(ctx, e.SignalID); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, _, err := repo.SavePaperRiskEvaluation(ctx, e); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	for _, id := range []string{"", " padded "} {
		if _, err := repo.LoadPaperRiskEvaluation(context.Background(), id); err == nil {
			t.Fatal("invalid query")
		}
	}
	e.ApprovalID = ""
	if _, _, err := repo.SavePaperRiskEvaluation(context.Background(), e); err == nil {
		t.Fatal("invalid write")
	}
	for _, r := range []*postgres.PaperSignalRiskRepository{nil, postgres.NewPaperSignalRiskRepository(nil)} {
		if _, err := r.LoadPaperRiskEvaluation(context.Background(), "signal"); err == nil {
			t.Fatal("missing database")
		}
		if _, _, err := r.SavePaperRiskEvaluation(context.Background(), e); err == nil {
			t.Fatal("missing database")
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPaperSignalRiskRepositoryIntegration(t *testing.T) {
	db := openTestPostgres(t)
	ctx := context.Background()
	applyMigrations(t, ctx, db)
	f := testutil.PaperRisk(t)
	cleanup := func() {
		for _, q := range []struct{ sql, id string }{
			{"DELETE FROM paper_signal_risk_evaluations WHERE approval_id IN (SELECT approval_id FROM paper_strategy_approvals WHERE validation_id=$1)", f.Approval.Grant.ValidationID},
			{"DELETE FROM risk_decisions WHERE decision_id LIKE 'paper_signal_risk_%' AND hypothesis_id=$1", f.Approval.Grant.HypothesisName},
			{"DELETE FROM strategy_signals WHERE hypothesis_id=$1", f.Approval.Grant.HypothesisName},
			{"DELETE FROM paper_strategy_approvals WHERE validation_id=$1", f.Approval.Grant.ValidationID},
			{"DELETE FROM paper_validation_records WHERE validation_id=$1", f.Approval.Grant.ValidationID},
			{"DELETE FROM research_results WHERE run_id=$1", f.Approval.Grant.RunID},
			{"DELETE FROM research_runs WHERE run_id=$1", f.Approval.Grant.RunID},
			{"DELETE FROM hypotheses WHERE name=$1", f.Approval.Grant.HypothesisName},
		} {
			if _, err := db.ExecContext(ctx, q.sql, q.id); err != nil {
				t.Error(err)
			}
		}
	}
	cleanup()
	defer cleanup()
	if _, err := postgres.NewHypothesisRepository(db).UpsertHypotheses(ctx, []hypothesis.Record{f.Approval.Evidence.Hypothesis}); err != nil {
		t.Fatal(err)
	}
	runs := postgres.NewResearchRunRepository(db)
	if _, err := runs.UpsertRuns(ctx, []research.Run{f.Approval.Evidence.Run}); err != nil {
		t.Fatal(err)
	}
	if _, err := runs.RecordResult(ctx, f.Approval.Evidence.Run, f.Approval.Evidence.Result); err != nil {
		t.Fatal(err)
	}
	validations := postgres.NewPaperValidationRepository(db)
	planned := f.Approval.Evidence.Validation
	planned.Status = paper.ValidationStatusPlanned
	planned.StartedAt = time.Time{}
	if _, err := validations.RecordValidation(ctx, planned); err != nil {
		t.Fatal(err)
	}
	if _, err := validations.TransitionValidation(ctx, f.Approval.Evidence.Validation, paper.ValidationStatusPlanned); err != nil {
		t.Fatal(err)
	}
	grants := postgres.NewStrategyApprovalRepository(db)
	if _, err := grants.RecordPaperApproval(ctx, f.Approval.Grant); err != nil {
		t.Fatal(err)
	}
	signals := postgres.NewStrategySignalRepository(db)
	repo := postgres.NewPaperSignalRiskRepository(db)
	seedSignal := func(t *testing.T, suffix string) testutil.PaperRiskFixture {
		t.Helper()
		copy := testutil.PaperRisk(t)
		copy.Signal.Signal.SignalID += suffix
		copy.Signal.Signal.Trade.SignalID = copy.Signal.Signal.SignalID
		intentID, _ := strategy.PaperRiskIDs(copy.Signal.Signal.SignalID)
		copy.Input.Intent.IntentID = intentID
		if _, err := signals.RecordSignal(ctx, copy.Signal); err != nil {
			t.Fatal(err)
		}
		return copy
	}
	t.Run("application persists linked approval and risk inputs", func(t *testing.T) {
		f := testutil.PaperRisk(t)
		f.Signal.Signal.SignalID += "_generated"
		c := clock.FixedClock{Time: f.Approval.Now}
		approvals, err := appstrategy.NewApprovalService(grants, c, f.Approval.Policy)
		if err != nil {
			t.Fatal(err)
		}
		selection := strategy.SelectionPolicy{MinConfidence: 70, MaxDataAge: 3 * time.Second}
		trend, err := strategy.NewTrendStrategy(strategy.TrendConfig{SelectionPolicy: selection, StopATRMultiple: decimal.NewFromInt(2), TakeProfitR: decimal.NewFromInt(2)})
		if err != nil {
			t.Fatal(err)
		}
		selector, err := strategy.NewSelector(selection, trend)
		if err != nil {
			t.Fatal(err)
		}
		r := f.Regime
		current := marketdata.Candle{Exchange: r.Exchange, Category: r.Category, Symbol: r.Symbol, Interval: r.Interval, OpenTime: r.OpenTime, CloseTime: r.CloseTime,
			Open: decimal.NewFromInt(98), High: decimal.NewFromInt(101), Low: decimal.NewFromInt(97), Close: decimal.NewFromInt(100), IsClosed: true}
		previous := current
		previous.OpenTime, previous.CloseTime = current.OpenTime.Add(-time.Minute), current.OpenTime
		previous.Open, previous.High, previous.Low, previous.Close = decimal.NewFromInt(96), decimal.NewFromInt(99), decimal.NewFromInt(94), decimal.NewFromInt(98)
		market := strategy.MarketContext{SignalID: f.Signal.Signal.SignalID, Now: c.Now(), ObservedAt: f.Signal.ObservedAt, Mode: risk.ModePaper, TradingEnabled: true, AllowShort: true, EntryPrice: current.Close,
			Regime: r, Current: current, Previous: previous,
			Trend: features.TrendFeatures{Exchange: r.Exchange, Category: r.Category, Symbol: r.Symbol, Interval: r.Interval, OpenTime: r.OpenTime, CloseTime: r.CloseTime,
				Complete: true, EMA20: decimal.NewFromInt(98), EMA50: decimal.NewFromInt(96), MA50Slope: decimal.NewFromInt(1)},
			Volatility: features.VolatilityFeatures{Exchange: r.Exchange, Category: r.Category, Symbol: r.Symbol, Interval: r.Interval, OpenTime: r.OpenTime, CloseTime: r.CloseTime, Complete: true, ATR: decimal.NewFromInt(2)}}
		generation := appstrategy.NewAuthorizedGenerationService(approvals, appstrategy.NewGenerationService(selector, signals, c))
		generated, err := generation.GenerateAndRecord(ctx, f.Approval.Grant.ApprovalID, market)
		if err != nil || generated.Signal.Type != strategy.SignalBuy {
			t.Fatalf("authorized trend generation: %+v %v", generated, err)
		}
		engine, err := risk.NewTradeRiskEngine(f.Policy)
		if err != nil {
			t.Fatal(err)
		}
		s, err := appstrategy.NewPaperRiskService(signals, approvals, repo, postgres.NewRiskKillSwitchRepository(db), engine, c)
		if err != nil {
			t.Fatal(err)
		}
		req := appstrategy.EvaluatePaperSignalRequest{SignalID: f.Signal.Signal.SignalID, ApprovalID: f.Approval.Grant.ApprovalID, Regime: f.Regime, Account: f.Input.Account, Market: f.Input.Market, Runtime: f.Input.Runtime}
		first, err := s.EvaluateStoredSignal(ctx, req)
		if err != nil || first.Replayed || !first.Evaluation.Audit.Decision.Approved || !first.Evaluation.Audit.Decision.FinalQuantity.Equal(decimal.RequireFromString("0.62")) {
			t.Fatalf("evaluation %+v %v", first, err)
		}
		retry, err := s.EvaluateStoredSignal(ctx, req)
		if err != nil || !retry.Replayed || !strategy.EquivalentPaperRiskEvaluation(first.Evaluation, retry.Evaluation) {
			t.Fatalf("retry %+v %v", retry, err)
		}
		audits, err := postgres.NewRiskDecisionRepository(db).ListDecisions(ctx, risk.DecisionAuditQuery{DecisionID: first.Evaluation.Audit.DecisionID})
		if err != nil || len(audits) != 1 {
			t.Fatalf("central audit %+v %v", audits, err)
		}
		var tickets int
		if err := db.QueryRowContext(ctx, "SELECT count(*) FROM paper_order_tickets WHERE decision_id=$1", first.Evaluation.Audit.DecisionID).Scan(&tickets); err != nil || tickets != 0 {
			t.Fatalf("risk evaluation unexpectedly created order: %d %v", tickets, err)
		}
		req.Account.OpenPositions++
		if _, err := s.EvaluateStoredSignal(ctx, req); !errors.Is(err, strategy.ErrPaperRiskConflict) {
			t.Fatalf("changed input: %v", err)
		}
	})
	t.Run("rejected decisions round trip", func(t *testing.T) {
		f := seedSignal(t, "_reject")
		f.Input.Runtime.TradingEnabled = false
		e := f.Evaluation(t)
		if e.Audit.Decision.Approved {
			t.Fatal("test must reject")
		}
		if _, _, err := repo.SavePaperRiskEvaluation(ctx, e); err != nil {
			t.Fatal(err)
		}
		got, err := repo.LoadPaperRiskEvaluation(ctx, e.SignalID)
		if err != nil || !strategy.EquivalentPaperRiskEvaluation(got, e) || got.Audit.Decision.Approved {
			t.Fatalf("rejected evidence %+v %v", got, err)
		}
	})
	t.Run("late retry keeps first timestamps", func(t *testing.T) {
		f := seedSignal(t, "_retry")
		e := f.Evaluation(t)
		if _, _, err := repo.SavePaperRiskEvaluation(ctx, e); err != nil {
			t.Fatal(err)
		}
		f.Input.EvaluatedAt = f.Input.EvaluatedAt.Add(time.Microsecond)
		got, stats, err := repo.SavePaperRiskEvaluation(ctx, f.Evaluation(t))
		if err != nil || stats.Skipped != 1 || !got.Audit.RecordedAt.Equal(e.Audit.RecordedAt) {
			t.Fatalf("late retry %+v %+v %v", got, stats, err)
		}
	})
	t.Run("concurrent equivalent writers create one decision", func(t *testing.T) {
		f := seedSignal(t, "_concurrent")
		e := f.Evaluation(t)
		const workers = 8
		var wg sync.WaitGroup
		stats := make(chan risk.DecisionAuditStats, workers)
		errs := make(chan error, workers)
		for i := 0; i < workers; i++ {
			wg.Add(1)
			go func() { defer wg.Done(); _, s, err := repo.SavePaperRiskEvaluation(ctx, e); stats <- s; errs <- err }()
		}
		wg.Wait()
		close(stats)
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatal(err)
			}
		}
		inserted, skipped := 0, 0
		for s := range stats {
			inserted += s.Inserted
			skipped += s.Skipped
		}
		if inserted != 1 || skipped != workers-1 {
			t.Fatalf("inserted=%d skipped=%d", inserted, skipped)
		}
	})
	t.Run("concurrent conflicting writers cannot overwrite", func(t *testing.T) {
		f := seedSignal(t, "_conflict")
		a := f.Evaluation(t)
		f.Input.Account.OpenPositions = 2
		b := f.Evaluation(t)
		var wg sync.WaitGroup
		errs := make(chan error, 2)
		for _, e := range []strategy.PaperRiskEvaluation{a, b} {
			wg.Add(1)
			go func(e strategy.PaperRiskEvaluation) {
				defer wg.Done()
				_, _, err := repo.SavePaperRiskEvaluation(ctx, e)
				errs <- err
			}(e)
		}
		wg.Wait()
		close(errs)
		wins, conflicts := 0, 0
		for err := range errs {
			if err == nil {
				wins++
			} else if errors.Is(err, strategy.ErrPaperRiskConflict) {
				conflicts++
			} else {
				t.Fatal(err)
			}
		}
		if wins != 1 || conflicts != 1 {
			t.Fatalf("wins=%d conflicts=%d", wins, conflicts)
		}
		got, err := repo.LoadPaperRiskEvaluation(ctx, a.SignalID)
		if err != nil || !strategy.EquivalentPaperRiskEvaluation(got, a) && !strategy.EquivalentPaperRiskEvaluation(got, b) {
			t.Fatalf("corrupt concurrent record %+v %v", got, err)
		}
	})
	t.Run("failed link rolls back central audit", func(t *testing.T) {
		for _, missing := range []string{"signal", "approval"} {
			t.Run(missing, func(t *testing.T) {
				f := seedSignal(t, "_rollback_"+missing)
				e := f.Evaluation(t)
				if missing == "signal" {
					if _, err := db.ExecContext(ctx, "DELETE FROM strategy_signals WHERE signal_id=$1", e.SignalID); err != nil {
						t.Fatal(err)
					}
				} else {
					e.ApprovalID = "missing_grant"
				}
				if _, _, err := repo.SavePaperRiskEvaluation(ctx, e); err == nil {
					t.Fatal("missing source accepted")
				}
				var count int
				if err := db.QueryRowContext(ctx, "SELECT count(*) FROM risk_decisions WHERE decision_id=$1", e.Audit.DecisionID).Scan(&count); err != nil || count != 0 {
					t.Fatalf("orphan audit count=%d err=%v", count, err)
				}
			})
		}
	})
	t.Run("database rejects malformed context", func(t *testing.T) {
		f := seedSignal(t, "_constraints")
		e := f.Evaluation(t)
		if _, err := postgres.NewRiskDecisionRepository(db).RecordDecision(ctx, e.Audit); err != nil {
			t.Fatal(err)
		}
		for i, raw := range []string{"null", "[]", "{}", `{"Regime":{},"Input":{"Runtime":{"Mode":"LIVE"}}}`} {
			if _, err := db.ExecContext(ctx, "INSERT INTO paper_signal_risk_evaluations(signal_id,approval_id,decision_id,context_json) VALUES ($1,$2,$3,$4::jsonb)", e.SignalID, e.ApprovalID, e.Audit.DecisionID, raw); err == nil {
				t.Fatalf("context %d accepted", i)
			}
		}
	})
	t.Run("revocation invalidates an application replay", func(t *testing.T) {
		f := seedSignal(t, "_revoked")
		e := f.Evaluation(t)
		if _, _, err := repo.SavePaperRiskEvaluation(ctx, e); err != nil {
			t.Fatal(err)
		}
		c := clock.FixedClock{Time: f.Approval.Now}
		approvals, err := appstrategy.NewApprovalService(grants, c, f.Approval.Policy)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := approvals.Revoke(ctx, e.ApprovalID, "operator", "stop"); err != nil {
			t.Fatal(err)
		}
		engine, err := risk.NewTradeRiskEngine(f.Policy)
		if err != nil {
			t.Fatal(err)
		}
		s, err := appstrategy.NewPaperRiskService(signals, approvals, repo, postgres.NewRiskKillSwitchRepository(db), engine, c)
		if err != nil {
			t.Fatal(err)
		}
		got, err := s.EvaluateStoredSignal(ctx, appstrategy.EvaluatePaperSignalRequest{SignalID: e.SignalID, ApprovalID: e.ApprovalID, Regime: f.Regime, Account: f.Input.Account, Market: f.Input.Market, Runtime: f.Input.Runtime})
		if !errors.Is(err, strategy.ErrApprovalUnavailable) || got.Evaluation.SignalID != "" {
			t.Fatalf("revoked replay %+v %v", got, err)
		}
	})
}
