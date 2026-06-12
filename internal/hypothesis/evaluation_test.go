package hypothesis_test

import (
	"strings"
	"testing"

	"github.com/shopspring/decimal"

	"github.com/VersoIt/Inquisitor/internal/hypothesis"
)

func TestEvaluateSignalsTableDriven(t *testing.T) {
	validSpec := func() hypothesis.Hypothesis {
		return hypothesis.Hypothesis{
			Name:        "trend_momentum_draft",
			Version:     "0.1.0",
			Status:      hypothesis.StatusDraft,
			Description: "Test hypothesis",
			Thesis:      "Trend continuation may persist.",
			Market: hypothesis.MarketScope{
				Exchange:  "bybit",
				Category:  "linear",
				Symbols:   []string{"BTCUSDT"},
				Intervals: []string{"1"},
			},
			Regime: hypothesis.RegimeScope{
				Allowed: []string{"TREND_UP"},
				Blocked: []string{"NO_TRADE"},
			},
			Direction: hypothesis.DirectionLong,
			Signals: []hypothesis.SignalRule{
				{
					Name:        "adx_filter",
					Description: "ADX must be high enough.",
					Feature:     "trend.adx",
					Operator:    ">=",
					Value:       hypothesis.Scalar("25"),
				},
			},
			Risk: hypothesis.RiskRules{
				MaxRiskPerTradePct: 0.25,
				MinConfidence:      70,
				RequireStopLoss:    true,
			},
			Validation: hypothesis.ValidationRules{
				MinTrades:             250,
				RequireOutOfSample:    true,
				RequireWalkForward:    true,
				RequireRegimeAnalysis: true,
			},
			Costs: hypothesis.CostRules{
				IncludeFees:     true,
				IncludeSpread:   true,
				IncludeSlippage: true,
			},
		}
	}

	tests := []struct {
		name         string
		mutate       func(*hypothesis.Hypothesis)
		current      hypothesis.FeatureSnapshot
		previous     hypothesis.FeatureSnapshot
		wantPassed   bool
		wantPassedN  int
		wantFailedN  int
		wantSkippedN int
		wantReason   string
		wantErrSub   string
	}{
		{
			name:        "constant threshold passes",
			current:     snapshot(map[string]string{"trend.adx": "32"}),
			wantPassed:  true,
			wantPassedN: 1,
		},
		{
			name: "feature to feature comparison passes",
			mutate: func(spec *hypothesis.Hypothesis) {
				spec.Signals[0] = hypothesis.SignalRule{
					Name:        "ma_alignment",
					Description: "Fast MA above slow MA.",
					Feature:     "trend.ma20",
					Operator:    ">",
					Value:       hypothesis.Scalar("trend.ma50"),
				}
			},
			current:     snapshot(map[string]string{"trend.ma20": "101", "trend.ma50": "100"}),
			wantPassed:  true,
			wantPassedN: 1,
		},
		{
			name:         "rule failure is explicit",
			current:      snapshot(map[string]string{"trend.adx": "18"}),
			wantFailedN:  1,
			wantReason:   "signal_rule_failed:adx_filter",
			wantPassed:   false,
			wantSkippedN: 0,
		},
		{
			name:         "missing feature skips evaluation",
			current:      snapshot(map[string]string{"trend.ma20": "100"}),
			wantSkippedN: 1,
			wantReason:   "feature_missing:trend.adx",
		},
		{
			name:         "incomplete feature skips evaluation",
			current:      snapshotWithIncomplete("trend.adx", "32", "adx_window"),
			wantSkippedN: 1,
			wantReason:   "feature_incomplete:trend.adx:adx_window",
		},
		{
			name: "crosses above passes with previous snapshot",
			mutate: func(spec *hypothesis.Hypothesis) {
				spec.Signals[0] = hypothesis.SignalRule{
					Name:        "ma_cross",
					Description: "Fast MA crosses above slow MA.",
					Feature:     "trend.ma20",
					Operator:    "crosses_above",
					Value:       hypothesis.Scalar("trend.ma50"),
				}
			},
			current:     snapshot(map[string]string{"trend.ma20": "101", "trend.ma50": "100"}),
			previous:    snapshot(map[string]string{"trend.ma20": "99", "trend.ma50": "100"}),
			wantPassed:  true,
			wantPassedN: 1,
		},
		{
			name: "cross skips when previous snapshot is missing",
			mutate: func(spec *hypothesis.Hypothesis) {
				spec.Signals[0].Operator = "crosses_below"
			},
			current:      snapshot(map[string]string{"trend.adx": "20"}),
			wantSkippedN: 1,
			wantReason:   "previous_feature_missing:trend.adx",
		},
		{
			name: "invalid scalar value fails fast",
			mutate: func(spec *hypothesis.Hypothesis) {
				spec.Signals[0].Value = hypothesis.Scalar("not-a-number")
			},
			current:    snapshot(map[string]string{"trend.adx": "32"}),
			wantErrSub: "value",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := validSpec()
			if tt.mutate != nil {
				tt.mutate(&spec)
			}

			got, err := hypothesis.EvaluateSignals(spec, tt.current, tt.previous)
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
				t.Fatalf("evaluate signals: %v", err)
			}
			if got.Passed != tt.wantPassed {
				t.Fatalf("passed mismatch: got %v want %v", got.Passed, tt.wantPassed)
			}
			if got.PassedRules != tt.wantPassedN || got.FailedRules != tt.wantFailedN || got.SkippedRules != tt.wantSkippedN {
				t.Fatalf("counts mismatch: %#v", got)
			}
			if tt.wantReason != "" && !contains(got.Reasons, tt.wantReason) {
				t.Fatalf("missing reason %q in %#v", tt.wantReason, got.Reasons)
			}
		})
	}
}

func snapshot(values map[string]string) hypothesis.FeatureSnapshot {
	features := make(map[string]hypothesis.FeatureValue, len(values))
	for path, value := range values {
		features[path] = hypothesis.FeatureValue{
			Value:    decimal.RequireFromString(value),
			Complete: true,
		}
	}
	return hypothesis.NewFeatureSnapshot(features)
}

func snapshotWithIncomplete(path, value, reason string) hypothesis.FeatureSnapshot {
	return hypothesis.NewFeatureSnapshot(map[string]hypothesis.FeatureValue{
		path: {
			Value:          decimal.RequireFromString(value),
			Complete:       false,
			MissingReasons: []string{reason},
		},
	})
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
