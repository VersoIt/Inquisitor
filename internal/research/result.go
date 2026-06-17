package research

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

type Outcome string

const (
	OutcomeNotExecuted  Outcome = "NOT_EXECUTED"
	OutcomeInconclusive Outcome = "INCONCLUSIVE"
	OutcomeRejected     Outcome = "REJECTED"
	OutcomeCandidate    Outcome = "CANDIDATE"
)

type Metrics struct {
	Trades                         int     `json:"trades"`
	RegimeStates                   int     `json:"regime_states"`
	ExpectedRegimeStates           int     `json:"expected_regime_states"`
	MissingRegimeStates            int     `json:"missing_regime_states"`
	RegimeCoveragePct              float64 `json:"regime_coverage_pct"`
	RuleObservations               int     `json:"rule_observations"`
	RegimeAllowedObservations      int     `json:"regime_allowed_observations"`
	RegimeBlockedObservations      int     `json:"regime_blocked_observations"`
	RuleEvaluations                int     `json:"rule_evaluations"`
	SignalRulePasses               int     `json:"signal_rule_passes"`
	SignalMatches                  int     `json:"signal_matches"`
	SignalFailures                 int     `json:"signal_failures"`
	SignalSkips                    int     `json:"signal_skips"`
	FeatureEvaluationFailures      int     `json:"feature_evaluation_failures"`
	InSampleTrades                 int     `json:"in_sample_trades,omitempty"`
	OutOfSampleTrades              int     `json:"out_of_sample_trades,omitempty"`
	GrossProfit                    string  `json:"gross_profit,omitempty"`
	GrossLoss                      string  `json:"gross_loss,omitempty"`
	TotalFees                      string  `json:"total_fees,omitempty"`
	NetPnL                         string  `json:"net_pnl,omitempty"`
	Expectancy                     string  `json:"expectancy,omitempty"`
	ProfitFactor                   string  `json:"profit_factor,omitempty"`
	ProfitFactorDefined            bool    `json:"profit_factor_defined,omitempty"`
	WinRatePct                     float64 `json:"win_rate_pct,omitempty"`
	MaxDrawdownPct                 float64 `json:"max_drawdown_pct,omitempty"`
	InitialEquity                  string  `json:"initial_equity,omitempty"`
	FinalEquity                    string  `json:"final_equity,omitempty"`
	InSampleNetPnL                 string  `json:"in_sample_net_pnl,omitempty"`
	InSampleProfitFactor           string  `json:"in_sample_profit_factor,omitempty"`
	InSampleProfitFactorDefined    bool    `json:"in_sample_profit_factor_defined,omitempty"`
	InSampleMaxDrawdownPct         float64 `json:"in_sample_max_drawdown_pct,omitempty"`
	OutOfSampleNetPnL              string  `json:"out_of_sample_net_pnl,omitempty"`
	OutOfSampleProfitFactor        string  `json:"out_of_sample_profit_factor,omitempty"`
	OutOfSampleProfitFactorDefined bool    `json:"out_of_sample_profit_factor_defined,omitempty"`
	OutOfSampleMaxDrawdownPct      float64 `json:"out_of_sample_max_drawdown_pct,omitempty"`
	FeesIncluded                   bool    `json:"fees_included"`
	SpreadIncluded                 bool    `json:"spread_included"`
	SlippageIncluded               bool    `json:"slippage_included"`
	OutOfSample                    bool    `json:"out_of_sample"`
	WalkForward                    bool    `json:"walk_forward"`
	RegimeAnalysisIncluded         bool    `json:"regime_analysis_included"`
}

type Result struct {
	RunID       string
	FinalStatus Status
	Outcome     Outcome
	Summary     string
	Metrics     Metrics
	Reasons     []string
	RecordedAt  time.Time
}

type ResultInput struct {
	RunID       string
	FinalStatus Status
	Outcome     Outcome
	Summary     string
	Metrics     Metrics
	Reasons     []string
	RecordedAt  time.Time
}

type ResultQuery struct {
	RunID       string
	FinalStatus Status
	Outcome     Outcome
	Start       time.Time
	End         time.Time
	Limit       int
}

type RecordResultStats struct {
	RunUpdated     int
	ResultInserted int
	ResultUpdated  int
}

type ResultRecorder interface {
	RecordResult(ctx context.Context, run Run, result Result) (RecordResultStats, error)
	ListResults(ctx context.Context, query ResultQuery) ([]Result, error)
}

func (s RecordResultStats) Total() int {
	return s.RunUpdated + s.ResultInserted + s.ResultUpdated
}

func NewResult(input ResultInput) (Result, error) {
	result := Result{
		RunID:       strings.TrimSpace(input.RunID),
		FinalStatus: Status(strings.ToUpper(strings.TrimSpace(string(input.FinalStatus)))),
		Outcome:     Outcome(strings.ToUpper(strings.TrimSpace(string(input.Outcome)))),
		Summary:     strings.TrimSpace(input.Summary),
		Metrics:     input.Metrics,
		Reasons:     canonicalStrings(input.Reasons, strings.TrimSpace),
		RecordedAt:  input.RecordedAt.UTC(),
	}
	if err := ValidateResult(result); err != nil {
		return Result{}, err
	}
	return result, nil
}

func FinalizeRun(run Run, result Result) (Run, error) {
	if err := ValidateRun(run); err != nil {
		return Run{}, err
	}
	if err := ValidateResult(result); err != nil {
		return Run{}, err
	}
	if run.RunID != result.RunID {
		return Run{}, errors.New("result run_id must match research run")
	}
	if IsFinalStatus(run.Status) && run.Status != result.FinalStatus {
		return Run{}, errors.New("final research run status cannot be changed")
	}

	run.Status = result.FinalStatus
	if err := ValidateRun(run); err != nil {
		return Run{}, err
	}
	return run, nil
}

func ValidateResults(results []Result) error {
	for index, result := range results {
		if err := ValidateResult(result); err != nil {
			return fmt.Errorf("research_result[%d]: %w", index, err)
		}
	}
	return nil
}

func ValidateResult(result Result) error {
	var problems []string
	addRequired := func(field, value string) {
		if strings.TrimSpace(value) == "" {
			problems = append(problems, field+" is required")
		}
	}

	addRequired("run_id", result.RunID)
	addRequired("summary", result.Summary)
	if result.RunID != "" && !runIDPattern.MatchString(result.RunID) {
		problems = append(problems, "run_id must be 8-128 url-safe characters")
	}
	if !IsFinalStatus(result.FinalStatus) {
		problems = append(problems, "final_status must be one of COMPLETED, FAILED, CANCELLED")
	}
	if !KnownOutcome(result.Outcome) {
		problems = append(problems, "outcome is unsupported")
	}
	if result.RecordedAt.IsZero() {
		problems = append(problems, "recorded_at is required")
	}
	problems = append(problems, validateMetrics(result.FinalStatus, result.Outcome, result.Metrics)...)
	problems = append(problems, validateReasons(result.Reasons)...)

	if len(problems) > 0 {
		return errors.New("research result validation failed: " + strings.Join(problems, "; "))
	}
	return nil
}

func ValidateResultQuery(query ResultQuery) error {
	if query.FinalStatus != "" && !IsFinalStatus(query.FinalStatus) {
		return errors.New("final_status must be one of COMPLETED, FAILED, CANCELLED")
	}
	if query.Outcome != "" && !KnownOutcome(query.Outcome) {
		return errors.New("outcome is unsupported")
	}
	if !query.Start.IsZero() && !query.End.IsZero() && !query.End.After(query.Start) {
		return errors.New("end must be after start")
	}
	if query.Limit < 0 {
		return errors.New("limit must be greater than or equal to zero")
	}
	return nil
}

func IsFinalStatus(status Status) bool {
	switch status {
	case StatusCompleted, StatusFailed, StatusCancelled:
		return true
	default:
		return false
	}
}

func KnownOutcome(outcome Outcome) bool {
	switch outcome {
	case OutcomeNotExecuted, OutcomeInconclusive, OutcomeRejected, OutcomeCandidate:
		return true
	default:
		return false
	}
}

func validateMetrics(finalStatus Status, outcome Outcome, metrics Metrics) []string {
	var problems []string
	if metrics.Trades < 0 {
		problems = append(problems, "metrics.trades must be greater than or equal to zero")
	}
	if metrics.RegimeStates < 0 {
		problems = append(problems, "metrics.regime_states must be greater than or equal to zero")
	}
	if metrics.ExpectedRegimeStates < 0 {
		problems = append(problems, "metrics.expected_regime_states must be greater than or equal to zero")
	}
	if metrics.MissingRegimeStates < 0 {
		problems = append(problems, "metrics.missing_regime_states must be greater than or equal to zero")
	}
	if metrics.ExpectedRegimeStates > 0 && metrics.RegimeStates+metrics.MissingRegimeStates != metrics.ExpectedRegimeStates {
		problems = append(problems, "metrics regime state counts must balance")
	}
	if metrics.RegimeCoveragePct < 0 || metrics.RegimeCoveragePct > 100 {
		problems = append(problems, "metrics.regime_coverage_pct must be between 0 and 100")
	}
	if metrics.RuleObservations < 0 {
		problems = append(problems, "metrics.rule_observations must be greater than or equal to zero")
	}
	if metrics.RegimeAllowedObservations < 0 {
		problems = append(problems, "metrics.regime_allowed_observations must be greater than or equal to zero")
	}
	if metrics.RegimeBlockedObservations < 0 {
		problems = append(problems, "metrics.regime_blocked_observations must be greater than or equal to zero")
	}
	if metrics.RuleObservations > 0 && metrics.RegimeAllowedObservations+metrics.RegimeBlockedObservations != metrics.RuleObservations {
		problems = append(problems, "metrics rule observation counts must balance")
	}
	if metrics.RuleEvaluations < 0 {
		problems = append(problems, "metrics.rule_evaluations must be greater than or equal to zero")
	}
	if metrics.SignalRulePasses < 0 {
		problems = append(problems, "metrics.signal_rule_passes must be greater than or equal to zero")
	}
	if metrics.SignalMatches < 0 {
		problems = append(problems, "metrics.signal_matches must be greater than or equal to zero")
	}
	if metrics.SignalFailures < 0 {
		problems = append(problems, "metrics.signal_failures must be greater than or equal to zero")
	}
	if metrics.SignalSkips < 0 {
		problems = append(problems, "metrics.signal_skips must be greater than or equal to zero")
	}
	if metrics.RuleEvaluations > 0 && metrics.SignalRulePasses+metrics.SignalFailures+metrics.SignalSkips != metrics.RuleEvaluations {
		problems = append(problems, "metrics signal evaluation counts must balance")
	}
	if metrics.FeatureEvaluationFailures < 0 {
		problems = append(problems, "metrics.feature_evaluation_failures must be greater than or equal to zero")
	}
	if metrics.InSampleTrades < 0 {
		problems = append(problems, "metrics.in_sample_trades must be greater than or equal to zero")
	}
	if metrics.OutOfSampleTrades < 0 {
		problems = append(problems, "metrics.out_of_sample_trades must be greater than or equal to zero")
	}
	splitTrades := metrics.InSampleTrades + metrics.OutOfSampleTrades
	if splitTrades > 0 && splitTrades != metrics.Trades {
		problems = append(problems, "metrics split trade counts must balance")
	}
	problems = append(problems, validateOptionalDecimalMetric("metrics.gross_profit", metrics.GrossProfit, true)...)
	problems = append(problems, validateOptionalDecimalMetric("metrics.gross_loss", metrics.GrossLoss, true)...)
	problems = append(problems, validateOptionalDecimalMetric("metrics.total_fees", metrics.TotalFees, true)...)
	problems = append(problems, validateOptionalDecimalMetric("metrics.net_pnl", metrics.NetPnL, false)...)
	problems = append(problems, validateOptionalDecimalMetric("metrics.expectancy", metrics.Expectancy, false)...)
	problems = append(problems, validateOptionalDecimalMetric("metrics.profit_factor", metrics.ProfitFactor, true)...)
	problems = append(problems, validateOptionalDecimalMetric("metrics.initial_equity", metrics.InitialEquity, true)...)
	problems = append(problems, validateOptionalDecimalMetric("metrics.final_equity", metrics.FinalEquity, false)...)
	problems = append(problems, validateOptionalDecimalMetric("metrics.in_sample_net_pnl", metrics.InSampleNetPnL, false)...)
	problems = append(problems, validateOptionalDecimalMetric("metrics.in_sample_profit_factor", metrics.InSampleProfitFactor, true)...)
	problems = append(problems, validateOptionalDecimalMetric("metrics.out_of_sample_net_pnl", metrics.OutOfSampleNetPnL, false)...)
	problems = append(problems, validateOptionalDecimalMetric("metrics.out_of_sample_profit_factor", metrics.OutOfSampleProfitFactor, true)...)
	if metrics.WinRatePct < 0 || metrics.WinRatePct > 100 {
		problems = append(problems, "metrics.win_rate_pct must be between 0 and 100")
	}
	if metrics.MaxDrawdownPct < 0 || metrics.MaxDrawdownPct > 100 {
		problems = append(problems, "metrics.max_drawdown_pct must be between 0 and 100")
	}
	if metrics.InSampleMaxDrawdownPct < 0 || metrics.InSampleMaxDrawdownPct > 100 {
		problems = append(problems, "metrics.in_sample_max_drawdown_pct must be between 0 and 100")
	}
	if metrics.OutOfSampleMaxDrawdownPct < 0 || metrics.OutOfSampleMaxDrawdownPct > 100 {
		problems = append(problems, "metrics.out_of_sample_max_drawdown_pct must be between 0 and 100")
	}
	if outcome == OutcomeNotExecuted {
		if finalStatus == StatusCompleted {
			problems = append(problems, "NOT_EXECUTED outcome must not use COMPLETED final status")
		}
		if metrics.Trades != 0 {
			problems = append(problems, "NOT_EXECUTED outcome requires zero trades")
		}
		return problems
	}
	if metrics.Trades > 0 {
		if !metrics.FeesIncluded {
			problems = append(problems, "metrics.fees_included must be true when trades are evaluated")
		}
		if !metrics.SpreadIncluded {
			problems = append(problems, "metrics.spread_included must be true when trades are evaluated")
		}
		if !metrics.SlippageIncluded {
			problems = append(problems, "metrics.slippage_included must be true when trades are evaluated")
		}
		if !metrics.RegimeAnalysisIncluded {
			problems = append(problems, "metrics.regime_analysis_included must be true when trades are evaluated")
		}
	}
	if outcome == OutcomeCandidate {
		if finalStatus != StatusCompleted {
			problems = append(problems, "CANDIDATE outcome requires COMPLETED final status")
		}
		if metrics.Trades == 0 {
			problems = append(problems, "CANDIDATE outcome requires evaluated trades")
		}
		if !metrics.OutOfSample {
			problems = append(problems, "CANDIDATE outcome requires out_of_sample=true")
		}
		if !metrics.WalkForward {
			problems = append(problems, "CANDIDATE outcome requires walk_forward=true")
		}
	}
	return problems
}

func validateOptionalDecimalMetric(field string, value string, nonNegative bool) []string {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	parsed, err := decimal.NewFromString(strings.TrimSpace(value))
	if err != nil {
		return []string{field + " must be a decimal string"}
	}
	if nonNegative && parsed.IsNegative() {
		return []string{field + " must be greater than or equal to zero"}
	}
	return nil
}

func validateReasons(reasons []string) []string {
	var problems []string
	for i, reason := range reasons {
		if strings.TrimSpace(reason) == "" {
			problems = append(problems, fmt.Sprintf("reasons[%d] must not be empty", i))
		}
	}
	return problems
}
