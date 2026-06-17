package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	domainresearch "github.com/VersoIt/Inquisitor/internal/research"
)

func TestWriteResearchReportTableDriven(t *testing.T) {
	run, result := testReportRunResult(t)
	generatedAt := time.Date(2026, 6, 12, 14, 0, 0, 0, time.UTC)

	tests := []struct {
		name        string
		path        func(string) string
		format      string
		wantWritten bool
		wantFormat  domainresearch.ReportFormat
		wantContent string
		wantErrSub  string
	}{
		{
			name:        "empty path is no op",
			path:        func(string) string { return "" },
			format:      "html",
			wantWritten: false,
		},
		{
			name:        "writes json report",
			path:        func(dir string) string { return filepath.Join(dir, "reports", "research.json") },
			format:      "json",
			wantWritten: true,
			wantFormat:  domainresearch.ReportFormatJSON,
			wantContent: `"schema_version": "research-report/v1"`,
		},
		{
			name:        "writes markdown report through alias",
			path:        func(dir string) string { return filepath.Join(dir, "reports", "research.md") },
			format:      "md",
			wantWritten: true,
			wantFormat:  domainresearch.ReportFormatMarkdown,
			wantContent: "# Research Report\n",
		},
		{
			name:       "rejects unsupported format",
			path:       func(dir string) string { return filepath.Join(dir, "reports", "research.html") },
			format:     "html",
			wantErrSub: "unsupported",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := tt.path(dir)

			got, err := writeResearchReport(path, tt.format, run, result, generatedAt)
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
				t.Fatalf("write research report: %v", err)
			}
			if got.Written != tt.wantWritten {
				t.Fatalf("written mismatch: got %v want %v", got.Written, tt.wantWritten)
			}
			if !tt.wantWritten {
				return
			}
			if got.Path != filepath.Clean(path) || got.Format != tt.wantFormat || got.Bytes <= 0 {
				t.Fatalf("write metadata mismatch: %#v", got)
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read report: %v", err)
			}
			if !strings.Contains(string(raw), tt.wantContent) {
				t.Fatalf("report missing %q:\n%s", tt.wantContent, raw)
			}
		})
	}
}

func testReportRunResult(t *testing.T) (domainresearch.Run, domainresearch.Result) {
	t.Helper()

	plannedAt := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	run, err := domainresearch.NewPlannedRun(domainresearch.PlanInput{
		RunID:                   "research_cmd_report_0001",
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
	result, err := domainresearch.NewResult(domainresearch.ResultInput{
		RunID:       run.RunID,
		FinalStatus: domainresearch.StatusCompleted,
		Outcome:     domainresearch.OutcomeInconclusive,
		Summary:     "Research report command test.",
		Metrics: domainresearch.Metrics{
			RegimeStates:           1,
			ExpectedRegimeStates:   1,
			RegimeCoveragePct:      100,
			RegimeAnalysisIncluded: true,
		},
		Reasons:    []string{"walk_forward_not_run"},
		RecordedAt: plannedAt.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("new result: %v", err)
	}
	finalRun, err := domainresearch.FinalizeRun(run, result)
	if err != nil {
		t.Fatalf("finalize run: %v", err)
	}
	return finalRun, result
}
