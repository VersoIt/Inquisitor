package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/VersoIt/Inquisitor/internal/risk"
	"github.com/VersoIt/Inquisitor/internal/strategy"
)

type PaperSignalRiskRepository struct{ db *sql.DB }

func NewPaperSignalRiskRepository(db *sql.DB) *PaperSignalRiskRepository {
	return &PaperSignalRiskRepository{db: db}
}

func (r *PaperSignalRiskRepository) LoadPaperRiskEvaluation(ctx context.Context, signalID string) (strategy.PaperRiskEvaluation, error) {
	if err := ctx.Err(); err != nil {
		return strategy.PaperRiskEvaluation{}, err
	}
	if r == nil || r.db == nil {
		return strategy.PaperRiskEvaluation{}, fmt.Errorf("paper signal risk repository requires database")
	}
	return loadPaperRiskEvaluation(ctx, r.db, signalID)
}

func (r *PaperSignalRiskRepository) SavePaperRiskEvaluation(ctx context.Context, value strategy.PaperRiskEvaluation) (strategy.PaperRiskEvaluation, risk.DecisionAuditStats, error) {
	if err := ctx.Err(); err != nil {
		return strategy.PaperRiskEvaluation{}, risk.DecisionAuditStats{}, err
	}
	if r == nil || r.db == nil {
		return strategy.PaperRiskEvaluation{}, risk.DecisionAuditStats{}, fmt.Errorf("paper signal risk repository requires database")
	}
	if err := strategy.ValidatePaperRiskEvaluation(value); err != nil {
		return strategy.PaperRiskEvaluation{}, risk.DecisionAuditStats{}, err
	}
	raw, err := json.Marshal(value.Context)
	if err != nil {
		return strategy.PaperRiskEvaluation{}, risk.DecisionAuditStats{}, err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return strategy.PaperRiskEvaluation{}, risk.DecisionAuditStats{}, fmt.Errorf("begin paper signal risk write: %w", err)
	}
	defer tx.Rollback()
	// Serialize only the short persistence step, not strategy/risk computation.
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, "paper_signal_risk:"+value.SignalID); err != nil {
		return strategy.PaperRiskEvaluation{}, risk.DecisionAuditStats{}, fmt.Errorf("lock paper signal risk write: %w", err)
	}
	existing, err := loadPaperRiskEvaluation(ctx, tx, value.SignalID)
	if err == nil {
		if !strategy.EquivalentPaperRiskEvaluation(existing, value) {
			return strategy.PaperRiskEvaluation{}, risk.DecisionAuditStats{}, strategy.ErrPaperRiskConflict
		}
		if err := tx.Commit(); err != nil {
			return strategy.PaperRiskEvaluation{}, risk.DecisionAuditStats{}, fmt.Errorf("commit paper risk retry: %w", err)
		}
		return existing, risk.DecisionAuditStats{Skipped: 1}, nil
	}
	if !errors.Is(err, strategy.ErrPaperRiskNotFound) {
		return strategy.PaperRiskEvaluation{}, risk.DecisionAuditStats{}, err
	}
	if _, err := recordRiskDecision(ctx, tx, value.Audit); err != nil {
		return strategy.PaperRiskEvaluation{}, risk.DecisionAuditStats{}, fmt.Errorf("record linked paper risk decision: %w", err)
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO paper_signal_risk_evaluations (signal_id, approval_id, decision_id, context_json) VALUES ($1,$2,$3,$4::jsonb)`, value.SignalID, value.ApprovalID, value.Audit.DecisionID, string(raw))
	if err != nil {
		return strategy.PaperRiskEvaluation{}, risk.DecisionAuditStats{}, fmt.Errorf("link paper signal risk decision: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return strategy.PaperRiskEvaluation{}, risk.DecisionAuditStats{}, fmt.Errorf("read paper signal risk link write count: %w", err)
	}
	if count != 1 {
		return strategy.PaperRiskEvaluation{}, risk.DecisionAuditStats{}, fmt.Errorf("invalid paper signal risk link write count: %d", count)
	}
	if err := tx.Commit(); err != nil {
		return strategy.PaperRiskEvaluation{}, risk.DecisionAuditStats{}, fmt.Errorf("commit paper signal risk write: %w", err)
	}
	return value, risk.DecisionAuditStats{Inserted: 1}, nil
}

type paperRiskQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

type paperRiskScanner func(...any) error

func (f paperRiskScanner) Scan(dest ...any) error { return f(dest...) }

func loadPaperRiskEvaluation(ctx context.Context, db paperRiskQueryer, signalID string) (strategy.PaperRiskEvaluation, error) {
	if signalID == "" || signalID != strings.TrimSpace(signalID) {
		return strategy.PaperRiskEvaluation{}, fmt.Errorf("paper risk signal ID must be nonempty and trimmed")
	}
	row := db.QueryRowContext(ctx, `
		SELECT e.signal_id, e.approval_id, e.context_json::text,
		       d.decision_id, d.intent_id, d.mode, d.hypothesis_id, d.strategy_name, d.symbol, d.side,
		       d.entry_price::text, d.leverage::text, d.confidence, d.intent_reason, d.intent_created_at,
		       d.approved, d.final_quantity::text, d.max_loss::text, d.stop_loss::text, d.take_profit::text,
		       d.reason, d.checks_json::text, d.created_at, d.recorded_at
		FROM paper_signal_risk_evaluations e JOIN risk_decisions d ON d.decision_id=e.decision_id
		WHERE e.signal_id=$1`, signalID)
	var value strategy.PaperRiskEvaluation
	var raw string
	audit, err := scanRiskDecision(paperRiskScanner(func(dest ...any) error {
		return row.Scan(append([]any{&value.SignalID, &value.ApprovalID, &raw}, dest...)...)
	}))
	if errors.Is(err, sql.ErrNoRows) {
		return strategy.PaperRiskEvaluation{}, strategy.ErrPaperRiskNotFound
	}
	if err != nil {
		return strategy.PaperRiskEvaluation{}, fmt.Errorf("load linked paper risk decision: %w", err)
	}
	value.Audit = audit
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value.Context); err != nil {
		return strategy.PaperRiskEvaluation{}, fmt.Errorf("decode paper risk context: %w", err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return strategy.PaperRiskEvaluation{}, fmt.Errorf("paper risk context has trailing JSON")
	}
	if err := strategy.ValidatePaperRiskEvaluation(value); err != nil {
		return strategy.PaperRiskEvaluation{}, err
	}
	return value, nil
}
