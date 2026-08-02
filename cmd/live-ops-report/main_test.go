package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/shopspring/decimal"

	"github.com/VersoIt/Inquisitor/internal/config"
	domainlive "github.com/VersoIt/Inquisitor/internal/live"
	domainrisk "github.com/VersoIt/Inquisitor/internal/risk"
)

func TestLiveOpsPendingQueryFromFlagsTableDriven(t *testing.T) {
	tests := []struct {
		name       string
		symbol     string
		limit      int
		want       domainlive.PendingLiveDecisionQuery
		wantErrSub string
	}{
		{name: "default", want: domainlive.PendingLiveDecisionQuery{Limit: 10}},
		{name: "normalizes lowercase symbol", symbol: "btcusdt", limit: 5, want: domainlive.PendingLiveDecisionQuery{Symbol: "BTCUSDT", Limit: 5}},
		{name: "zero limit defaults", symbol: "ETHUSDT", limit: 0, want: domainlive.PendingLiveDecisionQuery{Symbol: "ETHUSDT", Limit: 10}},
		{name: "untrimmed symbol", symbol: " BTCUSDT ", limit: 10, wantErrSub: "trimmed"},
		{name: "negative limit", limit: -1, wantErrSub: "limit"},
		{name: "limit above max", limit: 101, wantErrSub: "limit"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := liveOpsPendingQueryFromFlags(tt.symbol, tt.limit)
			if tt.wantErrSub != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("query from flags: %v", err)
			}
			if got != tt.want {
				t.Fatalf("query mismatch: got %#v want %#v", got, tt.want)
			}
		})
	}
}

func TestLiveOpsAuditLimitFromFlagsTableDriven(t *testing.T) {
	tests := []struct {
		name       string
		limit      int
		want       int
		wantErrSub string
	}{
		{name: "default", want: 10},
		{name: "explicit", limit: 5, want: 5},
		{name: "negative", limit: -1, wantErrSub: "limit"},
		{name: "above max", limit: 101, wantErrSub: "limit"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := liveOpsAuditLimitFromFlags(tt.limit)
			if tt.wantErrSub != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("audit limit from flags: %v", err)
			}
			if got != tt.want {
				t.Fatalf("limit mismatch: got %d want %d", got, tt.want)
			}
		})
	}
}

func TestLiveOpsPositionDriftSymbolListFromFlagTableDriven(t *testing.T) {
	tests := []struct {
		name       string
		value      string
		want       []string
		wantHas    bool
		wantErrSub string
	}{
		{name: "empty disables drift symbols"},
		{name: "single symbol uppercased", value: "btcusdt", want: []string{"BTCUSDT"}, wantHas: true},
		{name: "multiple symbols", value: "BTCUSDT,ETHUSDT", want: []string{"BTCUSDT", "ETHUSDT"}, wantHas: true},
		{name: "untrimmed list", value: " BTCUSDT ", wantErrSub: "trimmed"},
		{name: "item whitespace", value: "BTCUSDT, ETHUSDT", wantErrSub: "whitespace"},
		{name: "empty item", value: "BTCUSDT,,ETHUSDT", wantErrSub: "empty"},
		{name: "duplicates", value: "BTCUSDT,btcusdt", wantErrSub: "duplicates"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, has, err := liveOpsPositionDriftSymbolListFromFlag(tt.value)
			if tt.wantErrSub != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("symbol list from flag: %v", err)
			}
			if has != tt.wantHas || strings.Join(got, ",") != strings.Join(tt.want, ",") {
				t.Fatalf("symbols mismatch: got %v has=%t want %v has=%t", got, has, tt.want, tt.wantHas)
			}
		})
	}
}

func TestLiveOpsPositionDriftQueriesFromConfigTableDriven(t *testing.T) {
	tests := []struct {
		name               string
		cfg                *config.Config
		explicitSymbols    []string
		hasExplicitSymbols bool
		want               []domainlive.PositionSnapshotQuery
		wantErrSub         string
	}{
		{
			name: "uses config symbols by default",
			cfg:  validLiveOpsConfig(),
			want: []domainlive.PositionSnapshotQuery{
				{Exchange: "bybit", Category: "linear", Symbol: "BTCUSDT"},
				{Exchange: "bybit", Category: "linear", Symbol: "ETHUSDT"},
			},
		},
		{
			name:               "explicit symbols override config",
			cfg:                validLiveOpsConfig(),
			explicitSymbols:    []string{"SOLUSDT"},
			hasExplicitSymbols: true,
			want: []domainlive.PositionSnapshotQuery{
				{Exchange: "bybit", Category: "linear", Symbol: "SOLUSDT"},
			},
		},
		{name: "nil config", wantErrSub: "config"},
		{name: "missing symbols", cfg: &config.Config{Exchange: config.ExchangeConfig{Primary: "bybit", Category: "linear"}}, wantErrSub: "symbol"},
		{name: "missing exchange", cfg: &config.Config{Exchange: config.ExchangeConfig{Category: "linear", Symbols: []string{"BTCUSDT"}}}, wantErrSub: "exchange"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := liveOpsPositionDriftQueriesFromConfig(tt.cfg, tt.explicitSymbols, tt.hasExplicitSymbols)
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
				t.Fatalf("query count mismatch: got %#v want %#v", got, tt.want)
			}
			for index := range got {
				if got[index] != tt.want[index] {
					t.Fatalf("query[%d] mismatch: got %#v want %#v", index, got[index], tt.want[index])
				}
			}
		})
	}
}

func TestRunLiveOpsReportRejectsUnsafeFlagsBeforeSideEffects(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		wantErrSub string
	}{
		{name: "untrimmed symbol", args: []string{"-symbol", " BTCUSDT "}, wantErrSub: "symbol"},
		{name: "pending limit above max", args: []string{"-pending-limit", "101"}, wantErrSub: "limit"},
		{name: "audit limit above max", args: []string{"-audit-limit", "101"}, wantErrSub: "limit"},
		{name: "untrimmed artifact path", args: []string{"-artifact-path", " artifacts/live-ops-report.json "}, wantErrSub: "artifact-path"},
		{name: "untrimmed first order review path", args: []string{"-first-order-review-file", " artifacts/live-first-order-review.json "}, wantErrSub: "first-order-review-file"},
		{name: "bad timeout", args: []string{"-timeout", "0s"}, wantErrSub: "timeout"},
		{name: "bad first order max age", args: []string{"-max-first-order-review-age", "0s"}, wantErrSub: "max-first-order-review-age"},
		{name: "bad position drift current age", args: []string{"-position-drift-current-max-age", "0s"}, wantErrSub: "position-drift-current-max-age"},
		{name: "bad position drift baseline age", args: []string{"-position-drift-baseline-max-age", "0s"}, wantErrSub: "position-drift-baseline-max-age"},
		{name: "untrimmed position drift symbols", args: []string{"-position-drift-symbols", " BTCUSDT "}, wantErrSub: "position-drift-symbols"},
		{name: "position drift symbol item whitespace", args: []string{"-position-drift-symbols", "BTCUSDT, ETHUSDT"}, wantErrSub: "position-drift-symbols"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var loaded bool
			var opened bool
			err := runLiveOpsReport(context.Background(), tt.args, liveOpsReportDependencies{
				loadConfig: func(string) (*config.Config, error) {
					loaded = true
					return &config.Config{}, nil
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

func TestRunLiveOpsReportIncludesPositionDriftWhenExplicitlyEnabled(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	query := domainlive.PositionSnapshotQuery{Exchange: "bybit", Category: "linear", Symbol: "BTCUSDT"}
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	artifactPath := filepath.Join(t.TempDir(), "artifacts", "live-ops-report.json")
	positionReader := &fakeLiveOpsPositionReader{snapshots: map[string]domainlive.PositionSnapshot{
		liveOpsPositionKey(query): liveOpsPositionSnapshot(t, query, now.Add(-time.Second), false),
	}}
	positionHistory := &fakeLiveOpsPositionHistoryReader{snapshots: map[string]domainlive.PositionSnapshot{
		liveOpsPositionKey(query): liveOpsPositionSnapshot(t, query, now.Add(-time.Minute), false),
	}}
	var output bytes.Buffer
	err = runLiveOpsReport(context.Background(), []string{
		"-symbol", "BTCUSDT",
		"-position-drift",
		"-position-drift-symbols", "BTCUSDT",
		"-artifact-path", artifactPath,
	}, liveOpsReportDependencies{
		loadConfig: func(string) (*config.Config, error) {
			return validLiveOpsConfig(), nil
		},
		openDB: func(context.Context, config.DatabaseConfig) (*sql.DB, error) {
			return db, nil
		},
		newPendingReader: func(*sql.DB) domainlive.PendingLiveDecisionReader {
			return &fakeLiveOpsPendingReader{candidates: []domainlive.PendingLiveDecision{
				liveOpsPendingDecision("risk_decision_live_ops_cli_0001", "BTCUSDT", now.Add(-2*time.Minute)),
			}}
		},
		newAuditReader: func(*sql.DB) domainlive.LiveLoopAuditReader {
			return &fakeLiveOpsAuditReader{runs: []domainlive.LiveLoopRunAudit{
				liveOpsAuditRun(now.Add(-time.Minute), domainlive.LiveLoopRunStatusCompleted),
			}}
		},
		newKillSwitch: func(*sql.DB) domainrisk.KillSwitchRepository {
			return &fakeLiveOpsKillSwitchRepository{}
		},
		newPositionReader: func(*config.Config) (domainlive.PositionSnapshotReader, error) {
			return positionReader, nil
		},
		newPositionHistory: func(*sql.DB) domainlive.PositionSnapshotHistoryReader {
			return positionHistory
		},
		now: func() time.Time {
			return now
		},
		output: &output,
	})
	if err != nil {
		t.Fatalf("run live ops report with drift: %v\nlogs:\n%s", err, output.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
	}
	if positionReader.calls != 1 || positionHistory.calls != 1 ||
		positionReader.queries[0] != query || positionHistory.queries[0] != query {
		t.Fatalf("position drift query mismatch: reader=%#v history=%#v", positionReader.queries, positionHistory.queries)
	}
	logs := output.String()
	for _, want := range []string{
		`"position_drift":true`,
		`"position_drift_status":"CLEAR"`,
		`"msg":"live ops position drift comparison"`,
		`"symbol":"BTCUSDT"`,
		`"status":"CLEAR"`,
		`"name":"position_exposure_drift"`,
	} {
		if !strings.Contains(logs, want) {
			t.Fatalf("expected logs to contain %s, got\n%s", want, logs)
		}
	}
	artifact := readLiveOpsReportArtifact(t, artifactPath)
	if artifact.PositionDrift == nil ||
		artifact.PositionDrift.Status != domainlive.LiveOpsStatusClear ||
		len(artifact.PositionDrift.Comparisons) != 1 ||
		artifact.PositionDrift.Comparisons[0].Symbol != "BTCUSDT" {
		t.Fatalf("position drift artifact mismatch: %#v", artifact.PositionDrift)
	}
}

func TestRunLiveOpsReportCanFailOnBlockedPositionDrift(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	query := domainlive.PositionSnapshotQuery{Exchange: "bybit", Category: "linear", Symbol: "BTCUSDT"}
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	positionReader := &fakeLiveOpsPositionReader{snapshots: map[string]domainlive.PositionSnapshot{
		liveOpsPositionKey(query): liveOpsPositionSnapshot(t, query, now.Add(-time.Second), true),
	}}
	positionHistory := &fakeLiveOpsPositionHistoryReader{snapshots: map[string]domainlive.PositionSnapshot{
		liveOpsPositionKey(query): liveOpsPositionSnapshot(t, query, now.Add(-time.Minute), false),
	}}
	var output bytes.Buffer
	err = runLiveOpsReport(context.Background(), []string{
		"-symbol", "BTCUSDT",
		"-position-drift-symbols", "BTCUSDT",
		"-fail-on-blocked",
	}, liveOpsReportDependencies{
		loadConfig: func(string) (*config.Config, error) {
			return validLiveOpsConfig(), nil
		},
		openDB: func(context.Context, config.DatabaseConfig) (*sql.DB, error) {
			return db, nil
		},
		newPendingReader: func(*sql.DB) domainlive.PendingLiveDecisionReader {
			return &fakeLiveOpsPendingReader{candidates: []domainlive.PendingLiveDecision{
				liveOpsPendingDecision("risk_decision_live_ops_cli_0001", "BTCUSDT", now.Add(-2*time.Minute)),
			}}
		},
		newAuditReader: func(*sql.DB) domainlive.LiveLoopAuditReader {
			return &fakeLiveOpsAuditReader{runs: []domainlive.LiveLoopRunAudit{
				liveOpsAuditRun(now.Add(-time.Minute), domainlive.LiveLoopRunStatusCompleted),
			}}
		},
		newKillSwitch: func(*sql.DB) domainrisk.KillSwitchRepository {
			return &fakeLiveOpsKillSwitchRepository{}
		},
		newPositionReader: func(*config.Config) (domainlive.PositionSnapshotReader, error) {
			return positionReader, nil
		},
		newPositionHistory: func(*sql.DB) domainlive.PositionSnapshotHistoryReader {
			return positionHistory
		},
		now: func() time.Time {
			return now
		},
		output: &output,
	})
	if err == nil || !strings.Contains(err.Error(), "position_exposure_drift") {
		t.Fatalf("expected drift blocked error, got %v\nlogs:\n%s", err, output.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
	}
	if !strings.Contains(output.String(), `"position_drift_status":"BLOCKED"`) ||
		!strings.Contains(output.String(), `"name":"position_exposure_drift"`) ||
		!strings.Contains(output.String(), `"status":"FAIL"`) {
		t.Fatalf("expected blocked drift logs, got\n%s", output.String())
	}
}

func TestRunLiveOpsReportLogsClearReportAndWritesArtifact(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	reviewPath := writeLiveOpsFirstOrderReviewArtifact(t, validLiveOpsFirstOrderReviewArtifact(now.Add(-time.Minute)))
	artifactPath := filepath.Join(t.TempDir(), "artifacts", "live-ops-report.json")
	pendingReader := &fakeLiveOpsPendingReader{candidates: []domainlive.PendingLiveDecision{
		liveOpsPendingDecision("risk_decision_live_ops_cli_0001", "BTCUSDT", now.Add(-2*time.Minute)),
	}}
	auditReader := &fakeLiveOpsAuditReader{runs: []domainlive.LiveLoopRunAudit{
		liveOpsAuditRun(now.Add(-time.Minute), domainlive.LiveLoopRunStatusCompleted),
	}}
	killSwitch := &fakeLiveOpsKillSwitchRepository{}

	var output bytes.Buffer
	err = runLiveOpsReport(context.Background(), []string{
		"-symbol", "btcusdt",
		"-pending-limit", "10",
		"-audit-limit", "10",
		"-first-order-review-file", reviewPath,
		"-artifact-path", artifactPath,
	}, liveOpsReportDependencies{
		loadConfig: func(string) (*config.Config, error) {
			return validLiveOpsConfig(), nil
		},
		openDB: func(context.Context, config.DatabaseConfig) (*sql.DB, error) {
			return db, nil
		},
		newPendingReader: func(*sql.DB) domainlive.PendingLiveDecisionReader {
			return pendingReader
		},
		newAuditReader: func(*sql.DB) domainlive.LiveLoopAuditReader {
			return auditReader
		},
		newKillSwitch: func(*sql.DB) domainrisk.KillSwitchRepository {
			return killSwitch
		},
		now: func() time.Time {
			return now
		},
		output: &output,
	})
	if err != nil {
		t.Fatalf("run live ops report: %v\nlogs:\n%s", err, output.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
	}
	if killSwitch.calls != 1 {
		t.Fatalf("kill switch calls mismatch: %d", killSwitch.calls)
	}
	if pendingReader.query.Symbol != "BTCUSDT" || pendingReader.query.Limit != 10 {
		t.Fatalf("pending query mismatch: %#v", pendingReader.query)
	}
	if auditReader.query.Limit != 10 || !auditReader.query.IncludeIterations {
		t.Fatalf("audit query mismatch: %#v", auditReader.query)
	}

	logs := output.String()
	for _, want := range []string{
		`"msg":"live ops report"`,
		`"status":"CLEAR"`,
		`"pending_candidates":1`,
		`"audit_review_status":"CLEAR"`,
		`"first_order_review":true`,
		`"msg":"live ops report check"`,
		`"name":"first_order_review"`,
		`"status":"PASS"`,
		`"msg":"live ops first-order review"`,
		`"latest_order_status":"FILLED"`,
		`"msg":"live ops report artifact written"`,
		`"msg":"live ops report completed"`,
	} {
		if !strings.Contains(logs, want) {
			t.Fatalf("expected logs to contain %s, got\n%s", want, logs)
		}
	}

	artifact := readLiveOpsReportArtifact(t, artifactPath)
	reviewSHA, _, err := loadLiveOpsFirstOrderReviewArtifactFile(reviewPath)
	if err != nil {
		t.Fatalf("reload first order artifact: %v", err)
	}
	if artifact.SchemaVersion != domainlive.LiveOpsReportArtifactSchemaVersion ||
		!artifact.CreatedAt.Equal(now) ||
		artifact.ConfigPath != "configs/config.example.yaml" ||
		artifact.Status != domainlive.LiveOpsStatusClear ||
		artifact.Summary.Failed != 0 ||
		artifact.Pending.Symbol != "BTCUSDT" ||
		artifact.Pending.Total != 1 ||
		artifact.Audit.ReviewStatus != domainlive.LiveLoopAuditReviewStatusClear ||
		artifact.FirstOrderReview == nil ||
		artifact.FirstOrderReview.Path != reviewPath ||
		artifact.FirstOrderReview.SHA256 != reviewSHA.SHA256 ||
		!artifact.FirstOrderReview.Ready {
		t.Fatalf("ops artifact mismatch: %#v", artifact)
	}
}

func TestRunLiveOpsReportBlockedStatusIsReportByDefault(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	killSwitch := &fakeLiveOpsKillSwitchRepository{state: domainrisk.KillSwitchState{
		Active:    true,
		Reason:    "operator stop",
		Source:    "operator",
		UpdatedAt: now.Add(-time.Minute),
	}}
	var output bytes.Buffer
	err = runLiveOpsReport(context.Background(), []string{
		"-symbol", "BTCUSDT",
	}, liveOpsReportDependencies{
		loadConfig: func(string) (*config.Config, error) {
			return validLiveOpsConfig(), nil
		},
		openDB: func(context.Context, config.DatabaseConfig) (*sql.DB, error) {
			return db, nil
		},
		newPendingReader: func(*sql.DB) domainlive.PendingLiveDecisionReader {
			return &fakeLiveOpsPendingReader{candidates: []domainlive.PendingLiveDecision{
				liveOpsPendingDecision("risk_decision_live_ops_cli_0001", "BTCUSDT", now.Add(-2*time.Minute)),
			}}
		},
		newAuditReader: func(*sql.DB) domainlive.LiveLoopAuditReader {
			return &fakeLiveOpsAuditReader{runs: []domainlive.LiveLoopRunAudit{
				liveOpsAuditRun(now.Add(-time.Minute), domainlive.LiveLoopRunStatusCompleted),
			}}
		},
		newKillSwitch: func(*sql.DB) domainrisk.KillSwitchRepository {
			return killSwitch
		},
		now: func() time.Time {
			return now
		},
		output: &output,
	})
	if err != nil {
		t.Fatalf("blocked report should not fail by default: %v\nlogs:\n%s", err, output.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
	}
	if !strings.Contains(output.String(), `"status":"BLOCKED"`) ||
		!strings.Contains(output.String(), `"name":"kill_switch"`) ||
		!strings.Contains(output.String(), `"status":"FAIL"`) {
		t.Fatalf("expected blocked status logs, got\n%s", output.String())
	}
}

func TestRunLiveOpsReportCanFailOnBlockedStatus(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	var output bytes.Buffer
	err = runLiveOpsReport(context.Background(), []string{
		"-symbol", "BTCUSDT",
		"-fail-on-blocked",
	}, liveOpsReportDependencies{
		loadConfig: func(string) (*config.Config, error) {
			return validLiveOpsConfig(), nil
		},
		openDB: func(context.Context, config.DatabaseConfig) (*sql.DB, error) {
			return db, nil
		},
		newPendingReader: func(*sql.DB) domainlive.PendingLiveDecisionReader {
			return &fakeLiveOpsPendingReader{candidates: []domainlive.PendingLiveDecision{
				liveOpsPendingDecision("risk_decision_live_ops_cli_0001", "BTCUSDT", now.Add(-2*time.Minute)),
			}}
		},
		newAuditReader: func(*sql.DB) domainlive.LiveLoopAuditReader {
			return &fakeLiveOpsAuditReader{runs: []domainlive.LiveLoopRunAudit{
				liveOpsAuditRun(now.Add(-time.Minute), domainlive.LiveLoopRunStatusRunning),
			}}
		},
		newKillSwitch: func(*sql.DB) domainrisk.KillSwitchRepository {
			return &fakeLiveOpsKillSwitchRepository{}
		},
		now: func() time.Time {
			return now
		},
		output: &output,
	})
	if err == nil || !strings.Contains(err.Error(), "recent_live_loop_audit") {
		t.Fatalf("expected fail-on-blocked error, got %v\nlogs:\n%s", err, output.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
	}
}

type fakeLiveOpsPendingReader struct {
	query      domainlive.PendingLiveDecisionQuery
	candidates []domainlive.PendingLiveDecision
	calls      int
	err        error
}

func (r *fakeLiveOpsPendingReader) ListPendingLiveDecisions(
	_ context.Context,
	query domainlive.PendingLiveDecisionQuery,
) ([]domainlive.PendingLiveDecision, error) {
	r.calls++
	r.query = query
	if r.err != nil {
		return nil, r.err
	}
	return append([]domainlive.PendingLiveDecision(nil), r.candidates...), nil
}

type fakeLiveOpsAuditReader struct {
	query domainlive.LiveLoopAuditQuery
	runs  []domainlive.LiveLoopRunAudit
	calls int
	err   error
}

func (r *fakeLiveOpsAuditReader) ListLiveLoopRunAudits(
	_ context.Context,
	query domainlive.LiveLoopAuditQuery,
) ([]domainlive.LiveLoopRunAudit, error) {
	r.calls++
	r.query = query
	if r.err != nil {
		return nil, r.err
	}
	return append([]domainlive.LiveLoopRunAudit(nil), r.runs...), nil
}

type fakeLiveOpsKillSwitchRepository struct {
	state domainrisk.KillSwitchState
	calls int
	err   error
}

func (r *fakeLiveOpsKillSwitchRepository) AppendKillSwitchEvent(context.Context, domainrisk.KillSwitchEvent) (domainrisk.KillSwitchStats, error) {
	return domainrisk.KillSwitchStats{}, fmt.Errorf("not implemented")
}

func (r *fakeLiveOpsKillSwitchRepository) CurrentKillSwitchState(context.Context) (domainrisk.KillSwitchState, error) {
	r.calls++
	if r.err != nil {
		return domainrisk.KillSwitchState{}, r.err
	}
	return r.state, nil
}

func (r *fakeLiveOpsKillSwitchRepository) ListKillSwitchEvents(context.Context, domainrisk.KillSwitchEventQuery) ([]domainrisk.KillSwitchEvent, error) {
	return nil, fmt.Errorf("not implemented")
}

type fakeLiveOpsPositionReader struct {
	snapshots map[string]domainlive.PositionSnapshot
	queries   []domainlive.PositionSnapshotQuery
	calls     int
	err       error
}

func (r *fakeLiveOpsPositionReader) GetPositionSnapshot(
	_ context.Context,
	query domainlive.PositionSnapshotQuery,
) (domainlive.PositionSnapshot, error) {
	r.calls++
	r.queries = append(r.queries, query)
	if r.err != nil {
		return domainlive.PositionSnapshot{}, r.err
	}
	snapshot, ok := r.snapshots[liveOpsPositionKey(query)]
	if !ok {
		return domainlive.PositionSnapshot{}, fmt.Errorf("missing current position fixture")
	}
	return snapshot, nil
}

type fakeLiveOpsPositionHistoryReader struct {
	snapshots map[string]domainlive.PositionSnapshot
	missing   map[string]bool
	queries   []domainlive.PositionSnapshotQuery
	calls     int
	err       error
}

func (r *fakeLiveOpsPositionHistoryReader) GetLatestPositionSnapshot(
	_ context.Context,
	query domainlive.PositionSnapshotQuery,
) (domainlive.PositionSnapshot, bool, error) {
	r.calls++
	r.queries = append(r.queries, query)
	if r.err != nil {
		return domainlive.PositionSnapshot{}, false, r.err
	}
	key := liveOpsPositionKey(query)
	if r.missing[key] {
		return domainlive.PositionSnapshot{}, false, nil
	}
	snapshot, ok := r.snapshots[key]
	return snapshot, ok, nil
}

func validLiveOpsConfig() *config.Config {
	return &config.Config{
		App: config.AppConfig{LogLevel: "info"},
		Exchange: config.ExchangeConfig{
			Primary:     "bybit",
			RestBaseURL: "https://api-testnet.bybit.com",
			Category:    "linear",
			Symbols:     []string{"BTCUSDT", "ETHUSDT"},
		},
		Live: config.LiveConfig{
			APIKeyEnv:    "BYBIT_API_KEY",
			APISecretEnv: "BYBIT_API_SECRET",
		},
	}
}

func writeLiveOpsFirstOrderReviewArtifact(t *testing.T, artifact domainlive.LiveFirstOrderReviewArtifact) string {
	t.Helper()

	if err := domainlive.ValidateLiveFirstOrderReviewArtifact(artifact); err != nil {
		t.Fatalf("validate first-order artifact: %v", err)
	}
	payload, err := json.MarshalIndent(artifact, "", "  ")
	if err != nil {
		t.Fatalf("marshal first-order artifact: %v", err)
	}
	path := filepath.Join(t.TempDir(), "live-first-order-review.json")
	if err := os.WriteFile(path, append(payload, '\n'), 0o600); err != nil {
		t.Fatalf("write first-order artifact: %v", err)
	}
	return path
}

func readLiveOpsReportArtifact(t *testing.T, path string) domainlive.LiveOpsReportArtifact {
	t.Helper()

	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read live ops artifact: %v", err)
	}
	var artifact domainlive.LiveOpsReportArtifact
	if err := json.Unmarshal(payload, &artifact); err != nil {
		t.Fatalf("decode live ops artifact: %v", err)
	}
	if err := domainlive.ValidateLiveOpsReportArtifact(artifact); err != nil {
		t.Fatalf("validate live ops artifact: %v", err)
	}
	return artifact
}

func validLiveOpsFirstOrderReviewArtifact(createdAt time.Time) domainlive.LiveFirstOrderReviewArtifact {
	return domainlive.LiveFirstOrderReviewArtifact{
		SchemaVersion: domainlive.LiveFirstOrderReviewArtifactSchemaVersion,
		CreatedAt:     createdAt,
		ConfigPath:    "configs/live.local.yaml",
		Ready:         true,
		Summary:       domainlive.LiveFirstOrderReviewArtifactSummary{Total: 4, Passed: 4},
		Checks: []domainlive.LiveFirstOrderReviewArtifactCheck{
			{Name: "live_order_submission", Status: domainlive.ReadinessCheckStatusPass, Details: "submission was persisted before exchange contact"},
			{Name: "live_order_acknowledgement", Status: domainlive.ReadinessCheckStatusPass, Details: "exchange acknowledgement was persisted"},
			{Name: "live_order_status", Status: domainlive.ReadinessCheckStatusPass, Details: "latest exchange status is FILLED"},
			{Name: "live_position_snapshot", Status: domainlive.ReadinessCheckStatusPass, Details: "latest live position snapshot is open"},
		},
		PlanFile: domainlive.LiveFirstOrderReviewArtifactPlanFile{
			Path:          "artifacts/live-first-order/live-order-plan.json",
			SHA256:        strings.Repeat("a", 64),
			SchemaVersion: domainlive.LiveOrderPlanArtifactSchemaVersion,
			Source:        domainlive.LiveOrderPlanArtifactSourceDecisionID,
			DecisionID:    "risk_decision_live_ops_review_0001",
			SubmissionID:  "live_submission_ops_review_0001",
			ClientOrderID: "inq_live_ops_review_0001",
			Symbol:        "BTCUSDT",
		},
		Evidence: domainlive.LiveFirstOrderReviewArtifactEvidence{
			RunID:              "live_loop_ops_review_0001",
			DecisionID:         "risk_decision_live_ops_review_0001",
			SubmissionID:       "live_submission_ops_review_0001",
			ClientOrderID:      "inq_live_ops_review_0001",
			ExchangeOrderID:    "bybit_order_ops_review_0001",
			LatestOrderStatus:  domainlive.ExchangeOrderStatusFilled,
			LatestPositionOpen: true,
			LatestPositionSize: "0.005",
			StatusLimit:        domainlive.DefaultLiveFirstOrderReviewStatusLimit,
			PositionLimit:      domainlive.DefaultLiveFirstOrderReviewPositionLimit,
		},
	}
}

func liveOpsPendingDecision(decisionID string, symbol string, createdAt time.Time) domainlive.PendingLiveDecision {
	return domainlive.PendingLiveDecision{
		Decision: domainrisk.DecisionAuditRecord{
			DecisionID: decisionID,
			Decision: domainrisk.Decision{
				IntentID:      "risk_intent_" + strings.TrimPrefix(decisionID, "risk_decision_"),
				Approved:      true,
				FinalQuantity: decimal.RequireFromString("0.005"),
				MaxLoss:       decimal.RequireFromString("5"),
				StopLoss:      decimal.RequireFromString("99000"),
				TakeProfit:    decimal.RequireFromString("102000"),
				Reason:        "risk_checks_passed",
				Checks: []domainrisk.Check{{
					Name:   "trading_enabled",
					Passed: true,
				}},
				CreatedAt: createdAt,
			},
			Mode:            domainrisk.ModeLive,
			HypothesisID:    "hypothesis_live_ops_0001",
			StrategyName:    "trend-momentum",
			Symbol:          symbol,
			Side:            domainrisk.SideLong,
			EntryPrice:      decimal.RequireFromString("100000"),
			Leverage:        decimal.RequireFromString("1"),
			Confidence:      80,
			IntentReason:    "signal confirmed",
			IntentCreatedAt: createdAt.Add(-time.Minute),
			RecordedAt:      createdAt.Add(time.Second),
		},
	}
}

func liveOpsAuditRun(startedAt time.Time, status domainlive.LiveLoopRunStatus) domainlive.LiveLoopRunAudit {
	run := domainlive.LiveLoopRunAudit{
		RunID:                 "live_loop_ops_cli_0001",
		StartedAt:             startedAt,
		MaxIterations:         1,
		MaxRuntime:            time.Minute,
		IterationTimeout:      5 * time.Second,
		Status:                status,
		PreflightChecked:      true,
		PreflightReady:        true,
		IterationsAttempted:   1,
		IterationsSucceeded:   1,
		StopReason:            "ITERATION_REQUESTED",
		StopDetails:           "live_order_submitted",
		CompletedWithinBounds: true,
	}
	switch status {
	case domainlive.LiveLoopRunStatusRunning:
		run.PreflightChecked = false
		run.PreflightReady = false
		run.IterationsAttempted = 0
		run.IterationsSucceeded = 0
		run.StopReason = ""
		run.StopDetails = ""
		run.CompletedWithinBounds = false
	case domainlive.LiveLoopRunStatusFailed:
		run.FinishedAt = startedAt.Add(time.Second)
		run.IterationsSucceeded = 0
		run.CompletedWithinBounds = false
		run.Error = "live loop failed"
	default:
		run.FinishedAt = startedAt.Add(time.Second)
	}
	return run
}

func liveOpsPositionSnapshot(
	t *testing.T,
	query domainlive.PositionSnapshotQuery,
	observedAt time.Time,
	open bool,
) domainlive.PositionSnapshot {
	t.Helper()

	input := domainlive.PositionSnapshotInput{
		Exchange:   query.Exchange,
		Category:   query.Category,
		Symbol:     query.Symbol,
		ObservedAt: observedAt,
	}
	if open {
		input.Side = domainlive.OrderSideLong
		input.Size = decimal.RequireFromString("0.005")
		input.AveragePrice = decimal.RequireFromString("100000")
		input.PositionValue = decimal.RequireFromString("500")
		input.MarkPrice = decimal.RequireFromString("100050")
		input.Leverage = decimal.RequireFromString("1")
		input.ExchangeStatus = domainlive.ExchangePositionStatusNormal
		input.ExchangeCreatedAt = observedAt.Add(-time.Minute)
		input.ExchangeUpdatedAt = observedAt
	}
	snapshot, err := domainlive.NewPositionSnapshot(input)
	if err != nil {
		t.Fatalf("new position snapshot: %v", err)
	}
	return snapshot
}

func liveOpsPositionKey(query domainlive.PositionSnapshotQuery) string {
	return query.Exchange + "/" + query.Category + "/" + query.Symbol
}
