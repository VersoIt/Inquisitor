package live

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

const LiveLoopAuditArtifactSchemaVersion = "inquisitor.live_loop_audit.v1"

const DefaultLiveLoopAuditArtifactMaxAge = 10 * time.Minute

type LiveLoopAuditArtifact struct {
	SchemaVersion string                       `json:"schema_version"`
	CreatedAt     time.Time                    `json:"created_at"`
	ConfigPath    string                       `json:"config_path"`
	Query         LiveLoopAuditArtifactQuery   `json:"query"`
	Summary       LiveLoopAuditArtifactSummary `json:"summary"`
	Runs          []LiveLoopAuditArtifactRun   `json:"runs"`
}

type LiveLoopAuditArtifactQuery struct {
	RunID             string            `json:"run_id,omitempty"`
	Status            LiveLoopRunStatus `json:"status,omitempty"`
	Limit             int               `json:"limit"`
	IncludeIterations bool              `json:"include_iterations"`
}

type LiveLoopAuditArtifactSummary struct {
	Total                  int                       `json:"total"`
	Running                int                       `json:"running"`
	Completed              int                       `json:"completed"`
	Failed                 int                       `json:"failed"`
	ReviewStatus           LiveLoopAuditReviewStatus `json:"review_status"`
	ReviewRunID            string                    `json:"review_run_id,omitempty"`
	ReviewReason           string                    `json:"review_reason"`
	OperatorActionRequired bool                      `json:"operator_action_required"`
}

type LiveLoopAuditArtifactRun struct {
	RunID                 string                           `json:"run_id"`
	StartedAt             time.Time                        `json:"started_at"`
	FinishedAt            *time.Time                       `json:"finished_at,omitempty"`
	Status                LiveLoopRunStatus                `json:"status"`
	MaxIterations         int                              `json:"max_iterations"`
	MaxRuntime            string                           `json:"max_runtime"`
	IterationTimeout      string                           `json:"iteration_timeout"`
	PreflightChecked      bool                             `json:"preflight_checked"`
	PreflightReady        bool                             `json:"preflight_ready"`
	IterationsAttempted   int                              `json:"iterations_attempted"`
	IterationsSucceeded   int                              `json:"iterations_succeeded"`
	StopReason            string                           `json:"stop_reason,omitempty"`
	StopDetails           string                           `json:"stop_details,omitempty"`
	Error                 string                           `json:"error,omitempty"`
	CompletedWithinBounds bool                             `json:"completed_within_bounds"`
	Iterations            []LiveLoopAuditArtifactIteration `json:"iterations,omitempty"`
}

type LiveLoopAuditArtifactIteration struct {
	Iteration         int                          `json:"iteration"`
	Action            LiveLoopAuditIterationAction `json:"action"`
	RequestStop       bool                         `json:"request_stop"`
	Reason            string                       `json:"reason,omitempty"`
	DecisionID        string                       `json:"decision_id,omitempty"`
	SubmissionID      string                       `json:"submission_id,omitempty"`
	ClientOrderID     string                       `json:"client_order_id,omitempty"`
	ExchangeSubmitted bool                         `json:"exchange_submitted"`
	AlreadySubmitted  bool                         `json:"already_submitted"`
	StartedAt         time.Time                    `json:"started_at"`
	FinishedAt        time.Time                    `json:"finished_at"`
}

func ValidateLiveLoopAuditArtifact(artifact LiveLoopAuditArtifact) error {
	var problems []string
	if artifact.SchemaVersion != LiveLoopAuditArtifactSchemaVersion {
		problems = append(problems, "schema_version must be "+LiveLoopAuditArtifactSchemaVersion)
	}
	if artifact.CreatedAt.IsZero() {
		problems = append(problems, "created_at is required")
	}
	if strings.TrimSpace(artifact.ConfigPath) == "" {
		problems = append(problems, "config_path is required")
	} else if artifact.ConfigPath != strings.TrimSpace(artifact.ConfigPath) {
		problems = append(problems, "config_path must be trimmed")
	}
	query := LiveLoopAuditQuery{
		RunID:             artifact.Query.RunID,
		Status:            artifact.Query.Status,
		Limit:             artifact.Query.Limit,
		IncludeIterations: artifact.Query.IncludeIterations,
	}
	if err := ValidateLiveLoopAuditQuery(query); err != nil {
		problems = append(problems, err.Error())
	}
	if artifact.Query.Limit <= 0 {
		problems = append(problems, "query.limit must be positive")
	}
	if artifact.Query.Limit > 0 && len(artifact.Runs) > artifact.Query.Limit {
		problems = append(problems, "runs length must not exceed query.limit")
	}
	if !KnownLiveLoopAuditReviewStatus(artifact.Summary.ReviewStatus) {
		problems = append(problems, "summary.review_status must be CLEAR, REVIEW, or BLOCKED")
	}
	if artifact.Summary.ReviewReason != strings.TrimSpace(artifact.Summary.ReviewReason) {
		problems = append(problems, "summary.review_reason must be trimmed")
	}

	runs, runProblems := liveLoopAuditArtifactRuns(artifact.Runs)
	problems = append(problems, runProblems...)
	if len(runProblems) == 0 {
		problems = append(problems, liveLoopAuditArtifactQueryProblems(artifact.Query, runs)...)
		problems = append(problems, liveLoopAuditArtifactSummaryProblems(artifact.Summary, runs)...)
	}
	if len(problems) > 0 {
		return errors.New("live-loop audit artifact validation failed: " + strings.Join(problems, "; "))
	}
	return nil
}

func ValidateLiveLoopAuditArtifactFreshness(
	artifact LiveLoopAuditArtifact,
	now time.Time,
	maxAge time.Duration,
) error {
	if err := ValidateLiveLoopAuditArtifact(artifact); err != nil {
		return err
	}
	var problems []string
	if now.IsZero() {
		problems = append(problems, "now is required")
	}
	if maxAge <= 0 {
		problems = append(problems, "max_age must be positive")
	}
	if len(problems) == 0 {
		age := now.UTC().Sub(artifact.CreatedAt.UTC())
		if age < 0 {
			problems = append(problems, "created_at must not be in the future")
		}
		if age > maxAge {
			problems = append(problems, fmt.Sprintf("artifact is stale: age=%s max=%s", age, maxAge))
		}
	}
	if len(problems) > 0 {
		return errors.New("live-loop audit artifact freshness validation failed: " + strings.Join(problems, "; "))
	}
	return nil
}

func liveLoopAuditArtifactRuns(artifactRuns []LiveLoopAuditArtifactRun) ([]LiveLoopRunAudit, []string) {
	runs := make([]LiveLoopRunAudit, 0, len(artifactRuns))
	var problems []string
	for index, artifactRun := range artifactRuns {
		run, runProblems := liveLoopAuditArtifactRun(artifactRun)
		for _, problem := range runProblems {
			problems = append(problems, fmt.Sprintf("runs[%d]: %s", index, problem))
		}
		if len(runProblems) == 0 {
			runs = append(runs, run)
		}
	}
	if len(problems) == 0 {
		if err := ValidateLiveLoopRunAudits(runs); err != nil {
			problems = append(problems, err.Error())
		}
	}
	return runs, problems
}

func liveLoopAuditArtifactRun(artifactRun LiveLoopAuditArtifactRun) (LiveLoopRunAudit, []string) {
	var problems []string
	maxRuntime, err := time.ParseDuration(strings.TrimSpace(artifactRun.MaxRuntime))
	if err != nil || maxRuntime <= 0 {
		problems = append(problems, "max_runtime must be a positive duration")
	}
	iterationTimeout, err := time.ParseDuration(strings.TrimSpace(artifactRun.IterationTimeout))
	if err != nil || iterationTimeout <= 0 {
		problems = append(problems, "iteration_timeout must be a positive duration")
	}
	if artifactRun.MaxRuntime != strings.TrimSpace(artifactRun.MaxRuntime) {
		problems = append(problems, "max_runtime must be trimmed")
	}
	if artifactRun.IterationTimeout != strings.TrimSpace(artifactRun.IterationTimeout) {
		problems = append(problems, "iteration_timeout must be trimmed")
	}

	run := LiveLoopRunAudit{
		RunID:                 artifactRun.RunID,
		StartedAt:             artifactRun.StartedAt,
		MaxIterations:         artifactRun.MaxIterations,
		MaxRuntime:            maxRuntime,
		IterationTimeout:      iterationTimeout,
		Status:                artifactRun.Status,
		PreflightChecked:      artifactRun.PreflightChecked,
		PreflightReady:        artifactRun.PreflightReady,
		IterationsAttempted:   artifactRun.IterationsAttempted,
		IterationsSucceeded:   artifactRun.IterationsSucceeded,
		StopReason:            artifactRun.StopReason,
		StopDetails:           artifactRun.StopDetails,
		Error:                 artifactRun.Error,
		CompletedWithinBounds: artifactRun.CompletedWithinBounds,
	}
	if artifactRun.FinishedAt != nil {
		run.FinishedAt = artifactRun.FinishedAt.UTC()
	}
	for _, artifactIteration := range artifactRun.Iterations {
		run.Iterations = append(run.Iterations, LiveLoopIterationAudit{
			RunID:             artifactRun.RunID,
			RunStartedAt:      artifactRun.StartedAt,
			Iteration:         artifactIteration.Iteration,
			Action:            artifactIteration.Action,
			RequestStop:       artifactIteration.RequestStop,
			Reason:            artifactIteration.Reason,
			DecisionID:        artifactIteration.DecisionID,
			SubmissionID:      artifactIteration.SubmissionID,
			ClientOrderID:     artifactIteration.ClientOrderID,
			ExchangeSubmitted: artifactIteration.ExchangeSubmitted,
			AlreadySubmitted:  artifactIteration.AlreadySubmitted,
			StartedAt:         artifactIteration.StartedAt,
			FinishedAt:        artifactIteration.FinishedAt,
		})
	}
	return run, problems
}

func liveLoopAuditArtifactQueryProblems(query LiveLoopAuditArtifactQuery, runs []LiveLoopRunAudit) []string {
	var problems []string
	runID := strings.TrimSpace(query.RunID)
	for index, run := range runs {
		if runID != "" && run.RunID != runID {
			problems = append(problems, fmt.Sprintf("runs[%d].run_id must match query.run_id", index))
		}
		if query.Status != "" && run.Status != query.Status {
			problems = append(problems, fmt.Sprintf("runs[%d].status must match query.status", index))
		}
		if !query.IncludeIterations && len(run.Iterations) > 0 {
			problems = append(problems, fmt.Sprintf("runs[%d].iterations must be empty when query.include_iterations=false", index))
		}
	}
	return problems
}

func liveLoopAuditArtifactSummaryProblems(summary LiveLoopAuditArtifactSummary, runs []LiveLoopRunAudit) []string {
	var problems []string
	var total, running, completed, failed int
	for _, run := range runs {
		total++
		switch run.Status {
		case LiveLoopRunStatusRunning:
			running++
		case LiveLoopRunStatusCompleted:
			completed++
		case LiveLoopRunStatusFailed:
			failed++
		}
	}
	if summary.Total != total {
		problems = append(problems, "summary.total must match runs")
	}
	if summary.Running != running {
		problems = append(problems, "summary.running must match runs")
	}
	if summary.Completed != completed {
		problems = append(problems, "summary.completed must match runs")
	}
	if summary.Failed != failed {
		problems = append(problems, "summary.failed must match runs")
	}
	review, err := SummarizeLiveLoopAuditReview(runs)
	if err != nil {
		problems = append(problems, err.Error())
		return problems
	}
	if summary.ReviewStatus != review.Status {
		problems = append(problems, "summary.review_status must match runs")
	}
	if summary.ReviewRunID != review.RunID {
		problems = append(problems, "summary.review_run_id must match runs")
	}
	if summary.ReviewReason != review.Reason {
		problems = append(problems, "summary.review_reason must match runs")
	}
	if summary.OperatorActionRequired != review.OperatorActionRequired() {
		problems = append(problems, "summary.operator_action_required must match review_status")
	}
	return problems
}
