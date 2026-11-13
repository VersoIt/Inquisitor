package research

import (
	"testing"
	"time"

	"github.com/VersoIt/Inquisitor/internal/backtest"
	"github.com/shopspring/decimal"
)

func TestLongTradeAttribution(t *testing.T) {
	costs, _ := backtest.NewCostModel(10, 10, 5, 3, 1.5)
	for _, tc := range []struct{ name, exit, mid, entryImpact, exitImpact, fees, net string }{
		{"winner", "110", "20", "0.14", "0.154", "0.419986", "19.286014"},
		{"flat price still loses", "100", "0", "0.14", "0.14", "0.4", "-0.68"},
		{"loser", "90", "-20", "0.14", "0.126", "0.380014", "-20.646014"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			at := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
			trade, err := backtest.EvaluateRoundTrip(backtest.RoundTripInput{Direction: backtest.DirectionLong, EntryTime: at, ExitTime: at.Add(time.Hour), EntryMidPrice: decimal.NewFromInt(100), ExitMidPrice: decimal.RequireFromString(tc.exit), Quantity: decimal.NewFromInt(2), Costs: costs})
			if err != nil {
				t.Fatal(err)
			}
			a, err := AttributeLongTrade(trade, costs)
			if err != nil {
				t.Fatal(err)
			}
			for _, field := range []struct {
				name string
				got  decimal.Decimal
				want string
			}{
				{"mid", a.MidPricePnL, tc.mid}, {"entry", a.EntryImpact, tc.entryImpact}, {"exit", a.ExitImpact, tc.exitImpact}, {"fees", a.Fees, tc.fees}, {"net", a.NetPnL, tc.net},
			} {
				if !field.got.Equal(decimal.RequireFromString(field.want)) {
					t.Fatalf("%s got=%s want=%s", field.name, field.got, field.want)
				}
			}
			if !a.MidPricePnL.Sub(a.EntryImpact).Sub(a.ExitImpact).Sub(a.Fees).Equal(trade.NetPnL) {
				t.Fatal("attribution identity failed")
			}
		})
	}
}

func TestLongAttributionRejectsCorruptedLedger(t *testing.T) {
	costs, _ := backtest.NewCostModel(10, 10, 5, 3, 1.5)
	at := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	base, err := backtest.EvaluateRoundTrip(backtest.RoundTripInput{Direction: backtest.DirectionLong, EntryTime: at, ExitTime: at.Add(time.Hour), EntryMidPrice: decimal.NewFromInt(100), ExitMidPrice: decimal.NewFromInt(110), Quantity: decimal.NewFromInt(1), Costs: costs})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*backtest.RoundTrip)
	}{
		{"short", func(t *backtest.RoundTrip) { t.Direction = backtest.DirectionShort }},
		{"fee", func(t *backtest.RoundTrip) { t.Entry.Fee = decimal.Zero }},
		{"exit quantity", func(t *backtest.RoundTrip) { t.Exit.Quantity = decimal.NewFromInt(2) }},
		{"executed price", func(t *backtest.RoundTrip) { t.Exit.ExecutedPrice = t.Exit.MidPrice }},
		{"return", func(t *backtest.RoundTrip) { t.Return = decimal.Zero }},
		{"net", func(t *backtest.RoundTrip) { t.NetPnL = decimal.Zero }},
		{"time reversed", func(t *backtest.RoundTrip) { t.Exit.Time = t.Entry.Time }},
		{"zero price", func(t *backtest.RoundTrip) { t.Entry.MidPrice = decimal.Zero }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			trade := base
			tc.mutate(&trade)
			if _, err := AttributeLongTrade(trade, costs); err == nil {
				t.Fatal("corruption accepted")
			}
		})
	}
	other := costs
	other.TakerFeeBPS = decimal.NewFromInt(20)
	if _, err := AttributeLongTrade(base, other); err == nil {
		t.Fatal("changed costs accepted")
	}
}
