package postgres_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/shopspring/decimal"

	domainpaper "github.com/VersoIt/Inquisitor/internal/paper"
	"github.com/VersoIt/Inquisitor/internal/storage/postgres"
)

func TestPaperDailyPerformanceRepositorySQLMockTableDriven(t *testing.T) {
	ctx := context.Background()
	record := testDailyPerformance(t)

	tests := []struct {
		name string
		run  func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock)
	}{
		{
			name: "inserts daily snapshot",
			run: func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock) {
				mock.ExpectBegin()
				mock.ExpectPrepare("INSERT INTO paper_validation_daily_performance")
				mock.ExpectPrepare("UPDATE paper_validation_daily_performance")
				mock.ExpectExec("INSERT INTO paper_validation_daily_performance").
					WithArgs(dailyPerformanceSQLArgs(record)...).
					WillReturnResult(sqlmock.NewResult(1, 1))
				mock.ExpectCommit()

				stats, err := postgres.NewPaperDailyPerformanceRepository(db).RecordDailyPerformance(ctx, []domainpaper.DailyPerformance{record})
				if err != nil {
					t.Fatalf("record daily performance: %v", err)
				}
				if stats.Inserted != 1 || stats.Updated != 0 || stats.Total() != 1 {
					t.Fatalf("stats mismatch: %#v", stats)
				}
			},
		},
		{
			name: "updates existing daily snapshot",
			run: func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock) {
				mock.ExpectBegin()
				mock.ExpectPrepare("INSERT INTO paper_validation_daily_performance")
				mock.ExpectPrepare("UPDATE paper_validation_daily_performance")
				mock.ExpectExec("INSERT INTO paper_validation_daily_performance").
					WithArgs(dailyPerformanceSQLArgs(record)...).
					WillReturnResult(sqlmock.NewResult(0, 0))
				mock.ExpectExec("UPDATE paper_validation_daily_performance").
					WithArgs(dailyPerformanceSQLArgs(record)...).
					WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectCommit()

				stats, err := postgres.NewPaperDailyPerformanceRepository(db).RecordDailyPerformance(ctx, []domainpaper.DailyPerformance{record})
				if err != nil {
					t.Fatalf("record daily performance: %v", err)
				}
				if stats.Inserted != 0 || stats.Updated != 1 {
					t.Fatalf("stats mismatch: %#v", stats)
				}
			},
		},
		{
			name: "lists daily snapshots",
			run: func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock) {
				rows := sqlmock.NewRows(dailyPerformanceColumns()).AddRow(dailyPerformanceSQLArgs(record)...)
				mock.ExpectQuery("SELECT validation_id, day, trades").
					WithArgs(record.ValidationID, record.Day, record.Day.AddDate(0, 0, 1), 10).
					WillReturnRows(rows)

				got, err := postgres.NewPaperDailyPerformanceRepository(db).ListDailyPerformance(ctx, domainpaper.DailyPerformanceQuery{
					ValidationID: record.ValidationID, Start: record.Day, End: record.Day.AddDate(0, 0, 1), Limit: 10,
				})
				if err != nil {
					t.Fatalf("list daily performance: %v", err)
				}
				if len(got) != 1 || got[0].Summary.Trades != record.Summary.Trades || !got[0].Summary.NetPnL.Equal(record.Summary.NetPnL) {
					t.Fatalf("daily performance did not round-trip: %#v", got)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock := newSQLMock(t)
			defer db.Close()
			tt.run(t, db, mock)
			assertSQLExpectations(t, mock)
		})
	}
}

func testDailyPerformance(t *testing.T) domainpaper.DailyPerformance {
	t.Helper()
	trade := testPaperValidationTrade(t, time.Date(2026, 6, 18, 10, 0, 0, 0, time.UTC))
	records, err := domainpaper.BuildDailyPerformance(
		trade.ValidationID,
		decimal.RequireFromString("1000"),
		[]domainpaper.ValidationTrade{trade},
		time.Date(2026, 6, 19, 12, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatalf("build daily performance fixture: %v", err)
	}
	return records[0]
}

func dailyPerformanceColumns() []string {
	return []string{
		"validation_id", "day", "trades", "wins", "losses", "breakeven",
		"gross_profit", "gross_loss", "total_fees", "net_pnl", "expectancy",
		"profit_factor", "profit_factor_defined", "win_rate", "max_drawdown",
		"initial_equity", "final_equity", "calculated_at",
	}
}

func dailyPerformanceSQLArgs(record domainpaper.DailyPerformance) []driver.Value {
	summary := record.Summary
	return []driver.Value{
		record.ValidationID, record.Day.UTC(), int64(summary.Trades), int64(summary.Wins), int64(summary.Losses), int64(summary.Breakeven),
		summary.GrossProfit.String(), summary.GrossLoss.String(), summary.TotalFees.String(), summary.NetPnL.String(),
		summary.Expectancy.String(), summary.ProfitFactor.String(), summary.ProfitFactorDefined, summary.WinRate.String(),
		summary.MaxDrawdown.String(), summary.InitialEquity.String(), summary.FinalEquity.String(), record.CalculatedAt.UTC(),
	}
}
