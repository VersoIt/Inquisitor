package realtime

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	"github.com/VersoIt/Inquisitor/internal/marketdata"
)

const snapshotType = "snapshot"

type Problem struct {
	Field   string
	Code    string
	Message string
}

type OrderbookValidationError struct {
	Problems []Problem
}

func (e OrderbookValidationError) Error() string {
	if len(e.Problems) == 0 {
		return "orderbook validation failed"
	}

	parts := make([]string, 0, len(e.Problems))
	for _, problem := range e.Problems {
		parts = append(parts, fmt.Sprintf("%s: %s", problem.Field, problem.Message))
	}
	return "orderbook validation failed: " + strings.Join(parts, "; ")
}

type Spread struct {
	Exchange     string
	Category     string
	Symbol       string
	BestBid      decimal.Decimal
	BestAsk      decimal.Decimal
	Spread       decimal.Decimal
	SpreadBPS    decimal.Decimal
	ExchangeTime time.Time
}

type FreshnessStatus struct {
	DataTime     time.Time
	ObservedAt   time.Time
	MaxStaleness time.Duration
	Age          time.Duration
	Stale        bool
}

type QualityPolicy struct {
	MaxStaleness time.Duration
	MaxSpreadBPS decimal.Decimal
}

type OrderbookAssessment struct {
	Exchange      string
	Category      string
	Symbol        string
	Valid         bool
	Freshness     FreshnessStatus
	Spread        Spread
	Stale         bool
	SpreadTooWide bool
	Issues        []string
}

func CheckFreshness(dataTime, observedAt time.Time, maxStaleness time.Duration) (FreshnessStatus, error) {
	if dataTime.IsZero() {
		return FreshnessStatus{}, fmt.Errorf("data_time is required")
	}
	if observedAt.IsZero() {
		return FreshnessStatus{}, fmt.Errorf("observed_at is required")
	}
	if maxStaleness <= 0 {
		return FreshnessStatus{}, fmt.Errorf("max_staleness must be positive")
	}

	dataTime = dataTime.UTC()
	observedAt = observedAt.UTC()
	age := observedAt.Sub(dataTime)
	if age < 0 {
		age = 0
	}

	return FreshnessStatus{
		DataTime:     dataTime,
		ObservedAt:   observedAt,
		MaxStaleness: maxStaleness,
		Age:          age,
		Stale:        age > maxStaleness,
	}, nil
}

func ValidateOrderbookSnapshot(book marketdata.Orderbook) error {
	var problems []Problem
	add := func(field, code, message string) {
		problems = append(problems, Problem{Field: field, Code: code, Message: message})
	}

	if strings.TrimSpace(book.Exchange) == "" {
		add("exchange", "required", "exchange is required")
	}
	if strings.TrimSpace(book.Category) == "" {
		add("category", "required", "category is required")
	}
	if strings.TrimSpace(book.Symbol) == "" {
		add("symbol", "required", "symbol is required")
	}
	if !strings.EqualFold(strings.TrimSpace(book.Type), snapshotType) {
		add("type", "snapshot_required", "orderbook spread requires a snapshot")
	}
	if len(book.Bids) == 0 {
		add("bids", "required", "at least one bid is required")
	}
	if len(book.Asks) == 0 {
		add("asks", "required", "at least one ask is required")
	}

	validateLevels(problemsAppender(add), "bids", book.Bids, true)
	validateLevels(problemsAppender(add), "asks", book.Asks, false)

	if len(book.Bids) > 0 && len(book.Asks) > 0 {
		bestBid := book.Bids[0].Price
		bestAsk := book.Asks[0].Price
		if bestBid.GreaterThanOrEqual(bestAsk) {
			add("spread", "crossed", "best bid must be lower than best ask")
		}
	}

	if len(problems) > 0 {
		return OrderbookValidationError{Problems: problems}
	}
	return nil
}

func CalculateOrderbookSpread(book marketdata.Orderbook) (Spread, error) {
	if err := ValidateOrderbookSnapshot(book); err != nil {
		return Spread{}, err
	}

	bestBid := book.Bids[0].Price
	bestAsk := book.Asks[0].Price
	spread := bestAsk.Sub(bestBid)
	mid := bestAsk.Add(bestBid).Div(decimal.NewFromInt(2))
	spreadBPS := spread.Div(mid).Mul(decimal.NewFromInt(10000))

	return Spread{
		Exchange:     book.Exchange,
		Category:     book.Category,
		Symbol:       book.Symbol,
		BestBid:      bestBid,
		BestAsk:      bestAsk,
		Spread:       spread,
		SpreadBPS:    spreadBPS,
		ExchangeTime: book.ExchangeTime.UTC(),
	}, nil
}

func AssessOrderbookSnapshot(book marketdata.Orderbook, observedAt time.Time, policy QualityPolicy) (OrderbookAssessment, []marketdata.DataQualityEvent, error) {
	if err := validatePolicy(policy); err != nil {
		return OrderbookAssessment{}, nil, err
	}

	assessment := OrderbookAssessment{
		Exchange: book.Exchange,
		Category: book.Category,
		Symbol:   book.Symbol,
	}

	if err := ValidateOrderbookSnapshot(book); err != nil {
		assessment.Issues = append(assessment.Issues, err.Error())
		event, marshalErr := newQualityEvent(book, observedAt, marketdata.DataQualityEventOrderbookInvalid, marketdata.DataQualitySeverityCritical, "invalid orderbook snapshot", map[string]string{
			"reason": err.Error(),
			"type":   book.Type,
		})
		if marshalErr != nil {
			return OrderbookAssessment{}, nil, marshalErr
		}
		return assessment, []marketdata.DataQualityEvent{event}, nil
	}
	assessment.Valid = true

	freshness, err := CheckFreshness(book.ExchangeTime, observedAt, policy.MaxStaleness)
	if err != nil {
		assessment.Issues = append(assessment.Issues, err.Error())
		event, marshalErr := newQualityEvent(book, observedAt, marketdata.DataQualityEventOrderbookInvalid, marketdata.DataQualitySeverityCritical, "orderbook timestamp is invalid", map[string]string{
			"reason": err.Error(),
		})
		if marshalErr != nil {
			return OrderbookAssessment{}, nil, marshalErr
		}
		return assessment, []marketdata.DataQualityEvent{event}, nil
	}
	assessment.Freshness = freshness
	assessment.Stale = freshness.Stale

	spread, err := CalculateOrderbookSpread(book)
	if err != nil {
		return OrderbookAssessment{}, nil, err
	}
	assessment.Spread = spread

	var events []marketdata.DataQualityEvent
	if freshness.Stale {
		event, err := newQualityEvent(book, observedAt, marketdata.DataQualityEventStaleData, marketdata.DataQualitySeverityWarning, "stale orderbook snapshot", map[string]string{
			"exchange_time":     freshness.DataTime.Format(time.RFC3339Nano),
			"observed_at":       freshness.ObservedAt.Format(time.RFC3339Nano),
			"age_ms":            fmt.Sprintf("%d", freshness.Age.Milliseconds()),
			"max_staleness_ms":  fmt.Sprintf("%d", freshness.MaxStaleness.Milliseconds()),
			"matching_time_utc": book.MatchingEngineTime.UTC().Format(time.RFC3339Nano),
		})
		if err != nil {
			return OrderbookAssessment{}, nil, err
		}
		events = append(events, event)
	}

	if !policy.MaxSpreadBPS.IsZero() && spread.SpreadBPS.GreaterThan(policy.MaxSpreadBPS) {
		assessment.SpreadTooWide = true
		event, err := newQualityEvent(book, observedAt, marketdata.DataQualityEventSpreadTooWide, marketdata.DataQualitySeverityWarning, "orderbook spread is wider than policy", map[string]string{
			"best_bid":       spread.BestBid.String(),
			"best_ask":       spread.BestAsk.String(),
			"spread":         spread.Spread.String(),
			"spread_bps":     spread.SpreadBPS.String(),
			"max_spread_bps": policy.MaxSpreadBPS.String(),
		})
		if err != nil {
			return OrderbookAssessment{}, nil, err
		}
		events = append(events, event)
	}

	return assessment, events, nil
}

type problemsAppender func(field, code, message string)

func validateLevels(add problemsAppender, field string, levels []marketdata.OrderbookLevel, descending bool) {
	for i, level := range levels {
		fieldName := fmt.Sprintf("%s[%d]", field, i)
		if level.Price.LessThanOrEqual(decimal.Zero) {
			add(fieldName+".price", "must_be_positive", "price must be greater than zero")
		}
		if level.Quantity.LessThanOrEqual(decimal.Zero) {
			add(fieldName+".quantity", "must_be_positive", "quantity must be greater than zero")
		}
		if i == 0 {
			continue
		}

		previous := levels[i-1].Price
		switch {
		case descending && level.Price.GreaterThan(previous):
			add(fieldName+".price", "not_sorted", "bids must be sorted from highest to lowest price")
		case !descending && level.Price.LessThan(previous):
			add(fieldName+".price", "not_sorted", "asks must be sorted from lowest to highest price")
		}
	}
}

func validatePolicy(policy QualityPolicy) error {
	if policy.MaxStaleness <= 0 {
		return fmt.Errorf("max_staleness must be positive")
	}
	if policy.MaxSpreadBPS.LessThan(decimal.Zero) {
		return fmt.Errorf("max_spread_bps must be greater than or equal to zero")
	}
	return nil
}

func newQualityEvent(book marketdata.Orderbook, observedAt time.Time, eventType, severity, message string, data map[string]string) (marketdata.DataQualityEvent, error) {
	if observedAt.IsZero() {
		observedAt = time.Now().UTC()
	}
	payload, err := json.Marshal(data)
	if err != nil {
		return marketdata.DataQualityEvent{}, fmt.Errorf("marshal realtime quality event payload: %w", err)
	}

	return marketdata.DataQualityEvent{
		Exchange:  book.Exchange,
		Symbol:    book.Symbol,
		EventType: eventType,
		Severity:  severity,
		Message:   message,
		DataJSON:  payload,
		CreatedAt: observedAt.UTC(),
	}, nil
}
