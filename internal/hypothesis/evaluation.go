package hypothesis

import (
	"errors"
	"fmt"
	"strings"

	"github.com/shopspring/decimal"
)

type FeatureValue struct {
	Value          decimal.Decimal
	Complete       bool
	MissingReasons []string
}

type FeatureSnapshot struct {
	values map[string]FeatureValue
}

type SignalEvaluation struct {
	Name     string
	Passed   bool
	Skipped  bool
	Reason   string
	Left     decimal.Decimal
	Right    decimal.Decimal
	Operator string
}

type Evaluation struct {
	Passed       bool
	Evaluated    int
	PassedRules  int
	FailedRules  int
	SkippedRules int
	Signals      []SignalEvaluation
	Reasons      []string
}

func NewFeatureSnapshot(values map[string]FeatureValue) FeatureSnapshot {
	snapshot := FeatureSnapshot{values: make(map[string]FeatureValue, len(values))}
	for path, value := range values {
		snapshot.values[normalizeLower(path)] = FeatureValue{
			Value:          value.Value,
			Complete:       value.Complete,
			MissingReasons: canonicalStrings(value.MissingReasons, strings.TrimSpace),
		}
	}
	return snapshot
}

func EvaluateSignals(spec Hypothesis, current FeatureSnapshot, previous FeatureSnapshot) (Evaluation, error) {
	spec = canonicalize(spec)
	if err := spec.Validate(); err != nil {
		return Evaluation{}, err
	}

	evaluation := Evaluation{Signals: make([]SignalEvaluation, 0, len(spec.Signals))}
	for _, rule := range spec.Signals {
		result, err := evaluateSignal(rule, current, previous)
		if err != nil {
			return Evaluation{}, err
		}
		evaluation.Signals = append(evaluation.Signals, result)
		evaluation.Evaluated++
		switch {
		case result.Skipped:
			evaluation.SkippedRules++
			evaluation.Reasons = append(evaluation.Reasons, result.Reason)
		case result.Passed:
			evaluation.PassedRules++
		default:
			evaluation.FailedRules++
			evaluation.Reasons = append(evaluation.Reasons, result.Reason)
		}
	}
	evaluation.Passed = evaluation.Evaluated > 0 && evaluation.FailedRules == 0 && evaluation.SkippedRules == 0
	return evaluation, nil
}

func evaluateSignal(rule SignalRule, current FeatureSnapshot, previous FeatureSnapshot) (SignalEvaluation, error) {
	operator := normalizeLower(rule.Operator)
	result := SignalEvaluation{
		Name:     strings.TrimSpace(rule.Name),
		Operator: operator,
	}

	left, reason, ok := resolveFeature(rule.Feature, current)
	if !ok {
		result.Skipped = true
		result.Reason = reason
		return result, nil
	}
	right, reason, ok, err := resolveOperand(rule.Value.String(), current)
	if err != nil {
		return SignalEvaluation{}, fmt.Errorf("signal %q value: %w", rule.Name, err)
	}
	if !ok {
		result.Skipped = true
		result.Reason = reason
		return result, nil
	}

	result.Left = left
	result.Right = right
	if isCrossOperator(operator) {
		previousLeft, reason, ok := resolveFeature(rule.Feature, previous)
		if !ok {
			result.Skipped = true
			result.Reason = "previous_" + reason
			return result, nil
		}
		previousRight, reason, ok, err := resolveOperand(rule.Value.String(), previous)
		if err != nil {
			return SignalEvaluation{}, fmt.Errorf("signal %q previous value: %w", rule.Name, err)
		}
		if !ok {
			result.Skipped = true
			result.Reason = "previous_" + reason
			return result, nil
		}
		result.Passed = compareCross(operator, previousLeft, left, previousRight, right)
	} else {
		result.Passed, err = compare(operator, left, right)
		if err != nil {
			return SignalEvaluation{}, fmt.Errorf("signal %q operator: %w", rule.Name, err)
		}
	}
	if !result.Passed {
		result.Reason = "signal_rule_failed:" + result.Name
	}
	return result, nil
}

func resolveFeature(path string, snapshot FeatureSnapshot) (decimal.Decimal, string, bool) {
	key := normalizeLower(path)
	value, ok := snapshot.values[key]
	if !ok {
		return decimal.Zero, "feature_missing:" + key, false
	}
	if !value.Complete {
		reason := "feature_incomplete:" + key
		if len(value.MissingReasons) > 0 {
			reason += ":" + strings.Join(value.MissingReasons, ",")
		}
		return decimal.Zero, reason, false
	}
	return value.Value, "", true
}

func resolveOperand(raw string, snapshot FeatureSnapshot) (decimal.Decimal, string, bool, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return decimal.Zero, "", false, errors.New("must not be empty")
	}
	if featurePath.MatchString(value) {
		resolved, reason, ok := resolveFeature(value, snapshot)
		return resolved, reason, ok, nil
	}
	parsed, err := decimal.NewFromString(value)
	if err != nil {
		return decimal.Zero, "", false, fmt.Errorf("must be a decimal constant or feature path")
	}
	return parsed, "", true, nil
}

func compare(operator string, left decimal.Decimal, right decimal.Decimal) (bool, error) {
	switch operator {
	case ">":
		return left.GreaterThan(right), nil
	case ">=":
		return left.GreaterThanOrEqual(right), nil
	case "<":
		return left.LessThan(right), nil
	case "<=":
		return left.LessThanOrEqual(right), nil
	case "==":
		return left.Equal(right), nil
	case "!=":
		return !left.Equal(right), nil
	default:
		return false, fmt.Errorf("unsupported comparison operator %q", operator)
	}
}

func isCrossOperator(operator string) bool {
	return operator == "crosses_above" || operator == "crosses_below"
}

func compareCross(operator string, previousLeft, currentLeft, previousRight, currentRight decimal.Decimal) bool {
	switch operator {
	case "crosses_above":
		return previousLeft.LessThanOrEqual(previousRight) && currentLeft.GreaterThan(currentRight)
	case "crosses_below":
		return previousLeft.GreaterThanOrEqual(previousRight) && currentLeft.LessThan(currentRight)
	default:
		return false
	}
}
