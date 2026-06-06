package marketdata

import (
	"context"
	"errors"
	"time"
)

var ErrInstrumentNotFound = errors.New("instrument not found")

type CandleRepository interface {
	UpsertCandles(ctx context.Context, candles []Candle) (WriteStats, error)
	ListCandles(ctx context.Context, query CandleQuery) ([]Candle, error)
}

type InstrumentRepository interface {
	UpsertInstruments(ctx context.Context, instruments []Instrument) (WriteStats, error)
	GetInstrument(ctx context.Context, key InstrumentKey) (Instrument, error)
	ListInstruments(ctx context.Context, query InstrumentQuery) ([]Instrument, error)
}

type DataQualityEventRepository interface {
	CreateDataQualityEvents(ctx context.Context, events []DataQualityEvent) (WriteStats, error)
	ListDataQualityEvents(ctx context.Context, query DataQualityEventQuery) ([]DataQualityEvent, error)
}

type WriteStats struct {
	Inserted int
	Updated  int
}

func (s WriteStats) Total() int {
	return s.Inserted + s.Updated
}

type InstrumentKey struct {
	Exchange string
	Category string
	Symbol   string
}

type InstrumentQuery struct {
	Exchange string
	Category string
	Status   string
	Limit    int
}

type CandleQuery struct {
	Exchange string
	Category string
	Symbol   string
	Interval string
	Start    time.Time
	End      time.Time
	Limit    int
}

type DataQualityEventQuery struct {
	Exchange  string
	Symbol    string
	Interval  string
	EventType string
	Severity  string
	Start     time.Time
	End       time.Time
	Limit     int
}
