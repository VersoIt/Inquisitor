package strategy_test

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/VersoIt/Inquisitor/internal/risk"
	"github.com/VersoIt/Inquisitor/internal/strategy"
	"github.com/VersoIt/Inquisitor/internal/testutil"
	"github.com/shopspring/decimal"
)

func TestPaperRunInputIdentityAndJSONRoundTrip(t *testing.T) {
	in := testutil.PaperRun(t)
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var got strategy.PaperRunInput
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if err := strategy.ValidatePaperRunInput(got); err != nil {
		t.Fatal(err)
	}
	if got.SHA256 != in.SHA256 || strategy.PaperRunSignalID(in.RunID) != strategy.PaperRunSignalID(in.RunID) || strategy.PaperRunSignalID(in.RunID) == strategy.PaperRunSignalID(in.RunID+"x") {
		t.Fatal("unstable run/signal identity")
	}
	got.Market.Quote.Mid = decimal.NewFromInt(101)
	if err := strategy.ValidatePaperRunInput(got); err == nil {
		t.Fatal("changed source bypassed checksum")
	}
}

func TestPaperRunInputOwnsNestedSourceValues(t *testing.T) {
	in := testutil.PaperRun(t)
	owned, err := strategy.NewPaperRunInput(in)
	if err != nil {
		t.Fatal(err)
	}
	in.Market.Policy.AllowedSymbols[0] = "ETHUSDT"
	in.Market.Regime.Reasons[0] = "changed"
	in.Account.Account.Equity = decimal.NewFromInt(1)
	if err := strategy.ValidatePaperRunInput(owned); err != nil {
		t.Fatal("frozen input aliases caller", err)
	}
}

func TestPaperRunRejectsInvalidInputsAndReceipts(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*strategy.PaperRunInput)
	}{
		{"run ID", func(in *strategy.PaperRunInput) { in.RunID = " bad " }},
		{"unbounded ID", func(in *strategy.PaperRunInput) { in.RunID = strings.Repeat("a", 129) }},
		{"approval ID", func(in *strategy.PaperRunInput) { in.ApprovalID = "" }},
		{"foreign account", func(in *strategy.PaperRunInput) { in.Account.ValidationID = "foreign" }},
		{"future market", func(in *strategy.PaperRunInput) { in.Market.Query.AsOf = in.CreatedAt.Add(time.Microsecond) }},
		{"future account", func(in *strategy.PaperRunInput) { in.Account.AsOf = in.CreatedAt.Add(time.Microsecond) }},
		{"non-DB precision", func(in *strategy.PaperRunInput) { in.CreatedAt = in.CreatedAt.Add(time.Nanosecond) }},
		{"live mode", func(in *strategy.PaperRunInput) { in.Runtime.Mode = risk.ModeLive }},
		{"caller stop", func(in *strategy.PaperRunInput) { in.Runtime.KillSwitchActive = true }},
		{"missing account", func(in *strategy.PaperRunInput) { in.Account.Account = nil }},
		{"pending", func(in *strategy.PaperRunInput) { in.Account.Inventory.PendingTickets = 1 }},
		{"blocked", func(in *strategy.PaperRunInput) { in.Account.Blockers = []string{"pending_tickets"} }},
		{"bad config checksum", func(in *strategy.PaperRunInput) { in.ConfigSHA256 = "bad" }},
		{"wrong equity", func(in *strategy.PaperRunInput) { in.Account.RealizedEquity = decimal.NewFromInt(1) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			in := testutil.PaperRun(t)
			tc.change(&in)
			if _, err := strategy.NewPaperRunInput(in); err == nil {
				t.Fatal("unsafe input accepted")
			}
		})
	}
	in := testutil.PaperRun(t)
	signal := strategy.PaperRunSignalID(in.RunID)
	_, decision := strategy.PaperRiskIDs(signal)
	for _, outcome := range []string{"NO_TRADE", "RISK_REJECTED", "TICKET_RECORDED"} {
		r := strategy.PaperRunReceipt{RunID: in.RunID, InputSHA256: in.SHA256, SignalID: signal, Outcome: outcome, CompletedAt: in.CreatedAt}
		if outcome != "NO_TRADE" {
			r.DecisionID = decision
		}
		if outcome == "TICKET_RECORDED" {
			r.TicketID = strategy.PaperSignalTicketID(signal)
		}
		if err := strategy.ValidatePaperRunReceipt(r, in); err != nil {
			t.Fatal(err)
		}
		for _, change := range []func(*strategy.PaperRunReceipt){func(r *strategy.PaperRunReceipt) { r.InputSHA256 = "bad" }, func(r *strategy.PaperRunReceipt) { r.SignalID = "foreign" }, func(r *strategy.PaperRunReceipt) { r.CompletedAt = in.CreatedAt.Add(-time.Microsecond) }, func(r *strategy.PaperRunReceipt) { r.Outcome = "other" }, func(r *strategy.PaperRunReceipt) { r.TicketID = "foreign" }} {
			bad := r
			change(&bad)
			if err := strategy.ValidatePaperRunReceipt(bad, in); err == nil {
				t.Fatalf("unsafe receipt %+v", bad)
			}
		}
	}
}
