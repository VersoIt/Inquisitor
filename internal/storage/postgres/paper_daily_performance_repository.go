package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/shopspring/decimal"

	domainpaper "github.com/VersoIt/Inquisitor/internal/paper"
)

type PaperDailyPerformanceRepository struct {
	db *sql.DB
}

func NewPaperDailyPerformanceRepository(db *sql.DB) *PaperDailyPerformanceRepository {
	return &PaperDailyPerformanceRepository{db: db}
}

func (r *PaperDailyPerformanceRepository) RecordDailyPerformance(ctx context.Context, records []domainpaper.DailyPerformance) (domainpaper.DailyPerformanceStats, error) {
	if len(records) == 0 {
		return domainpaper.DailyPerformanceStats{}, nil
	}
	if err := domainpaper.ValidateDailyPerformanceRecords(records); err != nil {
		return domainpaper.DailyPerformanceStats{}, err
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return domainpaper.DailyPerformanceStats{}, fmt.Errorf("begin paper daily performance transaction: %w", err)
	}
	committed := false
	defer func() {
		if !committed {
			_ = tx.Rollback()
		}
	}()

	insertStatement, err := tx.PrepareContext(ctx, `
		INSERT INTO paper_validation_daily_performance (
			validation_id, day, trades, wins, losses, breakeven, gross_profit, gross_loss,
			total_fees, net_pnl, expectancy, profit_factor, profit_factor_defined, win_rate,
			max_drawdown, initial_equity, final_equity, calculated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18
		)
		ON CONFLICT (validation_id, day) DO NOTHING
	`)
	if err != nil {
		return domainpaper.DailyPerformanceStats{}, fmt.Errorf("prepare paper daily performance insert: %w", err)
	}
	defer insertStatement.Close()

	updateStatement, err := tx.PrepareContext(ctx, `
		UPDATE paper_validation_daily_performance
		SET trades = $3,
		    wins = $4,
		    losses = $5,
		    breakeven = $6,
		    gross_profit = $7,
		    gross_loss = $8,
		    total_fees = $9,
		    net_pnl = $10,
		    expectancy = $11,
		    profit_factor = $12,
		    profit_factor_defined = $13,
		    win_rate = $14,
		    max_drawdown = $15,
		    initial_equity = $16,
		    final_equity = $17,
		    calculated_at = $18,
		    updated_at = NOW()
		WHERE validation_id = $1 AND day = $2
	`)
	if err != nil {
		return domainpaper.DailyPerformanceStats{}, fmt.Errorf("prepare paper daily performance update: %w", err)
	}
	defer updateStatement.Close()

	var stats domainpaper.DailyPerformanceStats
	for _, record := range records {
		args := paperDailyPerformanceSQLArgs(record)
		result, err := insertStatement.ExecContext(ctx, args...)
		if err != nil {
			return domainpaper.DailyPerformanceStats{}, fmt.Errorf("insert paper daily performance %s/%s: %w", record.ValidationID, record.Day.Format("2006-01-02"), err)
		}
		inserted, err := result.RowsAffected()
		if err != nil {
			return domainpaper.DailyPerformanceStats{}, fmt.Errorf("read paper daily performance insert rows affected: %w", err)
		}
		if inserted == 1 {
			stats.Inserted++
			continue
		}
		result, err = updateStatement.ExecContext(ctx, args...)
		if err != nil {
			return domainpaper.DailyPerformanceStats{}, fmt.Errorf("update paper daily performance %s/%s: %w", record.ValidationID, record.Day.Format("2006-01-02"), err)
		}
		updated, err := result.RowsAffected()
		if err != nil {
			return domainpaper.DailyPerformanceStats{}, fmt.Errorf("read paper daily performance update rows affected: %w", err)
		}
		if updated != 1 {
			return domainpaper.DailyPerformanceStats{}, fmt.Errorf("upsert paper daily performance %s/%s affected %d rows", record.ValidationID, record.Day.Format("2006-01-02"), updated)
		}
		stats.Updated++
	}

	if err := tx.Commit(); err != nil {
		return domainpaper.DailyPerformanceStats{}, fmt.Errorf("commit paper daily performance transaction: %w", err)
	}
	committed = true
	return stats, nil
}

func (r *PaperDailyPerformanceRepository) ListDailyPerformance(ctx context.Context, query domainpaper.DailyPerformanceQuery) ([]domainpaper.DailyPerformance, error) {
	if err := domainpaper.ValidateDailyPerformanceQuery(query); err != nil {
		return nil, err
	}
	limit := query.Limit
	if limit <= 0 {
		limit = 1000
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT validation_id, day, trades, wins, losses, breakeven,
		       gross_profit::text, gross_loss::text, total_fees::text, net_pnl::text,
		       expectancy::text, profit_factor::text, profit_factor_defined, win_rate::text,
		       max_drawdown::text, initial_equity::text, final_equity::text, calculated_at
		FROM paper_validation_daily_performance
		WHERE ($1::text = '' OR validation_id = $1)
		  AND ($2::date IS NULL OR day >= $2)
		  AND ($3::date IS NULL OR day < $3)
		ORDER BY day ASC, id ASC
		LIMIT $4
	`, strings.TrimSpace(query.ValidationID), nullableTime(query.Start), nullableTime(query.End), limit)
	if err != nil {
		return nil, fmt.Errorf("list paper daily performance: %w", err)
	}
	defer rows.Close()

	var records []domainpaper.DailyPerformance
	for rows.Next() {
		record, err := scanPaperDailyPerformance(rows)
		if err != nil {
			return nil, err
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate paper daily performance: %w", err)
	}
	if err := domainpaper.ValidateDailyPerformanceRecords(records); err != nil {
		return nil, err
	}
	return records, nil
}

func paperDailyPerformanceSQLArgs(record domainpaper.DailyPerformance) []any {
	summary := record.Summary
	return []any{
		record.ValidationID, record.Day.UTC(), summary.Trades, summary.Wins, summary.Losses, summary.Breakeven,
		summary.GrossProfit.String(), summary.GrossLoss.String(), summary.TotalFees.String(), summary.NetPnL.String(),
		summary.Expectancy.String(), summary.ProfitFactor.String(), summary.ProfitFactorDefined, summary.WinRate.String(),
		summary.MaxDrawdown.String(), summary.InitialEquity.String(), summary.FinalEquity.String(), record.CalculatedAt.UTC(),
	}
}

func scanPaperDailyPerformance(scanner interface{ Scan(dest ...any) error }) (domainpaper.DailyPerformance, error) {
	var record domainpaper.DailyPerformance
	var grossProfit, grossLoss, totalFees, netPnL, expectancy, profitFactor string
	var winRate, maxDrawdown, initialEquity, finalEquity string
	if err := scanner.Scan(
		&record.ValidationID, &record.Day, &record.Summary.Trades, &record.Summary.Wins,
		&record.Summary.Losses, &record.Summary.Breakeven, &grossProfit, &grossLoss,
		&totalFees, &netPnL, &expectancy, &profitFactor, &record.Summary.ProfitFactorDefined,
		&winRate, &maxDrawdown, &initialEquity, &finalEquity, &record.CalculatedAt,
	); err != nil {
		return domainpaper.DailyPerformance{}, fmt.Errorf("scan paper daily performance: %w", err)
	}
	values := []struct {
		name   string
		raw    string
		target *decimal.Decimal
	}{
		{"gross_profit", grossProfit, &record.Summary.GrossProfit},
		{"gross_loss", grossLoss, &record.Summary.GrossLoss},
		{"total_fees", totalFees, &record.Summary.TotalFees},
		{"net_pnl", netPnL, &record.Summary.NetPnL},
		{"expectancy", expectancy, &record.Summary.Expectancy},
		{"profit_factor", profitFactor, &record.Summary.ProfitFactor},
		{"win_rate", winRate, &record.Summary.WinRate},
		{"max_drawdown", maxDrawdown, &record.Summary.MaxDrawdown},
		{"initial_equity", initialEquity, &record.Summary.InitialEquity},
		{"final_equity", finalEquity, &record.Summary.FinalEquity},
	}
	for _, value := range values {
		parsed, err := decimal.NewFromString(value.raw)
		if err != nil {
			return domainpaper.DailyPerformance{}, fmt.Errorf("parse paper daily performance %s: %w", value.name, err)
		}
		*value.target = parsed
	}
	return record, nil
}
