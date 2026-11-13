package strategy

import (
	"context"
	"fmt"
	"strings"

	"github.com/VersoIt/Inquisitor/internal/clock"
	"github.com/VersoIt/Inquisitor/internal/risk"
	"github.com/VersoIt/Inquisitor/internal/strategy"
)

type PaperSignalCycleOutcome string

const (
	PaperSignalCycleNoTrade        PaperSignalCycleOutcome = "NO_TRADE"
	PaperSignalCycleRiskRejected   PaperSignalCycleOutcome = "RISK_REJECTED"
	PaperSignalCycleTicketRecorded PaperSignalCycleOutcome = "TICKET_RECORDED"
)

type RunPaperSignalCycleRequest struct {
	ApprovalID string
	Market     strategy.MarketContext
	Account    risk.AccountState
	RiskMarket risk.MarketContext
}

type RunPaperSignalCycleResult struct {
	Outcome    PaperSignalCycleOutcome
	Generation GenerateResult
	Submission *SubmitPaperSignalResult `json:",omitempty"`
}

type PaperSignalCycleService struct {
	generation *AuthorizedGenerationService
	submission *PaperSignalSubmissionService
	killSwitch PaperRiskKillSwitchReader
	clock      clock.Clock
}

func NewPaperSignalCycleService(generation *AuthorizedGenerationService, submission *PaperSignalSubmissionService, killSwitch PaperRiskKillSwitchReader, value clock.Clock) *PaperSignalCycleService {
	return &PaperSignalCycleService{generation: generation, submission: submission, killSwitch: killSwitch, clock: value}
}

// Run persists each stage before advancing. A failed stage may already have
// committed; retry with the same IDs, evaluation time and observation snapshots.
// Recording a ticket never grants permission for a later fill.
func (s *PaperSignalCycleService) Run(ctx context.Context, req RunPaperSignalCycleRequest) (RunPaperSignalCycleResult, error) {
	if err := ctx.Err(); err != nil {
		return RunPaperSignalCycleResult{}, err
	}
	if s == nil || s.generation == nil || s.submission == nil || s.killSwitch == nil || s.clock == nil {
		return RunPaperSignalCycleResult{}, fmt.Errorf("paper signal cycle requires generation, submission, kill switch and clock")
	}
	if req.ApprovalID == "" || req.ApprovalID != strings.TrimSpace(req.ApprovalID) || req.Market.SignalID == "" || req.Market.SignalID != strings.TrimSpace(req.Market.SignalID) || req.Market.Mode != risk.ModePaper {
		return RunPaperSignalCycleResult{}, fmt.Errorf("paper signal cycle requires trimmed source IDs and PAPER mode")
	}
	now := s.clock.Now()
	if req.Market.Now.IsZero() || now.IsZero() || req.Market.Now.After(now) {
		return RunPaperSignalCycleResult{}, fmt.Errorf("paper signal evaluation time is missing or future")
	}
	state, err := s.killSwitch.CurrentKillSwitchState(ctx)
	if err != nil {
		return RunPaperSignalCycleResult{}, fmt.Errorf("load signal cycle kill switch: %w", err)
	}
	if err := risk.ValidateKillSwitchState(state); err != nil {
		return RunPaperSignalCycleResult{}, err
	}
	if state.UpdatedAt.After(now) {
		return RunPaperSignalCycleResult{}, fmt.Errorf("signal cycle kill switch is from the future")
	}
	req.Market.KillSwitchActive = req.Market.KillSwitchActive || state.Active
	generated, err := s.generation.GenerateAndRecord(ctx, req.ApprovalID, req.Market)
	if err != nil {
		return RunPaperSignalCycleResult{}, fmt.Errorf("paper signal cycle generation: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return RunPaperSignalCycleResult{}, err
	}
	if generated.Signal.Type == strategy.SignalNoTrade {
		return RunPaperSignalCycleResult{Outcome: PaperSignalCycleNoTrade, Generation: generated}, nil
	}
	// Runtime and regime have one source in this request, not independent
	// generation/risk flags that could contradict each other.
	submitted, err := s.submission.Submit(ctx, EvaluatePaperSignalRequest{
		SignalID: generated.Signal.SignalID, ApprovalID: req.ApprovalID, Regime: req.Market.Regime, Account: req.Account, Market: req.RiskMarket,
		Runtime: risk.RuntimeState{Mode: req.Market.Mode, TradingEnabled: req.Market.TradingEnabled, KillSwitchActive: req.Market.KillSwitchActive},
	})
	if err != nil {
		return RunPaperSignalCycleResult{}, fmt.Errorf("paper signal cycle submission: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return RunPaperSignalCycleResult{}, err
	}
	outcome := PaperSignalCycleRiskRejected
	if submitted.Evaluation.Audit.Decision.Approved {
		outcome = PaperSignalCycleTicketRecorded
	}
	return RunPaperSignalCycleResult{Outcome: outcome, Generation: generated, Submission: &submitted}, nil
}
