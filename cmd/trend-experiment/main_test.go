package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/VersoIt/Inquisitor/internal/exchanges"
	"github.com/VersoIt/Inquisitor/internal/marketdata"
	"github.com/VersoIt/Inquisitor/internal/research"
	"github.com/shopspring/decimal"
)

func registeredPlan(t *testing.T) research.TrendExperimentPlan {
	t.Helper()
	var p research.TrendExperimentPlan
	if err := readJSON(filepath.Join("..", "..", "experiments", "trend-spot-btc-1h-v1.json"), &p); err != nil {
		t.Fatal(err)
	}
	if err := research.ValidateTrendExperimentPlan(p); err != nil {
		t.Fatal(err)
	}
	return p
}

func fakeExperimentCandles(p research.TrendExperimentPlan, start, end time.Time) []marketdata.Candle {
	var rows []marketdata.Candle
	for at := start; at.Before(end); at = at.Add(time.Hour) {
		rows = append(rows, marketdata.Candle{Exchange: p.Exchange, Category: p.Category, Symbol: p.Symbol, Interval: p.Interval, OpenTime: at, CloseTime: at.Add(time.Hour), Open: decimal.NewFromInt(100), High: decimal.NewFromInt(101), Low: decimal.NewFromInt(99), Close: decimal.NewFromInt(100), IsClosed: true})
	}
	return rows
}

type publicKlinesFake struct {
	p        research.TrendExperimentPlan
	queries  []exchanges.KlinesRequest
	scenario string
}

func (f *publicKlinesFake) GetKlines(_ context.Context, q exchanges.KlinesRequest) ([]marketdata.Candle, error) {
	f.queries = append(f.queries, q)
	if f.scenario == "failure" {
		return nil, context.DeadlineExceeded
	}
	rows := fakeExperimentCandles(f.p, q.Start, q.End.Add(time.Millisecond))
	if f.scenario == "missing" {
		rows = rows[1:]
	}
	if f.scenario == "duplicate" {
		rows[1] = rows[0]
	}
	if f.scenario == "wrong scope" {
		rows[0].Category = "linear"
	}
	return rows, nil
}

func TestPublicDownloadPagesAreCompleteExclusiveAndScoped(t *testing.T) {
	for _, scenario := range []string{"valid", "failure", "missing", "duplicate", "wrong scope", "cancelled"} {
		t.Run(scenario, func(t *testing.T) {
			p := registeredPlan(t)
			p.ValidationEnd = p.DataStart.Add(2048 * time.Hour)
			p.DevelopmentStart = p.DataStart.Add(24 * time.Hour)
			p.ValidationStart = p.DataStart.Add(48 * time.Hour)
			f := &publicKlinesFake{p: p, scenario: scenario}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if scenario == "cancelled" {
				cancel()
			}
			rows, err := downloadCandles(ctx, f, p, 0)
			if scenario == "valid" {
				if err != nil || len(rows) != 2048 || len(f.queries) != 3 {
					t.Fatalf("download len=%d requests=%d err=%v", len(rows), len(f.queries), err)
				}
				for i, q := range f.queries {
					if q.Category != "spot" || q.Limit != 1000 || q.Interval != "60" || q.End.Add(time.Millisecond).Sub(q.Start) > 1000*time.Hour {
						t.Fatalf("request %+v", q)
					}
					if i > 0 && !q.Start.Equal(f.queries[i-1].End.Add(time.Millisecond)) {
						t.Fatal("page overlap/gap")
					}
				}
			} else if err == nil || len(rows) != 0 {
				t.Fatalf("partial/invalid data returned len=%d err=%v", len(rows), err)
			}
			if scenario == "cancelled" && (!errors.Is(err, context.Canceled) || len(f.queries) != 0) {
				t.Fatal("cancelled download performed IO")
			}
		})
	}
}

func TestExperimentCommandRunsOfflineAndNeverOverwritesEvidence(t *testing.T) {
	p := registeredPlan(t)
	p.DataStart = time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	p.DevelopmentStart = p.DataStart.Add(240 * time.Hour)
	p.ValidationStart = p.DevelopmentStart.Add(24 * time.Hour)
	p.ValidationEnd = p.ValidationStart.Add(80 * time.Hour)
	dir := t.TempDir()
	planPath, candlePath, reportPath := filepath.Join(dir, "plan.json"), filepath.Join(dir, "candles.json"), filepath.Join(dir, "report.json")
	if err := writeNewJSON(planPath, p); err != nil {
		t.Fatal(err)
	}
	rows := fakeExperimentCandles(p, p.DataStart, p.ValidationEnd)
	if err := writeNewJSON(candlePath, rows); err != nil {
		t.Fatal(err)
	}
	args := []string{"-plan", planPath, "-candles", candlePath, "-report", reportPath, "-source-root", filepath.Join("..", "..")}
	var output bytes.Buffer
	if err := run(context.Background(), args, &output); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "INCONCLUSIVE") || !strings.Contains(output.String(), "cash net=0") {
		t.Fatal(output.String())
	}
	before, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := run(context.Background(), args, &output); err == nil {
		t.Fatal("existing report overwritten")
	}
	after, err := os.ReadFile(reportPath)
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("immutable report changed")
	}
	if err := writeNewJSON(candlePath, rows); err == nil {
		t.Fatal("existing candle cache overwritten")
	}
}

func TestEvidenceJSONRejectsUnknownFieldsAndTrailingPayloads(t *testing.T) {
	for _, raw := range []string{`{"Unknown":1}`, `{} {}`, `{`} {
		t.Run(raw, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "bad.json")
			if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
				t.Fatal(err)
			}
			var p research.TrendExperimentPlan
			if err := readJSON(path, &p); err == nil {
				t.Fatal("invalid JSON accepted")
			}
		})
	}
}
