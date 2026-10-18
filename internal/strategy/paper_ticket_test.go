package strategy_test

import (
	"testing"
	"time"

	"github.com/VersoIt/Inquisitor/internal/hypothesis"
	"github.com/VersoIt/Inquisitor/internal/paper"
	"github.com/VersoIt/Inquisitor/internal/risk"
	"github.com/VersoIt/Inquisitor/internal/strategy"
	"github.com/VersoIt/Inquisitor/internal/testutil"
	"github.com/shopspring/decimal"
)

func paperTicketFixture(t *testing.T) (testutil.PaperRiskFixture, strategy.PaperRiskEvaluation, strategy.ApprovalSnapshot, paper.OrderTicket) {
	t.Helper()
	f := testutil.PaperRisk(t)
	e := f.Evaluation(t)
	a, err := strategy.ResolvePaperAuthorization(strategy.PaperAuthorization{Grant: f.Approval.Grant, Evidence: f.Approval.Evidence}, f.Approval.Policy, risk.ModePaper, f.Regime.Symbol, f.Regime.Interval, f.Regime.Regime, f.Approval.Now)
	if err != nil {
		t.Fatal(err)
	}
	ticket, err := strategy.NewPaperSignalTicket(e, a, f.Approval.Now)
	if err != nil {
		t.Fatal(err)
	}
	return f, e, a, ticket
}

func TestPaperSignalTicketBindsRiskAndAuthorization(t *testing.T) {
	f, e, a, ticket := paperTicketFixture(t)
	if ticket.SourceSignalID != e.SignalID || ticket.SourceApprovalID != e.ApprovalID || ticket.ValidationID != a.ValidationID || ticket.DecisionID != e.Audit.DecisionID || !ticket.Quantity.Equal(e.Audit.Decision.FinalQuantity) {
		t.Fatalf("binding lost: %+v", ticket)
	}
	if err := strategy.CheckPaperSignalTicket(ticket, e, a); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name   string
		change func(*strategy.ApprovalSnapshot)
	}{
		{"LIVE", func(a *strategy.ApprovalSnapshot) { a.Mode = risk.ModeLive }},
		{"disabled", func(a *strategy.ApprovalSnapshot) { a.Enabled = false }},
		{"edge decay", func(a *strategy.ApprovalSnapshot) { a.EdgeDecayed = true }},
		{"different grant", func(a *strategy.ApprovalSnapshot) { a.ApprovalID = "other" }},
		{"different hypothesis", func(a *strategy.ApprovalSnapshot) { a.HypothesisID = "other" }},
		{"different market", func(a *strategy.ApprovalSnapshot) { a.Symbol = "ETHUSDT" }},
		{"different cap", func(a *strategy.ApprovalSnapshot) { a.MaxRiskPct = decimal.RequireFromString("0.1") }},
		{"different confidence", func(a *strategy.ApprovalSnapshot) { a.MinConfidence++ }},
		{"wrong direction", func(a *strategy.ApprovalSnapshot) { a.Direction = hypothesis.DirectionShort }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			changed := a
			tt.change(&changed)
			got, err := strategy.NewPaperSignalTicket(e, changed, f.Approval.Now)
			if err == nil || got.TicketID != "" {
				t.Fatalf("invalid approval created ticket: %+v %v", got, err)
			}
		})
	}
	for _, change := range []string{"source", "approval", "quantity", "validation", "ticket ID", "time", "partial source"} {
		t.Run("tampered "+change, func(t *testing.T) {
			changed := ticket
			switch change {
			case "source":
				changed.SourceSignalID = "other"
			case "approval":
				changed.SourceApprovalID = "other"
			case "quantity":
				changed.Quantity = decimal.NewFromInt(1)
				changed.MaxLoss = decimal.NewFromInt(4)
			case "validation":
				changed.ValidationID = "other"
			case "ticket ID":
				changed.TicketID = "other"
			case "time":
				changed.CreatedAt = changed.CreatedAt.Add(-time.Microsecond)
			case "partial source":
				changed.SourceSignalID = ""
			}
			if err := strategy.CheckPaperSignalTicket(changed, e, a); err == nil {
				t.Fatal("tampered ticket accepted")
			}
		})
	}
}

func TestPaperSignalRiskFreshnessAndPolicyBoundaries(t *testing.T) {
	f, e, _, _ := paperTicketFixture(t)
	for _, tt := range []struct {
		name  string
		at    time.Time
		valid bool
	}{
		{"now", f.Approval.Now, true},
		{"inclusive maximum age", e.Context.Input.Market.DataTime.Add(e.Context.Policy.MaxDataAge), true},
		{"beyond maximum age", e.Context.Input.Market.DataTime.Add(e.Context.Policy.MaxDataAge + time.Nanosecond), false},
		{"before audit", e.Audit.RecordedAt.Add(-time.Microsecond), false},
		{"missing time", time.Time{}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if err := strategy.RequireFreshPaperRisk(e, tt.at); (err == nil) != tt.valid {
				t.Fatalf("fresh=%v, err=%v", tt.valid, err)
			}
		})
	}
	if err := strategy.CheckPaperRiskPolicy(e, f.Policy); err != nil {
		t.Fatal(err)
	}
	changed := f.Policy
	changed.MaxOpenPositions++
	if err := strategy.CheckPaperRiskPolicy(e, changed); err == nil {
		t.Fatal("changed policy accepted")
	}
}

func TestPaperSignalFillChecksActualLossAndCosts(t *testing.T) {
	_, e, _, ticket := paperTicketFixture(t)
	for _, tt := range []struct {
		name, price, fee, spread, slippage string
		valid                              bool
	}{
		{"exact entry", "100", "0", "2", "3", true},
		{"entry fee within rounding headroom", "100", "0.02", "5", "10", true},
		{"fee exhausts budget", "100", "0.02000001", "2", "3", false},
		{"adverse price exhausts budget", "100.1", "0", "2", "3", false},
		{"favorable entry", "99.5", "0.01", "2", "3", true},
		{"at stop", "96", "0", "2", "3", false},
		{"at target", "108", "0", "2", "3", false},
		{"spread too high", "100", "0", "5.0001", "3", false},
		{"slippage too high", "100", "0", "2", "10.0001", false},
		{"negative fee", "100", "-1", "2", "3", false},
		{"zero price", "0", "0", "2", "3", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			err := strategy.CheckPaperSignalFillBudget(e, ticket, decimal.RequireFromString(tt.price), decimal.RequireFromString(tt.fee), decimal.RequireFromString(tt.spread), decimal.RequireFromString(tt.slippage))
			if (err == nil) != tt.valid {
				t.Fatalf("valid=%v err=%v", tt.valid, err)
			}
		})
	}
	// Mirror the price protection for SHORT rather than relying on LONG-only tests.
	ticket.Side = paper.OrderSideShort
	ticket.StopLoss = decimal.NewFromInt(104)
	ticket.TakeProfit = decimal.NewFromInt(92)
	for _, tt := range []struct {
		price string
		valid bool
	}{{"100", true}, {"100.5", true}, {"99.9", false}, {"104", false}, {"92", false}} {
		t.Run("short "+tt.price, func(t *testing.T) {
			err := strategy.CheckPaperSignalFillBudget(e, ticket, decimal.RequireFromString(tt.price), decimal.Zero, decimal.NewFromInt(2), decimal.NewFromInt(3))
			if (err == nil) != tt.valid {
				t.Fatal(err)
			}
		})
	}
}
