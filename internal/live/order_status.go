package live

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

type ExchangeOrderStatus string

const (
	ExchangeOrderStatusNew                      ExchangeOrderStatus = "NEW"
	ExchangeOrderStatusPartiallyFilled          ExchangeOrderStatus = "PARTIALLY_FILLED"
	ExchangeOrderStatusUntriggered              ExchangeOrderStatus = "UNTRIGGERED"
	ExchangeOrderStatusRejected                 ExchangeOrderStatus = "REJECTED"
	ExchangeOrderStatusPartiallyFilledCancelled ExchangeOrderStatus = "PARTIALLY_FILLED_CANCELLED"
	ExchangeOrderStatusFilled                   ExchangeOrderStatus = "FILLED"
	ExchangeOrderStatusCancelled                ExchangeOrderStatus = "CANCELLED"
	ExchangeOrderStatusTriggered                ExchangeOrderStatus = "TRIGGERED"
	ExchangeOrderStatusDeactivated              ExchangeOrderStatus = "DEACTIVATED"
)

type OrderStatusQuery struct {
	Exchange      string
	Category      string
	Symbol        string
	ClientOrderID string
}

type OrderStatusSnapshot struct {
	ClientOrderID              string
	ExchangeOrderID            string
	Exchange                   string
	Category                   string
	Symbol                     string
	Side                       OrderSide
	Type                       OrderType
	TimeInForce                TimeInForce
	ExchangeStatus             ExchangeOrderStatus
	RejectReason               string
	Quantity                   decimal.Decimal
	Price                      decimal.Decimal
	AveragePrice               decimal.Decimal
	LeavesQuantity             decimal.Decimal
	CumulativeExecutedQuantity decimal.Decimal
	CumulativeExecutedValue    decimal.Decimal
	CumulativeFee              decimal.Decimal
	ReduceOnly                 bool
	ExchangeCreatedAt          time.Time
	ExchangeUpdatedAt          time.Time
	ObservedAt                 time.Time
}

type OrderStatusSnapshotInput struct {
	ClientOrderID              string
	ExchangeOrderID            string
	Exchange                   string
	Category                   string
	Symbol                     string
	Side                       OrderSide
	Type                       OrderType
	TimeInForce                TimeInForce
	ExchangeStatus             ExchangeOrderStatus
	RejectReason               string
	Quantity                   decimal.Decimal
	Price                      decimal.Decimal
	AveragePrice               decimal.Decimal
	LeavesQuantity             decimal.Decimal
	CumulativeExecutedQuantity decimal.Decimal
	CumulativeExecutedValue    decimal.Decimal
	CumulativeFee              decimal.Decimal
	ReduceOnly                 bool
	ExchangeCreatedAt          time.Time
	ExchangeUpdatedAt          time.Time
	ObservedAt                 time.Time
}

type OrderStatusSnapshotStats struct {
	Inserted int
	Skipped  int
}

type OrderStatusReader interface {
	GetOrderStatus(ctx context.Context, query OrderStatusQuery) (OrderStatusSnapshot, error)
}

type OrderStatusJournal interface {
	RecordOrderStatusSnapshot(ctx context.Context, snapshot OrderStatusSnapshot) (OrderStatusSnapshotStats, error)
}

func (s OrderStatusSnapshotStats) Total() int {
	return s.Inserted + s.Skipped
}

func NewOrderStatusSnapshot(input OrderStatusSnapshotInput) (OrderStatusSnapshot, error) {
	snapshot := OrderStatusSnapshot{
		ClientOrderID:              strings.TrimSpace(input.ClientOrderID),
		ExchangeOrderID:            strings.TrimSpace(input.ExchangeOrderID),
		Exchange:                   strings.ToLower(strings.TrimSpace(input.Exchange)),
		Category:                   strings.ToLower(strings.TrimSpace(input.Category)),
		Symbol:                     strings.ToUpper(strings.TrimSpace(input.Symbol)),
		Side:                       OrderSide(strings.ToUpper(strings.TrimSpace(string(input.Side)))),
		Type:                       OrderType(strings.ToUpper(strings.TrimSpace(string(input.Type)))),
		TimeInForce:                normalizeTimeInForce(input.TimeInForce),
		ExchangeStatus:             normalizeExchangeOrderStatus(input.ExchangeStatus),
		RejectReason:               strings.TrimSpace(input.RejectReason),
		Quantity:                   input.Quantity,
		Price:                      input.Price,
		AveragePrice:               input.AveragePrice,
		LeavesQuantity:             input.LeavesQuantity,
		CumulativeExecutedQuantity: input.CumulativeExecutedQuantity,
		CumulativeExecutedValue:    input.CumulativeExecutedValue,
		CumulativeFee:              input.CumulativeFee,
		ReduceOnly:                 input.ReduceOnly,
		ExchangeCreatedAt:          input.ExchangeCreatedAt.UTC(),
		ExchangeUpdatedAt:          input.ExchangeUpdatedAt.UTC(),
		ObservedAt:                 input.ObservedAt.UTC(),
	}
	if err := ValidateOrderStatusSnapshot(snapshot); err != nil {
		return OrderStatusSnapshot{}, err
	}
	return snapshot, nil
}

func ValidateOrderStatusQuery(query OrderStatusQuery) error {
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
	if strings.TrimSpace(query.ClientOrderID) == "" {
		problems = append(problems, "client_order_id is required")
	}
	if query.ClientOrderID != strings.TrimSpace(query.ClientOrderID) {
		problems = append(problems, "client_order_id must be trimmed")
	}
	if len(problems) > 0 {
		return errors.New("live order status query validation failed: " + strings.Join(problems, "; "))
	}
	return nil
}

func ValidateOrderStatusSnapshot(snapshot OrderStatusSnapshot) error {
	var problems []string
	if strings.TrimSpace(snapshot.ClientOrderID) == "" {
		problems = append(problems, "client_order_id is required")
	}
	if strings.TrimSpace(snapshot.ExchangeOrderID) == "" {
		problems = append(problems, "exchange_order_id is required")
	}
	if err := ValidateOrderStatusQuery(OrderStatusQuery{
		Exchange:      snapshot.Exchange,
		Category:      snapshot.Category,
		Symbol:        snapshot.Symbol,
		ClientOrderID: snapshot.ClientOrderID,
	}); err != nil {
		problems = append(problems, err.Error())
	}
	if !KnownOrderSide(snapshot.Side) {
		problems = append(problems, "side must be LONG or SHORT")
	}
	if !KnownOrderType(snapshot.Type) {
		problems = append(problems, "type must be MARKET or LIMIT")
	}
	if !KnownTimeInForce(snapshot.TimeInForce) {
		problems = append(problems, "time_in_force must be GTC, IOC, FOK, or POST_ONLY")
	}
	if !KnownExchangeOrderStatus(snapshot.ExchangeStatus) {
		problems = append(problems, "exchange_status is unknown")
	}
	if snapshot.Quantity.LessThanOrEqual(decimal.Zero) {
		problems = append(problems, "quantity must be positive")
	}
	nonNegative := []struct {
		name  string
		value decimal.Decimal
	}{
		{"price", snapshot.Price},
		{"average_price", snapshot.AveragePrice},
		{"leaves_quantity", snapshot.LeavesQuantity},
		{"cumulative_executed_quantity", snapshot.CumulativeExecutedQuantity},
		{"cumulative_executed_value", snapshot.CumulativeExecutedValue},
		{"cumulative_fee", snapshot.CumulativeFee},
	}
	for _, item := range nonNegative {
		if item.value.IsNegative() {
			problems = append(problems, item.name+" must be non-negative")
		}
	}
	if snapshot.LeavesQuantity.GreaterThan(snapshot.Quantity) {
		problems = append(problems, "leaves_quantity must not exceed quantity")
	}
	if snapshot.CumulativeExecutedQuantity.GreaterThan(snapshot.Quantity) {
		problems = append(problems, "cumulative_executed_quantity must not exceed quantity")
	}
	if snapshot.ExchangeCreatedAt.IsZero() {
		problems = append(problems, "exchange_created_at is required")
	}
	if snapshot.ExchangeUpdatedAt.IsZero() {
		problems = append(problems, "exchange_updated_at is required")
	}
	if snapshot.ObservedAt.IsZero() {
		problems = append(problems, "observed_at is required")
	}
	if !snapshot.ExchangeCreatedAt.IsZero() && !snapshot.ExchangeUpdatedAt.IsZero() && snapshot.ExchangeUpdatedAt.Before(snapshot.ExchangeCreatedAt) {
		problems = append(problems, "exchange_updated_at must not be before exchange_created_at")
	}
	if len(problems) > 0 {
		return errors.New("live order status snapshot validation failed: " + strings.Join(problems, "; "))
	}
	return nil
}

func KnownExchangeOrderStatus(status ExchangeOrderStatus) bool {
	switch status {
	case ExchangeOrderStatusNew,
		ExchangeOrderStatusPartiallyFilled,
		ExchangeOrderStatusUntriggered,
		ExchangeOrderStatusRejected,
		ExchangeOrderStatusPartiallyFilledCancelled,
		ExchangeOrderStatusFilled,
		ExchangeOrderStatusCancelled,
		ExchangeOrderStatusTriggered,
		ExchangeOrderStatusDeactivated:
		return true
	default:
		return false
	}
}

func normalizeExchangeOrderStatus(status ExchangeOrderStatus) ExchangeOrderStatus {
	normalized := strings.ToUpper(strings.TrimSpace(string(status)))
	normalized = strings.ReplaceAll(normalized, "-", "_")
	normalized = strings.ReplaceAll(normalized, " ", "_")
	switch normalized {
	case "PARTIALLYFILLED":
		return ExchangeOrderStatusPartiallyFilled
	case "PARTIALLYFILLEDCANCELLED", "PARTIALLYFILLEDCANCELED":
		return ExchangeOrderStatusPartiallyFilledCancelled
	default:
		return ExchangeOrderStatus(normalized)
	}
}

func ValidateOrderStatusSnapshots(snapshots []OrderStatusSnapshot) error {
	for index, snapshot := range snapshots {
		if err := ValidateOrderStatusSnapshot(snapshot); err != nil {
			return fmt.Errorf("live_order_status_snapshot[%d]: %w", index, err)
		}
	}
	return nil
}
