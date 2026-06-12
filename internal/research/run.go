package research

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/VersoIt/Inquisitor/internal/marketdata"
)

type Status string

const (
	StatusPlanned   Status = "PLANNED"
	StatusRunning   Status = "RUNNING"
	StatusCompleted Status = "COMPLETED"
	StatusFailed    Status = "FAILED"
	StatusCancelled Status = "CANCELLED"
)

var (
	runIDPattern     = regexp.MustCompile(`^[A-Za-z0-9_-]{8,128}$`)
	symbolPattern    = regexp.MustCompile(`^[A-Z0-9]{3,32}$`)
	sha256HexPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)
)

type WriteStats struct {
	Inserted int
	Updated  int
}

type Run struct {
	RunID                   string
	HypothesisName          string
	HypothesisVersion       string
	HypothesisContentSHA256 string
	Status                  Status
	WindowStart             time.Time
	WindowEnd               time.Time
	PlannedAt               time.Time
	Symbols                 []string
	Intervals               []string
	Notes                   []string
}

type PlanInput struct {
	RunID                   string
	HypothesisName          string
	HypothesisVersion       string
	HypothesisContentSHA256 string
	WindowStart             time.Time
	WindowEnd               time.Time
	PlannedAt               time.Time
	Symbols                 []string
	Intervals               []string
	Notes                   []string
}

type Query struct {
	RunID             string
	HypothesisName    string
	HypothesisVersion string
	Status            Status
	Start             time.Time
	End               time.Time
	Limit             int
}

type Repository interface {
	UpsertRuns(ctx context.Context, runs []Run) (WriteStats, error)
	ListRuns(ctx context.Context, query Query) ([]Run, error)
}

func (s WriteStats) Total() int {
	return s.Inserted + s.Updated
}

func NewPlannedRun(input PlanInput) (Run, error) {
	run := Run{
		RunID:                   strings.TrimSpace(input.RunID),
		HypothesisName:          strings.TrimSpace(input.HypothesisName),
		HypothesisVersion:       strings.TrimSpace(input.HypothesisVersion),
		HypothesisContentSHA256: strings.TrimSpace(input.HypothesisContentSHA256),
		Status:                  StatusPlanned,
		WindowStart:             input.WindowStart.UTC(),
		WindowEnd:               input.WindowEnd.UTC(),
		PlannedAt:               input.PlannedAt.UTC(),
		Symbols:                 canonicalStrings(input.Symbols, strings.ToUpper),
		Intervals:               canonicalStrings(input.Intervals, strings.TrimSpace),
		Notes:                   canonicalStrings(input.Notes, strings.TrimSpace),
	}
	if err := ValidateRun(run); err != nil {
		return Run{}, err
	}
	return run, nil
}

func ValidateRuns(runs []Run) error {
	for index, run := range runs {
		if err := ValidateRun(run); err != nil {
			return fmt.Errorf("research_run[%d]: %w", index, err)
		}
	}
	return nil
}

func ValidateRun(run Run) error {
	var problems []string
	addRequired := func(field, value string) {
		if strings.TrimSpace(value) == "" {
			problems = append(problems, field+" is required")
		}
	}

	addRequired("run_id", run.RunID)
	addRequired("hypothesis_name", run.HypothesisName)
	addRequired("hypothesis_version", run.HypothesisVersion)
	addRequired("hypothesis_content_sha256", run.HypothesisContentSHA256)
	if run.RunID != "" && !runIDPattern.MatchString(run.RunID) {
		problems = append(problems, "run_id must be 8-128 url-safe characters")
	}
	if run.HypothesisContentSHA256 != "" && !sha256HexPattern.MatchString(run.HypothesisContentSHA256) {
		problems = append(problems, "hypothesis_content_sha256 must be lowercase sha256 hex")
	}
	if !KnownStatus(run.Status) {
		problems = append(problems, "status is unsupported")
	}
	if run.WindowStart.IsZero() {
		problems = append(problems, "window_start is required")
	}
	if run.WindowEnd.IsZero() {
		problems = append(problems, "window_end is required")
	}
	if !run.WindowStart.IsZero() && !run.WindowEnd.IsZero() && !run.WindowEnd.After(run.WindowStart) {
		problems = append(problems, "window_end must be after window_start")
	}
	if run.PlannedAt.IsZero() {
		problems = append(problems, "planned_at is required")
	}
	if !run.WindowEnd.IsZero() && !run.PlannedAt.IsZero() && run.WindowEnd.After(run.PlannedAt) {
		problems = append(problems, "window_end must not be after planned_at")
	}
	problems = append(problems, validateSymbols(run.Symbols)...)
	problems = append(problems, validateIntervals(run.Intervals)...)
	problems = append(problems, validateNotes(run.Notes)...)

	if len(problems) > 0 {
		return errors.New("research run validation failed: " + strings.Join(problems, "; "))
	}
	return nil
}

func ValidateQuery(query Query) error {
	if query.Status != "" && !KnownStatus(query.Status) {
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

func KnownStatus(status Status) bool {
	switch status {
	case StatusPlanned, StatusRunning, StatusCompleted, StatusFailed, StatusCancelled:
		return true
	default:
		return false
	}
}

func validateSymbols(symbols []string) []string {
	if len(symbols) == 0 {
		return []string{"symbols must not be empty"}
	}
	var problems []string
	seen := map[string]struct{}{}
	for i, symbol := range symbols {
		trimmed := strings.TrimSpace(symbol)
		normalized := strings.ToUpper(trimmed)
		if trimmed == "" {
			problems = append(problems, fmt.Sprintf("symbols[%d] must not be empty", i))
			continue
		}
		if trimmed != normalized {
			problems = append(problems, fmt.Sprintf("symbols[%d] must be uppercase", i))
		}
		if !symbolPattern.MatchString(normalized) {
			problems = append(problems, fmt.Sprintf("symbols[%d] must be uppercase alphanumeric exchange symbol", i))
		}
		if _, exists := seen[normalized]; exists {
			problems = append(problems, "symbols must not contain duplicates")
			continue
		}
		seen[normalized] = struct{}{}
	}
	return problems
}

func validateIntervals(intervals []string) []string {
	if len(intervals) == 0 {
		return []string{"intervals must not be empty"}
	}
	var problems []string
	seen := map[string]struct{}{}
	for i, interval := range intervals {
		normalized := strings.TrimSpace(interval)
		if normalized == "" {
			problems = append(problems, fmt.Sprintf("intervals[%d] must not be empty", i))
			continue
		}
		if _, err := marketdata.IntervalDuration(normalized); err != nil {
			problems = append(problems, fmt.Sprintf("intervals[%d] contains unsupported candle interval %s", i, normalized))
		}
		if _, exists := seen[strings.ToLower(normalized)]; exists {
			problems = append(problems, "intervals must not contain duplicates")
			continue
		}
		seen[strings.ToLower(normalized)] = struct{}{}
	}
	return problems
}

func validateNotes(notes []string) []string {
	var problems []string
	for i, note := range notes {
		if strings.TrimSpace(note) == "" {
			problems = append(problems, fmt.Sprintf("notes[%d] must not be empty", i))
		}
	}
	return problems
}

func canonicalStrings(values []string, normalize func(string) string) []string {
	if values == nil {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, normalize(strings.TrimSpace(value)))
	}
	return out
}
