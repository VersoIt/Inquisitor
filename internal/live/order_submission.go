package live

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

type RiskMode string

const (
	RiskModeLive RiskMode = "LIVE"
)

type OrderSide string

const (
	OrderSideLong  OrderSide = "LONG"
	OrderSideShort OrderSide = "SHORT"
)

type OrderType string

const (
	OrderTypeMarket OrderType = "MARKET"
	OrderTypeLimit  OrderType = "LIMIT"
)

type TimeInForce string

const (
	TimeInForceGTC      TimeInForce = "GTC"
	TimeInForceIOC      TimeInForce = "IOC"
	TimeInForceFOK      TimeInForce = "FOK"
	TimeInForcePostOnly TimeInForce = "POST_ONLY"
)

type OrderStatus string

const (
	OrderStatusAccepted OrderStatus = "ACCEPTED"
	OrderStatusRejected OrderStatus = "REJECTED"
)

type OrderSubmission struct {
	SubmissionID     string
	ClientOrderID    string
	DecisionID       string
	DecisionApproved bool
	IntentID         string
	RiskMode         RiskMode
	Exchange         string
	Category         string
	Symbol           string
	Side             OrderSide
	Type             OrderType
	TimeInForce      TimeInForce
	ReduceOnly       bool
	Quantity         decimal.Decimal
	ReferencePrice   decimal.Decimal
	LimitPrice       decimal.Decimal
	StopLoss         decimal.Decimal
	TakeProfit       decimal.Decimal
	Leverage         decimal.Decimal
	MaxLoss          decimal.Decimal
	Notional         decimal.Decimal
	Confidence       int
	Reason           string
	CreatedAt        time.Time
}

type OrderSubmissionInput struct {
	SubmissionID     string
	ClientOrderID    string
	DecisionID       string
	DecisionApproved bool
	IntentID         string
	RiskMode         RiskMode
	Exchange         string
	Category         string
	Symbol           string
	Side             OrderSide
	Type             OrderType
	TimeInForce      TimeInForce
	ReduceOnly       bool
	Quantity         decimal.Decimal
	ReferencePrice   decimal.Decimal
	LimitPrice       decimal.Decimal
	StopLoss         decimal.Decimal
	TakeProfit       decimal.Decimal
	Leverage         decimal.Decimal
	MaxLoss          decimal.Decimal
	Confidence       int
	Reason           string
	CreatedAt        time.Time
}

type OrderAcknowledgement struct {
	SubmissionID    string
	ClientOrderID   string
	Exchange        string
	ExchangeOrderID string
	Status          OrderStatus
	RejectReason    string
	ReceivedAt      time.Time
}

type OrderAcknowledgementInput struct {
	SubmissionID    string
	ClientOrderID   string
	Exchange        string
	ExchangeOrderID string
	Status          OrderStatus
	RejectReason    string
	ReceivedAt      time.Time
}

type OrderSubmissionStats struct {
	Inserted int
	Skipped  int
}

type OrderAcknowledgementStats struct {
	Inserted int
	Skipped  int
}

type OrderExecutor interface {
	SubmitOrder(ctx context.Context, submission OrderSubmission) (OrderAcknowledgement, error)
}

type OrderJournal interface {
	RecordOrderSubmission(ctx context.Context, submission OrderSubmission) (OrderSubmissionStats, error)
	RecordOrderAcknowledgement(ctx context.Context, acknowledgement OrderAcknowledgement) (OrderAcknowledgementStats, error)
}

func NewOrderSubmission(input OrderSubmissionInput) (OrderSubmission, error) {
	submission := OrderSubmission{
		SubmissionID:     strings.TrimSpace(input.SubmissionID),
		ClientOrderID:    strings.TrimSpace(input.ClientOrderID),
		DecisionID:       strings.TrimSpace(input.DecisionID),
		DecisionApproved: input.DecisionApproved,
		IntentID:         strings.TrimSpace(input.IntentID),
		RiskMode:         RiskMode(strings.ToUpper(strings.TrimSpace(string(input.RiskMode)))),
		Exchange:         strings.ToLower(strings.TrimSpace(input.Exchange)),
		Category:         strings.ToLower(strings.TrimSpace(input.Category)),
		Symbol:           strings.ToUpper(strings.TrimSpace(input.Symbol)),
		Side:             OrderSide(strings.ToUpper(strings.TrimSpace(string(input.Side)))),
		Type:             OrderType(strings.ToUpper(strings.TrimSpace(string(input.Type)))),
		TimeInForce:      normalizeTimeInForce(input.TimeInForce),
		ReduceOnly:       input.ReduceOnly,
		Quantity:         input.Quantity,
		ReferencePrice:   input.ReferencePrice,
		LimitPrice:       input.LimitPrice,
		StopLoss:         input.StopLoss,
		TakeProfit:       input.TakeProfit,
		Leverage:         input.Leverage,
		MaxLoss:          input.MaxLoss,
		Notional:         input.ReferencePrice.Mul(input.Quantity),
		Confidence:       input.Confidence,
		Reason:           strings.TrimSpace(input.Reason),
		CreatedAt:        input.CreatedAt.UTC(),
	}
	if err := ValidateOrderSubmission(submission); err != nil {
		return OrderSubmission{}, err
	}
	return submission, nil
}

func NewOrderAcknowledgement(input OrderAcknowledgementInput) (OrderAcknowledgement, error) {
	acknowledgement := OrderAcknowledgement{
		SubmissionID:    strings.TrimSpace(input.SubmissionID),
		ClientOrderID:   strings.TrimSpace(input.ClientOrderID),
		Exchange:        strings.ToLower(strings.TrimSpace(input.Exchange)),
		ExchangeOrderID: strings.TrimSpace(input.ExchangeOrderID),
		Status:          OrderStatus(strings.ToUpper(strings.TrimSpace(string(input.Status)))),
		RejectReason:    strings.TrimSpace(input.RejectReason),
		ReceivedAt:      input.ReceivedAt.UTC(),
	}
	if err := ValidateOrderAcknowledgement(acknowledgement); err != nil {
		return OrderAcknowledgement{}, err
	}
	return acknowledgement, nil
}

func (s OrderSubmissionStats) Total() int {
	return s.Inserted + s.Skipped
}

func (s OrderAcknowledgementStats) Total() int {
	return s.Inserted + s.Skipped
}

func ValidateOrderSubmission(submission OrderSubmission) error {
	var problems []string
	addRequired := func(field, value string) {
		if strings.TrimSpace(value) == "" {
			problems = append(problems, field+" is required")
		}
	}

	addRequired("submission_id", submission.SubmissionID)
	addRequired("client_order_id", submission.ClientOrderID)
	addRequired("decision_id", submission.DecisionID)
	addRequired("intent_id", submission.IntentID)
	addRequired("exchange", submission.Exchange)
	addRequired("category", submission.Category)
	addRequired("symbol", submission.Symbol)
	if submission.SubmissionID != strings.TrimSpace(submission.SubmissionID) {
		problems = append(problems, "submission_id must be trimmed")
	}
	if submission.ClientOrderID != strings.TrimSpace(submission.ClientOrderID) {
		problems = append(problems, "client_order_id must be trimmed")
	}
	if submission.DecisionID != strings.TrimSpace(submission.DecisionID) {
		problems = append(problems, "decision_id must be trimmed")
	}
	if submission.IntentID != strings.TrimSpace(submission.IntentID) {
		problems = append(problems, "intent_id must be trimmed")
	}
	if submission.RiskMode != RiskModeLive {
		problems = append(problems, "risk_mode must be LIVE")
	}
	if !submission.DecisionApproved {
		problems = append(problems, "decision_approved must be true")
	}
	if strings.TrimSpace(submission.Exchange) != "" && submission.Exchange != strings.ToLower(strings.TrimSpace(submission.Exchange)) {
		problems = append(problems, "exchange must be lowercase and trimmed")
	}
	if strings.TrimSpace(submission.Category) != "" && submission.Category != strings.ToLower(strings.TrimSpace(submission.Category)) {
		problems = append(problems, "category must be lowercase and trimmed")
	}
	if strings.TrimSpace(submission.Symbol) != "" && submission.Symbol != strings.ToUpper(strings.TrimSpace(submission.Symbol)) {
		problems = append(problems, "symbol must be uppercase and trimmed")
	}
	if !KnownOrderSide(submission.Side) {
		problems = append(problems, "side must be LONG or SHORT")
	}
	if !KnownOrderType(submission.Type) {
		problems = append(problems, "type must be MARKET or LIMIT")
	}
	if !KnownTimeInForce(submission.TimeInForce) {
		problems = append(problems, "time_in_force must be GTC, IOC, FOK, or POST_ONLY")
	}
	problems = append(problems, validateExecutionInstructions(submission)...)
	problems = append(problems, validateRiskSnapshot(submission)...)
	if submission.CreatedAt.IsZero() {
		problems = append(problems, "created_at is required")
	}

	if len(problems) > 0 {
		return errors.New("live order submission validation failed: " + strings.Join(problems, "; "))
	}
	return nil
}

func ValidateOrderAcknowledgement(acknowledgement OrderAcknowledgement) error {
	var problems []string
	addRequired := func(field, value string) {
		if strings.TrimSpace(value) == "" {
			problems = append(problems, field+" is required")
		}
	}

	addRequired("submission_id", acknowledgement.SubmissionID)
	addRequired("client_order_id", acknowledgement.ClientOrderID)
	addRequired("exchange", acknowledgement.Exchange)
	if acknowledgement.SubmissionID != strings.TrimSpace(acknowledgement.SubmissionID) {
		problems = append(problems, "submission_id must be trimmed")
	}
	if acknowledgement.ClientOrderID != strings.TrimSpace(acknowledgement.ClientOrderID) {
		problems = append(problems, "client_order_id must be trimmed")
	}
	if strings.TrimSpace(acknowledgement.Exchange) != "" && acknowledgement.Exchange != strings.ToLower(strings.TrimSpace(acknowledgement.Exchange)) {
		problems = append(problems, "exchange must be lowercase and trimmed")
	}
	if !KnownOrderStatus(acknowledgement.Status) {
		problems = append(problems, "status must be ACCEPTED or REJECTED")
	}
	switch acknowledgement.Status {
	case OrderStatusAccepted:
		if strings.TrimSpace(acknowledgement.ExchangeOrderID) == "" {
			problems = append(problems, "accepted order requires exchange_order_id")
		}
		if strings.TrimSpace(acknowledgement.RejectReason) != "" {
			problems = append(problems, "accepted order must not include reject_reason")
		}
	case OrderStatusRejected:
		if strings.TrimSpace(acknowledgement.ExchangeOrderID) != "" {
			problems = append(problems, "rejected order must not include exchange_order_id")
		}
		if strings.TrimSpace(acknowledgement.RejectReason) == "" {
			problems = append(problems, "rejected order requires reject_reason")
		}
		if acknowledgement.RejectReason != strings.TrimSpace(acknowledgement.RejectReason) {
			problems = append(problems, "reject_reason must be trimmed")
		}
	}
	if acknowledgement.ReceivedAt.IsZero() {
		problems = append(problems, "received_at is required")
	}

	if len(problems) > 0 {
		return errors.New("live order acknowledgement validation failed: " + strings.Join(problems, "; "))
	}
	return nil
}

func KnownOrderSide(side OrderSide) bool {
	return side == OrderSideLong || side == OrderSideShort
}

func KnownOrderType(orderType OrderType) bool {
	return orderType == OrderTypeMarket || orderType == OrderTypeLimit
}

func KnownTimeInForce(timeInForce TimeInForce) bool {
	return timeInForce == TimeInForceGTC ||
		timeInForce == TimeInForceIOC ||
		timeInForce == TimeInForceFOK ||
		timeInForce == TimeInForcePostOnly
}

func KnownOrderStatus(status OrderStatus) bool {
	return status == OrderStatusAccepted || status == OrderStatusRejected
}

func validateExecutionInstructions(submission OrderSubmission) []string {
	var problems []string
	if submission.Quantity.LessThanOrEqual(decimal.Zero) {
		problems = append(problems, "quantity must be positive")
	}
	if submission.ReferencePrice.LessThanOrEqual(decimal.Zero) {
		problems = append(problems, "reference_price must be positive")
	}
	if submission.Notional.LessThanOrEqual(decimal.Zero) {
		problems = append(problems, "notional must be positive")
	}
	expectedNotional := submission.ReferencePrice.Mul(submission.Quantity)
	if !submission.Notional.Equal(expectedNotional) {
		problems = append(problems, "notional must equal reference_price times quantity")
	}
	switch submission.Type {
	case OrderTypeMarket:
		if !submission.LimitPrice.IsZero() {
			problems = append(problems, "market order must not include limit_price")
		}
		if submission.TimeInForce != TimeInForceIOC && submission.TimeInForce != TimeInForceFOK {
			problems = append(problems, "market order time_in_force must be IOC or FOK")
		}
	case OrderTypeLimit:
		if submission.LimitPrice.LessThanOrEqual(decimal.Zero) {
			problems = append(problems, "limit order requires positive limit_price")
		}
	}
	if submission.Type == OrderTypeLimit && submission.TimeInForce == TimeInForcePostOnly && submission.ReduceOnly {
		problems = append(problems, "reduce_only order must not be post-only")
	}
	if submission.Leverage.LessThanOrEqual(decimal.Zero) {
		problems = append(problems, "leverage must be positive")
	}
	return problems
}

func validateRiskSnapshot(submission OrderSubmission) []string {
	var problems []string
	if submission.Confidence < 0 || submission.Confidence > 100 {
		problems = append(problems, "confidence must be between zero and 100")
	}
	if strings.TrimSpace(submission.Reason) == "" {
		problems = append(problems, "reason is required")
	}
	if submission.Reason != strings.TrimSpace(submission.Reason) {
		problems = append(problems, "reason must be trimmed")
	}
	if submission.ReduceOnly {
		if !submission.StopLoss.IsZero() {
			problems = append(problems, "reduce_only order must not include stop_loss")
		}
		if !submission.TakeProfit.IsZero() {
			problems = append(problems, "reduce_only order must not include take_profit")
		}
		if !submission.MaxLoss.IsZero() {
			problems = append(problems, "reduce_only order must not include max_loss")
		}
		return problems
	}
	if submission.StopLoss.LessThanOrEqual(decimal.Zero) {
		problems = append(problems, "stop_loss must be positive")
	}
	if submission.TakeProfit.IsNegative() {
		problems = append(problems, "take_profit must be greater than or equal to zero")
	}
	if submission.MaxLoss.LessThanOrEqual(decimal.Zero) {
		problems = append(problems, "max_loss must be positive")
	}
	if !KnownOrderSide(submission.Side) || !submission.ReferencePrice.GreaterThan(decimal.Zero) || !submission.StopLoss.GreaterThan(decimal.Zero) {
		return problems
	}
	switch submission.Side {
	case OrderSideLong:
		if !submission.StopLoss.LessThan(submission.ReferencePrice) {
			problems = append(problems, "LONG order requires stop_loss below reference_price")
		}
		if submission.TakeProfit.IsPositive() && !submission.TakeProfit.GreaterThan(submission.ReferencePrice) {
			problems = append(problems, "LONG order requires take_profit above reference_price")
		}
	case OrderSideShort:
		if !submission.StopLoss.GreaterThan(submission.ReferencePrice) {
			problems = append(problems, "SHORT order requires stop_loss above reference_price")
		}
		if submission.TakeProfit.IsPositive() && !submission.TakeProfit.LessThan(submission.ReferencePrice) {
			problems = append(problems, "SHORT order requires take_profit below reference_price")
		}
	}
	expectedMaxLoss := submission.Quantity.Mul(submission.ReferencePrice.Sub(submission.StopLoss).Abs())
	if !submission.MaxLoss.Equal(expectedMaxLoss) {
		problems = append(problems, "max_loss must equal quantity times stop distance")
	}
	return problems
}

func normalizeTimeInForce(value TimeInForce) TimeInForce {
	normalized := strings.ToUpper(strings.TrimSpace(string(value)))
	normalized = strings.ReplaceAll(normalized, "-", "_")
	if normalized == "POSTONLY" {
		normalized = string(TimeInForcePostOnly)
	}
	return TimeInForce(normalized)
}

func ValidateOrderSubmissions(submissions []OrderSubmission) error {
	for index, submission := range submissions {
		if err := ValidateOrderSubmission(submission); err != nil {
			return fmt.Errorf("live_order_submission[%d]: %w", index, err)
		}
	}
	return nil
}

func ValidateOrderAcknowledgements(acknowledgements []OrderAcknowledgement) error {
	for index, acknowledgement := range acknowledgements {
		if err := ValidateOrderAcknowledgement(acknowledgement); err != nil {
			return fmt.Errorf("live_order_acknowledgement[%d]: %w", index, err)
		}
	}
	return nil
}
