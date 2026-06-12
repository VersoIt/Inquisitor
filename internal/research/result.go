package research

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

type Outcome string

const (
	OutcomeNotExecuted  Outcome = "NOT_EXECUTED"
	OutcomeInconclusive Outcome = "INCONCLUSIVE"
	OutcomeRejected     Outcome = "REJECTED"
	OutcomeCandidate    Outcome = "CANDIDATE"
)

type Metrics struct {
	Trades                 int  `json:"trades"`
	FeesIncluded           bool `json:"fees_included"`
	SpreadIncluded         bool `json:"spread_included"`
	SlippageIncluded       bool `json:"slippage_included"`
	OutOfSample            bool `json:"out_of_sample"`
	WalkForward            bool `json:"walk_forward"`
	RegimeAnalysisIncluded bool `json:"regime_analysis_included"`
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

func validateReasons(reasons []string) []string {
	var problems []string
	for i, reason := range reasons {
		if strings.TrimSpace(reason) == "" {
			problems = append(problems, fmt.Sprintf("reasons[%d] must not be empty", i))
		}
	}
	return problems
}
