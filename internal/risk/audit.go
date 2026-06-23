package risk

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"
)

type DecisionAuditInput struct {
	DecisionID string
	Decision   Decision
	Intent     TradeIntent
	Runtime    RuntimeState
	RecordedAt time.Time
}

type DecisionAuditRecord struct {
	DecisionID   string
	Decision     Decision
	Mode         Mode
	HypothesisID string
	StrategyName string
	Symbol       string
	Side         Side
	RecordedAt   time.Time
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
		DecisionID:   strings.TrimSpace(input.DecisionID),
		Decision:     decision,
		Mode:         input.Runtime.Mode,
		HypothesisID: intent.HypothesisID,
		StrategyName: intent.StrategyName,
		Symbol:       intent.Symbol,
		Side:         intent.Side,
		RecordedAt:   input.RecordedAt.UTC(),
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
