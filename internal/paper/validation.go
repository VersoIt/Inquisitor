package paper

import (
	"errors"
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
