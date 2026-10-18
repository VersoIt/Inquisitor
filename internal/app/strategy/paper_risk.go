package strategy

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/VersoIt/Inquisitor/internal/clock"
	"github.com/VersoIt/Inquisitor/internal/hypothesis"
	"github.com/VersoIt/Inquisitor/internal/regime"
	"github.com/VersoIt/Inquisitor/internal/risk"
	"github.com/VersoIt/Inquisitor/internal/strategy"
)

type PaperRiskKillSwitchReader interface {
	CurrentKillSwitchState(context.Context) (risk.KillSwitchState, error)
}

type PaperRiskEngine interface {
	Evaluate(risk.EvaluationInput) (risk.Decision, error)
	PolicySnapshot() risk.Policy
}

type PaperRiskService struct {
	signals    strategy.SignalRepository
	approvals  *ApprovalService
	journal    strategy.PaperRiskRepository
	killSwitch PaperRiskKillSwitchReader
	engine     PaperRiskEngine
	clock      clock.Clock
}

func NewPaperRiskService(signals strategy.SignalRepository, approvals *ApprovalService, journal strategy.PaperRiskRepository, killSwitch PaperRiskKillSwitchReader, engine PaperRiskEngine, value clock.Clock) (*PaperRiskService, error) {
	if signals == nil || approvals == nil || journal == nil || killSwitch == nil || engine == nil || value == nil {
		return nil, fmt.Errorf("paper risk service requires signals, approvals, journal, kill switch, engine and clock")
	}
	return &PaperRiskService{signals: signals, approvals: approvals, journal: journal, killSwitch: killSwitch, engine: engine, clock: value}, nil
}

type EvaluatePaperSignalRequest struct {
	SignalID, ApprovalID string
	Regime               regime.State
	Account              risk.AccountState
	Market               risk.MarketContext
	Runtime              risk.RuntimeState
}

type EvaluatePaperSignalResult struct {
	Evaluation strategy.PaperRiskEvaluation
	Replayed   bool
}

// EvaluateStoredSignal never creates a ticket. A replay returns historical evidence,
// not a fresh decision that can safely be used to open exposure.
func (s *PaperRiskService) EvaluateStoredSignal(ctx context.Context, req EvaluatePaperSignalRequest) (EvaluatePaperSignalResult, error) {
	if err := ctx.Err(); err != nil {
		return EvaluatePaperSignalResult{}, err
	}
	if s == nil || s.signals == nil || s.approvals == nil || s.journal == nil || s.killSwitch == nil || s.engine == nil || s.clock == nil {
		return EvaluatePaperSignalResult{}, fmt.Errorf("paper risk service dependencies are required")
	}
	if req.SignalID == "" || req.SignalID != strings.TrimSpace(req.SignalID) || req.ApprovalID == "" || req.ApprovalID != strings.TrimSpace(req.ApprovalID) || req.Runtime.Mode != risk.ModePaper {
		return EvaluatePaperSignalResult{}, fmt.Errorf("paper risk requires trimmed source IDs and PAPER mode")
	}
	now := s.clock.Now().UTC().Truncate(time.Microsecond)
	rows, err := s.signals.ListSignals(ctx, strategy.SignalQuery{SignalID: req.SignalID, Limit: 2})
	if err != nil {
		return EvaluatePaperSignalResult{}, fmt.Errorf("load paper risk signal: %w", err)
	}
	if len(rows) != 1 || rows[0].Signal.SignalID != req.SignalID {
		return EvaluatePaperSignalResult{}, fmt.Errorf("paper risk requires exactly one stored signal")
	}
	source := rows[0]
	if err := strategy.ValidateSignalAuditRecord(source); err != nil {
		return EvaluatePaperSignalResult{}, err
	}
	if source.Mode != risk.ModePaper || source.Signal.Trade == nil || source.RecordedAt.After(now) {
		return EvaluatePaperSignalResult{}, fmt.Errorf("paper risk requires a persisted PAPER trade signal from the past")
	}
	r := req.Regime
	r.Reasons = slices.Clone(r.Reasons)
	r.OpenTime, r.CloseTime, r.CalculatedAt = paperRiskTime(r.OpenTime), paperRiskTime(r.CloseTime), paperRiskTime(r.CalculatedAt)
	if err := regime.ValidateState(r); err != nil {
		return EvaluatePaperSignalResult{}, err
	}
	if r.Exchange != source.Exchange || r.Category != source.Category || r.Symbol != source.Signal.Symbol || r.Interval != source.Signal.Interval || string(r.Regime) != source.Features["regime.name"] || r.NoTrade || r.CalculatedAt.Before(r.CloseTime) || r.CalculatedAt.After(now) || r.CloseTime.After(now) {
		return EvaluatePaperSignalResult{}, fmt.Errorf("paper risk regime differs from signal scope or is invalid")
	}
	approval, err := s.approvals.Resolve(ctx, req.ApprovalID, strategy.MarketContext{Now: now, Mode: req.Runtime.Mode, Regime: r})
	if err != nil {
		return EvaluatePaperSignalResult{}, err
	}
	t := *source.Signal.Trade
	if approval.HypothesisID != t.HypothesisID || approval.Version != t.HypothesisVersion || approval.StrategyName != t.StrategyName || source.Features["approval.id"] != approval.ApprovalID || source.Features["approval.validation_id"] != approval.ValidationID || source.Features["approval.run_id"] != approval.RunID || source.Features["approval.hypothesis_sha256"] != approval.HypothesisSHA256 || source.Features["approval.mode"] != string(risk.ModePaper) {
		return EvaluatePaperSignalResult{}, fmt.Errorf("paper risk signal lacks matching persisted authorization provenance")
	}
	if approval.Direction == hypothesis.DirectionLong && t.Side != strategy.SideLong || approval.Direction == hypothesis.DirectionShort && t.Side != strategy.SideShort {
		return EvaluatePaperSignalResult{}, fmt.Errorf("paper risk signal direction is not authorized")
	}
	// Approval flags/caps in a signal are evidence, not authority for a later evaluation.
	t.HypothesisApprovedForMode, t.HypothesisMaxRiskPct, t.HypothesisMinConfidence = approval.Enabled, approval.MaxRiskPct, approval.MinConfidence
	// A deteriorated current regime cannot retain the old signal's higher confidence.
	t.Confidence = min(t.Confidence, r.Confidence)
	intentID, decisionID := strategy.PaperRiskIDs(req.SignalID)
	intent, err := t.RiskIntent(intentID)
	if err != nil {
		return EvaluatePaperSignalResult{}, err
	}
	state, err := s.killSwitch.CurrentKillSwitchState(ctx)
	if err != nil {
		return EvaluatePaperSignalResult{}, fmt.Errorf("load paper risk kill switch: %w", err)
	}
	if err := risk.ValidateKillSwitchState(state); err != nil {
		return EvaluatePaperSignalResult{}, err
	}
	if state.UpdatedAt.After(now) {
		return EvaluatePaperSignalResult{}, fmt.Errorf("paper risk kill switch state is from the future")
	}
	req.Runtime.KillSwitchActive = req.Runtime.KillSwitchActive || state.Active
	req.Market.DataTime = paperRiskTime(req.Market.DataTime)
	// A fresh quote/recalculation cannot refresh the signal or its underlying candle.
	for _, at := range []time.Time{source.ObservedAt, r.CloseTime} {
		if !req.Market.DataTime.After(now) && at.Before(req.Market.DataTime) {
			req.Market.DataTime = at
		}
	}
	policy := s.engine.PolicySnapshot()
	policy.AllowedSymbols = slices.Clone(policy.AllowedSymbols)
	if err := risk.ValidatePolicy(policy); err != nil {
		return EvaluatePaperSignalResult{}, err
	}
	if policy.AllowedMode != risk.ModePaper {
		return EvaluatePaperSignalResult{}, fmt.Errorf("paper risk requires a PAPER-only policy")
	}
	evaluation := strategy.PaperRiskEvaluation{SignalID: req.SignalID, ApprovalID: req.ApprovalID, Context: strategy.PaperRiskContext{Regime: r, Policy: policy,
		Input: risk.EvaluationInput{Intent: intent, Account: req.Account, Market: req.Market, Runtime: req.Runtime, EvaluatedAt: now}}}
	existing, err := s.journal.LoadPaperRiskEvaluation(ctx, req.SignalID)
	if err == nil {
		if err := strategy.ValidatePaperRiskEvaluation(existing); err != nil {
			return EvaluatePaperSignalResult{}, err
		}
		if !strategy.SamePaperRiskRequest(existing, evaluation) || existing.Audit.RecordedAt.After(now) {
			return EvaluatePaperSignalResult{}, strategy.ErrPaperRiskConflict
		}
		return EvaluatePaperSignalResult{Evaluation: existing, Replayed: true}, nil
	}
	if !errors.Is(err, strategy.ErrPaperRiskNotFound) {
		return EvaluatePaperSignalResult{}, fmt.Errorf("load paper risk evaluation: %w", err)
	}
	decision, err := s.engine.Evaluate(evaluation.Context.Input)
	if err != nil {
		return EvaluatePaperSignalResult{}, fmt.Errorf("evaluate stored paper signal: %w", err)
	}
	evaluation.Audit, err = risk.NewDecisionAuditRecord(risk.DecisionAuditInput{DecisionID: decisionID, Decision: decision, Intent: intent, Runtime: req.Runtime, RecordedAt: now})
	if err != nil {
		return EvaluatePaperSignalResult{}, err
	}
	if err := strategy.ValidatePaperRiskEvaluation(evaluation); err != nil {
		return EvaluatePaperSignalResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return EvaluatePaperSignalResult{}, err
	}
	saved, stats, err := s.journal.SavePaperRiskEvaluation(ctx, evaluation)
	if err != nil {
		return EvaluatePaperSignalResult{}, fmt.Errorf("save paper risk evaluation: %w", err)
	}
	if err := strategy.ValidatePaperRiskEvaluation(saved); err != nil {
		return EvaluatePaperSignalResult{}, err
	}
	if !strategy.EquivalentPaperRiskEvaluation(saved, evaluation) || saved.Audit.RecordedAt.After(now) || (stats.Inserted != 1 || stats.Skipped != 0) && (stats.Inserted != 0 || stats.Skipped != 1) {
		return EvaluatePaperSignalResult{}, fmt.Errorf("invalid paper risk write acknowledgement")
	}
	return EvaluatePaperSignalResult{Evaluation: saved, Replayed: stats.Skipped == 1}, nil
}

func paperRiskTime(at time.Time) time.Time { return at.UTC().Truncate(time.Microsecond) }
