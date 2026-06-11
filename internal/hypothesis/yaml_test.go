package hypothesis_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/VersoIt/Inquisitor/internal/hypothesis"
)

func TestParseYAMLAcceptsValidDraft(t *testing.T) {
	got, err := hypothesis.ParseYAML([]byte(validHypothesisYAML()))
	if err != nil {
		t.Fatalf("parse valid hypothesis: %v", err)
	}

	if got.Name != "trend_momentum_draft" {
		t.Fatalf("name mismatch: got %q", got.Name)
	}
	if got.Status != hypothesis.StatusDraft {
		t.Fatalf("status mismatch: got %q", got.Status)
	}
	if got.Signals[1].Value.String() != "25" {
		t.Fatalf("scalar signal value mismatch: got %q", got.Signals[1].Value.String())
	}
}

func TestParseYAMLRejectsInvalidDraftsTableDriven(t *testing.T) {
	tests := []struct {
		name       string
		yaml       string
		wantErrSub string
	}{
		{
			name:       "unknown top-level field",
			yaml:       validHypothesisYAML() + "\nexecution:\n  mode: live\n",
			wantErrSub: "field execution not found",
		},
		{
			name:       "missing thesis",
			yaml:       strings.Replace(validHypothesisYAML(), "thesis: Strong directional markets may persist after feature confirmation.\n", "", 1),
			wantErrSub: "thesis is required",
		},
		{
			name:       "approval status is not importable",
			yaml:       strings.Replace(validHypothesisYAML(), "status: DRAFT", "status: APPROVED_FOR_LIVE_MICRO", 1),
			wantErrSub: "status must be DRAFT",
		},
		{
			name:       "duplicate symbols",
			yaml:       strings.Replace(validHypothesisYAML(), "    - ETHUSDT", "    - BTCUSDT", 1),
			wantErrSub: "market.symbols must not contain duplicates",
		},
		{
			name:       "lowercase symbol",
			yaml:       strings.Replace(validHypothesisYAML(), "BTCUSDT", "btcusdt", 1),
			wantErrSub: "uppercase alphanumeric",
		},
		{
			name:       "unsupported interval",
			yaml:       strings.Replace(validHypothesisYAML(), "    - \"1\"", "    - \"2\"", 1),
			wantErrSub: "unsupported candle interval",
		},
		{
			name:       "allows no trade regime",
			yaml:       strings.Replace(validHypothesisYAML(), "    - TREND_UP", "    - NO_TRADE", 1),
			wantErrSub: "must not allow NO_TRADE",
		},
		{
			name:       "missing explicit no trade block",
			yaml:       strings.Replace(validHypothesisYAML(), "    - NO_TRADE\n", "", 1),
			wantErrSub: "must explicitly block NO_TRADE",
		},
		{
			name:       "same regime allowed and blocked",
			yaml:       strings.Replace(validHypothesisYAML(), "    - CHAOS", "    - TREND_UP", 1),
			wantErrSub: "must not also be allowed",
		},
		{
			name:       "unknown regime",
			yaml:       strings.Replace(validHypothesisYAML(), "    - TREND_UP", "    - MOON_ONLY", 1),
			wantErrSub: "unknown regime MOON_ONLY",
		},
		{
			name:       "unsupported operator",
			yaml:       strings.Replace(validHypothesisYAML(), "operator: \">\"", "operator: approximately", 1),
			wantErrSub: "supported comparison operator",
		},
		{
			name:       "nested signal value",
			yaml:       strings.Replace(validHypothesisYAML(), "value: trend.ma50", "value:\n      nested: true", 1),
			wantErrSub: "must be a scalar",
		},
		{
			name:       "risk above conservative import gate",
			yaml:       strings.Replace(validHypothesisYAML(), "max_risk_per_trade_pct: 0.25", "max_risk_per_trade_pct: 2.5", 1),
			wantErrSub: "risk.max_risk_per_trade_pct",
		},
		{
			name:       "stop loss not required",
			yaml:       strings.Replace(validHypothesisYAML(), "require_stop_loss: true", "require_stop_loss: false", 1),
			wantErrSub: "risk.require_stop_loss must be true",
		},
		{
			name:       "too few trades",
			yaml:       strings.Replace(validHypothesisYAML(), "min_trades: 250", "min_trades: 99", 1),
			wantErrSub: "validation.min_trades must be at least 100",
		},
		{
			name:       "out of sample gate disabled",
			yaml:       strings.Replace(validHypothesisYAML(), "require_out_of_sample: true", "require_out_of_sample: false", 1),
			wantErrSub: "validation.require_out_of_sample must be true",
		},
		{
			name:       "slippage costs disabled",
			yaml:       strings.Replace(validHypothesisYAML(), "include_slippage: true", "include_slippage: false", 1),
			wantErrSub: "costs.include_slippage must be true",
		},
		{
			name:       "multiple yaml documents",
			yaml:       validHypothesisYAML() + "\n---\nname: another\n",
			wantErrSub: "exactly one document",
		},
		{
			name:       "empty yaml",
			yaml:       " \n\t\n",
			wantErrSub: "must not be empty",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := hypothesis.ParseYAML([]byte(tt.yaml))
			if err == nil {
				t.Fatal("expected error")
			}
			if !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
		})
	}
}

func TestParseYAMLReturnsValidationErrorForDomainProblems(t *testing.T) {
	_, err := hypothesis.ParseYAML([]byte(strings.Replace(validHypothesisYAML(), "status: DRAFT", "status: LIVE_ENABLED", 1)))
	if err == nil {
		t.Fatal("expected error")
	}

	var validationErr hypothesis.ValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("expected ValidationError, got %T: %v", err, err)
	}
	if len(validationErr.Problems) == 0 {
		t.Fatal("expected validation problems")
	}
}

func validHypothesisYAML() string {
	return `name: trend_momentum_draft
version: "0.1.0"
status: DRAFT
description: Draft research hypothesis for directional momentum with regime gating.
thesis: Strong directional markets may persist after feature confirmation.
market:
  exchange: bybit
  category: linear
  symbols:
    - BTCUSDT
    - ETHUSDT
  intervals:
    - "1"
    - "5"
regime:
  allowed:
    - TREND_UP
    - TREND_DOWN
  blocked:
    - NO_TRADE
    - CHAOS
direction: BOTH
signals:
  - name: ma_alignment
    description: Fast trend average should stay on the directional side of slow average.
    feature: trend.ma20
    operator: ">"
    value: trend.ma50
  - name: adx_filter
    description: Trend strength must be high enough before research evaluation.
    feature: trend.adx
    operator: ">="
    value: 25
risk:
  max_risk_per_trade_pct: 0.25
  min_confidence: 70
  require_stop_loss: true
validation:
  min_trades: 250
  require_out_of_sample: true
  require_walk_forward: true
  require_regime_analysis: true
costs:
  include_fees: true
  include_spread: true
  include_slippage: true
tags:
  - phase4
  - draft
`
}
