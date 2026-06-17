package research

import (
	"errors"
	"fmt"
	"strings"

	"github.com/shopspring/decimal"
)

type ResultGatePolicy struct {
	Enabled               bool            `json:"enabled"`
	MinTrades             int             `json:"min_trades"`
	MinProfitFactor       decimal.Decimal `json:"min_profit_factor"`
	MinExpectancy         decimal.Decimal `json:"min_expectancy"`
	MaxDrawdownPct        float64         `json:"max_drawdown_pct"`
	RequireOutOfSample    bool            `json:"require_out_of_sample"`
	RequireWalkForward    bool            `json:"require_walk_forward"`
	RequireRegimeAnalysis bool            `json:"require_regime_analysis"`
	RequireCosts          bool            `json:"require_costs"`
}

type ResultGateEvaluation struct {
	Enabled           bool              `json:"enabled"`
	Passed            bool              `json:"passed"`
	CandidateEligible bool              `json:"candidate_eligible"`
	Checks            []ResultGateCheck `json:"checks"`
	Reasons           []string          `json:"reasons"`
}

type ResultGateCheck struct {
	Name      string `json:"name"`
	Passed    bool   `json:"passed"`
	Observed  string `json:"observed"`
	Threshold string `json:"threshold"`
	Reason    string `json:"reason,omitempty"`
}

type ResultGateDecision struct {
	FinalStatus Status   `json:"final_status"`
	Outcome     Outcome  `json:"outcome"`
	Summary     string   `json:"summary"`
	Reasons     []string `json:"reasons"`
}

func EvaluateResultGates(result Result, policy ResultGatePolicy) (ResultGateEvaluation, error) {
	if err := ValidateResult(result); err != nil {
		return ResultGateEvaluation{}, err
	}
	evaluation, err := EvaluateMetricsGates(result.Metrics, policy)
	if err != nil {
		return ResultGateEvaluation{}, err
	}
	evaluation.CandidateEligible = evaluation.Enabled && evaluation.Passed && result.FinalStatus == StatusCompleted && result.Outcome != OutcomeNotExecuted
	return evaluation, nil
}

func EvaluateMetricsGates(metrics Metrics, policy ResultGatePolicy) (ResultGateEvaluation, error) {
	if err := ValidateResultGatePolicy(policy); err != nil {
		return ResultGateEvaluation{}, err
	}

	evaluation := ResultGateEvaluation{
		Enabled: policy.Enabled,
		Passed:  true,
	}
	if !policy.Enabled {
		return evaluation, nil
	}

	addCheck := func(check ResultGateCheck) {
		evaluation.Checks = append(evaluation.Checks, check)
		if check.Passed {
			return
		}
		evaluation.Passed = false
		if strings.TrimSpace(check.Reason) != "" {
			evaluation.Reasons = append(evaluation.Reasons, check.Reason)
		}
	}

	addCheck(ResultGateCheck{
		Name:      "min_trades",
		Passed:    metrics.Trades >= policy.MinTrades,
		Observed:  fmt.Sprintf("%d", metrics.Trades),
		Threshold: fmt.Sprintf(">=%d", policy.MinTrades),
		Reason:    reasonIf(metrics.Trades < policy.MinTrades, fmt.Sprintf("gate_min_trades_failed:%d<%d", metrics.Trades, policy.MinTrades)),
	})
	if policy.RequireCosts {
		missing := missingCostFlags(metrics)
		addCheck(ResultGateCheck{
			Name:      "conservative_costs",
			Passed:    len(missing) == 0,
			Observed:  costFlagsValue(metrics),
			Threshold: "fees=true,spread=true,slippage=true",
			Reason:    reasonIf(len(missing) > 0, "gate_conservative_costs_missing:"+strings.Join(missing, ",")),
		})
	}
	if policy.MinProfitFactor.GreaterThan(decimal.Zero) {
		check, err := profitFactorGateCheck(metrics, policy.MinProfitFactor)
		if err != nil {
			return ResultGateEvaluation{}, err
		}
		addCheck(check)
	}
	if policy.MinExpectancy.GreaterThan(decimal.Zero) {
		check, err := decimalMetricGateCheck("expectancy", metrics.Expectancy, policy.MinExpectancy, "gate_expectancy")
		if err != nil {
			return ResultGateEvaluation{}, err
		}
		addCheck(check)
	}
	if policy.MaxDrawdownPct > 0 {
		addCheck(ResultGateCheck{
			Name:      "max_drawdown_pct",
			Passed:    metrics.MaxDrawdownPct <= policy.MaxDrawdownPct,
			Observed:  formatGateFloat(metrics.MaxDrawdownPct),
			Threshold: "<=" + formatGateFloat(policy.MaxDrawdownPct),
			Reason: reasonIf(
				metrics.MaxDrawdownPct > policy.MaxDrawdownPct,
				"gate_max_drawdown_failed:"+formatGateFloat(metrics.MaxDrawdownPct)+">"+formatGateFloat(policy.MaxDrawdownPct),
			),
		})
	}
	if policy.RequireOutOfSample {
		addCheck(ResultGateCheck{
			Name:      "out_of_sample",
			Passed:    metrics.OutOfSample,
			Observed:  fmt.Sprintf("%t", metrics.OutOfSample),
			Threshold: "true",
			Reason:    reasonIf(!metrics.OutOfSample, "gate_out_of_sample_missing"),
		})
		if metrics.OutOfSample {
			addCheck(ResultGateCheck{
				Name:      "out_of_sample_trades",
				Passed:    metrics.OutOfSampleTrades > 0,
				Observed:  fmt.Sprintf("%d", metrics.OutOfSampleTrades),
				Threshold: ">0",
				Reason:    reasonIf(metrics.OutOfSampleTrades <= 0, "gate_out_of_sample_trades_missing"),
			})
		}
	}
	if policy.RequireWalkForward {
		addCheck(ResultGateCheck{
			Name:      "walk_forward",
			Passed:    metrics.WalkForward,
			Observed:  fmt.Sprintf("%t", metrics.WalkForward),
			Threshold: "true",
			Reason:    reasonIf(!metrics.WalkForward, "gate_walk_forward_missing"),
		})
	}
	if policy.RequireRegimeAnalysis {
		addCheck(ResultGateCheck{
			Name:      "regime_analysis",
			Passed:    metrics.RegimeAnalysisIncluded,
			Observed:  fmt.Sprintf("%t", metrics.RegimeAnalysisIncluded),
			Threshold: "true",
			Reason:    reasonIf(!metrics.RegimeAnalysisIncluded, "gate_regime_analysis_missing"),
		})
	}
	if evaluation.Passed {
		evaluation.Reasons = append(evaluation.Reasons, "research_gates_passed")
		evaluation.CandidateEligible = true
	}
	return evaluation, nil
}

func DecideResultFromGates(metrics Metrics, policy ResultGatePolicy, evaluation ResultGateEvaluation) (ResultGateDecision, error) {
	if err := ValidateResultGatePolicy(policy); err != nil {
		return ResultGateDecision{}, err
	}
	if !policy.Enabled {
		return ResultGateDecision{
			FinalStatus: StatusCompleted,
			Outcome:     OutcomeInconclusive,
			Summary:     "Fixed-horizon research backtest completed, but research gates are disabled; candidate decision was not made.",
			Reasons:     []string{"research_gates_disabled"},
		}, nil
	}

	incomplete := incompleteValidationReasons(metrics, policy)
	if len(incomplete) > 0 {
		return ResultGateDecision{
			FinalStatus: StatusCompleted,
			Outcome:     OutcomeInconclusive,
			Summary:     "Fixed-horizon research backtest completed, but required validation gates are incomplete; candidate decision was not made.",
			Reasons:     incomplete,
		}, nil
	}
	if evaluation.Passed {
		return ResultGateDecision{
			FinalStatus: StatusCompleted,
			Outcome:     OutcomeCandidate,
			Summary:     "Fixed-horizon research backtest passed configured conservative research gates; live trading remains disabled.",
			Reasons:     []string{"research_decision_candidate"},
		}, nil
	}
	return ResultGateDecision{
		FinalStatus: StatusCompleted,
		Outcome:     OutcomeRejected,
		Summary:     "Fixed-horizon research backtest completed required validation but failed configured conservative research gates.",
		Reasons:     []string{"research_decision_rejected_by_gates"},
	}, nil
}

func ValidateResultGatePolicy(policy ResultGatePolicy) error {
	var problems []string
	if policy.MinTrades < 0 {
		problems = append(problems, "min_trades must be greater than or equal to zero")
	}
	if policy.MinProfitFactor.IsNegative() {
		problems = append(problems, "min_profit_factor must be greater than or equal to zero")
	}
	if policy.MinExpectancy.IsNegative() {
		problems = append(problems, "min_expectancy must be greater than or equal to zero")
	}
	if policy.MaxDrawdownPct < 0 || policy.MaxDrawdownPct > 100 {
		problems = append(problems, "max_drawdown_pct must be between 0 and 100")
	}
	if len(problems) > 0 {
		return errors.New("research result gate policy validation failed: " + strings.Join(problems, "; "))
	}
	return nil
}

func incompleteValidationReasons(metrics Metrics, policy ResultGatePolicy) []string {
	var reasons []string
	if policy.RequireOutOfSample && !metrics.OutOfSample {
		reasons = append(reasons, "validation_incomplete:out_of_sample")
	}
	if policy.RequireWalkForward && metrics.WalkForwardFolds == 0 {
		reasons = append(reasons, "validation_incomplete:walk_forward")
	}
	if policy.RequireRegimeAnalysis && !metrics.RegimeAnalysisIncluded {
		reasons = append(reasons, "validation_incomplete:regime_analysis")
	}
	return reasons
}

func profitFactorGateCheck(metrics Metrics, threshold decimal.Decimal) (ResultGateCheck, error) {
	if metrics.ProfitFactorDefined {
		return decimalMetricGateCheck("profit_factor", metrics.ProfitFactor, threshold, "gate_profit_factor")
	}

	grossProfit, profitPresent, err := parseGateDecimal("gross_profit", metrics.GrossProfit)
	if err != nil {
		return ResultGateCheck{}, err
	}
	grossLoss, lossPresent, err := parseGateDecimal("gross_loss", metrics.GrossLoss)
	if err != nil {
		return ResultGateCheck{}, err
	}
	if metrics.Trades > 0 && profitPresent && lossPresent && grossProfit.GreaterThan(decimal.Zero) && grossLoss.Equal(decimal.Zero) {
		return ResultGateCheck{
			Name:      "profit_factor",
			Passed:    true,
			Observed:  "unbounded",
			Threshold: ">=" + threshold.String(),
		}, nil
	}
	return ResultGateCheck{
		Name:      "profit_factor",
		Passed:    false,
		Observed:  "undefined",
		Threshold: ">=" + threshold.String(),
		Reason:    "gate_profit_factor_undefined",
	}, nil
}

func decimalMetricGateCheck(name, raw string, threshold decimal.Decimal, reasonPrefix string) (ResultGateCheck, error) {
	value, present, err := parseGateDecimal(name, raw)
	if err != nil {
		return ResultGateCheck{}, err
	}
	if !present {
		return ResultGateCheck{
			Name:      name,
			Passed:    false,
			Observed:  "missing",
			Threshold: ">=" + threshold.String(),
			Reason:    reasonPrefix + "_missing",
		}, nil
	}
	passed := value.GreaterThanOrEqual(threshold)
	return ResultGateCheck{
		Name:      name,
		Passed:    passed,
		Observed:  value.String(),
		Threshold: ">=" + threshold.String(),
		Reason:    reasonIf(!passed, reasonPrefix+"_failed:"+value.String()+"<"+threshold.String()),
	}, nil
}

func parseGateDecimal(field, raw string) (decimal.Decimal, bool, error) {
	if strings.TrimSpace(raw) == "" {
		return decimal.Zero, false, nil
	}
	value, err := decimal.NewFromString(strings.TrimSpace(raw))
	if err != nil {
		return decimal.Zero, false, fmt.Errorf("metrics.%s must be a decimal string", field)
	}
	return value, true, nil
}

func missingCostFlags(metrics Metrics) []string {
	var missing []string
	if !metrics.FeesIncluded {
		missing = append(missing, "fees")
	}
	if !metrics.SpreadIncluded {
		missing = append(missing, "spread")
	}
	if !metrics.SlippageIncluded {
		missing = append(missing, "slippage")
	}
	return missing
}

func costFlagsValue(metrics Metrics) string {
	return fmt.Sprintf("fees=%t,spread=%t,slippage=%t", metrics.FeesIncluded, metrics.SpreadIncluded, metrics.SlippageIncluded)
}

func reasonIf(condition bool, reason string) string {
	if condition {
		return reason
	}
	return ""
}

func formatGateFloat(value float64) string {
	return strings.TrimRight(strings.TrimRight(fmt.Sprintf("%.10f", value), "0"), ".")
}
