package live

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	maxLiveLoopIterations = 100
	maxLiveLoopRuntime    = 24 * time.Hour
)

type LiveLoopIterationAction string

const (
	LiveLoopIterationActionNone      LiveLoopIterationAction = "NONE"
	LiveLoopIterationActionSubmitted LiveLoopIterationAction = "SUBMITTED"
	LiveLoopIterationActionStop      LiveLoopIterationAction = "STOP"
)

type LiveLoopStopReason string

const (
	LiveLoopStopNone               LiveLoopStopReason = ""
	LiveLoopStopMaxIterations      LiveLoopStopReason = "MAX_ITERATIONS"
	LiveLoopStopMaxRuntime         LiveLoopStopReason = "MAX_RUNTIME"
	LiveLoopStopPreflightFailed    LiveLoopStopReason = "PREFLIGHT_FAILED"
	LiveLoopStopSafetyCheckError   LiveLoopStopReason = "SAFETY_CHECK_ERROR"
	LiveLoopStopKillSwitchActive   LiveLoopStopReason = "KILL_SWITCH_ACTIVE"
	LiveLoopStopIterationError     LiveLoopStopReason = "ITERATION_ERROR"
	LiveLoopStopIterationRequested LiveLoopStopReason = "ITERATION_REQUESTED"
)

type LiveLoopIterationRequest struct {
	RunID     string
	Iteration int
	StartedAt time.Time
	Deadline  time.Time
}

type LiveLoopIterationResult struct {
	RunID      string
	Iteration  int
	Action     LiveLoopIterationAction
	Reason     string
	StartedAt  time.Time
	FinishedAt time.Time
}

type LiveLoopIterationRunner interface {
	RunLiveLoopIteration(ctx context.Context, req LiveLoopIterationRequest) (LiveLoopIterationResult, error)
}

type RunBoundedLiveLoopRequest struct {
	RunID            string
	Preflight        PreflightLiveStartupRequest
	MaxIterations    int
	MaxRuntime       time.Duration
	IterationTimeout time.Duration
}

type RunBoundedLiveLoopResult struct {
	RunID                 string
	StartedAt             time.Time
	FinishedAt            time.Time
	PreflightChecked      bool
	Preflight             PreflightLiveStartupResult
	Iterations            []LiveLoopIterationResult
	IterationsAttempted   int
	IterationsSucceeded   int
	StopReason            LiveLoopStopReason
	StopDetails           string
	CompletedWithinBounds bool
}

func (s *Service) RunBoundedLiveLoop(ctx context.Context, req RunBoundedLiveLoopRequest) (RunBoundedLiveLoopResult, error) {
	if err := ctx.Err(); err != nil {
		return RunBoundedLiveLoopResult{}, err
	}
	result := RunBoundedLiveLoopResult{RunID: strings.TrimSpace(req.RunID)}
	if err := s.requireLiveLoopDependencies(); err != nil {
		return result, err
	}
	if err := validateRunBoundedLiveLoopRequest(req); err != nil {
		return result, err
	}

	startedAt := s.clock.Now()
	result.RunID = strings.TrimSpace(req.RunID)
	result.StartedAt = startedAt
	runtimeDeadline := startedAt.Add(req.MaxRuntime)

	preflight, err := s.PreflightLiveStartup(ctx, req.Preflight)
	result.PreflightChecked = true
	result.Preflight = preflight
	if err != nil {
		result.StopReason = LiveLoopStopPreflightFailed
		result.StopDetails = err.Error()
		result.FinishedAt = s.clock.Now()
		return result, fmt.Errorf("live loop startup preflight failed: %w", err)
	}

	for iteration := 1; iteration <= req.MaxIterations; iteration++ {
		if err := ctx.Err(); err != nil {
			result.FinishedAt = s.clock.Now()
			return result, err
		}
		now := s.clock.Now()
		if !now.Before(runtimeDeadline) {
			result.StopReason = LiveLoopStopMaxRuntime
			result.StopDetails = "max live loop runtime reached before next iteration"
			result.FinishedAt = now
			result.CompletedWithinBounds = true
			return result, nil
		}

		state, err := s.killSwitch.CurrentKillSwitchState(ctx)
		if err != nil {
			result.StopReason = LiveLoopStopSafetyCheckError
			result.StopDetails = err.Error()
			result.FinishedAt = s.clock.Now()
			return result, fmt.Errorf("load kill switch before live loop iteration %d: %w", iteration, err)
		}
		if state.Active {
			result.StopReason = LiveLoopStopKillSwitchActive
			result.StopDetails = fmt.Sprintf("reason=%q source=%q", state.Reason, state.Source)
			result.FinishedAt = s.clock.Now()
			return result, fmt.Errorf("live loop stopped by active kill switch before iteration %d: %s", iteration, result.StopDetails)
		}

		iterationStartedAt := s.clock.Now()
		remainingRuntime := runtimeDeadline.Sub(iterationStartedAt)
		if remainingRuntime <= 0 {
			result.StopReason = LiveLoopStopMaxRuntime
			result.StopDetails = "max live loop runtime reached before next iteration"
			result.FinishedAt = iterationStartedAt
			result.CompletedWithinBounds = true
			return result, nil
		}
		iterationTimeout := boundedIterationTimeout(req.IterationTimeout, remainingRuntime)
		iterationCtx, cancel := context.WithTimeout(ctx, iterationTimeout)
		iterationResult, err := s.loopRunner.RunLiveLoopIteration(iterationCtx, LiveLoopIterationRequest{
			RunID:     result.RunID,
			Iteration: iteration,
			StartedAt: iterationStartedAt,
			Deadline:  iterationStartedAt.Add(iterationTimeout),
		})
		cancel()
		result.IterationsAttempted++
		if err != nil {
			result.StopReason = LiveLoopStopIterationError
			result.StopDetails = err.Error()
			result.FinishedAt = s.clock.Now()
			return result, fmt.Errorf("run live loop iteration %d: %w", iteration, err)
		}

		iterationFinishedAt := s.clock.Now()
		normalized, err := normalizeLiveLoopIterationResult(result.RunID, iteration, iterationStartedAt, iterationFinishedAt, iterationResult)
		if err != nil {
			result.StopReason = LiveLoopStopIterationError
			result.StopDetails = err.Error()
			result.FinishedAt = iterationFinishedAt
			return result, err
		}
		result.Iterations = append(result.Iterations, normalized)
		result.IterationsSucceeded++
		if normalized.Action == LiveLoopIterationActionStop {
			result.StopReason = LiveLoopStopIterationRequested
			result.StopDetails = normalized.Reason
			result.FinishedAt = normalized.FinishedAt
			result.CompletedWithinBounds = true
			return result, nil
		}
	}

	result.StopReason = LiveLoopStopMaxIterations
	result.StopDetails = "max live loop iterations reached"
	result.FinishedAt = s.clock.Now()
	result.CompletedWithinBounds = true
	return result, nil
}

func (s *Service) requireLiveLoopDependencies() error {
	if s == nil || s.loopRunner == nil {
		return fmt.Errorf("live loop requires iteration runner")
	}
	if s.killSwitch == nil {
		return fmt.Errorf("live loop requires kill switch repository")
	}
	if s.env == nil {
		return fmt.Errorf("live loop requires environment reader")
	}
	if s.clock == nil {
		return fmt.Errorf("live loop requires clock")
	}
	return nil
}

func validateRunBoundedLiveLoopRequest(req RunBoundedLiveLoopRequest) error {
	var problems []string
	if strings.TrimSpace(req.RunID) == "" {
		problems = append(problems, "run_id is required")
	}
	if req.RunID != strings.TrimSpace(req.RunID) {
		problems = append(problems, "run_id must be trimmed")
	}
	if req.MaxIterations <= 0 {
		problems = append(problems, "max_iterations must be positive")
	}
	if req.MaxIterations > maxLiveLoopIterations {
		problems = append(problems, fmt.Sprintf("max_iterations must be no more than %d", maxLiveLoopIterations))
	}
	if req.MaxRuntime <= 0 {
		problems = append(problems, "max_runtime must be positive")
	}
	if req.MaxRuntime > maxLiveLoopRuntime {
		problems = append(problems, fmt.Sprintf("max_runtime must be no more than %s", maxLiveLoopRuntime))
	}
	if req.IterationTimeout <= 0 {
		problems = append(problems, "iteration_timeout must be positive")
	}
	if req.MaxRuntime > 0 && req.IterationTimeout > req.MaxRuntime {
		problems = append(problems, "iteration_timeout must not exceed max_runtime")
	}
	if len(problems) > 0 {
		return errors.New("bounded live loop validation failed: " + strings.Join(problems, "; "))
	}
	return nil
}

func boundedIterationTimeout(configured time.Duration, remaining time.Duration) time.Duration {
	if remaining > 0 && remaining < configured {
		return remaining
	}
	return configured
}

func normalizeLiveLoopIterationResult(
	runID string,
	iteration int,
	startedAt time.Time,
	finishedAt time.Time,
	result LiveLoopIterationResult,
) (LiveLoopIterationResult, error) {
	if strings.TrimSpace(result.RunID) == "" {
		result.RunID = runID
	}
	if result.Iteration == 0 {
		result.Iteration = iteration
	}
	if result.Action == "" {
		result.Action = LiveLoopIterationActionNone
	}
	if result.StartedAt.IsZero() {
		result.StartedAt = startedAt
	}
	if result.FinishedAt.IsZero() {
		result.FinishedAt = finishedAt
	}
	result.Reason = strings.TrimSpace(result.Reason)

	var problems []string
	if result.RunID != runID {
		problems = append(problems, fmt.Sprintf("iteration result run_id %q does not match live loop %q", result.RunID, runID))
	}
	if result.Iteration != iteration {
		problems = append(problems, fmt.Sprintf("iteration result number %d does not match live loop iteration %d", result.Iteration, iteration))
	}
	if !knownLiveLoopIterationAction(result.Action) {
		problems = append(problems, "iteration result action must be NONE, SUBMITTED, or STOP")
	}
	if result.StartedAt.IsZero() {
		problems = append(problems, "iteration result started_at is required")
	}
	if result.FinishedAt.IsZero() {
		problems = append(problems, "iteration result finished_at is required")
	}
	if !result.StartedAt.IsZero() && !result.FinishedAt.IsZero() && result.FinishedAt.Before(result.StartedAt) {
		problems = append(problems, "iteration result finished_at must not be before started_at")
	}
	if result.Action == LiveLoopIterationActionStop && result.Reason == "" {
		problems = append(problems, "STOP iteration result requires reason")
	}
	if len(problems) > 0 {
		return LiveLoopIterationResult{}, errors.New("live loop iteration result validation failed: " + strings.Join(problems, "; "))
	}
	return result, nil
}

func knownLiveLoopIterationAction(action LiveLoopIterationAction) bool {
	switch action {
	case LiveLoopIterationActionNone, LiveLoopIterationActionSubmitted, LiveLoopIterationActionStop:
		return true
	default:
		return false
	}
}
