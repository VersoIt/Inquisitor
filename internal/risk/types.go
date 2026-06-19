package risk

import (
	"time"

	"github.com/shopspring/decimal"
)

type Mode string

const (
	ModePaper Mode = "PAPER"
	ModeLive  Mode = "LIVE"
)

type Side string

const (
	SideLong  Side = "LONG"
	SideShort Side = "SHORT"
)

type Policy struct {
	AllowedMode              Mode
	AllowShort               bool
	RiskPerTradePct          decimal.Decimal
	MaxDailyLossPct          decimal.Decimal
	MaxWeeklyLossPct         decimal.Decimal
	MaxTotalDrawdownPct      decimal.Decimal
	MaxLosingStreak          int
	MaxOpenPositions         int
	MaxLeverage              decimal.Decimal
	MaxSpreadBPS             decimal.Decimal
	MaxSlippageBPS           decimal.Decimal
	MinConfidence            int
	MinLiquidityQuote        decimal.Decimal
	MaxPortfolioExposurePct  decimal.Decimal
	MaxCorrelatedExposurePct decimal.Decimal
	MaxDataAge               time.Duration
	AllowedSymbols           []string
}

type TradeIntent struct {
	IntentID                string
	HypothesisID            string
	StrategyName            string
	Symbol                  string
	Side                    Side
	Confidence              int
	EntryPrice              decimal.Decimal
	StopLoss                decimal.Decimal
	TakeProfit              decimal.Decimal
	Leverage                decimal.Decimal
	HypothesisApproved      bool
	HypothesisMaxRiskPct    decimal.Decimal
	HypothesisMinConfidence int
	Reason                  string
	CreatedAt               time.Time
}

type AccountState struct {
	Equity             decimal.Decimal
	DayStartEquity     decimal.Decimal
	WeekStartEquity    decimal.Decimal
	PeakEquity         decimal.Decimal
	DailyLoss          decimal.Decimal
	WeeklyLoss         decimal.Decimal
	LosingStreak       int
	OpenPositions      int
	TotalExposure      decimal.Decimal
	CorrelatedExposure decimal.Decimal
}

type Instrument struct {
	Available        bool
	Symbol           string
	TickSize         decimal.Decimal
	QuantityStep     decimal.Decimal
	MinOrderQuantity decimal.Decimal
	MaxOrderQuantity decimal.Decimal
	MinNotional      decimal.Decimal
}

type MarketContext struct {
	DataTime             time.Time
	SymbolAllowed        bool
	SpreadBPS            decimal.Decimal
	ExpectedSlippageBPS  decimal.Decimal
	VolatilityAcceptable bool
	OrderbookValid       bool
	LiquidityQuote       decimal.Decimal
	Instrument           Instrument
}

type RuntimeState struct {
	TradingEnabled   bool
	Mode             Mode
	KillSwitchActive bool
}

type EvaluationInput struct {
	Intent      TradeIntent
	Account     AccountState
	Market      MarketContext
	Runtime     RuntimeState
	EvaluatedAt time.Time
}

type Check struct {
	Name   string `json:"name"`
	Passed bool   `json:"passed"`
	Reason string `json:"reason,omitempty"`
}

type Decision struct {
	IntentID      string
	Approved      bool
	FinalQuantity decimal.Decimal
	MaxLoss       decimal.Decimal
	StopLoss      decimal.Decimal
	TakeProfit    decimal.Decimal
	Reason        string
	Checks        []Check
	CreatedAt     time.Time
}
