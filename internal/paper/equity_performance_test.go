package paper_test

import (
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/VersoIt/Inquisitor/internal/paper"
)

func TestSummarizeEquityEventsComputesMetrics(t *testing.T) {
	first := validEquityEvent()
	second := nextEquityEvent(t, first)

	got, err := paper.SummarizeEquityEvents(first.ValidationID, decimal.RequireFromString("1000"), []paper.EquityEvent{second, first})
	if err != nil {
		t.Fatalf("summarize equity events: %v", err)
	}

	if got.Trades != 2 || got.Wins != 2 || got.Losses != 0 || got.Breakeven != 0 {
		t.Fatalf("count mismatch: %#v", got)
	}
	if !got.GrossProfit.Equal(decimal.RequireFromString("779.4")) ||
		!got.TotalFees.Equal(decimal.RequireFromString("120.6")) ||
		!got.NetPnL.Equal(decimal.RequireFromString("779.4")) ||
		!got.Expectancy.Equal(decimal.RequireFromString("389.7")) ||
		!got.FinalEquity.Equal(decimal.RequireFromString("1779.4")) {
		t.Fatalf("summary math mismatch: %#v", got)
	}
	if !got.WinRate.Equal(decimal.NewFromInt(1)) || got.ProfitFactorDefined || !got.ProfitFactor.IsZero() {
		t.Fatalf("rate/profit factor mismatch: %#v", got)
	}
}

func TestSummarizeEquityEventsTracksLossesAndDrawdown(t *testing.T) {
	first := validEquityEvent()
	second := nextEquityEvent(t, first)
	second.NetPnL = decimal.RequireFromString("-100")
	second.Fees = decimal.RequireFromString("10")
	second.EquityBefore = first.EquityAfter
	second.EquityAfter = second.EquityBefore.Add(second.NetPnL)

	got, err := paper.SummarizeEquityEvents(first.ValidationID, decimal.RequireFromString("1000"), []paper.EquityEvent{first, second})
	if err != nil {
		t.Fatalf("summarize equity events: %v", err)
	}

	if got.Trades != 2 || got.Wins != 1 || got.Losses != 1 || got.Breakeven != 0 {
		t.Fatalf("count mismatch: %#v", got)
	}
	if !got.GrossProfit.Equal(decimal.RequireFromString("389.7")) || !got.GrossLoss.Equal(decimal.RequireFromString("100")) ||
		!got.NetPnL.Equal(decimal.RequireFromString("289.7")) || !got.ProfitFactor.Equal(decimal.RequireFromString("3.897")) ||
		!got.ProfitFactorDefined || !got.MaxDrawdown.GreaterThan(decimal.Zero) {
		t.Fatalf("loss summary mismatch: %#v", got)
	}
}

func TestBuildDailyEquityPerformanceGroupsByUTCEventDay(t *testing.T) {
	first := validEquityEvent()
	second := nextEquityEvent(t, first)
	second.OccurredAt = first.OccurredAt.Add(25 * time.Hour)
	second.RecordedAt = second.OccurredAt.Add(time.Minute)
	calculatedAt := second.RecordedAt.Add(time.Minute)

	got, err := paper.BuildDailyEquityPerformance(first.ValidationID, decimal.RequireFromString("1000"), []paper.EquityEvent{second, first}, calculatedAt)
	if err != nil {
		t.Fatalf("build daily equity performance: %v", err)
	}

	if len(got) != 2 {
		t.Fatalf("daily count mismatch: %#v", got)
	}
	if !got[0].Day.Equal(time.Date(first.OccurredAt.UTC().Year(), first.OccurredAt.UTC().Month(), first.OccurredAt.UTC().Day(), 0, 0, 0, 0, time.UTC)) ||
		!got[1].Summary.InitialEquity.Equal(first.EquityAfter) ||
		!got[1].Summary.FinalEquity.Equal(second.EquityAfter) {
		t.Fatalf("daily grouping/equity mismatch: %#v", got)
	}
}

func TestBuildDailyEquityPerformanceHandlesEmptyLedger(t *testing.T) {
	got, err := paper.BuildDailyEquityPerformance("paper_validation_equity_0001", decimal.NewFromInt(1000), nil, time.Now())
	if err != nil {
		t.Fatalf("build empty daily equity performance: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("expected empty daily performance, got %#v", got)
	}
}

func TestEquityPerformanceRejectsUnsafeInputsTableDriven(t *testing.T) {
	first := validEquityEvent()
	second := nextEquityEvent(t, first)
	calculatedAt := second.RecordedAt.Add(time.Minute)

	tests := []struct {
		name       string
		run        func() error
		wantErrSub string
	}{
		{
			name: "summary missing validation id",
			run: func() error {
				_, err := paper.SummarizeEquityEvents(" ", decimal.NewFromInt(1000), []paper.EquityEvent{first})
				return err
			},
			wantErrSub: "validation_id",
		},
		{
			name: "summary broken continuity",
			run: func() error {
				broken := second
				broken.EquityBefore = broken.EquityBefore.Add(decimal.NewFromInt(1))
				broken.EquityAfter = broken.EquityBefore.Add(broken.NetPnL)
				_, err := paper.SummarizeEquityEvents(first.ValidationID, decimal.NewFromInt(1000), []paper.EquityEvent{first, broken})
				return err
			},
			wantErrSub: "continuity",
		},
		{
			name: "daily missing calculated at",
			run: func() error {
				_, err := paper.BuildDailyEquityPerformance(first.ValidationID, decimal.NewFromInt(1000), []paper.EquityEvent{first}, time.Time{})
				return err
			},
			wantErrSub: "calculated_at",
		},
		{
			name: "daily event after calculated at",
			run: func() error {
				_, err := paper.BuildDailyEquityPerformance(first.ValidationID, decimal.NewFromInt(1000), []paper.EquityEvent{first, second}, first.RecordedAt)
				return err
			},
			wantErrSub: "calculated_at",
		},
		{
			name: "daily validation mismatch",
			run: func() error {
				mismatched := first
				mismatched.ValidationID = "paper_validation_other"
				_, err := paper.BuildDailyEquityPerformance(first.ValidationID, decimal.NewFromInt(1000), []paper.EquityEvent{mismatched}, calculatedAt)
				return err
			},
			wantErrSub: "validation_id",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.run()
			if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
		})
	}
}
