package live

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

type ExchangePositionStatus string

const (
	ExchangePositionStatusNormal ExchangePositionStatus = "NORMAL"
	ExchangePositionStatusLiq    ExchangePositionStatus = "LIQ"
	ExchangePositionStatusAdl    ExchangePositionStatus = "ADL"
)

type PositionSnapshotQuery struct {
	Exchange string
	Category string
	Symbol   string
}

type PositionSnapshot struct {
	Exchange              string
	Category              string
	Symbol                string
	Open                  bool
	Side                  OrderSide
	Size                  decimal.Decimal
	AveragePrice          decimal.Decimal
	PositionValue         decimal.Decimal
	MarkPrice             decimal.Decimal
	LiquidationPrice      decimal.Decimal
	Leverage              decimal.Decimal
	UnrealisedPnL         decimal.Decimal
	CurrentRealisedPnL    decimal.Decimal
	CumulativeRealisedPnL decimal.Decimal
	ExchangeStatus        ExchangePositionStatus
	PositionIndex         int
	Sequence              int64
	ExchangeReduceOnly    bool
	ExchangeCreatedAt     time.Time
	ExchangeUpdatedAt     time.Time
	ObservedAt            time.Time
}

type PositionSnapshotInput struct {
	Exchange              string
	Category              string
	Symbol                string
	Side                  OrderSide
	Size                  decimal.Decimal
	AveragePrice          decimal.Decimal
	PositionValue         decimal.Decimal
	MarkPrice             decimal.Decimal
	LiquidationPrice      decimal.Decimal
	Leverage              decimal.Decimal
	UnrealisedPnL         decimal.Decimal
	CurrentRealisedPnL    decimal.Decimal
	CumulativeRealisedPnL decimal.Decimal
	ExchangeStatus        ExchangePositionStatus
	PositionIndex         int
	Sequence              int64
	ExchangeReduceOnly    bool
	ExchangeCreatedAt     time.Time
	ExchangeUpdatedAt     time.Time
	ObservedAt            time.Time
}

type PositionSnapshotStats struct {
	Inserted int
	Skipped  int
}

type PositionSnapshotReader interface {
	GetPositionSnapshot(ctx context.Context, query PositionSnapshotQuery) (PositionSnapshot, error)
}

type PositionSnapshotJournal interface {
	RecordPositionSnapshot(ctx context.Context, snapshot PositionSnapshot) (PositionSnapshotStats, error)
}

func (s PositionSnapshotStats) Total() int {
	return s.Inserted + s.Skipped
}

func NewPositionSnapshot(input PositionSnapshotInput) (PositionSnapshot, error) {
	size := input.Size
	snapshot := PositionSnapshot{
		Exchange:              strings.ToLower(strings.TrimSpace(input.Exchange)),
		Category:              strings.ToLower(strings.TrimSpace(input.Category)),
		Symbol:                strings.ToUpper(strings.TrimSpace(input.Symbol)),
		Open:                  size.IsPositive(),
		Side:                  OrderSide(strings.ToUpper(strings.TrimSpace(string(input.Side)))),
		Size:                  size,
		AveragePrice:          input.AveragePrice,
		PositionValue:         input.PositionValue,
		MarkPrice:             input.MarkPrice,
		LiquidationPrice:      input.LiquidationPrice,
		Leverage:              input.Leverage,
		UnrealisedPnL:         input.UnrealisedPnL,
		CurrentRealisedPnL:    input.CurrentRealisedPnL,
		CumulativeRealisedPnL: input.CumulativeRealisedPnL,
		ExchangeStatus:        normalizeExchangePositionStatus(input.ExchangeStatus),
		PositionIndex:         input.PositionIndex,
		Sequence:              input.Sequence,
		ExchangeReduceOnly:    input.ExchangeReduceOnly,
		ExchangeCreatedAt:     input.ExchangeCreatedAt.UTC(),
		ExchangeUpdatedAt:     input.ExchangeUpdatedAt.UTC(),
		ObservedAt:            input.ObservedAt.UTC(),
	}
	if err := ValidatePositionSnapshot(snapshot); err != nil {
		return PositionSnapshot{}, err
	}
	return snapshot, nil
}

func ValidatePositionSnapshotQuery(query PositionSnapshotQuery) error {
	var problems []string
	if strings.TrimSpace(query.Exchange) == "" {
		problems = append(problems, "exchange is required")
	}
	if strings.TrimSpace(query.Exchange) != "" && query.Exchange != strings.ToLower(strings.TrimSpace(query.Exchange)) {
		problems = append(problems, "exchange must be lowercase and trimmed")
	}
	if strings.TrimSpace(query.Category) == "" {
		problems = append(problems, "category is required")
	}
	if strings.TrimSpace(query.Category) != "" && query.Category != strings.ToLower(strings.TrimSpace(query.Category)) {
		problems = append(problems, "category must be lowercase and trimmed")
	}
	if strings.TrimSpace(query.Symbol) == "" {
		problems = append(problems, "symbol is required")
	}
	if strings.TrimSpace(query.Symbol) != "" && query.Symbol != strings.ToUpper(strings.TrimSpace(query.Symbol)) {
		problems = append(problems, "symbol must be uppercase and trimmed")
	}
	if len(problems) > 0 {
		return errors.New("live position snapshot query validation failed: " + strings.Join(problems, "; "))
	}
	return nil
}

func ValidatePositionSnapshot(snapshot PositionSnapshot) error {
	var problems []string
	if err := ValidatePositionSnapshotQuery(PositionSnapshotQuery{
		Exchange: snapshot.Exchange,
		Category: snapshot.Category,
		Symbol:   snapshot.Symbol,
	}); err != nil {
		problems = append(problems, err.Error())
	}
	expectedOpen := snapshot.Size.IsPositive()
	if snapshot.Open != expectedOpen {
		problems = append(problems, "open must match positive size")
	}
	if snapshot.Size.IsNegative() {
		problems = append(problems, "size must be non-negative")
	}
	if snapshot.Open {
		if !KnownOrderSide(snapshot.Side) {
			problems = append(problems, "side must be LONG or SHORT for open position")
		}
		if !KnownExchangePositionStatus(snapshot.ExchangeStatus) {
			problems = append(problems, "exchange_status is unknown")
		}
		if snapshot.AveragePrice.LessThanOrEqual(decimal.Zero) {
			problems = append(problems, "average_price must be positive for open position")
		}
		if snapshot.PositionValue.LessThanOrEqual(decimal.Zero) {
			problems = append(problems, "position_value must be positive for open position")
		}
		if snapshot.ExchangeCreatedAt.IsZero() {
			problems = append(problems, "exchange_created_at is required for open position")
		}
		if snapshot.ExchangeUpdatedAt.IsZero() {
			problems = append(problems, "exchange_updated_at is required for open position")
		}
	} else {
		if strings.TrimSpace(string(snapshot.Side)) != "" {
			problems = append(problems, "side must be empty for flat position")
		}
		if snapshot.ExchangeStatus != "" && !KnownExchangePositionStatus(snapshot.ExchangeStatus) {
			problems = append(problems, "exchange_status is unknown")
		}
	}

	nonNegative := []struct {
		name  string
		value decimal.Decimal
	}{
		{"mark_price", snapshot.MarkPrice},
		{"liquidation_price", snapshot.LiquidationPrice},
		{"leverage", snapshot.Leverage},
	}
	for _, item := range nonNegative {
		if item.value.IsNegative() {
			problems = append(problems, item.name+" must be non-negative")
		}
	}
	if snapshot.PositionIndex < 0 {
		problems = append(problems, "position_index must be non-negative")
	}
	if !snapshot.ExchangeCreatedAt.IsZero() && !snapshot.ExchangeUpdatedAt.IsZero() && snapshot.ExchangeUpdatedAt.Before(snapshot.ExchangeCreatedAt) {
		problems = append(problems, "exchange_updated_at must not be before exchange_created_at")
	}
	if snapshot.ObservedAt.IsZero() {
		problems = append(problems, "observed_at is required")
	}
	if len(problems) > 0 {
		return errors.New("live position snapshot validation failed: " + strings.Join(problems, "; "))
	}
	return nil
}

func KnownExchangePositionStatus(status ExchangePositionStatus) bool {
	switch status {
	case ExchangePositionStatusNormal,
		ExchangePositionStatusLiq,
		ExchangePositionStatusAdl:
		return true
	default:
		return false
	}
}

func normalizeExchangePositionStatus(status ExchangePositionStatus) ExchangePositionStatus {
	normalized := strings.ToUpper(strings.TrimSpace(string(status)))
	switch normalized {
	case "LIQUIDATING":
		return ExchangePositionStatusLiq
	default:
		return ExchangePositionStatus(normalized)
	}
}

func ValidatePositionSnapshots(snapshots []PositionSnapshot) error {
	for index, snapshot := range snapshots {
		if err := ValidatePositionSnapshot(snapshot); err != nil {
			return fmt.Errorf("live_position_snapshot[%d]: %w", index, err)
		}
	}
	return nil
}
