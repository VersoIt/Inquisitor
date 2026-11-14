package strategy

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/VersoIt/Inquisitor/internal/features"
	"github.com/VersoIt/Inquisitor/internal/marketdata"
	"github.com/VersoIt/Inquisitor/internal/marketdata/validator"
	"github.com/VersoIt/Inquisitor/internal/regime"
	"github.com/VersoIt/Inquisitor/internal/research"
	"github.com/VersoIt/Inquisitor/internal/risk"
	"github.com/shopspring/decimal"
)

const (
	PaperMarketVersion     = "paper-market-candles-v1"
	PaperMarketCandleCount = 256
	MaxPaperBookLevels     = 1000
	MaxPaperBookJSONBytes  = 256 * 1024
)

type PaperMarketScope struct{ Exchange, Category, Symbol, Interval string }
type PaperMarketQuery struct {
	Scope PaperMarketScope
	AsOf  time.Time
}
type PaperMarketFacts struct {
	Candles    []marketdata.Candle
	Regime     regime.State
	Book       marketdata.OrderbookSnapshot
	Instrument marketdata.Instrument
}
type PaperMarketReader interface {
	LoadPaperMarket(context.Context, PaperMarketQuery) (PaperMarketFacts, error)
}

type PaperMarketPolicy struct {
	MaxDataAge, MaxInstrumentAge                    time.Duration
	MaxSpreadBPS, DepthBandBPS, ExpectedSlippageBPS decimal.Decimal
	AllowedSymbols                                  []string
}

type PaperMarketQuote struct {
	Bid, Ask, Mid, SpreadBPS, LiquidityQuote decimal.Decimal
	ExchangeTime, RecordedAt                 time.Time
	UpdateID, Sequence                       int64
}

type PaperMarketSnapshot struct {
	Version, SourceSHA256       string
	Query                       PaperMarketQuery
	Policy                      PaperMarketPolicy
	Quote                       PaperMarketQuote
	InstrumentUpdatedAt         time.Time
	Current, Previous           marketdata.Candle
	Regime                      regime.State
	Trend                       features.TrendFeatures
	Volatility, PriorVolatility features.VolatilityFeatures
	PriorPrice                  features.PriceFeatures
	PriorVolume                 features.VolumeFeatures
	TrendConfig                 features.TrendFeatureConfig
	VolatilityConfig            features.VolatilityFeatureConfig
	PriceConfig                 features.PriceFeatureConfig
	VolumeConfig                features.VolumeFeatureConfig
	RiskMarket                  risk.MarketContext
}

func ValidatePaperMarketQuery(q PaperMarketQuery) error {
	s := q.Scope
	if s.Exchange == "" || s.Exchange != strings.TrimSpace(s.Exchange) || s.Exchange != strings.ToLower(s.Exchange) ||
		(s.Category != "spot" && s.Category != "linear") || !signalSymbolPattern.MatchString(s.Symbol) || s.Interval != strings.TrimSpace(s.Interval) || q.AsOf.IsZero() {
		return fmt.Errorf("paper market requires canonical exchange/category/symbol/interval and observation time")
	}
	_, err := marketdata.IntervalDuration(s.Interval)
	return err
}

func ValidatePaperMarketPolicy(p PaperMarketPolicy) error {
	if p.MaxDataAge <= 0 || p.MaxInstrumentAge <= 0 || p.MaxInstrumentAge > 24*time.Hour || p.MaxSpreadBPS.IsNegative() || p.DepthBandBPS.IsNegative() || p.DepthBandBPS.GreaterThanOrEqual(decimal.NewFromInt(10000)) || p.ExpectedSlippageBPS.IsNegative() || p.ExpectedSlippageBPS.GreaterThan(p.DepthBandBPS) || len(p.AllowedSymbols) == 0 {
		return fmt.Errorf("invalid paper market freshness, metadata age or cost/depth policy")
	}
	seen := map[string]bool{}
	for _, s := range p.AllowedSymbols {
		if !signalSymbolPattern.MatchString(s) || seen[s] {
			return fmt.Errorf("paper market allowed symbols must be canonical and unique")
		}
		seen[s] = true
	}
	return nil
}

func BuildPaperMarketSnapshot(q PaperMarketQuery, p PaperMarketPolicy, f PaperMarketFacts) (PaperMarketSnapshot, error) {
	if err := ValidatePaperMarketQuery(q); err != nil {
		return PaperMarketSnapshot{}, err
	}
	if err := ValidatePaperMarketPolicy(p); err != nil {
		return PaperMarketSnapshot{}, err
	}
	if !slices.Contains(p.AllowedSymbols, q.Scope.Symbol) {
		return PaperMarketSnapshot{}, fmt.Errorf("paper market symbol is not configured")
	}
	q.AsOf = q.AsOf.UTC()
	p.AllowedSymbols = slices.Clone(p.AllowedSymbols)
	s := q.Scope
	duration, _ := marketdata.IntervalDuration(s.Interval)
	if len(f.Candles) != PaperMarketCandleCount {
		return PaperMarketSnapshot{}, fmt.Errorf("paper market requires exactly %d closed candles", PaperMarketCandleCount)
	}
	for i, c := range f.Candles {
		if err := validator.ValidateCandle(c); err != nil {
			return PaperMarketSnapshot{}, err
		}
		if c.Exchange != s.Exchange || c.Category != s.Category || c.Symbol != s.Symbol || c.Interval != s.Interval || !c.IsClosed || c.CloseTime.After(q.AsOf) || !c.OpenTime.Equal(c.OpenTime.UTC().Truncate(duration)) || !c.CloseTime.Equal(c.OpenTime.Add(duration)) || i > 0 && !f.Candles[i-1].CloseTime.Equal(c.OpenTime) {
			return PaperMarketSnapshot{}, fmt.Errorf("paper market candle %d has invalid scope, chronology or continuity", i)
		}
	}
	c := f.Candles[len(f.Candles)-1]
	if q.AsOf.Sub(c.CloseTime) > p.MaxDataAge {
		return PaperMarketSnapshot{}, fmt.Errorf("paper market closed candle is stale")
	}
	r := f.Regime
	if err := regime.ValidateState(r); err != nil {
		return PaperMarketSnapshot{}, err
	}
	if r.Exchange != s.Exchange || r.Category != s.Category || r.Symbol != s.Symbol || r.Interval != s.Interval || !r.OpenTime.Equal(c.OpenTime) || !r.CloseTime.Equal(c.CloseTime) || r.CalculatedAt.Before(r.CloseTime) || r.CalculatedAt.After(q.AsOf) {
		return PaperMarketSnapshot{}, fmt.Errorf("paper market regime is not a timely state for the latest candle")
	}
	instrument := f.Instrument
	if err := validator.ValidateInstrument(instrument); err != nil {
		return PaperMarketSnapshot{}, err
	}
	maxQty := decimal.Min(instrument.MaxOrderQty, instrument.MaxMarketOrderQty)
	if instrument.Exchange != s.Exchange || instrument.Category != s.Category || instrument.Symbol != s.Symbol || instrument.QuoteCoin != "USDT" || instrument.Symbol != instrument.BaseCoin+instrument.QuoteCoin || instrument.Status != "Trading" || instrument.UpdatedAt.After(q.AsOf) || q.AsOf.Sub(instrument.UpdatedAt) > p.MaxInstrumentAge || maxQty.LessThan(instrument.MinOrderQty) {
		return PaperMarketSnapshot{}, fmt.Errorf("paper market instrument is unavailable, stale or inconsistent")
	}
	quote, err := buildPaperMarketQuote(f.Book, q, p)
	if err != nil {
		return PaperMarketSnapshot{}, err
	}
	out := PaperMarketSnapshot{Version: PaperMarketVersion, Query: q, Policy: p, Quote: quote, InstrumentUpdatedAt: instrument.UpdatedAt, Current: c, Previous: f.Candles[len(f.Candles)-2], Regime: r,
		TrendConfig: features.DefaultTrendFeatureConfig(), VolatilityConfig: features.DefaultVolatilityFeatureConfig(), PriceConfig: features.PriceFeatureConfig{RollingWindow: 20}, VolumeConfig: features.DefaultVolumeFeatureConfig()}
	out.Regime.Reasons = slices.Clone(r.Reasons)
	trend, err := features.ComputeTrendFeatures(f.Candles, out.TrendConfig)
	if err != nil {
		return PaperMarketSnapshot{}, err
	}
	volatility, err := features.ComputeVolatilityFeatures(f.Candles, out.VolatilityConfig)
	if err != nil {
		return PaperMarketSnapshot{}, err
	}
	price, err := features.ComputePriceFeatures(f.Candles, out.PriceConfig)
	if err != nil {
		return PaperMarketSnapshot{}, err
	}
	volume, err := features.ComputeVolumeFeatures(f.Candles, out.VolumeConfig)
	if err != nil {
		return PaperMarketSnapshot{}, err
	}
	last, prior := len(f.Candles)-1, len(f.Candles)-2
	out.Trend, out.Volatility, out.PriorVolatility, out.PriorPrice, out.PriorVolume = trend[last], volatility[last], volatility[prior], price[prior], volume[prior]
	if !out.Trend.Complete || !out.Volatility.Complete || !out.PriorVolatility.Complete || !out.PriorPrice.Complete || !out.PriorVolume.Complete {
		return PaperMarketSnapshot{}, fmt.Errorf("paper market indicator warmup is incomplete")
	}
	dataTime := c.CloseTime
	if quote.ExchangeTime.Before(dataTime) {
		dataTime = quote.ExchangeTime
	}
	volatilityOK := r.Regime == regime.RegimeTrendUp || r.Regime == regime.RegimeTrendDown || r.Regime == regime.RegimeRange || r.Regime == regime.RegimeBreakoutSetup
	out.RiskMarket = risk.MarketContext{DataTime: dataTime, SymbolAllowed: true, SpreadBPS: quote.SpreadBPS, ExpectedSlippageBPS: p.ExpectedSlippageBPS, VolatilityAcceptable: volatilityOK && !r.NoTrade, OrderbookValid: true, LiquidityQuote: quote.LiquidityQuote,
		Instrument: risk.Instrument{Available: true, Symbol: s.Symbol, TickSize: instrument.TickSize, QuantityStep: instrument.QtyStep, MinOrderQuantity: instrument.MinOrderQty, MaxOrderQuantity: maxQty, MinNotional: instrument.MinNotionalValue}}
	// The fingerprint also rejects non-finite computed statistical features.
	out.SourceSHA256, err = research.ExperimentHash(struct {
		Snapshot PaperMarketSnapshot
		Facts    PaperMarketFacts
	}{out, f})
	if err != nil {
		return PaperMarketSnapshot{}, fmt.Errorf("fingerprint paper market sources/features: %w", err)
	}
	return out, nil
}

func buildPaperMarketQuote(b marketdata.OrderbookSnapshot, q PaperMarketQuery, p PaperMarketPolicy) (PaperMarketQuote, error) {
	if err := validator.ValidateOrderbookSnapshot(b); err != nil {
		return PaperMarketQuote{}, err
	}
	s := q.Scope
	if b.Exchange != s.Exchange || b.Category != s.Category || b.Symbol != s.Symbol || b.Depth > MaxPaperBookLevels || len(b.Bids) > b.Depth || len(b.Asks) > b.Depth || b.UpdateID <= 0 || b.Sequence <= 0 || b.ExchangeTime.After(q.AsOf) || b.CreatedAt.After(q.AsOf) || b.MatchingEngineTime.After(q.AsOf) || q.AsOf.Sub(b.ExchangeTime) > p.MaxDataAge {
		return PaperMarketQuote{}, fmt.Errorf("paper market book has invalid scope, age, depth or sequence")
	}
	if !b.BestBid.Equal(b.Bids[0].Price) || !b.BestAsk.Equal(b.Asks[0].Price) {
		return PaperMarketQuote{}, fmt.Errorf("paper market book top differs from its levels")
	}
	mid := b.BestBid.Add(b.BestAsk).Mul(decimal.RequireFromString("0.5"))
	spread := b.BestAsk.Sub(b.BestBid)
	bps := spread.Div(mid).Mul(decimal.NewFromInt(10000))
	if !b.Spread.Equal(spread) || !b.SpreadBPS.Equal(bps) || bps.GreaterThan(p.MaxSpreadBPS) {
		return PaperMarketQuote{}, fmt.Errorf("paper market spread is inconsistent or too wide")
	}
	band := p.DepthBandBPS.Shift(-4)
	low, high := mid.Mul(decimal.NewFromInt(1).Sub(band)), mid.Mul(decimal.NewFromInt(1).Add(band))
	liquidity := make([]decimal.Decimal, 2)
	for side, levels := range [][]marketdata.OrderbookLevel{b.Bids, b.Asks} {
		for i, level := range levels {
			if i > 0 && level.Price.Equal(levels[i-1].Price) {
				return PaperMarketQuote{}, fmt.Errorf("paper market book contains duplicate price levels")
			}
			if level.Price.GreaterThanOrEqual(low) && level.Price.LessThanOrEqual(high) {
				liquidity[side] = liquidity[side].Add(level.Price.Mul(level.Quantity))
			}
		}
	}
	return PaperMarketQuote{Bid: b.BestBid, Ask: b.BestAsk, Mid: mid, SpreadBPS: bps, LiquidityQuote: decimal.Min(liquidity[0], liquidity[1]), ExchangeTime: b.ExchangeTime, RecordedAt: b.CreatedAt, UpdateID: b.UpdateID, Sequence: b.Sequence}, nil
}

// StrategyContext supplies observations, not approval or an execution permit.
func (s PaperMarketSnapshot) StrategyContext(signalID string, runtime risk.RuntimeState, allowShort bool) (MarketContext, error) {
	if signalID == "" || signalID != strings.TrimSpace(signalID) || runtime.Mode != risk.ModePaper || s.Version != PaperMarketVersion || s.SourceSHA256 == "" || s.Query.AsOf.IsZero() {
		return MarketContext{}, fmt.Errorf("paper market context requires identified PAPER observation")
	}
	if allowShort && s.Query.Scope.Category == "spot" {
		return MarketContext{}, fmt.Errorf("paper market spot source cannot enable borrowed shorts")
	}
	r := s.Regime
	r.Reasons = slices.Clone(r.Reasons)
	// Observation is assembled after regime calculation. RiskMarket.DataTime
	// separately preserves the oldest input time for entry freshness checks.
	return MarketContext{SignalID: signalID, Now: s.Query.AsOf, ObservedAt: s.Query.AsOf, EntryPrice: s.Quote.Mid, Mode: risk.ModePaper, TradingEnabled: runtime.TradingEnabled, KillSwitchActive: runtime.KillSwitchActive, AllowShort: allowShort,
		Regime: r, Current: s.Current, Previous: s.Previous, Trend: s.Trend, Volatility: s.Volatility, PriorPrice: s.PriorPrice, PriorVolatility: s.PriorVolatility, PriorVolume: s.PriorVolume}, nil
}
