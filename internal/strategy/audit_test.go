package strategy_test

import (
	"context"
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/VersoIt/Inquisitor/internal/risk"
	"github.com/VersoIt/Inquisitor/internal/strategy"
	"github.com/shopspring/decimal"
)

func auditRecord(t *testing.T) strategy.SignalAuditRecord {
	t.Helper()
	m := trendMarket()
	s, err := strategy.NewTrendStrategy(trendConfig())
	if err != nil {
		t.Fatal(err)
	}
	signal, err := s.GenerateSignal(context.Background(), m)
	if err != nil {
		t.Fatal(err)
	}
	record, err := strategy.NewSignalAuditRecord(strategy.SignalAuditRecord{Signal: signal, Mode: m.Mode, Exchange: m.Regime.Exchange, Category: m.Regime.Category, ObservedAt: m.ObservedAt, Features: strategy.SignalEvidence(m), RecordedAt: m.Now.Add(time.Second)})
	if err != nil {
		t.Fatal(err)
	}
	return record
}

func TestSignalAuditOwnsPayloadAndPreservesDecimalJSON(t *testing.T) {
	input := auditRecord(t)
	offset := time.FixedZone("offset", 3*60*60)
	input.Signal.CreatedAt = input.Signal.CreatedAt.Add(123 * time.Nanosecond).In(offset)
	input.Signal.Trade.CreatedAt = input.Signal.CreatedAt
	input.ObservedAt = input.ObservedAt.Add(456 * time.Nanosecond).In(offset)
	input.Signal.Trade.ObservedAt = input.ObservedAt
	input.RecordedAt = input.RecordedAt.Add(789 * time.Nanosecond).In(offset)
	input.Signal.Trade.EntryPrice = decimal.RequireFromString("106.123456789123456789")
	got, err := strategy.NewSignalAuditRecord(input)
	if err != nil {
		t.Fatal(err)
	}
	if err := strategy.ValidateSignalAuditRecord(got); err != nil {
		t.Fatal(err)
	}
	input.Features["trend.ema20"] = "changed"
	input.Signal.Trade.Reason = "changed"
	if got.Features["trend.ema20"] != "104" || got.Signal.Trade.Reason == "changed" {
		t.Fatal("audit record aliases caller-owned values")
	}
	if got.Signal.CreatedAt.Location() != time.UTC || got.Signal.CreatedAt.Nanosecond()%1000 != 0 || got.ObservedAt.Nanosecond()%1000 != 0 || got.RecordedAt.Nanosecond()%1000 != 0 {
		t.Fatalf("timestamps not canonical: %+v", got)
	}
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var decoded strategy.SignalAuditRecord
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatal(err)
	}
	if !decoded.Signal.Trade.EntryPrice.Equal(decimal.RequireFromString("106.123456789123456789")) {
		t.Fatal("JSON lost decimal precision")
	}
	if err := strategy.ValidateSignalAuditRecord(decoded); err != nil {
		t.Fatal(err)
	}
}

func TestSignalAuditValidatesEnvelopeAndTrade(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*strategy.SignalAuditRecord)
		field  string
	}{
		{"missing ID", func(r *strategy.SignalAuditRecord) { r.Signal.SignalID = "" }, "signal_id"},
		{"padded ID", func(r *strategy.SignalAuditRecord) { r.Signal.SignalID = " padded " }, "signal_id"},
		{"missing hypothesis", func(r *strategy.SignalAuditRecord) { r.Signal.HypothesisID = "" }, "hypothesis_id"},
		{"missing version", func(r *strategy.SignalAuditRecord) { r.Signal.HypothesisVersion = "" }, "hypothesis_version"},
		{"missing strategy", func(r *strategy.SignalAuditRecord) { r.Signal.StrategyName = "" }, "strategy_name"},
		{"lowercase symbol", func(r *strategy.SignalAuditRecord) { r.Signal.Symbol = "btcusdt" }, "symbol"},
		{"bad interval", func(r *strategy.SignalAuditRecord) { r.Signal.Interval = "bad" }, "interval"},
		{"missing reason", func(r *strategy.SignalAuditRecord) { r.Signal.Reason = " " }, "reason"},
		{"missing exchange", func(r *strategy.SignalAuditRecord) { r.Exchange = "" }, "exchange"},
		{"missing category", func(r *strategy.SignalAuditRecord) { r.Category = "" }, "category"},
		{"unknown mode", func(r *strategy.SignalAuditRecord) { r.Mode = "unknown" }, "mode"},
		{"missing created", func(r *strategy.SignalAuditRecord) { r.Signal.CreatedAt = time.Time{} }, "created_at"},
		{"missing recorded", func(r *strategy.SignalAuditRecord) { r.RecordedAt = time.Time{} }, "recorded_at"},
		{"recorded before created", func(r *strategy.SignalAuditRecord) { r.RecordedAt = r.Signal.CreatedAt.Add(-time.Nanosecond) }, "recorded_at"},
		{"missing features", func(r *strategy.SignalAuditRecord) { r.Features = nil }, "features"},
		{"invalid feature key", func(r *strategy.SignalAuditRecord) { r.Features[" key "] = "value" }, "features"},
		{"missing trade", func(r *strategy.SignalAuditRecord) { r.Signal.Trade = nil }, "trade"},
		{"no trade with trade", func(r *strategy.SignalAuditRecord) { r.Signal.Type = strategy.SignalNoTrade }, "trade"},
		{"unknown signal", func(r *strategy.SignalAuditRecord) { r.Signal.Type = "HOLD" }, "type"},
		{"type side mismatch", func(r *strategy.SignalAuditRecord) { r.Signal.Type = strategy.SignalSell }, "side"},
		{"trade hypothesis mismatch", func(r *strategy.SignalAuditRecord) { r.Signal.Trade.HypothesisID = "other" }, "identity"},
		{"trade reason mismatch", func(r *strategy.SignalAuditRecord) { r.Signal.Trade.Reason = "other" }, "reason"},
		{"trade observation mismatch", func(r *strategy.SignalAuditRecord) { r.ObservedAt = r.ObservedAt.Add(time.Second) }, "timestamps"},
		{"invalid stop", func(r *strategy.SignalAuditRecord) { r.Signal.Trade.StopLoss = decimal.Zero }, "stop_loss"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := auditRecord(t)
			tt.mutate(&r)
			if _, err := strategy.NewSignalAuditRecord(r); err == nil || !strings.Contains(err.Error(), tt.field) {
				t.Fatalf("want %s error, got %v", tt.field, err)
			}
		})
	}
	r := auditRecord(t)
	r.Signal.CreatedAt = r.Signal.CreatedAt.Add(time.Nanosecond)
	r.Signal.Trade.CreatedAt = r.Signal.CreatedAt
	if err := strategy.ValidateSignalAuditRecord(r); err == nil {
		t.Fatal("repository boundary accepted sub-microsecond timestamps")
	}
}

func TestAbstentionCanAuditMissingOrFutureObservation(t *testing.T) {
	for _, observed := range []time.Time{{}, time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC)} {
		r := auditRecord(t)
		r.Signal.Type, r.Signal.Trade, r.ObservedAt = strategy.SignalNoTrade, nil, observed
		r.Signal.Reason = "market_time_invalid"
		got, err := strategy.NewSignalAuditRecord(r)
		if err != nil {
			t.Fatal(err)
		}
		if err := strategy.ValidateSignalAuditRecord(got); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSignalEvidenceCapturesRejectedNonfiniteInputs(t *testing.T) {
	for _, tt := range []struct {
		value float64
		want  string
	}{{math.NaN(), "NaN"}, {math.Inf(1), "+Inf"}, {math.Inf(-1), "-Inf"}} {
		m := breakoutMarket()
		m.PriorVolatility.VolatilityCompression = tt.value
		e := strategy.SignalEvidence(m)
		if e["prior_price.rolling_high"] != "110" || e["current.close"] != "114" || e["prior_price.close_time"] != m.Previous.CloseTime.Format(time.RFC3339Nano) || e["runtime.trading_enabled"] != "true" || e["prior_volatility.compression"] != tt.want {
			t.Fatalf("incomplete evidence: %+v", e)
		}
		if _, err := json.Marshal(e); err != nil {
			t.Fatalf("cannot audit invalid statistical input: %v", err)
		}
	}
}

func TestSignalQueryValidation(t *testing.T) {
	now := time.Now().UTC()
	for _, q := range []strategy.SignalQuery{
		{}, {SignalID: "id", HypothesisID: "hyp", HypothesisVersion: "v1", Mode: risk.ModePaper, Symbol: "BTCUSDT", Type: strategy.SignalNoTrade, Start: now, End: now.Add(time.Second), Limit: 1000},
	} {
		if err := strategy.ValidateSignalQuery(q); err != nil {
			t.Fatal(err)
		}
	}
	for _, q := range []strategy.SignalQuery{
		{SignalID: " id "}, {HypothesisID: " h "}, {HypothesisVersion: " v "}, {Mode: "other"}, {Symbol: "btcusdt"}, {Type: "HOLD"}, {Start: now, End: now}, {Limit: -1}, {Limit: 1001},
	} {
		if err := strategy.ValidateSignalQuery(q); err == nil {
			t.Fatalf("accepted invalid query %+v", q)
		}
	}
	if (strategy.SignalWriteStats{Inserted: 1, Skipped: 2}).Total() != 3 {
		t.Fatal("wrong total")
	}
}
