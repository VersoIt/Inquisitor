package postgres_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/shopspring/decimal"

	"github.com/VersoIt/Inquisitor/internal/marketdata"
	"github.com/VersoIt/Inquisitor/internal/storage/postgres"
)

func TestCandleRepositorySQLMockTableDriven(t *testing.T) {
	ctx := context.Background()
	openTime := time.Date(2026, 6, 5, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		run  func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock)
	}{
		{
			name: "upsert commits valid candles",
			run: func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock) {
				candle := testCandle(openTime, "105")
				mock.ExpectBegin()
				mock.ExpectPrepare("INSERT INTO candles")
				mock.ExpectPrepare("UPDATE candles")
				mock.ExpectExec("INSERT INTO candles").
					WithArgs(
						candle.Exchange,
						candle.Category,
						candle.Symbol,
						candle.Interval,
						candle.OpenTime.UTC(),
						candle.CloseTime.UTC(),
						candle.Open.String(),
						candle.High.String(),
						candle.Low.String(),
						candle.Close.String(),
						candle.Volume.String(),
						candle.Turnover.String(),
						candle.IsClosed,
					).
					WillReturnResult(sqlmock.NewResult(1, 1))
				mock.ExpectCommit()

				stats, err := postgres.NewCandleRepository(db).UpsertCandles(ctx, []marketdata.Candle{candle})
				if err != nil {
					t.Fatalf("upsert candles: %v", err)
				}
				if stats.Inserted != 1 || stats.Updated != 0 {
					t.Fatalf("expected one inserted candle, got %#v", stats)
				}
			},
		},
		{
			name: "list scans candles into domain model",
			run: func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock) {
				closeTime := openTime.Add(time.Minute)
				rows := sqlmock.NewRows([]string{
					"exchange", "category", "symbol", "interval", "open_time", "close_time",
					"open", "high", "low", "close", "volume", "turnover", "is_closed",
				}).AddRow(
					"bybit", "linear", "BTCUSDT", "1", openTime, closeTime,
					"100", "110", "90", "105", "10", "1000", true,
				)

				mock.ExpectQuery("SELECT exchange, category, symbol, interval").
					WithArgs("bybit", "linear", "BTCUSDT", "1", nil, nil, 1000).
					WillReturnRows(rows)

				candles, err := postgres.NewCandleRepository(db).ListCandles(ctx, marketdata.CandleQuery{
					Exchange: "bybit",
					Category: "linear",
					Symbol:   "BTCUSDT",
					Interval: "1",
				})
				if err != nil {
					t.Fatalf("list candles: %v", err)
				}
				if len(candles) != 1 {
					t.Fatalf("expected one candle, got %d", len(candles))
				}
				if !candles[0].Close.Equal(decimal.RequireFromString("105")) {
					t.Fatalf("expected close 105, got %s", candles[0].Close)
				}
			},
		},
		{
			name: "upsert reports updated candle on conflict",
			run: func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock) {
				candle := testCandle(openTime, "106")
				mock.ExpectBegin()
				mock.ExpectPrepare("INSERT INTO candles")
				mock.ExpectPrepare("UPDATE candles")
				mock.ExpectExec("INSERT INTO candles").
					WithArgs(
						candle.Exchange,
						candle.Category,
						candle.Symbol,
						candle.Interval,
						candle.OpenTime.UTC(),
						candle.CloseTime.UTC(),
						candle.Open.String(),
						candle.High.String(),
						candle.Low.String(),
						candle.Close.String(),
						candle.Volume.String(),
						candle.Turnover.String(),
						candle.IsClosed,
					).
					WillReturnResult(sqlmock.NewResult(0, 0))
				mock.ExpectExec("UPDATE candles").
					WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectCommit()

				stats, err := postgres.NewCandleRepository(db).UpsertCandles(ctx, []marketdata.Candle{candle})
				if err != nil {
					t.Fatalf("upsert candles: %v", err)
				}
				if stats.Inserted != 0 || stats.Updated != 1 {
					t.Fatalf("expected one updated candle, got %#v", stats)
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

func TestInstrumentRepositorySQLMockTableDriven(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name string
		run  func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock)
	}{
		{
			name: "upsert commits valid instruments",
			run: func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock) {
				instrument := testInstrument("BTCUSDT", "0.10")
				mock.ExpectBegin()
				mock.ExpectPrepare("INSERT INTO instruments")
				mock.ExpectPrepare("UPDATE instruments")
				mock.ExpectExec("INSERT INTO instruments").
					WithArgs(
						instrument.Exchange,
						instrument.Category,
						instrument.Symbol,
						instrument.BaseCoin,
						instrument.QuoteCoin,
						instrument.Status,
						instrument.TickSize.String(),
						instrument.QtyStep.String(),
						instrument.MinOrderQty.String(),
						instrument.MaxOrderQty.String(),
						instrument.MaxMarketOrderQty.String(),
						instrument.MinNotionalValue.String(),
						instrument.PriceScale,
						string(instrument.LeverageFilterJSON),
						string(instrument.PriceFilterJSON),
						string(instrument.LotSizeFilterJSON),
						string(instrument.RawJSON),
						instrument.UpdatedAt.UTC(),
					).
					WillReturnResult(sqlmock.NewResult(1, 1))
				mock.ExpectCommit()

				stats, err := postgres.NewInstrumentRepository(db).UpsertInstruments(ctx, []marketdata.Instrument{instrument})
				if err != nil {
					t.Fatalf("upsert instruments: %v", err)
				}
				if stats.Inserted != 1 || stats.Updated != 0 {
					t.Fatalf("expected one inserted instrument, got %#v", stats)
				}
			},
		},
		{
			name: "get maps sql no rows to domain not found",
			run: func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock) {
				mock.ExpectQuery("SELECT exchange, category, symbol").
					WithArgs("bybit", "linear", "MISSING").
					WillReturnError(sql.ErrNoRows)

				_, err := postgres.NewInstrumentRepository(db).GetInstrument(ctx, marketdata.InstrumentKey{
					Exchange: "bybit",
					Category: "linear",
					Symbol:   "MISSING",
				})
				if !errors.Is(err, marketdata.ErrInstrumentNotFound) {
					t.Fatalf("expected ErrInstrumentNotFound, got %v", err)
				}
			},
		},
		{
			name: "upsert reports updated instrument on conflict",
			run: func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock) {
				instrument := testInstrument("BTCUSDT", "0.50")
				mock.ExpectBegin()
				mock.ExpectPrepare("INSERT INTO instruments")
				mock.ExpectPrepare("UPDATE instruments")
				mock.ExpectExec("INSERT INTO instruments").
					WillReturnResult(sqlmock.NewResult(0, 0))
				mock.ExpectExec("UPDATE instruments").
					WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectCommit()

				stats, err := postgres.NewInstrumentRepository(db).UpsertInstruments(ctx, []marketdata.Instrument{instrument})
				if err != nil {
					t.Fatalf("upsert instruments: %v", err)
				}
				if stats.Inserted != 0 || stats.Updated != 1 {
					t.Fatalf("expected one updated instrument, got %#v", stats)
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

func TestDataQualityEventRepositorySQLMockTableDriven(t *testing.T) {
	ctx := context.Background()
	createdAt := time.Date(2026, 6, 5, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		run  func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock)
	}{
		{
			name: "create commits valid events",
			run: func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock) {
				event := testDataQualityEvent(createdAt, marketdata.DataQualityEventCandleGap, marketdata.DataQualitySeverityWarning)
				mock.ExpectBegin()
				mock.ExpectPrepare("INSERT INTO data_quality_events").
					ExpectExec().
					WithArgs(
						event.Exchange,
						event.Symbol,
						event.Interval,
						event.EventType,
						event.Severity,
						event.Message,
						string(event.DataJSON),
						event.CreatedAt.UTC(),
					).
					WillReturnResult(sqlmock.NewResult(1, 1))
				mock.ExpectCommit()

				stats, err := postgres.NewDataQualityEventRepository(db).CreateDataQualityEvents(ctx, []marketdata.DataQualityEvent{event})
				if err != nil {
					t.Fatalf("create data quality events: %v", err)
				}
				if stats.Inserted != 1 || stats.Updated != 0 {
					t.Fatalf("expected one inserted event, got %#v", stats)
				}
			},
		},
		{
			name: "list scans events into domain model",
			run: func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock) {
				rows := sqlmock.NewRows([]string{
					"exchange", "symbol", "interval", "event_type", "severity", "message", "data_json", "created_at",
				}).AddRow(
					"bybit", "BTCUSDT", "1", marketdata.DataQualityEventCandleGap, marketdata.DataQualitySeverityWarning, "missing candles", `{"missing_candles":1}`, createdAt,
				)

				mock.ExpectQuery("SELECT exchange, symbol").
					WithArgs("bybit", "BTCUSDT", "1", marketdata.DataQualityEventCandleGap, "", nil, nil, 10).
					WillReturnRows(rows)

				events, err := postgres.NewDataQualityEventRepository(db).ListDataQualityEvents(ctx, marketdata.DataQualityEventQuery{
					Exchange:  "bybit",
					Symbol:    "BTCUSDT",
					Interval:  "1",
					EventType: marketdata.DataQualityEventCandleGap,
					Limit:     10,
				})
				if err != nil {
					t.Fatalf("list data quality events: %v", err)
				}
				if len(events) != 1 {
					t.Fatalf("expected one event, got %d", len(events))
				}
				if events[0].EventType != marketdata.DataQualityEventCandleGap || events[0].Severity != marketdata.DataQualitySeverityWarning {
					t.Fatalf("unexpected event: %#v", events[0])
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

func TestRepositoriesRejectInvalidInputsBeforeSQLTableDriven(t *testing.T) {
	ctx := context.Background()
	openTime := time.Date(2026, 6, 5, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		run  func(db *sql.DB) error
	}{
		{
			name: "candle repository rejects invalid candle before transaction",
			run: func(db *sql.DB) error {
				candle := testCandle(openTime, "105")
				candle.High = decimal.RequireFromString("80")
				_, err := postgres.NewCandleRepository(db).UpsertCandles(ctx, []marketdata.Candle{candle})
				return err
			},
		},
		{
			name: "instrument repository rejects invalid instrument before transaction",
			run: func(db *sql.DB) error {
				instrument := testInstrument("BTCUSDT", "0.10")
				instrument.QtyStep = decimal.Zero
				_, err := postgres.NewInstrumentRepository(db).UpsertInstruments(ctx, []marketdata.Instrument{instrument})
				return err
			},
		},
		{
			name: "data quality repository rejects invalid event before transaction",
			run: func(db *sql.DB) error {
				event := testDataQualityEvent(openTime, marketdata.DataQualityEventCandleGap, marketdata.DataQualitySeverityWarning)
				event.Message = ""
				_, err := postgres.NewDataQualityEventRepository(db).CreateDataQualityEvents(ctx, []marketdata.DataQualityEvent{event})
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, mock := newSQLMock(t)
			defer db.Close()

			if err := tt.run(db); err == nil {
				t.Fatal("expected validation error")
			}
			assertSQLExpectations(t, mock)
		})
	}
}

func TestApplyMigrationsSQLMockTableDriven(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name       string
		setup      func(t *testing.T, dir string, mock sqlmock.Sqlmock)
		want       postgres.MigrationResult
		wantErrSub string
	}{
		{
			name: "applies pending migration and records checksum",
			setup: func(t *testing.T, dir string, mock sqlmock.Sqlmock) {
				writeMigration(t, dir, "001_create_test_table.sql", "CREATE TABLE test_table (id INT);")

				mock.ExpectExec("CREATE TABLE IF NOT EXISTS schema_migrations").
					WillReturnResult(sqlmock.NewResult(0, 0))
				mock.ExpectQuery("SELECT version, checksum_sha256").
					WithArgs("001").
					WillReturnError(sql.ErrNoRows)
				mock.ExpectBegin()
				mock.ExpectExec("CREATE TABLE test_table").
					WillReturnResult(sqlmock.NewResult(0, 0))
				mock.ExpectExec("INSERT INTO schema_migrations").
					WithArgs("001", "001_create_test_table.sql", sqlmock.AnyArg(), sqlmock.AnyArg()).
					WillReturnResult(sqlmock.NewResult(1, 1))
				mock.ExpectCommit()
			},
			want: postgres.MigrationResult{Applied: 1, Skipped: 0},
		},
		{
			name: "refuses checksum mismatch",
			setup: func(t *testing.T, dir string, mock sqlmock.Sqlmock) {
				writeMigration(t, dir, "001_create_test_table.sql", "CREATE TABLE test_table (id INT);")

				rows := sqlmock.NewRows([]string{"version", "checksum_sha256"}).
					AddRow("001", "different-checksum")
				mock.ExpectExec("CREATE TABLE IF NOT EXISTS schema_migrations").
					WillReturnResult(sqlmock.NewResult(0, 0))
				mock.ExpectQuery("SELECT version, checksum_sha256").
					WithArgs("001").
					WillReturnRows(rows)
			},
			want:       postgres.MigrationResult{Applied: 0, Skipped: 0},
			wantErrSub: "checksum mismatch",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			db, mock := newSQLMock(t)
			defer db.Close()

			tt.setup(t, dir, mock)
			got, err := postgres.ApplyMigrations(ctx, db, dir)
			if tt.wantErrSub != "" {
				if err == nil {
					t.Fatal("expected error")
				}
				if !strings.Contains(err.Error(), tt.wantErrSub) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
				}
			} else if err != nil {
				t.Fatalf("apply migrations: %v", err)
			}
			if got != tt.want {
				t.Fatalf("expected result %#v, got %#v", tt.want, got)
			}
			assertSQLExpectations(t, mock)
		})
	}
}

func newSQLMock(t *testing.T) (*sql.DB, sqlmock.Sqlmock) {
	t.Helper()

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("new sqlmock: %v", err)
	}
	return db, mock
}

func writeMigration(t *testing.T, dir, name, contents string) {
	t.Helper()

	if err := os.WriteFile(filepath.Join(dir, name), []byte(contents), 0o600); err != nil {
		t.Fatalf("write migration %s: %v", name, err)
	}
}

func assertSQLExpectations(t *testing.T, mock sqlmock.Sqlmock) {
	t.Helper()

	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("unmet SQL expectations: %v", err)
	}
}
