package research_test

import (
	"strings"
	"testing"
	"time"

	"github.com/VersoIt/Inquisitor/internal/research"
)

func TestNewPlannedRunBuildsValidatedRun(t *testing.T) {
	plannedAt := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)

	got, err := research.NewPlannedRun(research.PlanInput{
		RunID:                   "research_0123456789abcdef",
		HypothesisName:          "trend_momentum_draft",
		HypothesisVersion:       "0.1.0",
		HypothesisContentSHA256: strings.Repeat("a", 64),
		WindowStart:             plannedAt.Add(-24 * time.Hour),
		WindowEnd:               plannedAt.Add(-time.Hour),
		PlannedAt:               plannedAt,
		Symbols:                 []string{"btcusdt", " ETHUSDT "},
		Intervals:               []string{"1", "5"},
		Notes:                   []string{" first run "},
	})
	if err != nil {
		t.Fatalf("new planned run: %v", err)
	}

	if got.Status != research.StatusPlanned {
		t.Fatalf("status mismatch: got %s want %s", got.Status, research.StatusPlanned)
	}
	if got.Symbols[0] != "BTCUSDT" || got.Symbols[1] != "ETHUSDT" {
		t.Fatalf("symbols were not canonicalized: %#v", got.Symbols)
	}
	if got.Notes[0] != "first run" {
		t.Fatalf("notes were not canonicalized: %#v", got.Notes)
	}
}

func TestValidateRunRejectsInvalidRunsTableDriven(t *testing.T) {
	plannedAt := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	valid := research.Run{
		RunID:                   "research_0123456789abcdef",
		HypothesisName:          "trend_momentum_draft",
		HypothesisVersion:       "0.1.0",
		HypothesisContentSHA256: strings.Repeat("a", 64),
		Status:                  research.StatusPlanned,
		WindowStart:             plannedAt.Add(-24 * time.Hour),
		WindowEnd:               plannedAt.Add(-time.Hour),
		PlannedAt:               plannedAt,
		Symbols:                 []string{"BTCUSDT"},
		Intervals:               []string{"1"},
	}

	tests := []struct {
		name       string
		mutate     func(*research.Run)
		wantErrSub string
	}{
		{
			name: "missing run id",
			mutate: func(run *research.Run) {
				run.RunID = ""
			},
			wantErrSub: "run_id",
		},
		{
			name: "unsupported status",
			mutate: func(run *research.Run) {
				run.Status = "MOON"
			},
			wantErrSub: "status",
		},
		{
			name: "bad hash",
			mutate: func(run *research.Run) {
				run.HypothesisContentSHA256 = "bad"
			},
			wantErrSub: "sha256",
		},
		{
			name: "window end before start",
			mutate: func(run *research.Run) {
				run.WindowEnd = run.WindowStart
			},
			wantErrSub: "window_end must be after",
		},
		{
			name: "future window",
			mutate: func(run *research.Run) {
				run.WindowEnd = run.PlannedAt.Add(time.Minute)
			},
			wantErrSub: "planned_at",
		},
		{
			name: "lowercase symbol",
			mutate: func(run *research.Run) {
				run.Symbols = []string{"btcusdt"}
			},
			wantErrSub: "must be uppercase",
		},
		{
			name: "duplicate symbols",
			mutate: func(run *research.Run) {
				run.Symbols = []string{"BTCUSDT", "BTCUSDT"}
			},
			wantErrSub: "symbols must not contain duplicates",
		},
		{
			name: "unsupported interval",
			mutate: func(run *research.Run) {
				run.Intervals = []string{"2"}
			},
			wantErrSub: "unsupported candle interval",
		},
		{
			name: "empty note",
			mutate: func(run *research.Run) {
				run.Notes = []string{" "}
			},
			wantErrSub: "notes[0]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			run := valid
			tt.mutate(&run)

			err := research.ValidateRun(run)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
		})
	}
}

func TestValidateQueryRejectsInvalidInputsTableDriven(t *testing.T) {
	now := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name       string
		query      research.Query
		wantErrSub string
	}{
		{
			name:       "unsupported status",
			query:      research.Query{Status: "UNKNOWN"},
			wantErrSub: "status",
		},
		{
			name:       "inverted window",
			query:      research.Query{Start: now, End: now},
			wantErrSub: "end",
		},
		{
			name:       "negative limit",
			query:      research.Query{Limit: -1},
			wantErrSub: "limit",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := research.ValidateQuery(tt.query)
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
		})
	}
}
