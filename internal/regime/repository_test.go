package regime_test

import (
	"strings"
	"testing"
	"time"

	"github.com/VersoIt/Inquisitor/internal/regime"
)

func TestValidateStateTableDriven(t *testing.T) {
	now := time.Date(2026, 6, 11, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name       string
		mutate     func(*regime.State)
		wantErrSub string
	}{
		{
			name: "accepts valid tradeable state",
		},
		{
			name: "accepts valid no-trade state",
			mutate: func(state *regime.State) {
				state.Regime = regime.RegimeNoTrade
				state.CandidateRegime = regime.RegimeTrendUp
				state.NoTrade = true
				state.Reasons = []string{"low_confidence"}
			},
		},
		{
			name: "rejects missing identity",
			mutate: func(state *regime.State) {
				state.Symbol = ""
			},
			wantErrSub: "symbol is required",
		},
		{
			name: "rejects non-increasing close time",
			mutate: func(state *regime.State) {
				state.CloseTime = state.OpenTime
			},
			wantErrSub: "close_time must be after open_time",
		},
		{
			name: "rejects unsupported regime",
			mutate: func(state *regime.State) {
				state.Regime = "SIDEWAYS_BUT_MOODY"
			},
			wantErrSub: "regime is unsupported",
		},
		{
			name: "rejects confidence outside percent range",
			mutate: func(state *regime.State) {
				state.Confidence = 101
			},
			wantErrSub: "confidence must be between 0 and 100",
		},
		{
			name: "rejects no-trade flag without no-trade regime",
			mutate: func(state *regime.State) {
				state.NoTrade = true
			},
			wantErrSub: "no_trade=true requires regime=NO_TRADE",
		},
		{
			name: "rejects no-trade regime without no-trade flag",
			mutate: func(state *regime.State) {
				state.Regime = regime.RegimeNoTrade
			},
			wantErrSub: "regime=NO_TRADE requires no_trade=true",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := validState(now)
			if tt.mutate != nil {
				tt.mutate(&state)
			}

			err := regime.ValidateState(state)
			if tt.wantErrSub == "" {
				if err != nil {
					t.Fatalf("expected valid state, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected validation error")
			}
			if !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
		})
	}
}

func TestValidateStateQueryTableDriven(t *testing.T) {
	now := time.Date(2026, 6, 11, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name       string
		query      regime.StateQuery
		wantErrSub string
	}{
		{
			name: "accepts valid query",
			query: regime.StateQuery{
				Exchange: "bybit",
				Category: "linear",
				Symbol:   "BTCUSDT",
				Interval: "1",
				Regime:   regime.RegimeTrendUp,
				Start:    now.Add(-time.Hour),
				End:      now,
			},
		},
		{
			name: "rejects missing required fields",
			query: regime.StateQuery{
				Exchange: "bybit",
				Symbol:   "BTCUSDT",
				Interval: "1",
			},
			wantErrSub: "category",
		},
		{
			name: "rejects unsupported regime filter",
			query: regime.StateQuery{
				Exchange: "bybit",
				Category: "linear",
				Symbol:   "BTCUSDT",
				Interval: "1",
				Regime:   "SOME_MAGIC_STATE",
			},
			wantErrSub: "regime is unsupported",
		},
		{
			name: "rejects inverted time window",
			query: regime.StateQuery{
				Exchange: "bybit",
				Category: "linear",
				Symbol:   "BTCUSDT",
				Interval: "1",
				Start:    now,
				End:      now.Add(-time.Minute),
			},
			wantErrSub: "end must be after start",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := regime.ValidateStateQuery(tt.query)
			if tt.wantErrSub == "" {
				if err != nil {
					t.Fatalf("expected valid query, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("expected validation error")
			}
			if !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
		})
	}
}

func validState(now time.Time) regime.State {
	return regime.State{
		Exchange:        "bybit",
		Category:        "linear",
		Symbol:          "BTCUSDT",
		Interval:        "1",
		OpenTime:        now.Add(-time.Minute),
		CloseTime:       now,
		CalculatedAt:    now.Add(100 * time.Millisecond),
		Regime:          regime.RegimeTrendUp,
		CandidateRegime: regime.RegimeTrendUp,
		Confidence:      82,
		NoTrade:         false,
		Reasons:         []string{"candidate:trend_up", "adx_trend"},
	}
}
