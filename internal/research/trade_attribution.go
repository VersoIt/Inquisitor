package research

import (
	"fmt"

	"github.com/VersoIt/Inquisitor/internal/backtest"
	"github.com/shopspring/decimal"
)

type TradeCostAttribution struct {
	MidPricePnL, EntryImpact, ExitImpact decimal.Decimal
	GrossPnL, Fees, NetPnL               decimal.Decimal
}

// AttributeLongTrade decomposes the existing ledger, not a zero-cost replay.
// Quantities and entry/exit decisions remain those of the original cost model.
func AttributeLongTrade(t backtest.RoundTrip, costs backtest.CostModel) (TradeCostAttribution, error) {
	if t.Direction != backtest.DirectionLong {
		return TradeCostAttribution{}, fmt.Errorf("attribution requires a LONG spot trade")
	}
	expected, err := backtest.EvaluateRoundTrip(backtest.RoundTripInput{
		Direction: t.Direction, EntryTime: t.Entry.Time, ExitTime: t.Exit.Time,
		EntryMidPrice: t.Entry.MidPrice, ExitMidPrice: t.Exit.MidPrice,
		Quantity: t.Entry.Quantity, Costs: costs,
	})
	if err != nil {
		return TradeCostAttribution{}, err
	}
	actualHash, err := ExperimentHash(t)
	if err != nil {
		return TradeCostAttribution{}, err
	}
	expectedHash, err := ExperimentHash(expected)
	if err != nil {
		return TradeCostAttribution{}, err
	}
	if actualHash != expectedHash {
		return TradeCostAttribution{}, fmt.Errorf("trade does not reconcile with the original taker cost model")
	}
	q := t.Entry.Quantity
	a := TradeCostAttribution{
		MidPricePnL: t.Exit.MidPrice.Sub(t.Entry.MidPrice).Mul(q),
		EntryImpact: t.Entry.ExecutedPrice.Sub(t.Entry.MidPrice).Mul(q),
		ExitImpact:  t.Exit.MidPrice.Sub(t.Exit.ExecutedPrice).Mul(q),
		GrossPnL:    t.GrossPnL, Fees: t.Fees, NetPnL: t.NetPnL,
	}
	if !a.MidPricePnL.Sub(a.EntryImpact).Sub(a.ExitImpact).Equal(a.GrossPnL) || !a.GrossPnL.Sub(a.Fees).Equal(a.NetPnL) {
		return TradeCostAttribution{}, fmt.Errorf("trade attribution does not reconcile")
	}
	return a, nil
}
