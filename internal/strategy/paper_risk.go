package strategy

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/VersoIt/Inquisitor/internal/regime"
	"github.com/VersoIt/Inquisitor/internal/risk"
)

var (
	ErrPaperRiskNotFound = errors.New("paper signal risk evaluation not found")
	ErrPaperRiskConflict = errors.New("paper signal risk evaluation conflicts with existing evaluation")
)

type PaperRiskContext struct {
	Regime regime.State
	Input  risk.EvaluationInput
	Policy risk.Policy
}

// PaperRiskEvaluation is historical evidence, not an execution permit or reservation.
type PaperRiskEvaluation struct {
	SignalID, ApprovalID string
	Context              PaperRiskContext
	Audit                risk.DecisionAuditRecord
}

type PaperRiskRepository interface {
	LoadPaperRiskEvaluation(context.Context, string) (PaperRiskEvaluation, error)
	// Save atomically stores the risk audit and its source linkage. Equivalent retries
	// return the original record; changed inputs or outcomes must never overwrite it.
	SavePaperRiskEvaluation(context.Context, PaperRiskEvaluation) (PaperRiskEvaluation, risk.DecisionAuditStats, error)
}

// IDs depend only on the signal, so a different grant cannot create a second decision.
func PaperRiskIDs(signalID string) (intentID, decisionID string) {
	sum := sha256.Sum256([]byte(signalID))
	id := hex.EncodeToString(sum[:])
	return "paper_signal_intent_" + id, "paper_signal_risk_" + id
}

func ValidatePaperRiskEvaluation(e PaperRiskEvaluation) error {
	if e.SignalID == "" || e.SignalID != strings.TrimSpace(e.SignalID) || e.ApprovalID == "" || e.ApprovalID != strings.TrimSpace(e.ApprovalID) {
		return fmt.Errorf("paper risk source IDs must be nonempty and trimmed")
	}
	i, a := e.Context.Input, e.Audit
	intentID, decisionID := PaperRiskIDs(e.SignalID)
	if i.Intent.IntentID != intentID || a.DecisionID != decisionID || a.Decision.IntentID != intentID || i.Runtime.Mode != risk.ModePaper || a.Mode != risk.ModePaper {
		return fmt.Errorf("paper risk identity or mode mismatch")
	}
	if err := regime.ValidateState(e.Context.Regime); err != nil {
		return err
	}
	if e.Context.Regime.Symbol != i.Intent.Symbol || e.Context.Regime.NoTrade {
		return fmt.Errorf("paper risk regime scope is invalid")
	}
	for _, at := range []time.Time{i.EvaluatedAt, i.Intent.CreatedAt, i.Market.DataTime, a.Decision.CreatedAt, a.RecordedAt, a.IntentCreatedAt,
		e.Context.Regime.OpenTime, e.Context.Regime.CloseTime, e.Context.Regime.CalculatedAt} {
		_, offset := at.Zone()
		if !at.Equal(auditTime(at)) || offset != 0 {
			return fmt.Errorf("paper risk timestamps require UTC microsecond precision")
		}
	}
	if i.EvaluatedAt.IsZero() || !a.Decision.CreatedAt.Equal(i.EvaluatedAt) || !a.RecordedAt.Equal(i.EvaluatedAt) || i.Intent.CreatedAt.IsZero() || i.Intent.CreatedAt.After(i.EvaluatedAt) || e.Context.Regime.CloseTime.After(i.EvaluatedAt) || e.Context.Regime.CalculatedAt.Before(e.Context.Regime.CloseTime) || e.Context.Regime.CalculatedAt.After(i.EvaluatedAt) {
		return fmt.Errorf("paper risk chronology is invalid")
	}
	if !a.Decision.StopLoss.Equal(i.Intent.StopLoss) || !a.Decision.TakeProfit.Equal(i.Intent.TakeProfit) {
		return fmt.Errorf("paper risk decision changed the signal's protective prices")
	}
	expected, err := risk.NewDecisionAuditRecord(risk.DecisionAuditInput{DecisionID: decisionID, Decision: a.Decision, Intent: i.Intent, Runtime: i.Runtime, RecordedAt: i.EvaluatedAt})
	if err != nil {
		return err
	}
	if !sameRiskJSON(expected, a) {
		return fmt.Errorf("paper risk audit does not match its input snapshot")
	}
	engine, err := risk.NewTradeRiskEngine(e.Context.Policy)
	if err != nil {
		return err
	}
	if e.Context.Policy.AllowedMode != risk.ModePaper {
		return fmt.Errorf("paper risk policy must be PAPER-only")
	}
	decision, err := engine.Evaluate(i)
	if err != nil {
		return err
	}
	if !sameRiskJSON(decision, a.Decision) {
		return fmt.Errorf("paper risk decision does not match the recorded policy and inputs")
	}
	return nil
}

// SamePaperRiskRequest ignores only the server's evaluation time, never market time.
func SamePaperRiskRequest(a, b PaperRiskEvaluation) bool {
	a.Context.Input.EvaluatedAt, b.Context.Input.EvaluatedAt = time.Time{}, time.Time{}
	return a.SignalID == b.SignalID && a.ApprovalID == b.ApprovalID && sameRiskJSON(a.Context, b.Context)
}

func EquivalentPaperRiskEvaluation(a, b PaperRiskEvaluation) bool {
	if !SamePaperRiskRequest(a, b) {
		return false
	}
	a.Audit.Decision.CreatedAt, b.Audit.Decision.CreatedAt = time.Time{}, time.Time{}
	a.Audit.RecordedAt, b.Audit.RecordedAt = time.Time{}, time.Time{}
	return sameRiskJSON(a.Audit, b.Audit)
}

func sameRiskJSON(a, b any) bool {
	left, err := json.Marshal(a)
	if err != nil {
		return false
	}
	right, err := json.Marshal(b)
	return err == nil && bytes.Equal(left, right)
}
