package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/VersoIt/Inquisitor/internal/strategy"
)

type StrategyApprovalRepository struct{ db *sql.DB }

func NewStrategyApprovalRepository(db *sql.DB) *StrategyApprovalRepository {
	return &StrategyApprovalRepository{db: db}
}
func (r *StrategyApprovalRepository) require(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if r == nil || r.db == nil {
		return fmt.Errorf("approval repository requires database")
	}
	if id == "" || id != strings.TrimSpace(id) {
		return fmt.Errorf("approval/validation ID must be nonempty and trimmed")
	}
	return nil
}

func (r *StrategyApprovalRepository) RecordPaperApproval(ctx context.Context, a strategy.PaperApproval) (strategy.ApprovalStats, error) {
	if err := r.require(ctx, a.ApprovalID); err != nil {
		return strategy.ApprovalStats{}, err
	}
	if err := strategy.ValidatePaperApproval(a); err != nil {
		return strategy.ApprovalStats{}, err
	}
	data, err := json.Marshal(a)
	if err != nil {
		return strategy.ApprovalStats{}, err
	}
	result, err := r.db.ExecContext(ctx, `INSERT INTO paper_strategy_approvals(approval_id,validation_id,grant_json,granted_at) VALUES($1,$2,$3::jsonb,$4) ON CONFLICT(approval_id) DO NOTHING`, a.ApprovalID, a.ValidationID, string(data), a.GrantedAt)
	if err != nil {
		return strategy.ApprovalStats{}, fmt.Errorf("record paper approval: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return strategy.ApprovalStats{}, err
	}
	if n == 1 {
		return strategy.ApprovalStats{Written: 1}, nil
	}
	if n != 0 {
		return strategy.ApprovalStats{}, fmt.Errorf("unexpected approval insert count")
	}
	var exists int
	err = r.db.QueryRowContext(ctx, `SELECT 1 FROM paper_strategy_approvals WHERE approval_id=$1 AND grant_json=$2::jsonb`, a.ApprovalID, string(data)).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return strategy.ApprovalStats{}, strategy.ErrApprovalConflict
	}
	if err != nil {
		return strategy.ApprovalStats{}, err
	}
	return strategy.ApprovalStats{Skipped: 1}, nil
}

func (r *StrategyApprovalRepository) RevokePaperApproval(ctx context.Context, id string, revoke strategy.ApprovalRevocation) (strategy.ApprovalStats, error) {
	if err := r.require(ctx, id); err != nil {
		return strategy.ApprovalStats{}, err
	}
	if err := strategy.ValidateApprovalRevocation(revoke); err != nil {
		return strategy.ApprovalStats{}, err
	}
	result, err := r.db.ExecContext(ctx, `UPDATE paper_strategy_approvals SET revoked_at=$2,revoked_by=$3,revocation_reason=$4 WHERE approval_id=$1 AND revoked_at IS NULL AND granted_at <= $2`, id, revoke.RevokedAt, revoke.RevokedBy, revoke.Reason)
	if err != nil {
		return strategy.ApprovalStats{}, err
	}
	n, err := result.RowsAffected()
	if err != nil {
		return strategy.ApprovalStats{}, err
	}
	if n == 1 {
		return strategy.ApprovalStats{Written: 1}, nil
	}
	if n != 0 {
		return strategy.ApprovalStats{}, fmt.Errorf("unexpected approval revocation count")
	}
	var exists int
	err = r.db.QueryRowContext(ctx, `SELECT 1 FROM paper_strategy_approvals WHERE approval_id=$1 AND revoked_by=$2 AND revocation_reason=$3 AND revoked_at IS NOT NULL`, id, revoke.RevokedBy, revoke.Reason).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return strategy.ApprovalStats{}, strategy.ErrApprovalConflict
	}
	if err != nil {
		return strategy.ApprovalStats{}, err
	}
	return strategy.ApprovalStats{Skipped: 1}, nil
}

func (r *StrategyApprovalRepository) LoadPaperApprovalEvidence(ctx context.Context, id string) (strategy.PaperApprovalEvidence, error) {
	if err := r.require(ctx, id); err != nil {
		return strategy.PaperApprovalEvidence{}, err
	}
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return strategy.PaperApprovalEvidence{}, err
	}
	defer tx.Rollback()
	e, err := loadPaperApprovalEvidence(ctx, tx, id)
	if err != nil {
		return strategy.PaperApprovalEvidence{}, err
	}
	if err := tx.Commit(); err != nil {
		return strategy.PaperApprovalEvidence{}, err
	}
	return e, nil
}

func (r *StrategyApprovalRepository) LoadPaperAuthorization(ctx context.Context, id string) (strategy.PaperAuthorization, error) {
	if err := r.require(ctx, id); err != nil {
		return strategy.PaperAuthorization{}, err
	}
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return strategy.PaperAuthorization{}, err
	}
	defer tx.Rollback()
	var a strategy.PaperAuthorization
	var raw string
	var revoked sql.NullTime
	err = tx.QueryRowContext(ctx, `SELECT grant_json::text,revoked_at,revoked_by,revocation_reason FROM paper_strategy_approvals WHERE approval_id=$1`, id).Scan(&raw, &revoked, &a.Revocation.RevokedBy, &a.Revocation.Reason)
	if errors.Is(err, sql.ErrNoRows) {
		return strategy.PaperAuthorization{}, strategy.ErrApprovalUnavailable
	}
	if err != nil {
		return strategy.PaperAuthorization{}, fmt.Errorf("load approval: %w", err)
	}
	if err := decodeSignalJSON(raw, &a.Grant); err != nil {
		return strategy.PaperAuthorization{}, err
	}
	if a.Grant.ApprovalID != id {
		return strategy.PaperAuthorization{}, strategy.ErrApprovalUnavailable
	}
	if err := strategy.ValidatePaperApproval(a.Grant); err != nil {
		return strategy.PaperAuthorization{}, err
	}
	if revoked.Valid {
		a.Revocation.RevokedAt = revoked.Time.UTC()
		if err := strategy.ValidateApprovalRevocation(a.Revocation); err != nil {
			return strategy.PaperAuthorization{}, err
		}
	}
	if !revoked.Valid && (a.Revocation.RevokedBy != "" || a.Revocation.Reason != "") {
		return strategy.PaperAuthorization{}, strategy.ErrApprovalUnavailable
	}
	a.Evidence, err = loadPaperApprovalEvidence(ctx, tx, a.Grant.ValidationID)
	if err != nil {
		return strategy.PaperAuthorization{}, err
	}
	if err := tx.Commit(); err != nil {
		return strategy.PaperAuthorization{}, err
	}
	return a, nil
}

func loadPaperApprovalEvidence(ctx context.Context, tx *sql.Tx, id string) (strategy.PaperApprovalEvidence, error) {
	var e strategy.PaperApprovalEvidence
	var err error
	e.Validation, err = scanPaperValidationRecord(tx.QueryRowContext(ctx, `SELECT validation_id,run_id,status,status_reason,mode,initial_balance::text,minimum_days,reasons_json::text,planned_at,started_at,completed_at,cancelled_at FROM paper_validation_records WHERE validation_id=$1`, id))
	if err != nil {
		return e, fmt.Errorf("load paper validation evidence: %w", err)
	}
	e.Run, err = scanResearchRun(tx.QueryRowContext(ctx, `SELECT run_id,hypothesis_name,hypothesis_version,hypothesis_content_sha256,exchange,category,status,window_start,window_end,planned_at,symbols_json::text,intervals_json::text,notes_json::text FROM research_runs WHERE run_id=$1`, e.Validation.RunID))
	if err != nil {
		return e, fmt.Errorf("load research run evidence: %w", err)
	}
	e.Result, err = scanResearchResult(tx.QueryRowContext(ctx, `SELECT run_id,final_status,outcome,summary,metrics_json::text,reasons_json::text,recorded_at FROM research_results WHERE run_id=$1`, e.Run.RunID))
	if err != nil {
		return e, fmt.Errorf("load research result evidence: %w", err)
	}
	e.Hypothesis, err = scanHypothesis(tx.QueryRowContext(ctx, `SELECT name,version,status,source_path,content_sha256,spec_json::text,raw_yaml,imported_at FROM hypotheses WHERE name=$1 AND version=$2`, e.Run.HypothesisName, e.Run.HypothesisVersion))
	if err != nil {
		return e, fmt.Errorf("load hypothesis evidence: %w", err)
	}
	return e, nil
}
