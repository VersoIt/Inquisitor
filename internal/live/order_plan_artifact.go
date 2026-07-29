package live

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

const LiveOrderPlanArtifactSchemaVersion = "inquisitor.live_order_plan.v1"

type LiveOrderPlanArtifact struct {
	SchemaVersion       string      `json:"schema_version"`
	Source              string      `json:"source"`
	PendingSymbol       string      `json:"pending_symbol,omitempty"`
	RunID               string      `json:"run_id"`
	DecisionID          string      `json:"decision_id"`
	SubmissionID        string      `json:"submission_id"`
	ClientOrderID       string      `json:"client_order_id"`
	Exchange            string      `json:"exchange"`
	Category            string      `json:"category"`
	Symbol              string      `json:"symbol"`
	Side                OrderSide   `json:"side"`
	OrderType           OrderType   `json:"order_type"`
	TimeInForce         TimeInForce `json:"time_in_force"`
	LimitPrice          string      `json:"limit_price"`
	Quantity            string      `json:"quantity"`
	EntryPrice          string      `json:"entry_price"`
	Notional            string      `json:"notional"`
	MaxLoss             string      `json:"max_loss"`
	StopLoss            string      `json:"stop_loss"`
	TakeProfit          string      `json:"take_profit"`
	Leverage            string      `json:"leverage"`
	Confidence          int         `json:"confidence"`
	DecisionCreatedAt   time.Time   `json:"decision_created_at"`
	RecordedAt          time.Time   `json:"recorded_at"`
	SubmissionCreatedAt time.Time   `json:"submission_created_at"`
	Reserved            bool        `json:"reserved"`
	ExchangeContacted   bool        `json:"exchange_contacted"`
	OrderSubmitted      bool        `json:"order_submitted"`
}

func ValidateLiveOrderPlanArtifact(artifact LiveOrderPlanArtifact) error {
	var problems []string
	if artifact.SchemaVersion != LiveOrderPlanArtifactSchemaVersion {
		problems = append(problems, "schema_version must be "+LiveOrderPlanArtifactSchemaVersion)
	}
	switch strings.TrimSpace(artifact.Source) {
	case "decision-id", "select-pending":
	default:
		problems = append(problems, "source must be decision-id or select-pending")
	}
	problems = append(problems, liveOrderPlanArtifactRequiredTrimmedProblems(map[string]string{
		"source":          artifact.Source,
		"run_id":          artifact.RunID,
		"decision_id":     artifact.DecisionID,
		"submission_id":   artifact.SubmissionID,
		"client_order_id": artifact.ClientOrderID,
		"exchange":        artifact.Exchange,
		"category":        artifact.Category,
		"symbol":          artifact.Symbol,
	})...)
	if artifact.PendingSymbol != strings.ToUpper(strings.TrimSpace(artifact.PendingSymbol)) {
		problems = append(problems, "pending_symbol must be uppercase and trimmed")
	}
	if artifact.Exchange != strings.ToLower(strings.TrimSpace(artifact.Exchange)) {
		problems = append(problems, "exchange must be lowercase and trimmed")
	}
	if artifact.Category != strings.ToLower(strings.TrimSpace(artifact.Category)) {
		problems = append(problems, "category must be lowercase and trimmed")
	}
	if artifact.Symbol != strings.ToUpper(strings.TrimSpace(artifact.Symbol)) {
		problems = append(problems, "symbol must be uppercase and trimmed")
	}
	if !KnownOrderSide(artifact.Side) {
		problems = append(problems, "side must be LONG or SHORT")
	}
	if !KnownOrderType(artifact.OrderType) {
		problems = append(problems, "order_type must be MARKET or LIMIT")
	}
	if !KnownTimeInForce(artifact.TimeInForce) {
		problems = append(problems, "time_in_force must be GTC, IOC, FOK, or POST_ONLY")
	}
	problems = append(problems, liveOrderPlanArtifactDecimalProblems(artifact)...)
	if artifact.Confidence < 0 || artifact.Confidence > 100 {
		problems = append(problems, "confidence must be between zero and 100")
	}
	if artifact.DecisionCreatedAt.IsZero() {
		problems = append(problems, "decision_created_at is required")
	}
	if artifact.RecordedAt.IsZero() {
		problems = append(problems, "recorded_at is required")
	}
	if artifact.SubmissionCreatedAt.IsZero() {
		problems = append(problems, "submission_created_at is required")
	}
	if artifact.Reserved {
		problems = append(problems, "reserved must be false for a read-only order plan")
	}
	if artifact.ExchangeContacted {
		problems = append(problems, "exchange_contacted must be false for a read-only order plan")
	}
	if artifact.OrderSubmitted {
		problems = append(problems, "order_submitted must be false for a read-only order plan")
	}
	if len(problems) > 0 {
		return errors.New("live order plan artifact validation failed: " + strings.Join(problems, "; "))
	}
	return nil
}

func (artifact LiveOrderPlanArtifact) IdentityExpectation() LiveLoopOrderIdentityExpectation {
	return LiveLoopOrderIdentityExpectation{
		SubmissionID:  artifact.SubmissionID,
		ClientOrderID: artifact.ClientOrderID,
	}
}

func liveOrderPlanArtifactRequiredTrimmedProblems(values map[string]string) []string {
	var problems []string
	for field, value := range values {
		if strings.TrimSpace(value) == "" {
			problems = append(problems, field+" is required")
			continue
		}
		if value != strings.TrimSpace(value) {
			problems = append(problems, field+" must be trimmed")
		}
	}
	return problems
}

func liveOrderPlanArtifactDecimalProblems(artifact LiveOrderPlanArtifact) []string {
	var problems []string
	limitPrice, err := decimalFromLiveOrderPlanArtifact("limit_price", artifact.LimitPrice)
	if err != nil {
		problems = append(problems, err.Error())
	}
	positiveFields := map[string]string{
		"quantity":    artifact.Quantity,
		"entry_price": artifact.EntryPrice,
		"notional":    artifact.Notional,
		"max_loss":    artifact.MaxLoss,
		"stop_loss":   artifact.StopLoss,
		"leverage":    artifact.Leverage,
	}
	for field, value := range positiveFields {
		parsed, err := decimalFromLiveOrderPlanArtifact(field, value)
		if err != nil {
			problems = append(problems, err.Error())
			continue
		}
		if parsed.LessThanOrEqual(decimal.Zero) {
			problems = append(problems, field+" must be positive")
		}
	}
	if strings.TrimSpace(artifact.TakeProfit) != "" {
		if _, err := decimalFromLiveOrderPlanArtifact("take_profit", artifact.TakeProfit); err != nil {
			problems = append(problems, err.Error())
		}
	}
	switch artifact.OrderType {
	case OrderTypeMarket:
		if err == nil && !limitPrice.IsZero() {
			problems = append(problems, "market artifact limit_price must be zero")
		}
		if artifact.TimeInForce != TimeInForceIOC && artifact.TimeInForce != TimeInForceFOK {
			problems = append(problems, "market artifact time_in_force must be IOC or FOK")
		}
	case OrderTypeLimit:
		if err == nil && limitPrice.LessThanOrEqual(decimal.Zero) {
			problems = append(problems, "limit artifact requires positive limit_price")
		}
	}
	return problems
}

func decimalFromLiveOrderPlanArtifact(field string, value string) (decimal.Decimal, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return decimal.Zero, fmt.Errorf("%s is required", field)
	}
	parsed, err := decimal.NewFromString(trimmed)
	if err != nil {
		return decimal.Zero, fmt.Errorf("%s must be a decimal string: %w", field, err)
	}
	return parsed, nil
}
