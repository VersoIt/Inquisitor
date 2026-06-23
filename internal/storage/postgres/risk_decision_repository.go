package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	domainrisk "github.com/VersoIt/Inquisitor/internal/risk"
	"github.com/shopspring/decimal"
)

type RiskDecisionRepository struct {
	db *sql.DB
}

func NewRiskDecisionRepository(db *sql.DB) *RiskDecisionRepository {
	return &RiskDecisionRepository{db: db}
}

func (r *RiskDecisionRepository) RecordDecision(ctx context.Context, record domainrisk.DecisionAuditRecord) (domainrisk.DecisionAuditStats, error) {
	if err := domainrisk.ValidateDecisionAuditRecord(record); err != nil {
		return domainrisk.DecisionAuditStats{}, err
	}
	args, err := riskDecisionSQLArgs(record)
	if err != nil {
		return domainrisk.DecisionAuditStats{}, err
	}
	result, err := r.db.ExecContext(ctx, `
		INSERT INTO risk_decisions (
			decision_id, intent_id, mode, hypothesis_id, strategy_name, symbol, side,
			approved, final_quantity, max_loss, stop_loss, take_profit, reason,
			checks_json, created_at, recorded_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7,
			$8, $9, $10, $11, $12, $13,
			$14, $15, $16
		)
		ON CONFLICT (decision_id) DO NOTHING
	`, args...)
	if err != nil {
		return domainrisk.DecisionAuditStats{}, fmt.Errorf("insert risk decision %s: %w", record.DecisionID, err)
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return domainrisk.DecisionAuditStats{}, fmt.Errorf("read risk decision insert rows affected: %w", err)
	}
	if inserted == 1 {
		return domainrisk.DecisionAuditStats{Inserted: 1}, nil
	}
	if err := r.assertExistingDecisionMatches(ctx, args); err != nil {
		return domainrisk.DecisionAuditStats{}, err
	}
	return domainrisk.DecisionAuditStats{Skipped: 1}, nil
}

func (r *RiskDecisionRepository) ListDecisions(ctx context.Context, query domainrisk.DecisionAuditQuery) ([]domainrisk.DecisionAuditRecord, error) {
	if err := domainrisk.ValidateDecisionAuditQuery(query); err != nil {
		return nil, err
	}
	limit := query.Limit
	if limit <= 0 {
		limit = 1000
	}
	var approved any
	if query.Approved != nil {
		approved = *query.Approved
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT decision_id, intent_id, mode, hypothesis_id, strategy_name, symbol, side,
		       approved, final_quantity::text, max_loss::text, stop_loss::text, take_profit::text,
		       reason, checks_json::text, created_at, recorded_at
		FROM risk_decisions
		WHERE ($1::text = '' OR decision_id = $1)
		  AND ($2::text = '' OR intent_id = $2)
		  AND ($3::text = '' OR symbol = $3)
		  AND ($4::boolean IS NULL OR approved = $4)
		  AND ($5::timestamptz IS NULL OR created_at >= $5)
		  AND ($6::timestamptz IS NULL OR created_at < $6)
		ORDER BY created_at DESC, id DESC
		LIMIT $7
	`, strings.TrimSpace(query.DecisionID), strings.TrimSpace(query.IntentID), strings.TrimSpace(query.Symbol), approved, nullableTime(query.Start), nullableTime(query.End), limit)
	if err != nil {
		return nil, fmt.Errorf("list risk decisions: %w", err)
	}
	defer rows.Close()

	var records []domainrisk.DecisionAuditRecord
	for rows.Next() {
		record, err := scanRiskDecision(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate risk decisions: %w", err)
	}
	if err := domainrisk.ValidateDecisionAuditRecords(records); err != nil {
		return nil, err
	}
	return records, nil
}

func (r *RiskDecisionRepository) assertExistingDecisionMatches(ctx context.Context, args []any) error {
	var exists int
	if err := r.db.QueryRowContext(ctx, `
		SELECT 1
		FROM risk_decisions
		WHERE decision_id = $1
		  AND intent_id = $2
		  AND mode = $3
		  AND hypothesis_id = $4
		  AND strategy_name = $5
		  AND symbol = $6
		  AND side = $7
		  AND approved = $8
		  AND final_quantity = $9::numeric
		  AND max_loss = $10::numeric
		  AND stop_loss = $11::numeric
		  AND take_profit = $12::numeric
		  AND reason = $13
		  AND checks_json = $14::jsonb
		  AND created_at = $15
		  AND recorded_at = $16
	`, args...).Scan(&exists); err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("risk decision %s already exists with different payload", args[0])
		}
		return fmt.Errorf("verify existing risk decision %s: %w", args[0], err)
	}
	return nil
}

func riskDecisionSQLArgs(record domainrisk.DecisionAuditRecord) ([]any, error) {
	checksJSON, err := json.Marshal(record.Decision.Checks)
	if err != nil {
		return nil, fmt.Errorf("marshal risk decision checks: %w", err)
	}
	return []any{
		record.DecisionID,
		record.Decision.IntentID,
		string(record.Mode),
		record.HypothesisID,
		record.StrategyName,
		record.Symbol,
		string(record.Side),
		record.Decision.Approved,
		record.Decision.FinalQuantity.String(),
		record.Decision.MaxLoss.String(),
		record.Decision.StopLoss.String(),
		record.Decision.TakeProfit.String(),
		record.Decision.Reason,
		string(checksJSON),
		record.Decision.CreatedAt.UTC(),
		record.RecordedAt.UTC(),
	}, nil
}

func scanRiskDecision(scanner interface{ Scan(dest ...any) error }) (domainrisk.DecisionAuditRecord, error) {
	var record domainrisk.DecisionAuditRecord
	var mode, side string
	var finalQuantity, maxLoss, stopLoss, takeProfit string
	var checksRaw string
	if err := scanner.Scan(
		&record.DecisionID,
		&record.Decision.IntentID,
		&mode,
		&record.HypothesisID,
		&record.StrategyName,
		&record.Symbol,
		&side,
		&record.Decision.Approved,
		&finalQuantity,
		&maxLoss,
		&stopLoss,
		&takeProfit,
		&record.Decision.Reason,
		&checksRaw,
		&record.Decision.CreatedAt,
		&record.RecordedAt,
	); err != nil {
		return domainrisk.DecisionAuditRecord{}, fmt.Errorf("scan risk decision: %w", err)
	}
	record.Mode = domainrisk.Mode(mode)
	record.Side = domainrisk.Side(side)
	values := []struct {
		name   string
		raw    string
		target *decimal.Decimal
	}{
		{"final_quantity", finalQuantity, &record.Decision.FinalQuantity},
		{"max_loss", maxLoss, &record.Decision.MaxLoss},
		{"stop_loss", stopLoss, &record.Decision.StopLoss},
		{"take_profit", takeProfit, &record.Decision.TakeProfit},
	}
	for _, value := range values {
		parsed, err := decimal.NewFromString(value.raw)
		if err != nil {
			return domainrisk.DecisionAuditRecord{}, fmt.Errorf("parse risk decision %s: %w", value.name, err)
		}
		*value.target = parsed
	}
	if err := json.Unmarshal([]byte(checksRaw), &record.Decision.Checks); err != nil {
		return domainrisk.DecisionAuditRecord{}, fmt.Errorf("parse risk decision checks: %w", err)
	}
	record.Decision.CreatedAt = record.Decision.CreatedAt.UTC()
	record.RecordedAt = record.RecordedAt.UTC()
	if err := domainrisk.ValidateDecisionAuditRecord(record); err != nil {
		return domainrisk.DecisionAuditRecord{}, err
	}
	return record, nil
}
