package postgres_test

import (
	"context"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	appstrategy "github.com/VersoIt/Inquisitor/internal/app/strategy"
	"github.com/VersoIt/Inquisitor/internal/clock"
	"github.com/VersoIt/Inquisitor/internal/regime"
	"github.com/VersoIt/Inquisitor/internal/risk"
	"github.com/VersoIt/Inquisitor/internal/storage/postgres"
	"github.com/VersoIt/Inquisitor/internal/strategy"
	"github.com/shopspring/decimal"
)

func testStrategyAudit(t *testing.T, abstain bool) strategy.SignalAuditRecord {
	t.Helper()
	now := time.Date(2026, 10, 18, 12, 0, 0, 123456000, time.UTC)
	trade := strategy.TradeSignal{SignalID: "strategy_repo_test_trade", HypothesisID: "strategy_repo_hyp", HypothesisVersion: "v1", StrategyName: strategy.NameTrend, Symbol: "BTCUSDT", Side: strategy.SideLong, Confidence: 80,
		EntryPrice: decimal.RequireFromString("100.123456789123456789"), StopLoss: decimal.NewFromInt(95), TakeProfit: decimal.NewFromInt(110), Leverage: decimal.NewFromInt(1), HypothesisApprovedForMode: true,
		HypothesisMaxRiskPct: decimal.RequireFromString("0.25"), HypothesisMinConfidence: 70, Reason: "trend_confirmed", ObservedAt: now.Add(-time.Second), CreatedAt: now}
	signal := strategy.Signal{SignalID: trade.SignalID, HypothesisID: trade.HypothesisID, HypothesisVersion: trade.HypothesisVersion, StrategyName: trade.StrategyName, Symbol: trade.Symbol, Interval: "1", Type: strategy.SignalBuy, Reason: trade.Reason, CreatedAt: now, Trade: &trade}
	if abstain {
		signal.SignalID, signal.Type, signal.Trade, signal.Reason, signal.StrategyName = "strategy_repo_test_no_trade", strategy.SignalNoTrade, nil, "kill_switch_active", strategy.NameNoTrade
	}
	r, err := strategy.NewSignalAuditRecord(strategy.SignalAuditRecord{Signal: signal, Mode: risk.ModePaper, Exchange: "bybit", Category: "linear", ObservedAt: trade.ObservedAt, RecordedAt: now.Add(time.Second), Features: map[string]string{"price": "100.123456789123456789", "compression": "NaN"}})
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func signalDriverArgs(t *testing.T, r strategy.SignalAuditRecord) []driver.Value {
	t.Helper()
	var trade any
	if r.Signal.Trade != nil {
		data, err := json.Marshal(r.Signal.Trade)
		if err != nil {
			t.Fatal(err)
		}
		trade = string(data)
	}
	f, err := json.Marshal(r.Features)
	if err != nil {
		t.Fatal(err)
	}
	var observed any
	if !r.ObservedAt.IsZero() {
		observed = r.ObservedAt
	}
	s := r.Signal
	return []driver.Value{s.SignalID, s.HypothesisID, s.HypothesisVersion, s.StrategyName, string(r.Mode), r.Exchange, r.Category, s.Symbol, s.Interval, string(s.Type), s.Reason, s.CreatedAt, observed, r.RecordedAt, trade, string(f)}
}

func signalRows(t *testing.T, r strategy.SignalAuditRecord) *sqlmock.Rows {
	t.Helper()
	return sqlmock.NewRows([]string{"signal_id", "hypothesis_id", "hypothesis_version", "strategy_name", "mode", "exchange", "category", "symbol", "interval", "signal_type", "reason", "created_at", "observed_at", "recorded_at", "trade_json", "features_json"}).AddRow(signalDriverArgs(t, r)...)
}

func TestStrategySignalRepositoryWritesSQLMock(t *testing.T) {
	for _, abstain := range []bool{false, true} {
		for _, scenario := range []string{"insert", "retry", "conflict", "insert error", "verification error", "rows affected error", "unexpected count"} {
			t.Run(fmt.Sprintf("abstain=%t/%s", abstain, scenario), func(t *testing.T) {
				db, mock := newSQLMock(t)
				defer db.Close()
				r := testStrategyAudit(t, abstain)
				args := signalDriverArgs(t, r)
				wantStats := strategy.SignalWriteStats{Inserted: 1}
				var wantErr error
				storageErr := errors.New("storage failure")
				expect := mock.ExpectExec("INSERT INTO strategy_signals").WithArgs(args...)
				switch scenario {
				case "insert":
					expect.WillReturnResult(sqlmock.NewResult(1, 1))
				case "insert error":
					expect.WillReturnError(storageErr)
					wantErr = storageErr
				case "rows affected error":
					expect.WillReturnResult(sqlmock.NewErrorResult(storageErr))
					wantErr = storageErr
				case "unexpected count":
					expect.WillReturnResult(sqlmock.NewResult(0, 2))
					wantErr = errors.New("unexpected")
				default:
					expect.WillReturnResult(sqlmock.NewResult(0, 0))
					checkArgs := append(append([]driver.Value(nil), args[:13]...), args[14:]...)
					query := mock.ExpectQuery("SELECT 1 FROM strategy_signals").WithArgs(checkArgs...)
					if scenario == "retry" {
						query.WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(1))
						wantStats = strategy.SignalWriteStats{Skipped: 1}
					} else if scenario == "conflict" {
						query.WillReturnRows(sqlmock.NewRows([]string{"exists"}))
						wantErr = strategy.ErrSignalConflict
					} else {
						query.WillReturnError(storageErr)
						wantErr = storageErr
					}
				}
				got, err := postgres.NewStrategySignalRepository(db).RecordSignal(context.Background(), r)
				if wantErr == nil {
					if err != nil || got != wantStats {
						t.Fatalf("got %+v, %v", got, err)
					}
				} else {
					if err == nil || got.Total() != 0 {
						t.Fatalf("error must return zero stats: %+v, %v", got, err)
					}
					if scenario != "unexpected count" && !errors.Is(err, wantErr) {
						t.Fatalf("want %v, got %v", wantErr, err)
					}
				}
				assertSQLExpectations(t, mock)
			})
		}
	}
}

func TestStrategySignalRepositoryListsSQLMock(t *testing.T) {
	for _, abstain := range []bool{false, true} {
		t.Run(fmt.Sprint(abstain), func(t *testing.T) {
			db, mock := newSQLMock(t)
			defer db.Close()
			r := testStrategyAudit(t, abstain)
			if abstain {
				r.ObservedAt = time.Time{}
			}
			mock.ExpectQuery("SELECT signal_id, hypothesis_id").WithArgs(r.Signal.SignalID, r.Signal.HypothesisID, r.Signal.HypothesisVersion, string(r.Mode), r.Signal.Symbol, string(r.Signal.Type), r.Signal.CreatedAt.Add(-time.Hour), r.Signal.CreatedAt.Add(time.Hour), 10).WillReturnRows(signalRows(t, r)).RowsWillBeClosed()
			got, err := postgres.NewStrategySignalRepository(db).ListSignals(context.Background(), strategy.SignalQuery{SignalID: r.Signal.SignalID, HypothesisID: r.Signal.HypothesisID, HypothesisVersion: r.Signal.HypothesisVersion, Mode: r.Mode, Symbol: r.Signal.Symbol, Type: r.Signal.Type, Start: r.Signal.CreatedAt.Add(-time.Hour), End: r.Signal.CreatedAt.Add(time.Hour), Limit: 10})
			if err != nil || len(got) != 1 {
				t.Fatalf("got %+v, %v", got, err)
			}
			assertStrategyAuditEqual(t, got[0], r)
			assertSQLExpectations(t, mock)
		})
	}
}

func TestStrategySignalRepositoryRejectsCorruptRowsAndReadFailures(t *testing.T) {
	for _, scenario := range []string{"query error", "scan error", "iteration error", "bad trade JSON", "null trade JSON", "unknown trade field", "bad features JSON", "trailing JSON", "null features", "invalid envelope"} {
		t.Run(scenario, func(t *testing.T) {
			db, mock := newSQLMock(t)
			defer db.Close()
			r := testStrategyAudit(t, false)
			args := signalDriverArgs(t, r)
			expect := mock.ExpectQuery("SELECT signal_id, hypothesis_id").WithArgs("", "", "", "", "", "", nil, nil, 100)
			switch scenario {
			case "query error":
				expect.WillReturnError(errors.New("unavailable"))
			case "scan error":
				expect.WillReturnRows(sqlmock.NewRows([]string{"wrong"}).AddRow(1))
			default:
				switch scenario {
				case "bad trade JSON":
					args[14] = "{"
				case "null trade JSON":
					args[14] = "null"
				case "unknown trade field":
					args[14] = strings.TrimSuffix(args[14].(string), "}") + `,"unknown":true}`
				case "bad features JSON":
					args[15] = `{"key":42}`
				case "trailing JSON":
					args[15] = `{} {}`
				case "null features":
					args[15] = "null"
				case "invalid envelope":
					args[9] = "SELL"
				}
				rows := signalRows(t, r)
				if scenario == "iteration error" {
					rows.RowError(0, errors.New("read failed"))
				} else {
					rows = sqlmock.NewRows([]string{"signal_id", "hypothesis_id", "hypothesis_version", "strategy_name", "mode", "exchange", "category", "symbol", "interval", "signal_type", "reason", "created_at", "observed_at", "recorded_at", "trade_json", "features_json"}).AddRow(args...)
				}
				expect.WillReturnRows(rows).RowsWillBeClosed()
			}
			got, err := postgres.NewStrategySignalRepository(db).ListSignals(context.Background(), strategy.SignalQuery{})
			if err == nil || got != nil {
				t.Fatalf("corrupt or unavailable read returned data: %+v, %v", got, err)
			}
			assertSQLExpectations(t, mock)
		})
	}
}

func TestStrategySignalRepositoryValidationBeforeSQL(t *testing.T) {
	db, mock := newSQLMock(t)
	defer db.Close()
	repo := postgres.NewStrategySignalRepository(db)
	r := testStrategyAudit(t, false)
	r.Signal.Reason = ""
	if _, err := repo.RecordSignal(context.Background(), r); err == nil {
		t.Fatal("accepted invalid signal")
	}
	if _, err := repo.ListSignals(context.Background(), strategy.SignalQuery{Limit: 1001}); err == nil {
		t.Fatal("accepted invalid query")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := repo.RecordSignal(ctx, testStrategyAudit(t, false)); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
	if _, err := repo.ListSignals(ctx, strategy.SignalQuery{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
	for _, r := range []*postgres.StrategySignalRepository{nil, postgres.NewStrategySignalRepository(nil)} {
		if _, err := r.RecordSignal(context.Background(), testStrategyAudit(t, false)); err == nil {
			t.Fatal("missing database accepted")
		}
		if _, err := r.ListSignals(context.Background(), strategy.SignalQuery{}); err == nil {
			t.Fatal("missing database accepted")
		}
	}
	assertSQLExpectations(t, mock)
}

func assertStrategyAuditEqual(t *testing.T, got, want strategy.SignalAuditRecord) {
	t.Helper()
	a, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(want)
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != string(b) {
		t.Fatalf("roundtrip mismatch:\ngot %s\nwant %s", a, b)
	}
}

func TestStrategySignalRepositoryIntegration(t *testing.T) {
	db := openTestPostgres(t)
	ctx := context.Background()
	applyMigrations(t, ctx, db)
	defer func() {
		if _, err := db.ExecContext(ctx, "DELETE FROM strategy_signals WHERE hypothesis_id = 'strategy_repo_hyp'"); err != nil {
			t.Error(err)
		}
	}()
	if _, err := db.ExecContext(ctx, "DELETE FROM strategy_signals WHERE hypothesis_id = 'strategy_repo_hyp'"); err != nil {
		t.Fatal(err)
	}
	repo := postgres.NewStrategySignalRepository(db)
	for _, abstain := range []bool{false, true} {
		t.Run(fmt.Sprint(abstain), func(t *testing.T) {
			r := testStrategyAudit(t, abstain)
			stats, err := repo.RecordSignal(ctx, r)
			if err != nil || stats.Inserted != 1 {
				t.Fatalf("insert: %+v, %v", stats, err)
			}
			retry := r
			retry.RecordedAt = r.RecordedAt.Add(time.Minute)
			stats, err = repo.RecordSignal(ctx, retry)
			if err != nil || stats.Skipped != 1 {
				t.Fatalf("retry: %+v, %v", stats, err)
			}
			got, err := repo.ListSignals(ctx, strategy.SignalQuery{SignalID: r.Signal.SignalID})
			if err != nil || len(got) != 1 {
				t.Fatalf("list: %+v, %v", got, err)
			}
			assertStrategyAuditEqual(t, got[0], r)
			for _, mutate := range []func(*strategy.SignalAuditRecord){
				func(x *strategy.SignalAuditRecord) { x.Features = map[string]string{"changed": "evidence"} },
				func(x *strategy.SignalAuditRecord) { x.Mode = risk.ModeLive },
				func(x *strategy.SignalAuditRecord) {
					x.Signal.Reason = "changed"
					if x.Signal.Trade != nil {
						trade := *x.Signal.Trade
						trade.Reason = "changed"
						x.Signal.Trade = &trade
					}
				},
			} {
				conflict := r
				mutate(&conflict)
				if _, err := repo.RecordSignal(ctx, conflict); !errors.Is(err, strategy.ErrSignalConflict) {
					t.Fatalf("want conflict, got %v", err)
				}
			}
		})
	}
	t.Run("concurrent retry", func(t *testing.T) {
		r := testStrategyAudit(t, false)
		r.Signal.SignalID = "strategy_repo_test_concurrent"
		trade := *r.Signal.Trade
		trade.SignalID = r.Signal.SignalID
		r.Signal.Trade = &trade
		var wg sync.WaitGroup
		results := make(chan strategy.SignalWriteStats, 8)
		failures := make(chan error, 8)
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() { defer wg.Done(); stats, err := repo.RecordSignal(ctx, r); results <- stats; failures <- err }()
		}
		wg.Wait()
		close(results)
		close(failures)
		for err := range failures {
			if err != nil {
				t.Fatal(err)
			}
		}
		var inserted, skipped int
		for stats := range results {
			inserted += stats.Inserted
			skipped += stats.Skipped
		}
		if inserted != 1 || skipped != 7 {
			t.Fatalf("inserted=%d skipped=%d", inserted, skipped)
		}
	})
	t.Run("query filters and exclusive end", func(t *testing.T) {
		r := testStrategyAudit(t, false)
		got, err := repo.ListSignals(ctx, strategy.SignalQuery{HypothesisID: r.Signal.HypothesisID, HypothesisVersion: "v1", Mode: risk.ModePaper, Symbol: "BTCUSDT", Type: strategy.SignalNoTrade, Start: r.Signal.CreatedAt, End: r.Signal.CreatedAt.Add(time.Microsecond)})
		if err != nil || len(got) != 1 {
			t.Fatalf("filter: %+v, %v", got, err)
		}
		got, err = repo.ListSignals(ctx, strategy.SignalQuery{HypothesisID: r.Signal.HypothesisID, End: r.Signal.CreatedAt})
		if err != nil || len(got) != 0 {
			t.Fatalf("end must be exclusive: %+v, %v", got, err)
		}
	})
	t.Run("abstention without observation timestamp", func(t *testing.T) {
		r := testStrategyAudit(t, true)
		r.Signal.SignalID = "strategy_repo_null_observed"
		r.ObservedAt = time.Time{}
		stats, err := repo.RecordSignal(ctx, r)
		if err != nil || stats.Inserted != 1 {
			t.Fatalf("insert nullable observation: %+v, %v", stats, err)
		}
		got, err := repo.ListSignals(ctx, strategy.SignalQuery{SignalID: r.Signal.SignalID})
		if err != nil || len(got) != 1 {
			t.Fatalf("read nullable observation: %+v, %v", got, err)
		}
		assertStrategyAuditEqual(t, got[0], r)
	})
	t.Run("concurrent conflicting payloads", func(t *testing.T) {
		r := testStrategyAudit(t, true)
		r.Signal.SignalID = "strategy_repo_test_concurrent_conflict"
		start := make(chan struct{})
		results := make(chan error, 2)
		for i := 0; i < 2; i++ {
			candidate := r
			candidate.Features = map[string]string{"candidate": fmt.Sprint(i)}
			go func() { <-start; _, err := repo.RecordSignal(ctx, candidate); results <- err }()
		}
		close(start)
		var inserted, conflicts int
		for i := 0; i < 2; i++ {
			err := <-results
			if err == nil {
				inserted++
			} else if errors.Is(err, strategy.ErrSignalConflict) {
				conflicts++
			} else {
				t.Fatal(err)
			}
		}
		if inserted != 1 || conflicts != 1 {
			t.Fatalf("inserted=%d conflicts=%d", inserted, conflicts)
		}
	})
	t.Run("application persists real selector rejection", func(t *testing.T) {
		r := testStrategyAudit(t, true)
		m := strategy.MarketContext{SignalID: "strategy_repo_test_app", Now: r.Signal.CreatedAt, ObservedAt: r.ObservedAt, Mode: r.Mode, TradingEnabled: true, KillSwitchActive: true,
			Approval: strategy.ApprovalSnapshot{HypothesisID: r.Signal.HypothesisID, Version: r.Signal.HypothesisVersion},
			Regime:   regime.State{Exchange: r.Exchange, Category: r.Category, Symbol: r.Signal.Symbol, Interval: r.Signal.Interval}}
		selector, err := strategy.NewSelector(strategy.SelectionPolicy{MinConfidence: 70, MaxDataAge: 3 * time.Second})
		if err != nil {
			t.Fatal(err)
		}
		service := appstrategy.NewGenerationService(selector, repo, clock.FixedClock{Time: r.RecordedAt})
		for i := 0; i < 2; i++ {
			got, err := service.GenerateAndRecord(ctx, m)
			if err != nil || got.Signal.Type != strategy.SignalNoTrade || got.Signal.Trade != nil {
				t.Fatalf("generation: %+v, %v", got, err)
			}
			if (i == 0 && got.Stats.Inserted != 1) || (i == 1 && got.Stats.Skipped != 1) {
				t.Fatalf("retry statistics: %+v", got.Stats)
			}
		}
		stored, err := repo.ListSignals(ctx, strategy.SignalQuery{SignalID: m.SignalID})
		if err != nil || len(stored) != 1 || stored[0].Features["runtime.kill_switch_active"] != "true" || stored[0].Signal.Reason != "kill_switch_active" {
			t.Fatalf("persisted rejection: %+v, %v", stored, err)
		}
	})
	t.Run("database constraints reject invalid raw payloads", func(t *testing.T) {
		for i, scenario := range []string{"missing trade", "null trade", "no trade with payload", "null identity", "missing stop", "wrong symbol", "wrong side", "invalid geometry", "zero leverage", "null risk", "invalid features", "invalid interval", "recorded too early"} {
			t.Run(scenario, func(t *testing.T) {
				r := testStrategyAudit(t, false)
				args := signalDriverArgs(t, r)
				args[0] = fmt.Sprintf("strategy_repo_constraint_%d", i)
				var payload map[string]any
				if err := json.Unmarshal([]byte(args[14].(string)), &payload); err != nil {
					t.Fatal(err)
				}
				payload["SignalID"] = args[0]
				switch scenario {
				case "no trade with payload":
					args[9] = "NO_TRADE"
				case "null identity":
					payload["HypothesisID"] = nil
				case "missing stop":
					delete(payload, "StopLoss")
				case "wrong symbol":
					payload["Symbol"] = "ETHUSDT"
				case "wrong side":
					payload["Side"] = "SHORT"
				case "invalid geometry":
					payload["StopLoss"] = "105"
				case "zero leverage":
					payload["Leverage"] = "0"
				case "null risk":
					payload["HypothesisMaxRiskPct"] = nil
				case "invalid features":
					args[15] = "[]"
				case "invalid interval":
					args[8] = "invalid"
				case "recorded too early":
					args[13] = r.Signal.CreatedAt.Add(-time.Microsecond)
				}
				encoded, err := json.Marshal(payload)
				if err != nil {
					t.Fatal(err)
				}
				args[14] = string(encoded)
				if scenario == "missing trade" {
					args[14] = nil
				}
				if scenario == "null trade" {
					args[14] = "null"
				}
				sqlArgs := make([]any, len(args))
				for j, arg := range args {
					sqlArgs[j] = arg
				}
				_, err = db.ExecContext(ctx, `INSERT INTO strategy_signals (
					signal_id, hypothesis_id, hypothesis_version, strategy_name, mode, exchange, category, symbol,
					interval, signal_type, reason, created_at, observed_at, recorded_at, trade_json, features_json
				) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15::jsonb,$16::jsonb)`, sqlArgs...)
				if err == nil {
					t.Fatalf("database accepted invalid %s", scenario)
				}
			})
		}
	})
}
