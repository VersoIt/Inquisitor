package research_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/VersoIt/Inquisitor/internal/research"
)

func TestBuildReportCopiesValidatedInput(t *testing.T) {
	input := testReportInput(t)

	got, err := research.BuildReport(input)
	if err != nil {
		t.Fatalf("build report: %v", err)
	}
	input.Run.Symbols[0] = "ETHUSDT"
	input.Result.Reasons[0] = "mutated"

	if got.SchemaVersion != research.ReportSchemaVersion {
		t.Fatalf("schema mismatch: %s", got.SchemaVersion)
	}
	if got.Run.Symbols[0] != "BTCUSDT" {
		t.Fatalf("report did not copy run symbols: %#v", got.Run.Symbols)
	}
	if got.Result.Reasons[0] != "regime_coverage:BTCUSDT:1:1" {
		t.Fatalf("report did not copy result reasons: %#v", got.Result.Reasons)
	}
	if got.Safety.LiveTradingAllowed {
		t.Fatal("research report must not allow live trading")
	}
	if !containsString(got.Safety.BlockingValidationGaps, "walk_forward_validation_missing") {
		t.Fatalf("missing conservative safety gap: %#v", got.Safety.BlockingValidationGaps)
	}
}

func TestBuildReportRejectsInvalidInputTableDriven(t *testing.T) {
	tests := []struct {
		name       string
		mutate     func(*research.ReportInput)
		wantErrSub string
	}{
		{
			name: "missing generated at",
			mutate: func(input *research.ReportInput) {
				input.GeneratedAt = time.Time{}
			},
			wantErrSub: "generated_at",
		},
		{
			name: "mismatched run id",
			mutate: func(input *research.ReportInput) {
				input.Result.RunID = "research_other_0001"
			},
			wantErrSub: "must match",
		},
		{
			name: "invalid run",
			mutate: func(input *research.ReportInput) {
				input.Run.Symbols = nil
			},
			wantErrSub: "symbols",
		},
		{
			name: "invalid result",
			mutate: func(input *research.ReportInput) {
				input.Result.Summary = ""
			},
			wantErrSub: "summary",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := testReportInput(t)
			tt.mutate(&input)

			_, err := research.BuildReport(input)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
		})
	}
}

func TestParseReportFormatTableDriven(t *testing.T) {
	tests := []struct {
		name       string
		value      string
		want       research.ReportFormat
		wantErrSub string
	}{
		{name: "empty defaults to json", value: "", want: research.ReportFormatJSON},
		{name: "json", value: " JSON ", want: research.ReportFormatJSON},
		{name: "markdown", value: "markdown", want: research.ReportFormatMarkdown},
		{name: "md alias", value: "md", want: research.ReportFormatMarkdown},
		{name: "unsupported", value: "html", wantErrSub: "unsupported"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := research.ParseReportFormat(tt.value)
			if tt.wantErrSub != "" {
				if err == nil {
					t.Fatal("expected error")
				}
				if !strings.Contains(err.Error(), tt.wantErrSub) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("parse report format: %v", err)
			}
			if got != tt.want {
				t.Fatalf("format mismatch: got %s want %s", got, tt.want)
			}
		})
	}
}

func TestRenderReportJSONUsesStableSnakeCase(t *testing.T) {
	report := testReport(t)

	got, err := research.RenderReportJSON(report)
	if err != nil {
		t.Fatalf("render json: %v", err)
	}
	raw := string(got)
	for _, forbidden := range []string{"SchemaVersion", "GeneratedAt", "FinalStatus"} {
		if strings.Contains(raw, forbidden) {
			t.Fatalf("json leaked Go field name %q:\n%s", forbidden, raw)
		}
	}
	for _, required := range []string{`"schema_version": "research-report/v1"`, `"generated_at": "2026-06-12T14:00:00Z"`, `"out_of_sample_trades": 2`, `"live_trading_allowed": false`} {
		if !strings.Contains(raw, required) {
			t.Fatalf("json missing %q:\n%s", required, raw)
		}
	}
	if !strings.HasSuffix(raw, "\n") {
		t.Fatalf("json report must end with newline: %q", raw)
	}

	var decoded research.Report
	if err := json.Unmarshal(got, &decoded); err != nil {
		t.Fatalf("json should decode back into report: %v", err)
	}
	if decoded.Run.RunID != report.Run.RunID || decoded.Result.Metrics.OutOfSampleTrades != 2 {
		t.Fatalf("decoded report mismatch: %#v", decoded)
	}
}

func TestRenderReportMarkdownMatchesExpectedArtifact(t *testing.T) {
	report := testReport(t)

	got, err := research.RenderReportMarkdown(report)
	if err != nil {
		t.Fatalf("render markdown: %v", err)
	}

	want := `# Research Report

Generated at: 2026-06-12T14:00:00Z

## Run

| Field | Value |
| --- | --- |
| Run ID | research_report_0001 |
| Hypothesis | trend_momentum_draft 0.1.0 |
| Status | COMPLETED |
| Exchange | bybit |
| Category | linear |
| Symbols | BTCUSDT |
| Intervals | 1 |
| Window | 2026-06-11T12:00:00Z to 2026-06-12T11:00:00Z |
| Planned at | 2026-06-12T12:00:00Z |
| Content SHA256 | aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa |

## Decision

| Field | Value |
| --- | --- |
| Final status | COMPLETED |
| Outcome | INCONCLUSIVE |
| Recorded at | 2026-06-12T13:00:00Z |
| Summary | Fixed-horizon backtest completed; walk-forward validation is not implemented yet. |

## Metrics

| Field | Value |
| --- | --- |
| Trades | 3 |
| Regime coverage | 100% |
| Regime states | 10/10 |
| Net PnL | 2 |
| Total fees | 0.5 |
| Expectancy | 0.6667 |
| Profit factor | 2 |
| Profit factor defined | true |
| Win rate | 66.67% |
| Max drawdown | 1.25% |
| Initial equity | 1000 |
| Final equity | 1002 |
| Fees included | true |
| Spread included | true |
| Slippage included | true |
| Out of sample | true |
| Walk forward | false |
| In-sample trades | 1 |
| In-sample net PnL | 1 |
| In-sample profit factor | - |
| In-sample max drawdown | 0% |
| Out-of-sample trades | 2 |
| Out-of-sample net PnL | 1 |
| Out-of-sample profit factor | 1.5 |
| Out-of-sample max drawdown | 1% |

## Safety

| Field | Value |
| --- | --- |
| Live trading allowed | false |
| Candidate outcome reached | false |
| Conservative costs included | true |
| Blocking validation gaps | live_execution_not_implemented, candidate_outcome_not_reached, walk_forward_validation_missing |

## Reasons

- regime_coverage:BTCUSDT:1:1
- walk_forward_not_run
`
	if string(got) != want {
		t.Fatalf("markdown mismatch:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestRenderReportMarkdownEscapesTableCells(t *testing.T) {
	report := testReport(t)
	report.Result.Summary = "PnL | drawdown\nreviewed"

	got, err := research.RenderReportMarkdown(report)
	if err != nil {
		t.Fatalf("render markdown: %v", err)
	}
	if !strings.Contains(string(got), "| Summary | PnL \\| drawdown reviewed |") {
		t.Fatalf("markdown did not escape table cell:\n%s", got)
	}
}

func testReport(t *testing.T) research.Report {
	t.Helper()

	report, err := research.BuildReport(testReportInput(t))
	if err != nil {
		t.Fatalf("build report: %v", err)
	}
	return report
}

func testReportInput(t *testing.T) research.ReportInput {
	t.Helper()

	plannedAt := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	run, err := research.NewPlannedRun(research.PlanInput{
		RunID:                   "research_report_0001",
		HypothesisName:          "trend_momentum_draft",
		HypothesisVersion:       "0.1.0",
		HypothesisContentSHA256: strings.Repeat("a", 64),
		Exchange:                "bybit",
		Category:                "linear",
		WindowStart:             plannedAt.Add(-24 * time.Hour),
		WindowEnd:               plannedAt.Add(-time.Hour),
		PlannedAt:               plannedAt,
		Symbols:                 []string{"BTCUSDT"},
		Intervals:               []string{"1"},
	})
	if err != nil {
		t.Fatalf("new run: %v", err)
	}
	result, err := research.NewResult(research.ResultInput{
		RunID:       run.RunID,
		FinalStatus: research.StatusCompleted,
		Outcome:     research.OutcomeInconclusive,
		Summary:     "Fixed-horizon backtest completed; walk-forward validation is not implemented yet.",
		Metrics: research.Metrics{
			Trades:                         3,
			RegimeStates:                   10,
			ExpectedRegimeStates:           10,
			RegimeCoveragePct:              100,
			InSampleTrades:                 1,
			OutOfSampleTrades:              2,
			GrossProfit:                    "4",
			GrossLoss:                      "2",
			TotalFees:                      "0.5",
			NetPnL:                         "2",
			Expectancy:                     "0.6667",
			ProfitFactor:                   "2",
			ProfitFactorDefined:            true,
			WinRatePct:                     66.67,
			MaxDrawdownPct:                 1.25,
			InitialEquity:                  "1000",
			FinalEquity:                    "1002",
			InSampleNetPnL:                 "1",
			OutOfSampleNetPnL:              "1",
			OutOfSampleProfitFactor:        "1.5",
			OutOfSampleProfitFactorDefined: true,
			OutOfSampleMaxDrawdownPct:      1,
			FeesIncluded:                   true,
			SpreadIncluded:                 true,
			SlippageIncluded:               true,
			OutOfSample:                    true,
			RegimeAnalysisIncluded:         true,
		},
		Reasons:    []string{"regime_coverage:BTCUSDT:1:1", "walk_forward_not_run"},
		RecordedAt: plannedAt.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("new result: %v", err)
	}
	finalRun, err := research.FinalizeRun(run, result)
	if err != nil {
		t.Fatalf("finalize run: %v", err)
	}
	return research.ReportInput{
		Run:         finalRun,
		Result:      result,
		GeneratedAt: plannedAt.Add(2 * time.Hour),
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
