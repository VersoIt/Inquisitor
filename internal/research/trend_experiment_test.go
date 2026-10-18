package research_test

import (
	"testing"
	"time"

	"github.com/VersoIt/Inquisitor/internal/backtest"
	"github.com/VersoIt/Inquisitor/internal/marketdata"
	"github.com/VersoIt/Inquisitor/internal/research"
	"github.com/shopspring/decimal"
)

func experimentPlan() research.TrendExperimentPlan {
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	costs, _ := backtest.NewCostModel(10, 10, 5, 3, 1.5)
	return research.TrendExperimentPlan{ID: "test-experiment", SchemaVersion: 1, Exchange: "bybit", Category: "spot", Symbol: "BTCUSDT", Interval: "60", DataStart: start, DevelopmentStart: start.Add(24 * time.Hour), ValidationStart: start.Add(48 * time.Hour), ValidationEnd: start.Add(72 * time.Hour),
		InitialEquity: decimal.NewFromInt(1000), RiskPerTradePct: decimal.RequireFromString("0.25"), QuantityStep: decimal.RequireFromString("0.000001"), MinNotional: decimal.NewFromInt(5), MinADX: decimal.NewFromInt(25), StopATRMultiple: decimal.NewFromInt(2), TakeProfitR: decimal.NewFromInt(2), MaxHoldingCandles: 48, Costs: costs,
		Criteria: research.TrendExperimentCriteria{MinValidationTrades: 100, MinBlockTrades: 20, MinProfitableBlocks: 3, MinProfitFactor: decimal.RequireFromString("1.15"), MaxMarkedDrawdownPct: decimal.NewFromInt(15)}}
}

func TestTrendExperimentPlanRejectsUnsafeOrAmbiguousInputs(t *testing.T) {
	if err := research.ValidateTrendExperimentPlan(experimentPlan()); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name   string
		change func(*research.TrendExperimentPlan)
	}{
		{"missing identity", func(p *research.TrendExperimentPlan) { p.ID = "" }},
		{"wrong schema", func(p *research.TrendExperimentPlan) { p.SchemaVersion++ }},
		{"perpetual funding not modeled", func(p *research.TrendExperimentPlan) { p.Category = "linear" }},
		{"wrong market", func(p *research.TrendExperimentPlan) { p.Symbol = "ETHUSDT" }},
		{"overlapping windows", func(p *research.TrendExperimentPlan) { p.ValidationStart = p.DevelopmentStart }},
		{"unaligned boundary", func(p *research.TrendExperimentPlan) { p.ValidationEnd = p.ValidationEnd.Add(time.Second) }},
		{"local boundary", func(p *research.TrendExperimentPlan) { p.DataStart = p.DataStart.In(time.FixedZone("local", 3600)) }},
		{"excessive risk", func(p *research.TrendExperimentPlan) { p.RiskPerTradePct = decimal.NewFromInt(2) }},
		{"zero quantity step", func(p *research.TrendExperimentPlan) { p.QuantityStep = decimal.Zero }},
		{"free commissions", func(p *research.TrendExperimentPlan) { p.Costs.TakerFeeBPS = decimal.Zero }},
		{"free slippage", func(p *research.TrendExperimentPlan) { p.Costs.SlippageBPS = decimal.Zero }},
		{"weakened sample", func(p *research.TrendExperimentPlan) { p.Criteria.MinValidationTrades = 5 }},
		{"weakened drawdown", func(p *research.TrendExperimentPlan) { p.Criteria.MaxMarkedDrawdownPct = decimal.NewFromInt(50) }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p := experimentPlan()
			tt.change(&p)
			if err := research.ValidateTrendExperimentPlan(p); err == nil {
				t.Fatal("unsafe plan accepted")
			}
		})
	}
}

func TestExperimentHistoryCannotSilentlyDropOrMixCandles(t *testing.T) {
	p := experimentPlan()
	makeCandles := func() []marketdata.Candle {
		var rows []marketdata.Candle
		for at := p.DataStart; at.Before(p.ValidationEnd); at = at.Add(time.Hour) {
			rows = append(rows, marketdata.Candle{Exchange: p.Exchange, Category: p.Category, Symbol: p.Symbol, Interval: p.Interval, OpenTime: at, CloseTime: at.Add(time.Hour), Open: decimal.NewFromInt(100), High: decimal.NewFromInt(105), Low: decimal.NewFromInt(95), Close: decimal.NewFromInt(101), IsClosed: true})
		}
		return rows
	}
	if err := research.ValidateExperimentCandles(p, makeCandles()); err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"missing", "duplicate", "unclosed", "different market", "bad prices", "wrong interval"} {
		t.Run(scenario, func(t *testing.T) {
			rows := makeCandles()
			switch scenario {
			case "missing":
				rows = rows[1:]
			case "duplicate":
				rows[4] = rows[3]
			case "unclosed":
				rows[4].IsClosed = false
			case "different market":
				rows[4].Category = "linear"
			case "bad prices":
				rows[4].High = decimal.NewFromInt(90)
			case "wrong interval":
				rows[4].Interval = "15"
			}
			if err := research.ValidateExperimentCandles(p, rows); err == nil {
				t.Fatal("invalid history accepted")
			}
		})
	}
}

func TestLongExperimentExitConservativeOrdering(t *testing.T) {
	at := experimentPlan().DataStart
	for _, tt := range []struct {
		name, open, high, low string
		held                  int
		end                   time.Time
		reason, price         string
	}{
		{"both touched stop first", "100", "125", "85", 1, at.Add(2 * time.Hour), "stop", "90"},
		{"gap below stop", "80", "105", "75", 1, at.Add(2 * time.Hour), "stop_gap", "80"},
		{"no favorable gap windfall", "130", "135", "125", 1, at.Add(2 * time.Hour), "target_gap_capped", "120"},
		{"target only", "100", "125", "95", 1, at.Add(2 * time.Hour), "target", "120"},
		{"holding horizon", "100", "105", "95", 48, at.Add(2 * time.Hour), "time_limit", "101"},
		{"window boundary", "100", "105", "95", 1, at.Add(time.Hour), "window_end", "101"},
		{"still holding", "100", "105", "95", 1, at.Add(2 * time.Hour), "", "0"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			c := marketdata.Candle{OpenTime: at, CloseTime: at.Add(time.Hour), Open: decimal.RequireFromString(tt.open), High: decimal.RequireFromString(tt.high), Low: decimal.RequireFromString(tt.low), Close: decimal.NewFromInt(101)}
			got := research.LongExperimentExit(c, decimal.NewFromInt(90), decimal.NewFromInt(120), tt.held, 48, tt.end)
			if got.Reason != tt.reason || !got.MidPrice.Equal(decimal.RequireFromString(tt.price)) || got.Triggered != (tt.reason != "") {
				t.Fatalf("exit %+v", got)
			}
		})
	}
}

func TestSpotExperimentSizingIncludesBothLegsAndNeverBorrows(t *testing.T) {
	p := experimentPlan()
	for _, tt := range []struct{ equity, entry, stop, step string }{{"1000", "100", "96", "0.000001"}, {"1000", "100", "99.99", "0.000001"}, {"100", "40000", "39000", "0.000001"}, {"1000", "100", "96", "0.1"}} {
		t.Run(tt.equity+"/"+tt.entry+"/"+tt.step, func(t *testing.T) {
			p.QuantityStep = decimal.RequireFromString(tt.step)
			equity, entry, stop := decimal.RequireFromString(tt.equity), decimal.RequireFromString(tt.entry), decimal.RequireFromString(tt.stop)
			q, err := research.SpotExperimentQuantity(p, equity, entry, stop, p.Costs)
			if err != nil {
				t.Fatal(err)
			}
			if !q.IsPositive() {
				t.Fatal("expected a sized position")
			}
			trip, err := backtest.EvaluateRoundTrip(backtest.RoundTripInput{Direction: backtest.DirectionLong, EntryTime: p.DevelopmentStart, ExitTime: p.DevelopmentStart.Add(time.Hour), EntryMidPrice: entry, ExitMidPrice: stop, Quantity: q, Costs: p.Costs})
			if err != nil {
				t.Fatal(err)
			}
			if trip.NetPnL.Neg().GreaterThan(equity.Mul(p.RiskPerTradePct).Div(decimal.NewFromInt(100))) || trip.Entry.Notional.Add(trip.Entry.Fee).GreaterThan(equity) || !q.Mod(p.QuantityStep).IsZero() {
				t.Fatalf("unsafe position q=%s trip=%+v", q, trip)
			}
		})
	}
	p.MinNotional = decimal.NewFromInt(100000)
	if q, err := research.SpotExperimentQuantity(p, p.InitialEquity, decimal.NewFromInt(100), decimal.NewFromInt(96), p.Costs); err != nil || !q.IsZero() {
		t.Fatal("below-minimum order accepted")
	}
}
