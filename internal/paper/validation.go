package paper

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/shopspring/decimal"

	domainresearch "github.com/VersoIt/Inquisitor/internal/research"
)

type SafetyPolicy struct {
	TradingEnabled              bool
	TradingMode                 string
	AllowLive                   bool
	WithdrawalPermissionAllowed bool
	InitialBalance              decimal.Decimal
	MinimumDays                 int
	SimulateFees                bool
	SimulateSlippage            bool
	SimulateSpread              bool
}

type ValidationPlan struct {
	RunID          string
	Allowed        bool
	Mode           string
	InitialBalance decimal.Decimal
	MinimumDays    int
	RequestedAt    time.Time
	Reasons        []string
}

type ValidationStatus string

const (
	ValidationStatusPlanned   ValidationStatus = "PLANNED"
	ValidationStatusRunning   ValidationStatus = "RUNNING"
	ValidationStatusCompleted ValidationStatus = "COMPLETED"
	ValidationStatusCancelled ValidationStatus = "CANCELLED"
)

type ValidationRecord struct {
	ValidationID   string
	RunID          string
	Status         ValidationStatus
	StatusReason   string
	Mode           string
	InitialBalance decimal.Decimal
	MinimumDays    int
	Reasons        []string
	PlannedAt      time.Time
	StartedAt      time.Time
	CompletedAt    time.Time
	CancelledAt    time.Time
}

type ValidationRecordInput struct {
	ValidationID string
	Plan         ValidationPlan
}

type ValidationRecordStats struct {
	Inserted int
	Updated  int
}

type ValidationRecordQuery struct {
	ValidationID string
	RunID        string
	Status       ValidationStatus
	Start        time.Time
	End          time.Time
	Limit        int
}

type ValidationRecordRepository interface {
	RecordValidation(ctx context.Context, record ValidationRecord) (ValidationRecordStats, error)
	TransitionValidation(ctx context.Context, record ValidationRecord, expectedStatus ValidationStatus) (ValidationRecordStats, error)
	ListValidationRecords(ctx context.Context, query ValidationRecordQuery) ([]ValidationRecord, error)
}

func (s ValidationRecordStats) Total() int {
	return s.Inserted + s.Updated
}

func NewValidationPlan(run domainresearch.Run, result domainresearch.Result, policy SafetyPolicy, requestedAt time.Time) (ValidationPlan, error) {
	if err := domainresearch.ValidateRun(run); err != nil {
		return ValidationPlan{}, err
	}
	if err := domainresearch.ValidateResult(result); err != nil {
		return ValidationPlan{}, err
	}
	if err := ValidateSafetyPolicy(policy); err != nil {
		return ValidationPlan{}, err
	}
	if requestedAt.IsZero() {
		return ValidationPlan{}, errors.New("paper validation plan failed: requested_at is required")
	}
	if run.RunID != result.RunID {
		return ValidationPlan{}, errors.New("paper validation plan failed: result run_id must match research run")
	}
	if run.Status != result.FinalStatus {
		return ValidationPlan{}, errors.New("paper validation plan failed: research run status must match result final_status")
	}

	reasons := paperBlockers(run, result, policy)
	if len(reasons) == 0 {
		reasons = []string{"paper_validation_allowed"}
	}
	return ValidationPlan{
		RunID:          run.RunID,
		Allowed:        len(reasons) == 1 && reasons[0] == "paper_validation_allowed",
		Mode:           strings.ToLower(strings.TrimSpace(policy.TradingMode)),
		InitialBalance: policy.InitialBalance,
		MinimumDays:    policy.MinimumDays,
		RequestedAt:    requestedAt.UTC(),
		Reasons:        reasons,
	}, nil
}

func NewValidationRecord(input ValidationRecordInput) (ValidationRecord, error) {
	if !input.Plan.Allowed {
		return ValidationRecord{}, errors.New("paper validation record failed: allowed validation plan is required")
	}
	record := ValidationRecord{
		ValidationID:   strings.TrimSpace(input.ValidationID),
		RunID:          strings.TrimSpace(input.Plan.RunID),
		Status:         ValidationStatusPlanned,
		Mode:           strings.ToLower(strings.TrimSpace(input.Plan.Mode)),
		InitialBalance: input.Plan.InitialBalance,
		MinimumDays:    input.Plan.MinimumDays,
		Reasons:        append([]string(nil), input.Plan.Reasons...),
		PlannedAt:      input.Plan.RequestedAt.UTC(),
	}
	if err := ValidateValidationRecord(record); err != nil {
		return ValidationRecord{}, err
	}
	return record, nil
}

func StartValidation(record ValidationRecord, startedAt time.Time) (ValidationRecord, error) {
	if err := ValidateValidationRecord(record); err != nil {
		return ValidationRecord{}, err
	}
	if record.Status != ValidationStatusPlanned {
		return ValidationRecord{}, errors.New("paper validation start failed: status must be PLANNED")
	}
	startedAt = startedAt.UTC()
	if startedAt.IsZero() {
		return ValidationRecord{}, errors.New("paper validation start failed: started_at is required")
	}
	if startedAt.Before(record.PlannedAt) {
		return ValidationRecord{}, errors.New("paper validation start failed: started_at must not be before planned_at")
	}
	record.Status = ValidationStatusRunning
	record.StartedAt = startedAt
	if err := ValidateValidationRecord(record); err != nil {
		return ValidationRecord{}, err
	}
	return record, nil
}

func CompleteValidation(record ValidationRecord, completedAt time.Time) (ValidationRecord, error) {
	if err := ValidateValidationRecord(record); err != nil {
		return ValidationRecord{}, err
	}
	if record.Status != ValidationStatusRunning {
		return ValidationRecord{}, errors.New("paper validation completion failed: status must be RUNNING")
	}
	completedAt = completedAt.UTC()
	if completedAt.IsZero() {
		return ValidationRecord{}, errors.New("paper validation completion failed: completed_at is required")
	}
	minimumEnd := record.StartedAt.AddDate(0, 0, record.MinimumDays)
	if completedAt.Before(minimumEnd) {
		return ValidationRecord{}, errors.New("paper validation completion failed: minimum validation period has not elapsed")
	}
	record.Status = ValidationStatusCompleted
	record.StatusReason = "minimum_validation_period_completed"
	record.CompletedAt = completedAt
	if err := ValidateValidationRecord(record); err != nil {
		return ValidationRecord{}, err
	}
	return record, nil
}

func CancelValidation(record ValidationRecord, cancelledAt time.Time, reason string) (ValidationRecord, error) {
	if err := ValidateValidationRecord(record); err != nil {
		return ValidationRecord{}, err
	}
	if record.Status != ValidationStatusPlanned && record.Status != ValidationStatusRunning {
		return ValidationRecord{}, errors.New("paper validation cancellation failed: status must be PLANNED or RUNNING")
	}
	cancelledAt = cancelledAt.UTC()
	if cancelledAt.IsZero() {
		return ValidationRecord{}, errors.New("paper validation cancellation failed: cancelled_at is required")
	}
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return ValidationRecord{}, errors.New("paper validation cancellation failed: reason is required")
	}
	if cancelledAt.Before(record.PlannedAt) || (!record.StartedAt.IsZero() && cancelledAt.Before(record.StartedAt)) {
		return ValidationRecord{}, errors.New("paper validation cancellation failed: cancelled_at precedes validation lifecycle")
	}
	record.Status = ValidationStatusCancelled
	record.StatusReason = reason
	record.CancelledAt = cancelledAt
	if err := ValidateValidationRecord(record); err != nil {
		return ValidationRecord{}, err
	}
	return record, nil
}

func ValidateSafetyPolicy(policy SafetyPolicy) error {
	var problems []string
	if policy.InitialBalance.LessThanOrEqual(decimal.Zero) {
		problems = append(problems, "initial_balance must be positive")
	}
	if policy.MinimumDays <= 0 {
		problems = append(problems, "minimum_days must be positive")
	}
	if len(problems) > 0 {
		return errors.New("paper safety policy validation failed: " + strings.Join(problems, "; "))
	}
	return nil
}

func ValidateValidationRecord(record ValidationRecord) error {
	var problems []string
	if strings.TrimSpace(record.ValidationID) == "" {
		problems = append(problems, "validation_id is required")
	}
	if strings.TrimSpace(record.RunID) == "" {
		problems = append(problems, "run_id is required")
	}
	if !KnownValidationStatus(record.Status) {
		problems = append(problems, "status is unsupported")
	}
	if strings.ToLower(strings.TrimSpace(record.Mode)) != "paper" {
		problems = append(problems, "mode must be paper")
	}
	if record.InitialBalance.LessThanOrEqual(decimal.Zero) {
		problems = append(problems, "initial_balance must be positive")
	}
	if record.MinimumDays <= 0 {
		problems = append(problems, "minimum_days must be positive")
	}
	if record.PlannedAt.IsZero() {
		problems = append(problems, "planned_at is required")
	}
	problems = append(problems, validateValidationLifecycle(record)...)
	for index, reason := range record.Reasons {
		if strings.TrimSpace(reason) == "" {
			problems = append(problems, "reasons["+strconv.Itoa(index)+"] must not be empty")
		}
	}
	if len(problems) > 0 {
		return errors.New("paper validation record validation failed: " + strings.Join(problems, "; "))
	}
	return nil
}

func validateValidationLifecycle(record ValidationRecord) []string {
	var problems []string
	reason := strings.TrimSpace(record.StatusReason)
	switch record.Status {
	case ValidationStatusPlanned:
		if !record.StartedAt.IsZero() || !record.CompletedAt.IsZero() || !record.CancelledAt.IsZero() {
			problems = append(problems, "PLANNED status must not have lifecycle timestamps")
		}
		if reason != "" {
			problems = append(problems, "PLANNED status must not have status_reason")
		}
	case ValidationStatusRunning:
		if record.StartedAt.IsZero() {
			problems = append(problems, "RUNNING status requires started_at")
		}
		if !record.CompletedAt.IsZero() || !record.CancelledAt.IsZero() {
			problems = append(problems, "RUNNING status must not have terminal timestamps")
		}
		if reason != "" {
			problems = append(problems, "RUNNING status must not have status_reason")
		}
	case ValidationStatusCompleted:
		if record.StartedAt.IsZero() || record.CompletedAt.IsZero() {
			problems = append(problems, "COMPLETED status requires started_at and completed_at")
		}
		if !record.CancelledAt.IsZero() {
			problems = append(problems, "COMPLETED status must not have cancelled_at")
		}
		if reason == "" {
			problems = append(problems, "COMPLETED status requires status_reason")
		}
	case ValidationStatusCancelled:
		if record.CancelledAt.IsZero() {
			problems = append(problems, "CANCELLED status requires cancelled_at")
		}
		if !record.CompletedAt.IsZero() {
			problems = append(problems, "CANCELLED status must not have completed_at")
		}
		if reason == "" {
			problems = append(problems, "CANCELLED status requires status_reason")
		}
	}
	if !record.StartedAt.IsZero() && record.StartedAt.Before(record.PlannedAt) {
		problems = append(problems, "started_at must not be before planned_at")
	}
	if !record.CompletedAt.IsZero() && (record.StartedAt.IsZero() || record.CompletedAt.Before(record.StartedAt.AddDate(0, 0, record.MinimumDays))) {
		problems = append(problems, "completed_at must satisfy minimum validation period")
	}
	if !record.CancelledAt.IsZero() && (record.CancelledAt.Before(record.PlannedAt) || (!record.StartedAt.IsZero() && record.CancelledAt.Before(record.StartedAt))) {
		problems = append(problems, "cancelled_at must not precede validation lifecycle")
	}
	return problems
}

func ValidateValidationRecordQuery(query ValidationRecordQuery) error {
	if query.Status != "" && !KnownValidationStatus(query.Status) {
		return errors.New("status is unsupported")
	}
	if !query.Start.IsZero() && !query.End.IsZero() && !query.End.After(query.Start) {
		return errors.New("end must be after start")
	}
	if query.Limit < 0 {
		return errors.New("limit must be greater than or equal to zero")
	}
	return nil
}

func ValidateValidationRecords(records []ValidationRecord) error {
	for index, record := range records {
		if err := ValidateValidationRecord(record); err != nil {
			return errors.New("paper_validation_record[" + decimal.NewFromInt(int64(index)).String() + "]: " + err.Error())
		}
	}
	return nil
}

func ValidateValidationTransition(expectedStatus ValidationStatus, record ValidationRecord) error {
	if err := ValidateValidationRecord(record); err != nil {
		return err
	}
	valid := expectedStatus == ValidationStatusPlanned && (record.Status == ValidationStatusRunning || record.Status == ValidationStatusCancelled) ||
		expectedStatus == ValidationStatusRunning && (record.Status == ValidationStatusCompleted || record.Status == ValidationStatusCancelled)
	if !valid {
		return errors.New("paper validation transition is unsupported")
	}
	return nil
}

func KnownValidationStatus(status ValidationStatus) bool {
	switch status {
	case ValidationStatusPlanned, ValidationStatusRunning, ValidationStatusCompleted, ValidationStatusCancelled:
		return true
	default:
		return false
	}
}

func paperBlockers(run domainresearch.Run, result domainresearch.Result, policy SafetyPolicy) []string {
	var reasons []string
	if run.Status != domainresearch.StatusCompleted {
		reasons = append(reasons, "research_run_not_completed")
	}
	if result.FinalStatus != domainresearch.StatusCompleted {
		reasons = append(reasons, "research_result_not_completed")
	}
	if result.Outcome != domainresearch.OutcomeCandidate {
		reasons = append(reasons, "research_result_not_candidate")
	}
	if !result.Metrics.OutOfSample {
		reasons = append(reasons, "candidate_missing_out_of_sample")
	}
	if !result.Metrics.WalkForward {
		reasons = append(reasons, "candidate_missing_walk_forward")
	}
	if !result.Metrics.RegimeAnalysisIncluded {
		reasons = append(reasons, "candidate_missing_regime_analysis")
	}
	if !result.Metrics.FeesIncluded || !result.Metrics.SpreadIncluded || !result.Metrics.SlippageIncluded {
		reasons = append(reasons, "candidate_missing_conservative_costs")
	}
	if !policy.TradingEnabled {
		reasons = append(reasons, "paper_trading_disabled")
	}
	if strings.ToLower(strings.TrimSpace(policy.TradingMode)) != "paper" {
		reasons = append(reasons, "trading_mode_not_paper")
	}
	if policy.AllowLive {
		reasons = append(reasons, "live_trading_enabled")
	}
	if policy.WithdrawalPermissionAllowed {
		reasons = append(reasons, "withdrawal_permission_enabled")
	}
	if !policy.SimulateFees {
		reasons = append(reasons, "paper_fee_simulation_disabled")
	}
	if !policy.SimulateSlippage {
		reasons = append(reasons, "paper_slippage_simulation_disabled")
	}
	if !policy.SimulateSpread {
		reasons = append(reasons, "paper_spread_simulation_disabled")
	}
	return reasons
}
