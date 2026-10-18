package research

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/VersoIt/Inquisitor/internal/backtest"
	"github.com/VersoIt/Inquisitor/internal/marketdata"
	"github.com/VersoIt/Inquisitor/internal/marketdata/validator"
	"github.com/shopspring/decimal"
)

type TrendExperimentPlan struct {
	ID                                                          string
	SchemaVersion                                               int
	Exchange, Category, Symbol, Interval                        string
	DataStart, DevelopmentStart, ValidationStart, ValidationEnd time.Time
	InitialEquity, RiskPerTradePct, QuantityStep, MinNotional   decimal.Decimal
	MinADX, StopATRMultiple, TakeProfitR                        decimal.Decimal
	MaxHoldingCandles                                           int
	Costs                                                       backtest.CostModel
	Criteria                                                    TrendExperimentCriteria
}

type TrendExperimentCriteria struct {
	MinValidationTrades, MinBlockTrades, MinProfitableBlocks int
	MinProfitFactor, MaxMarkedDrawdownPct                    decimal.Decimal
}

func ValidateTrendExperimentPlan(p TrendExperimentPlan) error {
	if p.ID == "" || p.ID != strings.TrimSpace(p.ID) || p.SchemaVersion != 1 || p.Exchange != "bybit" || p.Category != "spot" || p.Symbol != "BTCUSDT" || p.Interval != "60" {
		return fmt.Errorf("experiment v1 requires an identified Bybit BTCUSDT spot hourly plan")
	}
	if !p.DataStart.Before(p.DevelopmentStart) || !p.DevelopmentStart.Before(p.ValidationStart) || !p.ValidationStart.Before(p.ValidationEnd) {
		return fmt.Errorf("experiment windows must be ordered and nonempty")
	}
	for _, at := range []time.Time{p.DataStart, p.DevelopmentStart, p.ValidationStart, p.ValidationEnd} {
		_, offset := at.Zone()
		if at.IsZero() || offset != 0 || !at.Equal(at.Truncate(time.Hour)) {
			return fmt.Errorf("experiment windows require UTC hour boundaries")
		}
	}
	if p.ValidationEnd.Sub(p.ValidationStart)%(4*time.Hour) != 0 {
		return fmt.Errorf("validation must split into four whole-hour blocks")
	}
	for _, v := range []decimal.Decimal{p.InitialEquity, p.RiskPerTradePct, p.QuantityStep, p.MinNotional, p.MinADX, p.StopATRMultiple, p.TakeProfitR} {
		if !v.IsPositive() {
			return fmt.Errorf("experiment monetary and strategy parameters must be positive")
		}
	}
	if p.RiskPerTradePct.GreaterThan(decimal.NewFromInt(1)) || p.MinADX.GreaterThan(decimal.NewFromInt(100)) || p.MaxHoldingCandles < 1 {
		return fmt.Errorf("experiment risk/ADX/holding limits are invalid")
	}
	if err := backtest.ValidateCostModel(p.Costs); err != nil {
		return err
	}
	if !p.Costs.TakerFeeBPS.IsPositive() || !p.Costs.SpreadBPS.IsPositive() || !p.Costs.SlippageBPS.IsPositive() {
		return fmt.Errorf("experiment must model positive fees, spread and slippage")
	}
	c := p.Criteria
	if c.MinValidationTrades < 100 || c.MinBlockTrades < 20 || c.MinProfitableBlocks < 3 || c.MinProfitableBlocks > 4 || !c.MinProfitFactor.GreaterThan(decimal.NewFromInt(1)) || !c.MaxMarkedDrawdownPct.IsPositive() || c.MaxMarkedDrawdownPct.GreaterThan(decimal.NewFromInt(15)) {
		return fmt.Errorf("experiment acceptance gates must remain conservative")
	}
	return nil
}

func ExperimentHash(value any) (string, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

// ValidateExperimentCandles rejects truncation, gaps, duplicates and scope mixing.
func ValidateExperimentCandles(p TrendExperimentPlan, candles []marketdata.Candle) error {
	if err := ValidateTrendExperimentPlan(p); err != nil {
		return err
	}
	expected := int(p.ValidationEnd.Sub(p.DataStart) / time.Hour)
	if len(candles) != expected {
		return fmt.Errorf("experiment requires %d contiguous candles, got %d", expected, len(candles))
	}
	for i, c := range candles {
		if err := validator.ValidateCandle(c); err != nil {
			return fmt.Errorf("candle %d: %w", i, err)
		}
		if !c.IsClosed || c.Exchange != p.Exchange || c.Category != p.Category || c.Symbol != p.Symbol || c.Interval != p.Interval || !c.OpenTime.Equal(p.DataStart.Add(time.Duration(i)*time.Hour)) {
			return fmt.Errorf("candle %d is unclosed, missing, unordered or differently scoped", i)
		}
	}
	return nil
}

type LongBarExit struct {
	Triggered bool
	MidPrice  decimal.Decimal
	At        time.Time
	Reason    string
}

// LongExperimentExit never assumes a favorable intrabar path. A gap through a
// stop fills at the open; if both barriers are touched, the stop wins.
func LongExperimentExit(c marketdata.Candle, stop, target decimal.Decimal, held, maxHeld int, end time.Time) LongBarExit {
	switch {
	case c.Open.LessThanOrEqual(stop):
		return LongBarExit{true, c.Open, c.OpenTime, "stop_gap"}
	case c.Open.GreaterThanOrEqual(target):
		return LongBarExit{true, target, c.OpenTime, "target_gap_capped"}
	case c.Low.LessThanOrEqual(stop):
		return LongBarExit{true, stop, c.CloseTime, "stop"}
	case c.High.GreaterThanOrEqual(target):
		return LongBarExit{true, target, c.CloseTime, "target"}
	case held >= maxHeld:
		return LongBarExit{true, c.Close, c.CloseTime, "time_limit"}
	case !c.CloseTime.Before(end):
		return LongBarExit{true, c.Close, c.CloseTime, "window_end"}
	default:
		return LongBarExit{}
	}
}

// SpotExperimentQuantity reserves both entry and modeled stop-exit costs, and
// caps cash usage. Gaps beyond the stop can still lose more than this budget.
func SpotExperimentQuantity(p TrendExperimentPlan, equity, entryMid, stop decimal.Decimal, costs backtest.CostModel) (decimal.Decimal, error) {
	if !equity.IsPositive() || !stop.IsPositive() || !entryMid.GreaterThan(stop) {
		return decimal.Zero, nil
	}
	at := p.DevelopmentStart
	entry, err := backtest.EvaluateFill(backtest.FillInput{Direction: backtest.DirectionLong, Role: backtest.FillRoleEntry, Time: at, MidPrice: entryMid, Quantity: decimal.NewFromInt(1), Liquidity: backtest.LiquidityTaker, Costs: costs})
	if err != nil {
		return decimal.Zero, err
	}
	exit, err := backtest.EvaluateFill(backtest.FillInput{Direction: backtest.DirectionLong, Role: backtest.FillRoleExit, Time: at, MidPrice: stop, Quantity: decimal.NewFromInt(1), Liquidity: backtest.LiquidityTaker, Costs: costs})
	if err != nil {
		return decimal.Zero, err
	}
	unitLoss := entry.ExecutedPrice.Sub(exit.ExecutedPrice).Add(entry.Fee).Add(exit.Fee)
	if !unitLoss.IsPositive() {
		return decimal.Zero, nil
	}
	budget := equity.Mul(p.RiskPerTradePct).Div(decimal.NewFromInt(100))
	quantity := decimal.Min(budget.Div(unitLoss), equity.Div(entry.ExecutedPrice.Add(entry.Fee)))
	quantity = quantity.Div(p.QuantityStep).Floor().Mul(p.QuantityStep)
	// Recheck actual rounded fees rather than assuming that a unit-cost estimate
	// has identical decimal rounding at the final quantity.
	for attempt := 0; attempt < 2 && quantity.IsPositive(); attempt++ {
		trip, err := backtest.EvaluateRoundTrip(backtest.RoundTripInput{Direction: backtest.DirectionLong, EntryTime: at, ExitTime: at.Add(time.Hour), EntryMidPrice: entryMid, ExitMidPrice: stop, Quantity: quantity, Costs: costs})
		if err != nil {
			return decimal.Zero, err
		}
		if trip.NetPnL.Neg().LessThanOrEqual(budget) && trip.Entry.Notional.Add(trip.Entry.Fee).LessThanOrEqual(equity) {
			break
		}
		if attempt == 1 {
			return decimal.Zero, fmt.Errorf("rounded experiment quantity exceeds cash/risk budget")
		}
		quantity = quantity.Sub(p.QuantityStep)
	}
	if !quantity.IsPositive() || quantity.Mul(entry.ExecutedPrice).LessThan(p.MinNotional) {
		return decimal.Zero, nil
	}
	return quantity, nil
}
