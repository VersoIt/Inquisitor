package strategy_test

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"

	"github.com/VersoIt/Inquisitor/internal/regime"
	"github.com/VersoIt/Inquisitor/internal/risk"
	"github.com/VersoIt/Inquisitor/internal/strategy"
	"github.com/VersoIt/Inquisitor/internal/testutil"
	"github.com/shopspring/decimal"
)

func TestPaperMarketSourceDerivesCausalInputsAndConservativeDepth(t *testing.T) {
	q, p, f := testutil.PaperMarket()
	before, _ := json.Marshal(f)
	got, err := strategy.BuildPaperMarketSnapshot(q, p, f)
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != strategy.PaperMarketVersion || len(got.SourceSHA256) != 64 || !got.Quote.Mid.Equal(decimal.NewFromInt(100)) || !got.Quote.LiquidityQuote.Equal(decimal.NewFromInt(199980)) || !got.RiskMarket.Instrument.MaxOrderQuantity.Equal(decimal.NewFromInt(50)) || !got.RiskMarket.DataTime.Equal(got.Current.CloseTime) {
		t.Fatalf("bad source snapshot %+v", got)
	}
	if !got.PriorPrice.CloseTime.Equal(got.Previous.CloseTime) || !got.PriorVolatility.CloseTime.Equal(got.Previous.CloseTime) || !got.PriorVolume.CloseTime.Equal(got.Previous.CloseTime) || !got.Trend.CloseTime.Equal(got.Current.CloseTime) {
		t.Fatal("future/current baseline leaked into prior features")
	}
	context, err := got.StrategyContext("source_signal", risk.RuntimeState{Mode: risk.ModePaper, TradingEnabled: true}, false)
	if err != nil || context.Approval.Enabled || context.Approval.ApprovalID != "" || !context.EntryPrice.Equal(got.Quote.Mid) || !context.ObservedAt.Equal(q.AsOf) || !got.RiskMarket.DataTime.Before(context.ObservedAt) {
		t.Fatalf("context invented authorization %+v %v", context, err)
	}
	after, _ := json.Marshal(f)
	if !bytes.Equal(before, after) {
		t.Fatal("source inputs mutated")
	}
	again, err := strategy.BuildPaperMarketSnapshot(q, p, f)
	if err != nil || again.SourceSHA256 != got.SourceSHA256 {
		t.Fatal("non-deterministic source fingerprint")
	}
	f.Regime.Reasons[0] = "mutated"
	p.AllowedSymbols[0] = "ETHUSDT"
	if got.Regime.Reasons[0] == "mutated" || got.Policy.AllowedSymbols[0] != "BTCUSDT" {
		t.Fatal("snapshot aliases caller metadata")
	}
}

func TestPaperMarketRejectsCorruptStaleFutureOrMixedSources(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*strategy.PaperMarketQuery, *strategy.PaperMarketPolicy, *strategy.PaperMarketFacts)
	}{
		{"history truncated", func(_ *strategy.PaperMarketQuery, _ *strategy.PaperMarketPolicy, f *strategy.PaperMarketFacts) {
			f.Candles = f.Candles[1:]
		}},
		{"future candle", func(_ *strategy.PaperMarketQuery, _ *strategy.PaperMarketPolicy, f *strategy.PaperMarketFacts) {
			f.Candles[255].CloseTime = f.Candles[255].CloseTime.Add(time.Minute)
		}},
		{"unclosed candle", func(_ *strategy.PaperMarketQuery, _ *strategy.PaperMarketPolicy, f *strategy.PaperMarketFacts) {
			f.Candles[255].IsClosed = false
		}},
		{"mixed candle scope", func(_ *strategy.PaperMarketQuery, _ *strategy.PaperMarketPolicy, f *strategy.PaperMarketFacts) {
			f.Candles[15].Symbol = "ETHUSDT"
		}},
		{"gap", func(_ *strategy.PaperMarketQuery, _ *strategy.PaperMarketPolicy, f *strategy.PaperMarketFacts) {
			f.Candles[10] = f.Candles[9]
		}},
		{"stale candle not refreshed by calculation", func(q *strategy.PaperMarketQuery, _ *strategy.PaperMarketPolicy, f *strategy.PaperMarketFacts) {
			q.AsOf = q.AsOf.Add(3 * time.Second)
			f.Regime.CalculatedAt = q.AsOf
			f.Book.ExchangeTime = q.AsOf
			f.Book.CreatedAt = q.AsOf
		}},
		{"old regime", func(_ *strategy.PaperMarketQuery, _ *strategy.PaperMarketPolicy, f *strategy.PaperMarketFacts) {
			f.Regime.OpenTime = f.Regime.OpenTime.Add(-time.Minute)
			f.Regime.CloseTime = f.Regime.CloseTime.Add(-time.Minute)
		}},
		{"future regime", func(q *strategy.PaperMarketQuery, _ *strategy.PaperMarketPolicy, f *strategy.PaperMarketFacts) {
			f.Regime.CalculatedAt = q.AsOf.Add(time.Microsecond)
		}},
		{"regime before candle closes", func(_ *strategy.PaperMarketQuery, _ *strategy.PaperMarketPolicy, f *strategy.PaperMarketFacts) {
			f.Regime.CalculatedAt = f.Regime.CloseTime.Add(-time.Microsecond)
		}},
		{"foreign regime", func(_ *strategy.PaperMarketQuery, _ *strategy.PaperMarketPolicy, f *strategy.PaperMarketFacts) {
			f.Regime.Category = "spot"
		}},
		{"stale book", func(q *strategy.PaperMarketQuery, _ *strategy.PaperMarketPolicy, f *strategy.PaperMarketFacts) {
			f.Book.ExchangeTime = q.AsOf.Add(-4 * time.Second)
		}},
		{"future book", func(q *strategy.PaperMarketQuery, _ *strategy.PaperMarketPolicy, f *strategy.PaperMarketFacts) {
			f.Book.ExchangeTime = q.AsOf.Add(time.Microsecond)
		}},
		{"future recording", func(q *strategy.PaperMarketQuery, _ *strategy.PaperMarketPolicy, f *strategy.PaperMarketFacts) {
			f.Book.CreatedAt = q.AsOf.Add(time.Microsecond)
		}},
		{"future engine timestamp", func(q *strategy.PaperMarketQuery, _ *strategy.PaperMarketPolicy, f *strategy.PaperMarketFacts) {
			f.Book.MatchingEngineTime = q.AsOf.Add(time.Microsecond)
		}},
		{"foreign book", func(_ *strategy.PaperMarketQuery, _ *strategy.PaperMarketPolicy, f *strategy.PaperMarketFacts) {
			f.Book.Symbol = "ETHUSDT"
		}},
		{"zero sequence", func(_ *strategy.PaperMarketQuery, _ *strategy.PaperMarketPolicy, f *strategy.PaperMarketFacts) {
			f.Book.Sequence = 0
		}},
		{"oversize depth", func(_ *strategy.PaperMarketQuery, _ *strategy.PaperMarketPolicy, f *strategy.PaperMarketFacts) {
			f.Book.Depth = strategy.MaxPaperBookLevels + 1
		}},
		{"more levels than declared", func(_ *strategy.PaperMarketQuery, _ *strategy.PaperMarketPolicy, f *strategy.PaperMarketFacts) {
			f.Book.Depth = 1
		}},
		{"duplicate level", func(_ *strategy.PaperMarketQuery, _ *strategy.PaperMarketPolicy, f *strategy.PaperMarketFacts) {
			f.Book.Bids[1] = f.Book.Bids[0]
		}},
		{"top mismatch", func(_ *strategy.PaperMarketQuery, _ *strategy.PaperMarketPolicy, f *strategy.PaperMarketFacts) {
			f.Book.BestBid = decimal.NewFromInt(99)
		}},
		{"forged spread", func(_ *strategy.PaperMarketQuery, _ *strategy.PaperMarketPolicy, f *strategy.PaperMarketFacts) {
			f.Book.SpreadBPS = decimal.Zero
		}},
		{"crossed book", func(_ *strategy.PaperMarketQuery, _ *strategy.PaperMarketPolicy, f *strategy.PaperMarketFacts) {
			f.Book.BestAsk = f.Book.BestBid
		}},
		{"stale instrument", func(q *strategy.PaperMarketQuery, _ *strategy.PaperMarketPolicy, f *strategy.PaperMarketFacts) {
			f.Instrument.UpdatedAt = q.AsOf.Add(-25 * time.Hour)
		}},
		{"future instrument", func(q *strategy.PaperMarketQuery, _ *strategy.PaperMarketPolicy, f *strategy.PaperMarketFacts) {
			f.Instrument.UpdatedAt = q.AsOf.Add(time.Microsecond)
		}},
		{"instrument stopped", func(_ *strategy.PaperMarketQuery, _ *strategy.PaperMarketPolicy, f *strategy.PaperMarketFacts) {
			f.Instrument.Status = "Settled"
		}},
		{"non-USDT quote", func(_ *strategy.PaperMarketQuery, _ *strategy.PaperMarketPolicy, f *strategy.PaperMarketFacts) {
			f.Instrument.QuoteCoin = "USDC"
		}},
		{"market maximum below minimum", func(_ *strategy.PaperMarketQuery, _ *strategy.PaperMarketPolicy, f *strategy.PaperMarketFacts) {
			f.Instrument.MaxMarketOrderQty = decimal.RequireFromString("0.0001")
		}},
		{"unconfigured symbol", func(_ *strategy.PaperMarketQuery, p *strategy.PaperMarketPolicy, _ *strategy.PaperMarketFacts) {
			p.AllowedSymbols = []string{"ETHUSDT"}
		}},
		{"loosened metadata age", func(_ *strategy.PaperMarketQuery, p *strategy.PaperMarketPolicy, _ *strategy.PaperMarketFacts) {
			p.MaxInstrumentAge = 25 * time.Hour
		}},
		{"slippage outside band", func(_ *strategy.PaperMarketQuery, p *strategy.PaperMarketPolicy, _ *strategy.PaperMarketFacts) {
			p.ExpectedSlippageBPS = decimal.NewFromInt(11)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q, p, f := testutil.PaperMarket()
			tc.mutate(&q, &p, &f)
			if got, err := strategy.BuildPaperMarketSnapshot(q, p, f); err == nil || got.SourceSHA256 != "" {
				t.Fatalf("unsafe source returned %+v %v", got, err)
			}
		})
	}
}

func TestPaperMarketBoundariesAndAbstentionDoNotInventTradePermission(t *testing.T) {
	for _, past := range []bool{false, true} {
		q, p, f := testutil.PaperMarket()
		q.AsOf = f.Candles[255].CloseTime.Add(p.MaxDataAge)
		f.Book.ExchangeTime = q.AsOf.Add(-p.MaxDataAge)
		f.Instrument.UpdatedAt = q.AsOf.Add(-p.MaxInstrumentAge)
		if past {
			q.AsOf = q.AsOf.Add(time.Microsecond)
		}
		got, err := strategy.BuildPaperMarketSnapshot(q, p, f)
		if (err != nil) != past {
			t.Fatalf("age boundary past=%t err=%v", past, err)
		}
		if !past && !got.RiskMarket.DataTime.Equal(f.Book.ExchangeTime) {
			t.Fatal("data timestamp was refreshed")
		}
	}
	q, p, f := testutil.PaperMarket()
	f.Regime.Regime = regime.RegimeNoTrade
	f.Regime.NoTrade = true
	got, err := strategy.BuildPaperMarketSnapshot(q, p, f)
	if err != nil || got.RiskMarket.VolatilityAcceptable {
		t.Fatal("NO_TRADE state became risk-eligible", err)
	}
	for _, mode := range []risk.Mode{risk.ModeLive, ""} {
		if _, err := got.StrategyContext("signal", risk.RuntimeState{Mode: mode}, false); err == nil {
			t.Fatal("non-PAPER context accepted")
		}
	}
	got.Query.Scope.Category = "spot"
	if _, err := got.StrategyContext("signal", risk.RuntimeState{Mode: risk.ModePaper}, true); err == nil {
		t.Fatal("spot source enabled shorts")
	}
}

func TestPaperMarketDepthIsBandLimitedAndTwoSided(t *testing.T) {
	q, p, f := testutil.PaperMarket()
	p.DepthBandBPS = decimal.NewFromInt(500)
	got, err := strategy.BuildPaperMarketSnapshot(q, p, f)
	if err != nil {
		t.Fatal(err)
	}
	want := decimal.NewFromInt(95_000_000 + 199980)
	if !got.Quote.LiquidityQuote.Equal(want) {
		t.Fatalf("inclusive band or weaker-side depth wrong: %s", got.Quote.LiquidityQuote)
	}
	p.DepthBandBPS = decimal.Zero
	p.ExpectedSlippageBPS = decimal.Zero
	got, err = strategy.BuildPaperMarketSnapshot(q, p, f)
	if err != nil || !got.RiskMarket.LiquidityQuote.IsZero() {
		t.Fatal("empty band invented liquidity", err)
	}
}

func TestPaperMarketCurrentCandleCannotChangePriorBaselines(t *testing.T) {
	q, p, f := testutil.PaperMarket()
	before, err := strategy.BuildPaperMarketSnapshot(q, p, f)
	if err != nil {
		t.Fatal(err)
	}
	f.Candles[255].High = decimal.NewFromInt(105)
	f.Candles[255].Low = decimal.NewFromInt(95)
	f.Candles[255].Volume = decimal.NewFromInt(9999)
	after, err := strategy.BuildPaperMarketSnapshot(q, p, f)
	if err != nil {
		t.Fatal(err)
	}
	oldPrior, _ := json.Marshal([]any{before.PriorPrice, before.PriorVolatility, before.PriorVolume})
	newPrior, _ := json.Marshal([]any{after.PriorPrice, after.PriorVolatility, after.PriorVolume})
	if !bytes.Equal(oldPrior, newPrior) || before.SourceSHA256 == after.SourceSHA256 || before.Volatility.ATR.Equal(after.Volatility.ATR) {
		t.Fatal("current candle leaked into prior features or source fingerprint missed changed inputs")
	}
}

func TestPaperMarketSpreadThresholdAndOldestInputTime(t *testing.T) {
	for _, tc := range []struct {
		name      string
		maxSpread string
		bookAge   time.Duration
		wantError bool
	}{
		{"inclusive spread", "2", time.Second, false},
		{"spread exceeded", "1.99999999", time.Second, true},
		{"book older than candle", "2", 2 * time.Second, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q, p, f := testutil.PaperMarket()
			p.MaxSpreadBPS = decimal.RequireFromString(tc.maxSpread)
			f.Book.ExchangeTime = q.AsOf.Add(-tc.bookAge)
			got, err := strategy.BuildPaperMarketSnapshot(q, p, f)
			if (err != nil) != tc.wantError {
				t.Fatalf("spread boundary err=%v", err)
			}
			if err == nil && !got.RiskMarket.DataTime.Equal(f.Book.ExchangeTime) {
				t.Fatal("oldest observed input time lost")
			}
		})
	}
}
