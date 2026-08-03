package main

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/shopspring/decimal"

	"github.com/VersoIt/Inquisitor/internal/config"
	domainlive "github.com/VersoIt/Inquisitor/internal/live"
	domainrisk "github.com/VersoIt/Inquisitor/internal/risk"
)

func TestLivePositionDriftSymbolListFromFlagTableDriven(t *testing.T) {
	tests := []struct {
		name        string
		value       string
		want        []string
		wantPresent bool
		wantErrSub  string
	}{
		{name: "empty"},
		{name: "normalizes lowercase", value: "btcusdt,ETHUSDT", want: []string{"BTCUSDT", "ETHUSDT"}, wantPresent: true},
		{name: "untrimmed list", value: " BTCUSDT", wantErrSub: "trimmed"},
		{name: "item whitespace", value: "BTCUSDT, ETHUSDT", wantErrSub: "whitespace"},
		{name: "empty item", value: "BTCUSDT,,ETHUSDT", wantErrSub: "empty"},
		{name: "duplicate", value: "btcusdt,BTCUSDT", wantErrSub: "duplicates"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, present, err := livePositionDriftSymbolListFromFlag(tt.value)
			if tt.wantErrSub != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("parse symbols: %v", err)
			}
			if present != tt.wantPresent || strings.Join(got, ",") != strings.Join(tt.want, ",") {
				t.Fatalf("symbols mismatch: got=%#v present=%t want=%#v present=%t", got, present, tt.want, tt.wantPresent)
			}
		})
	}
}

func TestLivePositionDriftQueriesFromConfigTableDriven(t *testing.T) {
	cfg := validLivePositionDriftConfig()

	tests := []struct {
		name        string
		cfg         *config.Config
		symbols     []string
		hasExplicit bool
		want        []domainlive.PositionSnapshotQuery
		wantErrSub  string
	}{
		{
			name: "config symbols",
			cfg:  cfg,
			want: []domainlive.PositionSnapshotQuery{
				{Exchange: "bybit", Category: "linear", Symbol: "BTCUSDT"},
				{Exchange: "bybit", Category: "linear", Symbol: "ETHUSDT"},
			},
		},
		{
			name:        "explicit symbols",
			cfg:         cfg,
			symbols:     []string{"SOLUSDT"},
			hasExplicit: true,
			want: []domainlive.PositionSnapshotQuery{
				{Exchange: "bybit", Category: "linear", Symbol: "SOLUSDT"},
			},
		},
		{name: "nil config", wantErrSub: "config"},
		{name: "missing symbols", cfg: &config.Config{Exchange: config.ExchangeConfig{Primary: "bybit", Category: "linear"}}, wantErrSub: "symbol"},
		{name: "invalid exchange", cfg: &config.Config{Exchange: config.ExchangeConfig{Primary: "", Category: "linear", Symbols: []string{"BTCUSDT"}}}, wantErrSub: "exchange"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := livePositionDriftQueriesFromConfig(tt.cfg, tt.symbols, tt.hasExplicit)
			if tt.wantErrSub != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("queries from config: %v", err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("query length mismatch: got %#v want %#v", got, tt.want)
			}
			for index := range got {
				if got[index] != tt.want[index] {
					t.Fatalf("query[%d] mismatch: got %#v want %#v", index, got[index], tt.want[index])
				}
			}
		})
	}
}

func TestRunLivePositionDriftRejectsUnsafeFlagsBeforeSideEffects(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantErrSub string
	}{
		{name: "untrimmed symbols", args: []string{"-symbols", " BTCUSDT"}, wantErrSub: "symbols"},
		{name: "current max age", args: []string{"-current-max-age", "0s"}, wantErrSub: "current-max-age"},
		{name: "baseline max age", args: []string{"-baseline-max-age", "0s"}, wantErrSub: "baseline-max-age"},
		{name: "timeout", args: []string{"-timeout", "0s"}, wantErrSub: "timeout"},
		{name: "untrimmed kill switch event id", args: []string{"-activate-kill-switch-on-blocked", "-kill-switch-event-id", " risk_kill_switch_drift_0001 "}, wantErrSub: "kill-switch-event-id"},
		{name: "kill switch event id without activation", args: []string{"-kill-switch-event-id", "risk_kill_switch_drift_0001"}, wantErrSub: "activate-kill-switch-on-blocked"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var loaded bool
			var opened bool
			err := runLivePositionDrift(context.Background(), tt.args, livePositionDriftDependencies{
				loadConfig: func(string) (*config.Config, error) {
					loaded = true
					return validLivePositionDriftConfig(), nil
				},
				openDB: func(context.Context, config.DatabaseConfig) (*sql.DB, error) {
					opened = true
					return nil, nil
				},
				output: &bytes.Buffer{},
			})
			if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
			if loaded || opened {
				t.Fatalf("unsafe flags must stop before side effects: loaded=%t opened=%t", loaded, opened)
			}
		})
	}
}

func TestLivePositionDriftKillSwitchEventIDFromFlagTableDriven(t *testing.T) {
	tests := []struct {
		name       string
		value      string
		activate   bool
		want       string
		wantErrSub string
	}{
		{name: "empty without activation"},
		{name: "empty with activation", activate: true},
		{name: "explicit with activation", value: "risk_kill_switch_drift_0001", activate: true, want: "risk_kill_switch_drift_0001"},
		{name: "untrimmed", value: " risk_kill_switch_drift_0001 ", activate: true, wantErrSub: "trimmed"},
		{name: "without activation", value: "risk_kill_switch_drift_0001", wantErrSub: "activate-kill-switch-on-blocked"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := livePositionDriftKillSwitchEventIDFromFlag(tt.value, tt.activate)
			if tt.wantErrSub != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("event id from flag: %v", err)
			}
			if got != tt.want {
				t.Fatalf("event id mismatch: got %q want %q", got, tt.want)
			}
		})
	}
}

func TestLivePositionDriftGeneratedKillSwitchEventIDIsUTC(t *testing.T) {
	now := time.Date(2026, 8, 2, 15, 30, 45, 123456789, time.FixedZone("MSK", 3*60*60))
	got := livePositionDriftGeneratedKillSwitchEventID(now)
	want := "risk_kill_switch_live_position_drift_20260802T123045.123456789Z"
	if got != want {
		t.Fatalf("generated id mismatch: got %q want %q", got, want)
	}
}

func TestRunLivePositionDriftLogsClearReport(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	query := domainlive.PositionSnapshotQuery{Exchange: "bybit", Category: "linear", Symbol: "BTCUSDT"}
	current := livePositionDriftFlatSnapshot(t, query, now.Add(-time.Second))
	baseline := livePositionDriftFlatSnapshot(t, query, now.Add(-time.Minute))
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	reader := &fakeLivePositionDriftReader{snapshots: map[string]domainlive.PositionSnapshot{livePositionDriftTestKey(query): current}}
	history := &fakeLivePositionDriftHistory{snapshots: map[string]domainlive.PositionSnapshot{livePositionDriftTestKey(query): baseline}}
	var output bytes.Buffer
	err = runLivePositionDrift(context.Background(), []string{
		"-symbols", "BTCUSDT",
	}, livePositionDriftDependencies{
		loadConfig: func(string) (*config.Config, error) {
			return validLivePositionDriftConfig(), nil
		},
		openDB: func(context.Context, config.DatabaseConfig) (*sql.DB, error) {
			return db, nil
		},
		newPositionReader: func(*config.Config) (domainlive.PositionSnapshotReader, error) {
			return reader, nil
		},
		newHistoryReader: func(*sql.DB) domainlive.PositionSnapshotHistoryReader {
			return history
		},
		now: func() time.Time {
			return now
		},
		output: &output,
	})
	if err != nil {
		t.Fatalf("run live position drift: %v\nlogs:\n%s", err, output.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
	}
	if reader.calls != 1 || history.calls != 1 || reader.query.Symbol != "BTCUSDT" || history.query.Symbol != "BTCUSDT" {
		t.Fatalf("query mismatch: reader=%d %#v history=%d %#v", reader.calls, reader.query, history.calls, history.query)
	}
	logs := output.String()
	for _, want := range []string{
		`"msg":"live position drift report"`,
		`"status":"CLEAR"`,
		`"symbols":1`,
		`"msg":"live position drift comparison"`,
		`"current_open":false`,
		`"has_db_baseline":true`,
		`"msg":"live position drift check"`,
		`"name":"position_exposure_drift"`,
		`"status":"PASS"`,
		`"msg":"live position drift completed"`,
	} {
		if !strings.Contains(logs, want) {
			t.Fatalf("expected logs to contain %s, got\n%s", want, logs)
		}
	}
}

func TestRunLivePositionDriftCanFailOnBlockedStatus(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	query := domainlive.PositionSnapshotQuery{Exchange: "bybit", Category: "linear", Symbol: "BTCUSDT"}
	current := livePositionDriftOpenSnapshot(t, query, now.Add(-time.Second))
	baseline := livePositionDriftFlatSnapshot(t, query, now.Add(-time.Minute))
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	var output bytes.Buffer
	err = runLivePositionDrift(context.Background(), []string{
		"-symbols", "BTCUSDT",
		"-fail-on-blocked",
	}, livePositionDriftDependencies{
		loadConfig: func(string) (*config.Config, error) {
			return validLivePositionDriftConfig(), nil
		},
		openDB: func(context.Context, config.DatabaseConfig) (*sql.DB, error) {
			return db, nil
		},
		newPositionReader: func(*config.Config) (domainlive.PositionSnapshotReader, error) {
			return &fakeLivePositionDriftReader{snapshots: map[string]domainlive.PositionSnapshot{livePositionDriftTestKey(query): current}}, nil
		},
		newHistoryReader: func(*sql.DB) domainlive.PositionSnapshotHistoryReader {
			return &fakeLivePositionDriftHistory{snapshots: map[string]domainlive.PositionSnapshot{livePositionDriftTestKey(query): baseline}}
		},
		now: func() time.Time {
			return now
		},
		output: &output,
	})
	if err == nil || !strings.Contains(err.Error(), "position_exposure_drift") {
		t.Fatalf("expected blocked drift error, got %v\nlogs:\n%s", err, output.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
	}
	if !strings.Contains(output.String(), `"status":"BLOCKED"`) ||
		!strings.Contains(output.String(), `"name":"position_exposure_drift"`) ||
		!strings.Contains(output.String(), `"status":"FAIL"`) {
		t.Fatalf("expected blocked drift logs, got\n%s", output.String())
	}
}

func TestRunLivePositionDriftActivatesKillSwitchOnBlockedStatus(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	query := domainlive.PositionSnapshotQuery{Exchange: "bybit", Category: "linear", Symbol: "BTCUSDT"}
	current := livePositionDriftOpenSnapshot(t, query, now.Add(-time.Second))
	baseline := livePositionDriftFlatSnapshot(t, query, now.Add(-time.Minute))
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	killSwitch := &fakeLivePositionDriftKillSwitchRepository{}
	var output bytes.Buffer
	err = runLivePositionDrift(context.Background(), []string{
		"-symbols", "BTCUSDT",
		"-activate-kill-switch-on-blocked",
		"-kill-switch-event-id", "risk_kill_switch_drift_0001",
	}, livePositionDriftDependencies{
		loadConfig: func(string) (*config.Config, error) {
			return validLivePositionDriftConfig(), nil
		},
		openDB: func(context.Context, config.DatabaseConfig) (*sql.DB, error) {
			return db, nil
		},
		newPositionReader: func(*config.Config) (domainlive.PositionSnapshotReader, error) {
			return &fakeLivePositionDriftReader{snapshots: map[string]domainlive.PositionSnapshot{livePositionDriftTestKey(query): current}}, nil
		},
		newHistoryReader: func(*sql.DB) domainlive.PositionSnapshotHistoryReader {
			return &fakeLivePositionDriftHistory{snapshots: map[string]domainlive.PositionSnapshot{livePositionDriftTestKey(query): baseline}}
		},
		newKillSwitch: func(*sql.DB) domainrisk.KillSwitchRepository {
			return killSwitch
		},
		now: func() time.Time {
			return now
		},
		output: &output,
	})
	if err == nil || !strings.Contains(err.Error(), "position_exposure_drift") {
		t.Fatalf("expected blocked drift error, got %v\nlogs:\n%s", err, output.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
	}
	if killSwitch.appendCalls != 1 || len(killSwitch.appended) != 1 {
		t.Fatalf("kill switch append mismatch: calls=%d events=%#v", killSwitch.appendCalls, killSwitch.appended)
	}
	event := killSwitch.appended[0]
	if !event.Active ||
		event.EventID != "risk_kill_switch_drift_0001" ||
		event.Source != "live_position_drift" ||
		event.Reason != "position drift BLOCKED: position_exposure_drift" ||
		!event.CreatedAt.Equal(now) {
		t.Fatalf("kill switch event mismatch: %#v", event)
	}
	if !strings.Contains(output.String(), `"msg":"live position drift activated kill switch"`) ||
		!strings.Contains(output.String(), `"event_id":"risk_kill_switch_drift_0001"`) {
		t.Fatalf("expected kill switch activation log, got\n%s", output.String())
	}
}

func TestRunLivePositionDriftPropagatesReaderCreationErrors(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	var output bytes.Buffer
	err = runLivePositionDrift(context.Background(), []string{
		"-symbols", "BTCUSDT",
	}, livePositionDriftDependencies{
		loadConfig: func(string) (*config.Config, error) {
			return validLivePositionDriftConfig(), nil
		},
		openDB: func(context.Context, config.DatabaseConfig) (*sql.DB, error) {
			return db, nil
		},
		newPositionReader: func(*config.Config) (domainlive.PositionSnapshotReader, error) {
			return nil, errors.New("credentials missing")
		},
		output: &output,
	})
	if err == nil || !strings.Contains(err.Error(), "credentials missing") {
		t.Fatalf("expected reader creation error, got %v", err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
	}
}

type fakeLivePositionDriftReader struct {
	snapshots map[string]domainlive.PositionSnapshot
	query     domainlive.PositionSnapshotQuery
	calls     int
	err       error
}

func (r *fakeLivePositionDriftReader) GetPositionSnapshot(
	_ context.Context,
	query domainlive.PositionSnapshotQuery,
) (domainlive.PositionSnapshot, error) {
	r.calls++
	r.query = query
	if r.err != nil {
		return domainlive.PositionSnapshot{}, r.err
	}
	snapshot, ok := r.snapshots[livePositionDriftTestKey(query)]
	if !ok {
		return domainlive.PositionSnapshot{}, errors.New("missing current snapshot fixture")
	}
	return snapshot, nil
}

type fakeLivePositionDriftHistory struct {
	snapshots map[string]domainlive.PositionSnapshot
	query     domainlive.PositionSnapshotQuery
	calls     int
	err       error
}

func (r *fakeLivePositionDriftHistory) GetLatestPositionSnapshot(
	_ context.Context,
	query domainlive.PositionSnapshotQuery,
) (domainlive.PositionSnapshot, bool, error) {
	r.calls++
	r.query = query
	if r.err != nil {
		return domainlive.PositionSnapshot{}, false, r.err
	}
	snapshot, ok := r.snapshots[livePositionDriftTestKey(query)]
	return snapshot, ok, nil
}

type fakeLivePositionDriftKillSwitchRepository struct {
	appendCalls int
	appended    []domainrisk.KillSwitchEvent
	appendErr   error
}

func (r *fakeLivePositionDriftKillSwitchRepository) AppendKillSwitchEvent(_ context.Context, event domainrisk.KillSwitchEvent) (domainrisk.KillSwitchStats, error) {
	r.appendCalls++
	if r.appendErr != nil {
		return domainrisk.KillSwitchStats{}, r.appendErr
	}
	r.appended = append(r.appended, event)
	return domainrisk.KillSwitchStats{Inserted: 1}, nil
}

func (r *fakeLivePositionDriftKillSwitchRepository) CurrentKillSwitchState(context.Context) (domainrisk.KillSwitchState, error) {
	return domainrisk.KillSwitchState{}, nil
}

func (r *fakeLivePositionDriftKillSwitchRepository) ListKillSwitchEvents(context.Context, domainrisk.KillSwitchEventQuery) ([]domainrisk.KillSwitchEvent, error) {
	return nil, errors.New("not implemented")
}

func validLivePositionDriftConfig() *config.Config {
	return &config.Config{
		App: config.AppConfig{LogLevel: "info"},
		Exchange: config.ExchangeConfig{
			Primary:  "bybit",
			Category: "linear",
			Symbols:  []string{"BTCUSDT", "ETHUSDT"},
		},
	}
}

func livePositionDriftFlatSnapshot(
	t *testing.T,
	query domainlive.PositionSnapshotQuery,
	observedAt time.Time,
) domainlive.PositionSnapshot {
	t.Helper()

	snapshot, err := domainlive.NewPositionSnapshot(domainlive.PositionSnapshotInput{
		Exchange:       query.Exchange,
		Category:       query.Category,
		Symbol:         query.Symbol,
		Size:           decimal.Zero,
		MarkPrice:      decimal.RequireFromString("100000"),
		ExchangeStatus: domainlive.ExchangePositionStatusNormal,
		PositionIndex:  0,
		Sequence:       -1,
		ObservedAt:     observedAt,
	})
	if err != nil {
		t.Fatalf("new flat position snapshot: %v", err)
	}
	return snapshot
}

func livePositionDriftOpenSnapshot(
	t *testing.T,
	query domainlive.PositionSnapshotQuery,
	observedAt time.Time,
) domainlive.PositionSnapshot {
	t.Helper()

	snapshot, err := domainlive.NewPositionSnapshot(domainlive.PositionSnapshotInput{
		Exchange:          query.Exchange,
		Category:          query.Category,
		Symbol:            query.Symbol,
		Side:              domainlive.OrderSideLong,
		Size:              decimal.RequireFromString("0.001"),
		AveragePrice:      decimal.RequireFromString("100000"),
		PositionValue:     decimal.RequireFromString("100"),
		MarkPrice:         decimal.RequireFromString("100000"),
		LiquidationPrice:  decimal.RequireFromString("99000"),
		Leverage:          decimal.RequireFromString("1"),
		ExchangeStatus:    domainlive.ExchangePositionStatusNormal,
		PositionIndex:     0,
		Sequence:          123,
		ExchangeCreatedAt: observedAt.Add(-time.Minute),
		ExchangeUpdatedAt: observedAt,
		ObservedAt:        observedAt,
	})
	if err != nil {
		t.Fatalf("new open position snapshot: %v", err)
	}
	return snapshot
}

func livePositionDriftTestKey(query domainlive.PositionSnapshotQuery) string {
	return query.Exchange + "/" + query.Category + "/" + query.Symbol
}
