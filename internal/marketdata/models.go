package marketdata

import (
	"time"

	"github.com/shopspring/decimal"
)

const (
	DataQualityEventCandleGap        = "CANDLE_GAP"
	DataQualityEventOrderbookInvalid = "ORDERBOOK_INVALID"
	DataQualityEventSpreadTooWide    = "SPREAD_TOO_WIDE"
	DataQualityEventStaleData        = "STALE_DATA"

	DataQualitySeverityInfo     = "info"
	DataQualitySeverityWarning  = "warning"
	DataQualitySeverityCritical = "critical"
)

type Instrument struct {
	Exchange           string
	Category           string
	Symbol             string
	BaseCoin           string
	QuoteCoin          string
	Status             string
	TickSize           decimal.Decimal
	QtyStep            decimal.Decimal
	MinOrderQty        decimal.Decimal
	MaxOrderQty        decimal.Decimal
	MaxMarketOrderQty  decimal.Decimal
	MinNotionalValue   decimal.Decimal
	PriceScale         int
	LeverageFilterJSON []byte
	PriceFilterJSON    []byte
	LotSizeFilterJSON  []byte
	RawJSON            []byte
	UpdatedAt          time.Time
}

type Candle struct {
	Exchange  string
	Category  string
	Symbol    string
	Interval  string
	OpenTime  time.Time
	CloseTime time.Time
	Open      decimal.Decimal
	High      decimal.Decimal
	Low       decimal.Decimal
	Close     decimal.Decimal
	Volume    decimal.Decimal
	Turnover  decimal.Decimal
	IsClosed  bool
}

type Ticker struct {
	Exchange     string
	Category     string
	Symbol       string
	LastPrice    decimal.Decimal
	Bid1Price    decimal.Decimal
	Bid1Size     decimal.Decimal
	Ask1Price    decimal.Decimal
	Ask1Size     decimal.Decimal
	MarkPrice    decimal.Decimal
	IndexPrice   decimal.Decimal
	OpenInterest decimal.Decimal
	FundingRate  decimal.Decimal
	ExchangeTime time.Time
}

type PublicTrade struct {
	Exchange     string
	Category     string
	Symbol       string
	TradeID      string
	Side         string
	Price        decimal.Decimal
	Quantity     decimal.Decimal
	TradeTime    time.Time
	IsBlockTrade bool
	Sequence     int64
}

type PublicTradeQuery struct {
	Exchange string
	Category string
	Symbol   string
	Start    time.Time
	End      time.Time
	Limit    int
}

type OrderbookLevel struct {
	Price    decimal.Decimal
	Quantity decimal.Decimal
}

type Orderbook struct {
	Exchange           string
	Category           string
	Symbol             string
	Type               string
	Bids               []OrderbookLevel
	Asks               []OrderbookLevel
	UpdateID           int64
	Sequence           int64
	ExchangeTime       time.Time
	MatchingEngineTime time.Time
}

type OrderbookSnapshot struct {
	Exchange           string
	Category           string
	Symbol             string
	Depth              int
	Bids               []OrderbookLevel
	Asks               []OrderbookLevel
	BestBid            decimal.Decimal
	BestAsk            decimal.Decimal
	Spread             decimal.Decimal
	SpreadBPS          decimal.Decimal
	UpdateID           int64
	Sequence           int64
	ExchangeTime       time.Time
	MatchingEngineTime time.Time
	CreatedAt          time.Time
}

type OrderbookSnapshotQuery struct {
	Exchange string
	Category string
	Symbol   string
	Start    time.Time
	End      time.Time
	Limit    int
}

type DataQualityEvent struct {
	Exchange  string
	Symbol    string
	Interval  string
	EventType string
	Severity  string
	Message   string
	DataJSON  []byte
	CreatedAt time.Time
}
