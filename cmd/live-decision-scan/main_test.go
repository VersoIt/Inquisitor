package main

import (
	"bytes"
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/shopspring/decimal"

	"github.com/VersoIt/Inquisitor/internal/config"
	domainlive "github.com/VersoIt/Inquisitor/internal/live"
	domainrisk "github.com/VersoIt/Inquisitor/internal/risk"
)

func TestLiveDecisionScanQueryFromFlagsTableDriven(t *testing.T) {
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
		{name: "untrimmed symbol", symbol: " BTCUSDT ", limit: 1, wantErrSub: "trimmed"},
		{name: "negative limit", limit: -1, wantErrSub: "limit"},
		{name: "limit above max", limit: 101, wantErrSub: "limit"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := liveDecisionScanQueryFromFlags(tt.symbol, tt.limit)
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

func TestRunLiveDecisionScanRejectsUnsafeFlagsBeforeSideEffects(t *testing.T) {
	var loaded bool
	var opened bool

	err := runLiveDecisionScan(context.Background(), []string{
		"-symbol", " BTCUSDT ",
	}, liveDecisionScanDependencies{
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
	if err == nil || !strings.Contains(err.Error(), "symbol") {
		t.Fatalf("expected symbol error, got %v", err)
	}
	if loaded || opened {
		t.Fatalf("unsafe flags must stop before side effects: loaded=%t opened=%t", loaded, opened)
	}
}

func TestRunLiveDecisionScanLogsPendingCandidates(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	reader := &fakeLiveDecisionScanReader{
		candidates: []domainlive.PendingLiveDecision{
			liveDecisionScanCandidate("risk_decision_live_scan_0001", "BTCUSDT", time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)),
		},
	}
	var output bytes.Buffer
	err = runLiveDecisionScan(context.Background(), []string{
		"-symbol", "btcusdt",
		"-limit", "5",
	}, liveDecisionScanDependencies{
		loadConfig: func(string) (*config.Config, error) {
			return &config.Config{App: config.AppConfig{LogLevel: "info"}}, nil
		},
		openDB: func(context.Context, config.DatabaseConfig) (*sql.DB, error) {
			return db, nil
		},
		newPendingReader: func(*sql.DB) domainlive.PendingLiveDecisionReader {
			return reader
		},
		output: &output,
	})
	if err != nil {
		t.Fatalf("run live decision scan: %v\nlogs:\n%s", err, output.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
	}
	if reader.calls != 1 || reader.query.Symbol != "BTCUSDT" || reader.query.Limit != 5 {
		t.Fatalf("reader query mismatch: calls=%d query=%#v", reader.calls, reader.query)
	}
	logs := output.String()
	for _, want := range []string{
		`"msg":"pending live decision scan"`,
		`"candidates":1`,
		`"next_decision_id":"risk_decision_live_scan_0001"`,
		`"symbol_filter":"BTCUSDT"`,
		`"msg":"pending live decision candidate"`,
		`"decision_id":"risk_decision_live_scan_0001"`,
		`"quantity":"0.005"`,
		`"max_loss":"5"`,
	} {
		if !strings.Contains(logs, want) {
			t.Fatalf("expected logs to contain %s, got\n%s", want, logs)
		}
	}
}

type fakeLiveDecisionScanReader struct {
	query      domainlive.PendingLiveDecisionQuery
	candidates []domainlive.PendingLiveDecision
	calls      int
}

func (r *fakeLiveDecisionScanReader) ListPendingLiveDecisions(
	_ context.Context,
	query domainlive.PendingLiveDecisionQuery,
) ([]domainlive.PendingLiveDecision, error) {
	r.calls++
	r.query = query
	return append([]domainlive.PendingLiveDecision(nil), r.candidates...), nil
}

func liveDecisionScanCandidate(decisionID string, symbol string, createdAt time.Time) domainlive.PendingLiveDecision {
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
			HypothesisID:    "hypothesis_live_scan_0001",
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
