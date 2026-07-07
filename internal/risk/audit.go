package risk

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

type DecisionAuditInput struct {
	DecisionID string
	Decision   Decision
	Intent     TradeIntent
	Runtime    RuntimeState
	RecordedAt time.Time
}

type DecisionAuditRecord struct {
	DecisionID      string
	Decision        Decision
	Mode            Mode
	HypothesisID    string
	StrategyName    string
	Symbol          string
	Side            Side
	EntryPrice      decimal.Decimal
	Leverage        decimal.Decimal
	Confidence      int
	IntentReason    string
	IntentCreatedAt time.Time
	RecordedAt      time.Time
}

type DecisionAuditStats struct {
	Inserted int
	Skipped  int
}

type DecisionAuditQuery struct {
	DecisionID string
	IntentID   string
	Symbol     string
	Approved   *bool
	Start      time.Time
	End        time.Time
	Limit      int
}

type DecisionAuditRepository interface {
	RecordDecision(ctx context.Context, record DecisionAuditRecord) (DecisionAuditStats, error)
	ListDecisions(ctx context.Context, query DecisionAuditQuery) ([]DecisionAuditRecord, error)
}

func NewDecisionAuditRecord(input DecisionAuditInput) (DecisionAuditRecord, error) {
	intent := normalizeIntent(input.Intent)
	decision := input.Decision
	decision.Checks = append([]Check(nil), input.Decision.Checks...)
	decision.CreatedAt = decision.CreatedAt.UTC()

	record := DecisionAuditRecord{
		DecisionID:      strings.TrimSpace(input.DecisionID),
		Decision:        decision,
		Mode:            input.Runtime.Mode,
		HypothesisID:    intent.HypothesisID,
		StrategyName:    intent.StrategyName,
		Symbol:          intent.Symbol,
		Side:            intent.Side,
		EntryPrice:      intent.EntryPrice,
		Leverage:        intent.Leverage,
		Confidence:      intent.Confidence,
		IntentReason:    intent.Reason,
		IntentCreatedAt: intent.CreatedAt.UTC(),
		RecordedAt:      input.RecordedAt.UTC(),
	}
	if err := ValidateDecisionAuditRecord(record); err != nil {
		return DecisionAuditRecord{}, err
	}
	return record, nil
}

func (s DecisionAuditStats) Total() int {
	return s.Inserted + s.Skipped
}

func ValidateDecisionAuditRecord(record DecisionAuditRecord) error {
	var problems []string
	if strings.TrimSpace(record.DecisionID) == "" {
		problems = append(problems, "decision_id is required")
	}
	if record.DecisionID != strings.TrimSpace(record.DecisionID) {
		problems = append(problems, "decision_id must be trimmed")
	}
	if err := ValidateDecision(record.Decision); err != nil {
		problems = append(problems, err.Error())
	}
	if !KnownMode(record.Mode) {
		problems = append(problems, "mode must be PAPER or LIVE")
	}
	if strings.TrimSpace(record.HypothesisID) == "" {
		problems = append(problems, "hypothesis_id is required")
	}
	if record.HypothesisID != strings.TrimSpace(record.HypothesisID) {
		problems = append(problems, "hypothesis_id must be trimmed")
	}
	if strings.TrimSpace(record.StrategyName) == "" {
		problems = append(problems, "strategy_name is required")
	}
	if record.StrategyName != strings.TrimSpace(record.StrategyName) {
		problems = append(problems, "strategy_name must be trimmed")
	}
	if strings.TrimSpace(record.Symbol) == "" {
		problems = append(problems, "symbol is required")
	}
	if record.Symbol != strings.ToUpper(strings.TrimSpace(record.Symbol)) {
		problems = append(problems, "symbol must be uppercase and trimmed")
	}
	if !KnownSide(record.Side) {
		problems = append(problems, "side must be LONG or SHORT")
	}
	if record.Decision.Approved {
		problems = append(problems, validateApprovedDecisionIntentSnapshot(record)...)
	}
	if record.RecordedAt.IsZero() {
		problems = append(problems, "recorded_at is required")
	}
	if !record.Decision.CreatedAt.IsZero() && !record.RecordedAt.IsZero() && record.RecordedAt.Before(record.Decision.CreatedAt) {
		problems = append(problems, "recorded_at must not be before decision created_at")
	}
	if len(problems) > 0 {
		return errors.New("risk decision audit validation failed: " + strings.Join(problems, "; "))
	}
	return nil
}

func validateApprovedDecisionIntentSnapshot(record DecisionAuditRecord) []string {
	var problems []string
	if record.EntryPrice.LessThanOrEqual(decimal.Zero) {
		problems = append(problems, "approved decision requires positive entry_price")
	}
	if record.Leverage.LessThanOrEqual(decimal.Zero) {
		problems = append(problems, "approved decision requires positive leverage")
	}
	if record.Confidence < 0 || record.Confidence > 100 {
		problems = append(problems, "approved decision requires confidence between zero and 100")
	}
	if strings.TrimSpace(record.IntentReason) == "" {
		problems = append(problems, "approved decision requires intent_reason")
	}
	if record.IntentReason != strings.TrimSpace(record.IntentReason) {
		problems = append(problems, "intent_reason must be trimmed")
	}
	if record.IntentCreatedAt.IsZero() {
		problems = append(problems, "approved decision requires intent_created_at")
	}
	if !record.IntentCreatedAt.IsZero() && !record.Decision.CreatedAt.IsZero() && record.IntentCreatedAt.After(record.Decision.CreatedAt) {
		problems = append(problems, "intent_created_at must not be after decision created_at")
	}
	if record.EntryPrice.GreaterThan(decimal.Zero) && record.Decision.StopLoss.GreaterThan(decimal.Zero) && KnownSide(record.Side) {
		switch record.Side {
		case SideLong:
			if !record.Decision.StopLoss.LessThan(record.EntryPrice) {
				problems = append(problems, "approved LONG decision requires stop_loss below entry_price")
			}
			if record.Decision.TakeProfit.IsPositive() && !record.Decision.TakeProfit.GreaterThan(record.EntryPrice) {
				problems = append(problems, "approved LONG decision requires take_profit above entry_price")
			}
		case SideShort:
			if !record.Decision.StopLoss.GreaterThan(record.EntryPrice) {
				problems = append(problems, "approved SHORT decision requires stop_loss above entry_price")
			}
			if record.Decision.TakeProfit.IsPositive() && !record.Decision.TakeProfit.LessThan(record.EntryPrice) {
				problems = append(problems, "approved SHORT decision requires take_profit below entry_price")
			}
		}
	}
	return problems
}

func ValidateDecisionAuditRecords(records []DecisionAuditRecord) error {
	for index, record := range records {
		if err := ValidateDecisionAuditRecord(record); err != nil {
			return errors.New("risk_decision_audit_record[" + strconv.Itoa(index) + "]: " + err.Error())
		}
	}
	return nil
}

func ValidateDecisionAuditQuery(query DecisionAuditQuery) error {
	if strings.TrimSpace(query.Symbol) != "" && query.Symbol != strings.ToUpper(strings.TrimSpace(query.Symbol)) {
		return errors.New("symbol must be uppercase and trimmed")
	}
	if !query.Start.IsZero() && !query.End.IsZero() && !query.End.After(query.Start) {
		return errors.New("end must be after start")
	}
	if query.Limit < 0 {
		return errors.New("limit must be greater than or equal to zero")
	}
	return nil
}
