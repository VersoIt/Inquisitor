package hypothesis

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/VersoIt/Inquisitor/internal/marketdata"
	"github.com/VersoIt/Inquisitor/internal/regime"
)

const minImportedHypothesisTrades = 100

var (
	symbolPattern = regexp.MustCompile(`^[A-Z0-9]{3,32}$`)
	featurePath   = regexp.MustCompile(`^[a-z][a-z0-9_]*(\.[a-z][a-z0-9_]*)+$`)
)

type Problem struct {
	Field   string
	Message string
}

type ValidationError struct {
	Problems []Problem
}

func (e ValidationError) Error() string {
	if len(e.Problems) == 0 {
		return "hypothesis validation failed"
	}
	parts := make([]string, 0, len(e.Problems))
	for _, problem := range e.Problems {
		parts = append(parts, problem.Field+" "+problem.Message)
	}
	return "hypothesis validation failed: " + strings.Join(parts, "; ")
}

func (h Hypothesis) Validate() error {
	var problems []Problem

	problems = appendRequired(problems, "name", h.Name)
	problems = appendRequired(problems, "version", h.Version)
	problems = appendRequired(problems, "description", h.Description)
	problems = appendRequired(problems, "thesis", h.Thesis)

	if normalizedStatus(h.Status) != string(StatusDraft) {
		problems = append(problems, Problem{Field: "status", Message: "must be DRAFT for import"})
	}
	if !oneOf(normalizedDirection(h.Direction), string(DirectionLong), string(DirectionShort), string(DirectionBoth)) {
		problems = append(problems, Problem{Field: "direction", Message: "must be one of LONG, SHORT, BOTH"})
	}

	problems = append(problems, validateMarket(h.Market)...)
	problems = append(problems, validateRegime(h.Regime)...)
	problems = append(problems, validateSignals(h.Signals)...)
	problems = append(problems, validateRisk(h.Risk)...)
	problems = append(problems, validateResearchGates(h.Validation)...)
	problems = append(problems, validateCosts(h.Costs)...)
	problems = append(problems, validateUniqueNonEmpty("tags", h.Tags, normalizeLower, false)...)

	if len(problems) > 0 {
		return ValidationError{Problems: problems}
	}
	return nil
}

func validateMarket(scope MarketScope) []Problem {
	var problems []Problem
	if strings.ToLower(strings.TrimSpace(scope.Exchange)) != "bybit" {
		problems = append(problems, Problem{Field: "market.exchange", Message: "must be bybit in the current research scope"})
	}
	if strings.ToLower(strings.TrimSpace(scope.Category)) != "linear" {
		problems = append(problems, Problem{Field: "market.category", Message: "must be linear in the current research scope"})
	}
	if len(scope.Symbols) == 0 {
		problems = append(problems, Problem{Field: "market.symbols", Message: "must not be empty"})
	}
	problems = append(problems, validateUniqueNonEmpty("market.symbols", scope.Symbols, normalizeUpper, true)...)
	for i, symbol := range scope.Symbols {
		trimmed := strings.TrimSpace(symbol)
		if trimmed == "" {
			continue
		}
		if !symbolPattern.MatchString(trimmed) {
			problems = append(problems, Problem{
				Field:   fmt.Sprintf("market.symbols[%d]", i),
				Message: "must be uppercase alphanumeric exchange symbol",
			})
		}
	}

	if len(scope.Intervals) == 0 {
		problems = append(problems, Problem{Field: "market.intervals", Message: "must not be empty"})
	}
	problems = append(problems, validateUniqueNonEmpty("market.intervals", scope.Intervals, normalizeLower, true)...)
	for i, interval := range scope.Intervals {
		trimmed := strings.TrimSpace(interval)
		if trimmed == "" {
			continue
		}
		if _, err := marketdata.IntervalDuration(trimmed); err != nil {
			problems = append(problems, Problem{
				Field:   fmt.Sprintf("market.intervals[%d]", i),
				Message: "contains unsupported candle interval " + trimmed,
			})
		}
	}
	return problems
}

func validateRegime(scope RegimeScope) []Problem {
	var problems []Problem
	if len(scope.Allowed) == 0 {
		problems = append(problems, Problem{Field: "regime.allowed", Message: "must not be empty"})
	}
	problems = append(problems, validateUniqueNonEmpty("regime.allowed", scope.Allowed, normalizeUpper, true)...)
	problems = append(problems, validateUniqueNonEmpty("regime.blocked", scope.Blocked, normalizeUpper, false)...)

	allowed := map[string]struct{}{}
	for i, value := range scope.Allowed {
		normalized := normalizeUpper(value)
		if normalized == "" {
			continue
		}
		if !knownRegime(normalized) {
			problems = append(problems, Problem{Field: fmt.Sprintf("regime.allowed[%d]", i), Message: "contains unknown regime " + normalized})
		}
		if normalized == string(regime.RegimeNoTrade) {
			problems = append(problems, Problem{Field: fmt.Sprintf("regime.allowed[%d]", i), Message: "must not allow NO_TRADE"})
		}
		allowed[normalized] = struct{}{}
	}

	blockedNoTrade := false
	for i, value := range scope.Blocked {
		normalized := normalizeUpper(value)
		if normalized == "" {
			continue
		}
		if !knownRegime(normalized) {
			problems = append(problems, Problem{Field: fmt.Sprintf("regime.blocked[%d]", i), Message: "contains unknown regime " + normalized})
		}
		if normalized == string(regime.RegimeNoTrade) {
			blockedNoTrade = true
		}
		if _, exists := allowed[normalized]; exists {
			problems = append(problems, Problem{Field: fmt.Sprintf("regime.blocked[%d]", i), Message: "must not also be allowed"})
		}
	}
	if !blockedNoTrade {
		problems = append(problems, Problem{Field: "regime.blocked", Message: "must explicitly block NO_TRADE"})
	}
	return problems
}

func validateSignals(signals []SignalRule) []Problem {
	if len(signals) == 0 {
		return []Problem{{Field: "signals", Message: "must not be empty"}}
	}

	var problems []Problem
	seenNames := map[string]struct{}{}
	for i, signal := range signals {
		prefix := fmt.Sprintf("signals[%d]", i)
		problems = appendRequired(problems, prefix+".name", signal.Name)
		problems = appendRequired(problems, prefix+".description", signal.Description)
		problems = appendRequired(problems, prefix+".feature", signal.Feature)
		problems = appendRequired(problems, prefix+".operator", signal.Operator)
		problems = appendRequired(problems, prefix+".value", signal.Value.String())

		nameKey := normalizeLower(signal.Name)
		if nameKey != "" {
			if _, exists := seenNames[nameKey]; exists {
				problems = append(problems, Problem{Field: prefix + ".name", Message: "must be unique"})
			}
			seenNames[nameKey] = struct{}{}
		}
		if trimmedFeature := strings.TrimSpace(signal.Feature); trimmedFeature != "" && !featurePath.MatchString(trimmedFeature) {
			problems = append(problems, Problem{Field: prefix + ".feature", Message: "must be a dotted lowercase feature path"})
		}
		if !allowedOperator(signal.Operator) {
			problems = append(problems, Problem{Field: prefix + ".operator", Message: "must be a supported comparison operator"})
		}
	}
	return problems
}

func validateRisk(risk RiskRules) []Problem {
	var problems []Problem
	if risk.MaxRiskPerTradePct <= 0 || risk.MaxRiskPerTradePct > 1 {
		problems = append(problems, Problem{Field: "risk.max_risk_per_trade_pct", Message: "must be greater than 0 and no more than 1"})
	}
	if risk.MinConfidence < 50 || risk.MinConfidence > 100 {
		problems = append(problems, Problem{Field: "risk.min_confidence", Message: "must be between 50 and 100"})
	}
	if !risk.RequireStopLoss {
		problems = append(problems, Problem{Field: "risk.require_stop_loss", Message: "must be true"})
	}
	return problems
}

func validateResearchGates(validation ValidationRules) []Problem {
	var problems []Problem
	if validation.MinTrades < minImportedHypothesisTrades {
		problems = append(problems, Problem{
			Field:   "validation.min_trades",
			Message: fmt.Sprintf("must be at least %d", minImportedHypothesisTrades),
		})
	}
	if !validation.RequireOutOfSample {
		problems = append(problems, Problem{Field: "validation.require_out_of_sample", Message: "must be true"})
	}
	if !validation.RequireWalkForward {
		problems = append(problems, Problem{Field: "validation.require_walk_forward", Message: "must be true"})
	}
	if !validation.RequireRegimeAnalysis {
		problems = append(problems, Problem{Field: "validation.require_regime_analysis", Message: "must be true"})
	}
	return problems
}

func validateCosts(costs CostRules) []Problem {
	var problems []Problem
	if !costs.IncludeFees {
		problems = append(problems, Problem{Field: "costs.include_fees", Message: "must be true"})
	}
	if !costs.IncludeSpread {
		problems = append(problems, Problem{Field: "costs.include_spread", Message: "must be true"})
	}
	if !costs.IncludeSlippage {
		problems = append(problems, Problem{Field: "costs.include_slippage", Message: "must be true"})
	}
	return problems
}

func appendRequired(problems []Problem, field, value string) []Problem {
	if strings.TrimSpace(value) == "" {
		return append(problems, Problem{Field: field, Message: "is required"})
	}
	return problems
}

func validateUniqueNonEmpty(field string, values []string, normalize func(string) string, requireValues bool) []Problem {
	if !requireValues && len(values) == 0 {
		return nil
	}

	var problems []Problem
	seen := map[string]struct{}{}
	for i, value := range values {
		normalized := normalize(value)
		if normalized == "" {
			problems = append(problems, Problem{Field: fmt.Sprintf("%s[%d]", field, i), Message: "must not be empty"})
			continue
		}
		if _, exists := seen[normalized]; exists {
			problems = append(problems, Problem{Field: field, Message: "must not contain duplicates"})
			continue
		}
		seen[normalized] = struct{}{}
	}
	return problems
}

func knownRegime(value string) bool {
	switch regime.Regime(value) {
	case regime.RegimeTrendUp,
		regime.RegimeTrendDown,
		regime.RegimeRange,
		regime.RegimeBreakoutSetup,
		regime.RegimeHighVol,
		regime.RegimeChaos,
		regime.RegimeNoTrade:
		return true
	default:
		return false
	}
}

func allowedOperator(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case ">", ">=", "<", "<=", "==", "!=", "crosses_above", "crosses_below":
		return true
	default:
		return false
	}
}

func normalizedStatus(status Status) string {
	return normalizeUpper(string(status))
}

func normalizedDirection(direction Direction) string {
	return normalizeUpper(string(direction))
}

func normalizeUpper(value string) string {
	return strings.ToUpper(strings.TrimSpace(value))
}

func normalizeLower(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func oneOf(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}
