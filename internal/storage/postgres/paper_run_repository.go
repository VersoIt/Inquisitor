package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/VersoIt/Inquisitor/internal/strategy"
)

const maxPaperRunJSONBytes = 1024 * 1024

type PaperRunRepository struct{ db *sql.DB }

func NewPaperRunRepository(db *sql.DB) *PaperRunRepository { return &PaperRunRepository{db: db} }

func (r *PaperRunRepository) ready(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if r == nil || r.db == nil {
		return fmt.Errorf("paper run repository requires database")
	}
	return nil
}

func (r *PaperRunRepository) LoadPaperRun(ctx context.Context, id string) (strategy.PaperRunRecord, error) {
	if err := r.ready(ctx); err != nil {
		return strategy.PaperRunRecord{}, err
	}
	if err := strategy.ValidatePaperRunID(id); err != nil {
		return strategy.PaperRunRecord{}, err
	}
	var input string
	var receipt sql.NullString
	var validationID, approvalID, hash string
	var createdAt sql.NullTime
	var completedAt sql.NullTime
	err := r.db.QueryRowContext(ctx, `SELECT validation_id,approval_id,input_sha256,created_at,
 CASE WHEN octet_length(input_json::text)<=$2 THEN input_json::text END,
 CASE WHEN octet_length(receipt_json::text)<=$2 THEN receipt_json::text END,completed_at
 FROM paper_signal_runs WHERE run_id=$1`, id, maxPaperRunJSONBytes).Scan(&validationID, &approvalID, &hash, &createdAt, &input, &receipt, &completedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return strategy.PaperRunRecord{}, strategy.ErrPaperRunNotFound
	}
	if err != nil {
		return strategy.PaperRunRecord{}, err
	}
	var out strategy.PaperRunRecord
	if err := decodePaperRunJSON(input, &out.Input); err != nil {
		return strategy.PaperRunRecord{}, err
	}
	if err := strategy.ValidatePaperRunInput(out.Input); err != nil {
		return strategy.PaperRunRecord{}, err
	}
	if out.Input.RunID != id || out.Input.ValidationID != validationID || out.Input.ApprovalID != approvalID || out.Input.SHA256 != hash || !createdAt.Valid || !out.Input.CreatedAt.Equal(createdAt.Time) || receipt.Valid != completedAt.Valid {
		return strategy.PaperRunRecord{}, fmt.Errorf("paper run stored envelope mismatch")
	}
	if receipt.Valid {
		out.Receipt = &strategy.PaperRunReceipt{}
		if err := decodePaperRunJSON(receipt.String, out.Receipt); err != nil {
			return strategy.PaperRunRecord{}, err
		}
		if err := strategy.ValidatePaperRunReceipt(*out.Receipt, out.Input); err != nil {
			return strategy.PaperRunRecord{}, err
		}
		if !out.Receipt.CompletedAt.Equal(completedAt.Time) {
			return strategy.PaperRunRecord{}, fmt.Errorf("paper run completion time mismatch")
		}
	}
	return out, nil
}

func decodePaperRunJSON(raw string, value any) error {
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("paper run contains trailing JSON")
	}
	return nil
}

func (r *PaperRunRepository) RecordPaperRunInput(ctx context.Context, in strategy.PaperRunInput) error {
	if err := r.ready(ctx); err != nil {
		return err
	}
	if err := strategy.ValidatePaperRunInput(in); err != nil {
		return err
	}
	raw, err := json.Marshal(in)
	if err != nil {
		return err
	}
	if len(raw) > maxPaperRunJSONBytes {
		return fmt.Errorf("paper run input exceeds size limit")
	}
	if _, err = r.db.ExecContext(ctx, `INSERT INTO paper_signal_runs(run_id,validation_id,approval_id,input_sha256,input_json,created_at)
 VALUES($1,$2,$3,$4,$5::jsonb,$6) ON CONFLICT(run_id) DO NOTHING`, in.RunID, in.ValidationID, in.ApprovalID, in.SHA256, string(raw), in.CreatedAt); err != nil {
		return err
	}
	saved, err := r.LoadPaperRun(ctx, in.RunID)
	if err != nil {
		return err
	}
	if saved.Input.SHA256 != in.SHA256 {
		return strategy.ErrPaperRunConflict
	}
	return nil
}

func (r *PaperRunRepository) RecordPaperRunReceipt(ctx context.Context, receipt strategy.PaperRunReceipt) (strategy.PaperRunReceipt, error) {
	record, err := r.LoadPaperRun(ctx, receipt.RunID)
	if err != nil {
		return strategy.PaperRunReceipt{}, err
	}
	if err := strategy.ValidatePaperRunReceipt(receipt, record.Input); err != nil {
		return strategy.PaperRunReceipt{}, err
	}
	raw, err := json.Marshal(receipt)
	if err != nil {
		return strategy.PaperRunReceipt{}, err
	}
	if _, err = r.db.ExecContext(ctx, `UPDATE paper_signal_runs SET receipt_json=$2::jsonb,completed_at=$3 WHERE run_id=$1 AND receipt_json IS NULL`, receipt.RunID, string(raw), receipt.CompletedAt); err != nil {
		return strategy.PaperRunReceipt{}, err
	}
	saved, err := r.LoadPaperRun(ctx, receipt.RunID)
	if err != nil {
		return strategy.PaperRunReceipt{}, err
	}
	if saved.Receipt == nil {
		return strategy.PaperRunReceipt{}, fmt.Errorf("paper run completion acknowledgement missing")
	}
	want, got := receipt, *saved.Receipt
	want.CompletedAt = got.CompletedAt
	if want != got {
		return strategy.PaperRunReceipt{}, strategy.ErrPaperRunConflict
	}
	return got, nil
}

// This cooperative lock serializes this runner across every symbol/interval of
// a validation. It is not a portfolio reservation or a lock for legacy writers.
func (r *PaperRunRepository) WithPaperAdmissionLock(ctx context.Context, validationID string, fn func(context.Context) error) error {
	if err := r.ready(ctx); err != nil {
		return err
	}
	if fn == nil || validationID == "" || validationID != strings.TrimSpace(validationID) || len(validationID) > 256 {
		return fmt.Errorf("paper admission requires callback and trimmed validation ID")
	}
	if r.db.Stats().MaxOpenConnections == 1 {
		return fmt.Errorf("paper admission needs at least two database connections")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var acquired bool
	if err := tx.QueryRowContext(ctx, `SELECT pg_try_advisory_xact_lock(hashtextextended('inquisitor:paper-admission:'||current_schema()||':'||$1,0))`, validationID).Scan(&acquired); err != nil {
		return err
	}
	if !acquired {
		return strategy.ErrPaperAdmissionBusy
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := fn(ctx); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return tx.Commit()
}
