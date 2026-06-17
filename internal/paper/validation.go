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
	Mode           string
	InitialBalance decimal.Decimal
	MinimumDays    int
	Reasons        []string
	PlannedAt      time.Time
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
