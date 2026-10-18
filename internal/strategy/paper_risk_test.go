package strategy_test

import (
	"testing"
	"time"

	"github.com/VersoIt/Inquisitor/internal/risk"
	"github.com/VersoIt/Inquisitor/internal/strategy"
	"github.com/VersoIt/Inquisitor/internal/testutil"
	"github.com/shopspring/decimal"
)

func TestPaperRiskEvidenceValidation(t *testing.T) {
	for _, tt := range []struct {
		name   string
		change func(*strategy.PaperRiskEvaluation)
	}{
		{"missing source", func(e *strategy.PaperRiskEvaluation) { e.SignalID = "" }},
		{"padded approval", func(e *strategy.PaperRiskEvaluation) { e.ApprovalID += " " }},
		{"unbound decision", func(e *strategy.PaperRiskEvaluation) { e.Audit.DecisionID = "other" }},
		{"LIVE input", func(e *strategy.PaperRiskEvaluation) { e.Context.Input.Runtime.Mode = risk.ModeLive }},
		{"LIVE audit", func(e *strategy.PaperRiskEvaluation) { e.Audit.Mode = risk.ModeLive }},
		{"wrong regime symbol", func(e *strategy.PaperRiskEvaluation) { e.Context.Regime.Symbol = "ETHUSDT" }},
		{"future intent", func(e *strategy.PaperRiskEvaluation) {
			e.Context.Input.Intent.CreatedAt = e.Context.Input.EvaluatedAt.Add(time.Second)
		}},
		{"future regime", func(e *strategy.PaperRiskEvaluation) {
			e.Context.Regime.CalculatedAt = e.Context.Input.EvaluatedAt.Add(time.Second)
		}},
		{"submicrosecond time", func(e *strategy.PaperRiskEvaluation) {
			e.Context.Input.EvaluatedAt = e.Context.Input.EvaluatedAt.Add(time.Nanosecond)
		}},
		{"nonUTC time", func(e *strategy.PaperRiskEvaluation) {
			e.Context.Input.Market.DataTime = e.Context.Input.Market.DataTime.In(time.FixedZone("other", 3600))
		}},
		{"decision time mismatch", func(e *strategy.PaperRiskEvaluation) {
			e.Audit.Decision.CreatedAt = e.Audit.Decision.CreatedAt.Add(-time.Microsecond)
		}},
		{"audit snapshot mismatch", func(e *strategy.PaperRiskEvaluation) { e.Audit.EntryPrice = decimal.NewFromInt(101) }},
		{"changed target", func(e *strategy.PaperRiskEvaluation) { e.Audit.Decision.TakeProfit = decimal.NewFromInt(110) }},
		{"missing policy", func(e *strategy.PaperRiskEvaluation) { e.Context.Policy = risk.Policy{} }},
		{"LIVE policy", func(e *strategy.PaperRiskEvaluation) { e.Context.Policy.AllowedMode = risk.ModeLive }},
		{"changed policy", func(e *strategy.PaperRiskEvaluation) {
			e.Context.Policy.RiskPerTradePct = decimal.RequireFromString("0.1")
		}},
		{"unreproducible quantity", func(e *strategy.PaperRiskEvaluation) {
			e.Audit.Decision.FinalQuantity = decimal.NewFromInt(1)
			e.Audit.Decision.MaxLoss = decimal.NewFromInt(4)
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			e := testutil.PaperRisk(t).Evaluation(t)
			tt.change(&e)
			if err := strategy.ValidatePaperRiskEvaluation(e); err == nil {
				t.Fatal("invalid evidence accepted")
			}
		})
	}
}

func TestPaperRiskEquivalenceIgnoresOnlyEvaluationTime(t *testing.T) {
	f := testutil.PaperRisk(t)
	first := f.Evaluation(t)
	f.Input.EvaluatedAt = f.Input.EvaluatedAt.Add(time.Microsecond)
	later := f.Evaluation(t)
	if !strategy.SamePaperRiskRequest(first, later) || !strategy.EquivalentPaperRiskEvaluation(first, later) {
		t.Fatal("equivalent retry conflicts")
	}
	later.Context.Input.Market.SpreadBPS = decimal.NewFromInt(3)
	if strategy.SamePaperRiskRequest(first, later) || strategy.EquivalentPaperRiskEvaluation(first, later) {
		t.Fatal("changed risk input considered equal")
	}
	later = first
	later.ApprovalID = "different"
	if strategy.SamePaperRiskRequest(first, later) {
		t.Fatal("different authorization considered equal")
	}
	later = first
	later.Audit.Decision.FinalQuantity = decimal.NewFromInt(1)
	if !strategy.SamePaperRiskRequest(first, later) || strategy.EquivalentPaperRiskEvaluation(first, later) {
		t.Fatal("changed decision considered equal")
	}
	later = first
	later.Context.Policy.MaxOpenPositions++
	if strategy.SamePaperRiskRequest(first, later) {
		t.Fatal("changed policy considered equal")
	}
	a, b := strategy.PaperRiskIDs("signal")
	c, d := strategy.PaperRiskIDs("other")
	if a == b || a == c || b == d {
		t.Fatal("identity domains collide")
	}
}
