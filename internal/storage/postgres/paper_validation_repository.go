package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	domainpaper "github.com/VersoIt/Inquisitor/internal/paper"
	"github.com/shopspring/decimal"
)

type PaperValidationRepository struct {
	db *sql.DB
}

func NewPaperValidationRepository(db *sql.DB) *PaperValidationRepository {
	return &PaperValidationRepository{db: db}
}

func (r *PaperValidationRepository) RecordValidation(ctx context.Context, record domainpaper.ValidationRecord) (domainpaper.ValidationRecordStats, error) {
	if err := domainpaper.ValidateValidationRecord(record); err != nil {
		return domainpaper.ValidationRecordStats{}, err
	}
	if record.Status != domainpaper.ValidationStatusPlanned {
		return domainpaper.ValidationRecordStats{}, fmt.Errorf("record paper validation requires PLANNED status")
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return domainpaper.ValidationRecordStats{}, fmt.Errorf("begin paper validation record transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	insertStatement, err := tx.PrepareContext(ctx, `
		INSERT INTO paper_validation_records (
			validation_id, run_id, status, status_reason, mode, initial_balance, minimum_days,
			reasons_json, planned_at, started_at, completed_at, cancelled_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12
		)
		ON CONFLICT (validation_id)
		DO NOTHING
	`)
	if err != nil {
		return domainpaper.ValidationRecordStats{}, fmt.Errorf("prepare paper validation record insert: %w", err)
	}
	defer insertStatement.Close()

	updateStatement, err := tx.PrepareContext(ctx, `
		UPDATE paper_validation_records
		SET updated_at = NOW()
		WHERE validation_id = $1
		  AND run_id = $2
		  AND status = $3
		  AND status_reason = $4
		  AND mode = $5
		  AND initial_balance = $6::numeric
		  AND minimum_days = $7
		  AND reasons_json = $8::jsonb
		  AND planned_at = $9
		  AND started_at IS NOT DISTINCT FROM $10::timestamptz
		  AND completed_at IS NOT DISTINCT FROM $11::timestamptz
		  AND cancelled_at IS NOT DISTINCT FROM $12::timestamptz
	`)
	if err != nil {
		return domainpaper.ValidationRecordStats{}, fmt.Errorf("prepare paper validation record update: %w", err)
	}
	defer updateStatement.Close()

	args, err := paperValidationSQLArgs(record)
	if err != nil {
		return domainpaper.ValidationRecordStats{}, err
	}
	result, err := insertStatement.ExecContext(ctx, args...)
	if err != nil {
		return domainpaper.ValidationRecordStats{}, fmt.Errorf("insert paper validation record %s: %w", record.ValidationID, err)
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return domainpaper.ValidationRecordStats{}, fmt.Errorf("read paper validation record insert rows affected: %w", err)
	}
	if inserted > 0 {
		if err := tx.Commit(); err != nil {
			return domainpaper.ValidationRecordStats{}, fmt.Errorf("commit paper validation record transaction: %w", err)
		}
		committed = true
		return domainpaper.ValidationRecordStats{Inserted: int(inserted)}, nil
	}

	result, err = updateStatement.ExecContext(ctx, args...)
	if err != nil {
		return domainpaper.ValidationRecordStats{}, fmt.Errorf("update paper validation record %s: %w", record.ValidationID, err)
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return domainpaper.ValidationRecordStats{}, fmt.Errorf("read paper validation record update rows affected: %w", err)
	}
	if updated == 0 {
		return domainpaper.ValidationRecordStats{}, fmt.Errorf("upsert paper validation record %s affected no rows", record.ValidationID)
	}

	if err := tx.Commit(); err != nil {
		return domainpaper.ValidationRecordStats{}, fmt.Errorf("commit paper validation record transaction: %w", err)
	}
	committed = true

	return domainpaper.ValidationRecordStats{Updated: int(updated)}, nil
}

func (r *PaperValidationRepository) TransitionValidation(ctx context.Context, record domainpaper.ValidationRecord, expectedStatus domainpaper.ValidationStatus) (domainpaper.ValidationRecordStats, error) {
	if err := domainpaper.ValidateValidationTransition(expectedStatus, record); err != nil {
		return domainpaper.ValidationRecordStats{}, err
	}
	result, err := r.db.ExecContext(ctx, `
		UPDATE paper_validation_records
		SET status = $2,
		    status_reason = $3,
		    started_at = $4,
		    completed_at = $5,
		    cancelled_at = $6,
		    updated_at = NOW()
		WHERE validation_id = $1
		  AND status = $7
		  AND ($2 <> 'RUNNING' OR NOT EXISTS (
		      SELECT 1
		      FROM paper_validation_trades
		      WHERE paper_validation_trades.validation_id = paper_validation_records.validation_id
		  ))
	`,
		record.ValidationID,
		string(record.Status),
		record.StatusReason,
		nullableTime(record.StartedAt),
		nullableTime(record.CompletedAt),
		nullableTime(record.CancelledAt),
		string(expectedStatus),
	)
	if err != nil {
		return domainpaper.ValidationRecordStats{}, fmt.Errorf("transition paper validation %s from %s to %s: %w", record.ValidationID, expectedStatus, record.Status, err)
	}
	updated, err := result.RowsAffected()
	if err != nil {
		return domainpaper.ValidationRecordStats{}, fmt.Errorf("read paper validation transition rows affected: %w", err)
	}
	if updated != 1 {
		return domainpaper.ValidationRecordStats{}, fmt.Errorf("transition paper validation %s from %s affected %d rows", record.ValidationID, expectedStatus, updated)
	}
	return domainpaper.ValidationRecordStats{Updated: int(updated)}, nil
}

func (r *PaperValidationRepository) ListValidationRecords(ctx context.Context, query domainpaper.ValidationRecordQuery) ([]domainpaper.ValidationRecord, error) {
	if err := domainpaper.ValidateValidationRecordQuery(query); err != nil {
		return nil, err
	}

	limit := query.Limit
	if limit <= 0 {
		limit = 1000
	}

	rows, err := r.db.QueryContext(ctx, `
		SELECT validation_id, run_id, status, status_reason, mode, initial_balance::text, minimum_days,
		       reasons_json::text, planned_at, started_at, completed_at, cancelled_at
		FROM paper_validation_records
		WHERE ($1::text = '' OR validation_id = $1)
		  AND ($2::text = '' OR run_id = $2)
		  AND ($3::text = '' OR status = $3)
		  AND ($4::timestamptz IS NULL OR planned_at >= $4)
		  AND ($5::timestamptz IS NULL OR planned_at < $5)
		ORDER BY planned_at DESC, id DESC
		LIMIT $6
	`, strings.TrimSpace(query.ValidationID), strings.TrimSpace(query.RunID), string(query.Status), nullableTime(query.Start), nullableTime(query.End), limit)
	if err != nil {
		return nil, fmt.Errorf("list paper validation records: %w", err)
	}
	defer rows.Close()

	var records []domainpaper.ValidationRecord
	for rows.Next() {
		record, err := scanPaperValidationRecord(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate paper validation records: %w", err)
	}
	if err := domainpaper.ValidateValidationRecords(records); err != nil {
		return nil, err
	}
	return records, nil
}

func paperValidationSQLArgs(record domainpaper.ValidationRecord) ([]any, error) {
	reasonsJSON, err := stringSliceJSON(record.Reasons)
	if err != nil {
		return nil, fmt.Errorf("marshal paper validation record reasons: %w", err)
	}
	return []any{
		record.ValidationID,
		record.RunID,
		string(record.Status),
		record.StatusReason,
		record.Mode,
		record.InitialBalance.String(),
		record.MinimumDays,
		reasonsJSON,
		record.PlannedAt.UTC(),
		nullableTime(record.StartedAt),
		nullableTime(record.CompletedAt),
		nullableTime(record.CancelledAt),
	}, nil
}

func scanPaperValidationRecord(scanner interface {
	Scan(dest ...any) error
}) (domainpaper.ValidationRecord, error) {
	var record domainpaper.ValidationRecord
	var statusValue, initialBalanceValue, reasonsJSON string
	var startedAt, completedAt, cancelledAt sql.NullTime
	if err := scanner.Scan(
		&record.ValidationID,
		&record.RunID,
		&statusValue,
		&record.StatusReason,
		&record.Mode,
		&initialBalanceValue,
		&record.MinimumDays,
		&reasonsJSON,
		&record.PlannedAt,
		&startedAt,
		&completedAt,
		&cancelledAt,
	); err != nil {
		return domainpaper.ValidationRecord{}, fmt.Errorf("scan paper validation record: %w", err)
	}

	var err error
	record.Status = domainpaper.ValidationStatus(statusValue)
	record.StartedAt = startedAt.Time
	record.CompletedAt = completedAt.Time
	record.CancelledAt = cancelledAt.Time
	record.InitialBalance, err = decimal.NewFromString(initialBalanceValue)
	if err != nil {
		return domainpaper.ValidationRecord{}, fmt.Errorf("parse paper validation record initial balance: %w", err)
	}
	record.Reasons, err = parseStringSliceJSON(reasonsJSON)
	if err != nil {
		return domainpaper.ValidationRecord{}, fmt.Errorf("parse paper validation record reasons: %w", err)
	}
	record.PlannedAt = record.PlannedAt.UTC()
	if err := domainpaper.ValidateValidationRecord(record); err != nil {
		return domainpaper.ValidationRecord{}, err
	}
	return record, nil
}
