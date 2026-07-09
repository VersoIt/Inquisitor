package risk_test

import (
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/VersoIt/Inquisitor/internal/risk"
)

func TestNewDecisionAuditRecordCopiesAndValidatesContext(t *testing.T) {
	createdAt := time.Date(2026, 6, 21, 12, 0, 0, 0, time.FixedZone("UTC+3", 3*60*60))
	input := risk.DecisionAuditInput{
		DecisionID: "decision_0001",
		Decision: risk.Decision{
			IntentID:      "intent_0001",
			Approved:      true,
			FinalQuantity: decimal.RequireFromString("0.5"),
			MaxLoss:       decimal.RequireFromString("1000"),
			StopLoss:      decimal.RequireFromString("98000"),
			TakeProfit:    decimal.RequireFromString("102000"),
			Reason:        "risk_checks_passed",
			Checks:        []risk.Check{{Name: "trading_enabled", Passed: true}},
			CreatedAt:     createdAt,
		},
		Intent: risk.TradeIntent{
			IntentID:     "intent_0001",
			HypothesisID: " hypothesis_0001 ",
			StrategyName: " trend-momentum ",
			Symbol:       " btcusdt ",
			Side:         " long ",
			Confidence:   80,
			EntryPrice:   decimal.RequireFromString("100000"),
			Leverage:     decimal.RequireFromString("1"),
			Reason:       " signal confirmed ",
			CreatedAt:    createdAt.Add(-time.Minute),
		},
		Runtime:    risk.RuntimeState{Mode: risk.ModePaper},
		RecordedAt: createdAt.Add(time.Second),
	}

	got, err := risk.NewDecisionAuditRecord(input)
	if err != nil {
		t.Fatalf("new decision audit record: %v", err)
	}
	input.Decision.Checks[0].Name = "mutated"

	if got.DecisionID != "decision_0001" || got.Mode != risk.ModePaper || got.Symbol != "BTCUSDT" || got.Side != risk.SideLong {
		t.Fatalf("metadata was not normalized: %#v", got)
	}
	if got.HypothesisID != "hypothesis_0001" || got.StrategyName != "trend-momentum" {
		t.Fatalf("intent metadata mismatch: %#v", got)
	}
	if !got.EntryPrice.Equal(decimal.RequireFromString("100000")) || !got.Leverage.Equal(decimal.RequireFromString("1")) ||
		got.Confidence != 80 || got.IntentReason != "signal confirmed" {
		t.Fatalf("intent snapshot mismatch: %#v", got)
	}
	if got.Decision.Checks[0].Name != "trading_enabled" {
		t.Fatalf("checks were not defensively copied: %#v", got.Decision.Checks)
	}
	if got.RecordedAt.Location() != time.UTC || got.Decision.CreatedAt.Location() != time.UTC || got.IntentCreatedAt.Location() != time.UTC {
		t.Fatalf("timestamps must be stored in UTC: intent=%s decision=%s recorded=%s", got.IntentCreatedAt, got.Decision.CreatedAt, got.RecordedAt)
	}
}

func TestDecisionAuditValidationRejectsInvalidRecordsTableDriven(t *testing.T) {
	now := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	valid := testDecisionAuditRecord(now)

	tests := []struct {
		name       string
		mutate     func(*risk.DecisionAuditRecord)
		wantErrSub string
	}{
		{"missing decision id", func(r *risk.DecisionAuditRecord) { r.DecisionID = " " }, "decision_id"},
		{"invalid decision", func(r *risk.DecisionAuditRecord) { r.Decision.Checks = nil }, "checks"},
		{"invalid mode", func(r *risk.DecisionAuditRecord) { r.Mode = "SIM" }, "mode"},
		{"missing hypothesis id", func(r *risk.DecisionAuditRecord) { r.HypothesisID = "" }, "hypothesis_id"},
		{"symbol not normalized", func(r *risk.DecisionAuditRecord) { r.Symbol = "btcusdt" }, "symbol"},
		{"invalid side", func(r *risk.DecisionAuditRecord) { r.Side = "BUY" }, "side"},
		{"approved missing entry price", func(r *risk.DecisionAuditRecord) { r.EntryPrice = decimal.Zero }, "entry_price"},
		{"approved missing leverage", func(r *risk.DecisionAuditRecord) { r.Leverage = decimal.Zero }, "leverage"},
		{"approved invalid confidence", func(r *risk.DecisionAuditRecord) { r.Confidence = 101 }, "confidence"},
		{"approved missing intent reason", func(r *risk.DecisionAuditRecord) { r.IntentReason = "" }, "intent_reason"},
		{"approved future intent time", func(r *risk.DecisionAuditRecord) { r.IntentCreatedAt = r.Decision.CreatedAt.Add(time.Nanosecond) }, "intent_created_at"},
		{"approved max loss mismatch", func(r *risk.DecisionAuditRecord) { r.Decision.MaxLoss = decimal.RequireFromString("999") }, "max_loss"},
		{"approved long stop above entry", func(r *risk.DecisionAuditRecord) { r.Decision.StopLoss = decimal.RequireFromString("101000") }, "stop_loss"},
		{"approved long take profit below entry", func(r *risk.DecisionAuditRecord) { r.Decision.TakeProfit = decimal.RequireFromString("99000") }, "take_profit"},
		{"recorded before decision", func(r *risk.DecisionAuditRecord) { r.RecordedAt = r.Decision.CreatedAt.Add(-time.Nanosecond) }, "recorded_at"},
		{"missing recorded at", func(r *risk.DecisionAuditRecord) { r.RecordedAt = time.Time{} }, "recorded_at"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			record := valid
			record.Decision.Checks = append([]risk.Check(nil), valid.Decision.Checks...)
			tt.mutate(&record)

			err := risk.ValidateDecisionAuditRecord(record)
			if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
		})
	}
}

func TestDecisionAuditValidationAllowsRejectedInvalidIntentSnapshot(t *testing.T) {
	now := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	record := testDecisionAuditRecord(now)
	record.Decision.Approved = false
	record.Decision.FinalQuantity = decimal.Zero
	record.Decision.MaxLoss = decimal.Zero
	record.Decision.Reason = "intent_identity_invalid"
	record.Decision.Checks = []risk.Check{{Name: "intent_identity", Passed: false, Reason: "intent_identity_invalid"}}
	record.EntryPrice = decimal.RequireFromString("-1")
	record.Leverage = decimal.Zero
	record.Confidence = 150
	record.IntentReason = ""
	record.IntentCreatedAt = time.Time{}

	if err := risk.ValidateDecisionAuditRecord(record); err != nil {
		t.Fatalf("rejected decisions should preserve invalid intent snapshots for audit: %v", err)
	}
}

func TestDecisionAuditQueryValidationTableDriven(t *testing.T) {
	now := time.Date(2026, 6, 21, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name       string
		query      risk.DecisionAuditQuery
		wantErrSub string
	}{
		{"valid empty query", risk.DecisionAuditQuery{}, ""},
		{"valid filtered query", risk.DecisionAuditQuery{Symbol: "BTCUSDT", Start: now, End: now.Add(time.Hour), Limit: 10}, ""},
		{"rejects lowercase symbol", risk.DecisionAuditQuery{Symbol: "btcusdt"}, "symbol"},
		{"rejects invalid window", risk.DecisionAuditQuery{Start: now, End: now}, "end"},
		{"rejects negative limit", risk.DecisionAuditQuery{Limit: -1}, "limit"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := risk.ValidateDecisionAuditQuery(tt.query)
			if tt.wantErrSub == "" {
				if err != nil {
					t.Fatalf("validate query: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
		})
	}
}

func testDecisionAuditRecord(now time.Time) risk.DecisionAuditRecord {
	return risk.DecisionAuditRecord{
		DecisionID: "decision_0001",
		Decision: risk.Decision{
			IntentID:      "intent_0001",
			Approved:      true,
			FinalQuantity: decimal.RequireFromString("0.5"),
			MaxLoss:       decimal.RequireFromString("1000"),
			StopLoss:      decimal.RequireFromString("98000"),
			TakeProfit:    decimal.RequireFromString("102000"),
			Reason:        "risk_checks_passed",
			Checks:        []risk.Check{{Name: "trading_enabled", Passed: true}},
			CreatedAt:     now,
		},
		Mode:            risk.ModePaper,
		HypothesisID:    "hypothesis_0001",
		StrategyName:    "trend-momentum",
		Symbol:          "BTCUSDT",
		Side:            risk.SideLong,
		EntryPrice:      decimal.RequireFromString("100000"),
		Leverage:        decimal.RequireFromString("1"),
		Confidence:      80,
		IntentReason:    "signal confirmed",
		IntentCreatedAt: now.Add(-time.Minute),
		RecordedAt:      now.Add(time.Second),
	}
}
