package exchanges

import (
	"context"
	"time"

	"github.com/VersoIt/Inquisitor/internal/marketdata"
)

type MarketDataClient interface {
	GetServerTime(ctx context.Context) (time.Time, error)
	GetInstrumentsInfo(ctx context.Context, req InstrumentsInfoRequest) ([]marketdata.Instrument, error)
	GetKlines(ctx context.Context, req KlinesRequest) ([]marketdata.Candle, error)
}

type InstrumentsInfoRequest struct {
	Category string
	Symbol   string
}

type KlinesRequest struct {
	Category string
	Symbol   string
	Interval string
	Start    time.Time
	End      time.Time
	Limit    int
}
