package postgres_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/VersoIt/Inquisitor/internal/marketdata"
	"github.com/VersoIt/Inquisitor/internal/storage/postgres"
)

func TestDataQualityEventRepositoryIntegrationTableDriven(t *testing.T) {
	db := openTestPostgres(t)
	ctx := context.Background()
	applyMigrations(t, ctx, db)
	cleanupDataQualityEvents(t, ctx, db)
	t.Cleanup(func() {
		cleanupDataQualityEvents(t, context.Background(), db)
	})

	repo := postgres.NewDataQualityEventRepository(db)
	now := time.Date(2026, 6, 5, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		run  func(t *testing.T)
	}{
		{
			name: "creates and lists events by type",
			run: func(t *testing.T) {
				stats, err := repo.CreateDataQualityEvents(ctx, []marketdata.DataQualityEvent{
					testDataQualityEvent(now, marketdata.DataQualityEventCandleGap, marketdata.DataQualitySeverityWarning),
					testDataQualityEvent(now.Add(time.Minute), marketdata.DataQualityEventStaleData, marketdata.DataQualitySeverityCritical),
				})
				if err != nil {
					t.Fatalf("create data quality events: %v", err)
				}
				if stats.Inserted != 2 || stats.Updated != 0 {
					t.Fatalf("expected two inserted events, got %#v", stats)
				}

				got, err := repo.ListDataQualityEvents(ctx, marketdata.DataQualityEventQuery{
					Exchange:  "bybit",
					Symbol:    "BTCUSDT",
					EventType: marketdata.DataQualityEventCandleGap,
					Limit:     10,
				})
				if err != nil {
					t.Fatalf("list data quality events: %v", err)
				}
				if len(got) != 1 {
					t.Fatalf("expected one CANDLE_GAP event, got %d", len(got))
				}
				if got[0].Severity != marketdata.DataQualitySeverityWarning {
					t.Fatalf("expected warning severity, got %s", got[0].Severity)
				}
			},
		},
		{
			name: "filters events by time window",
			run: func(t *testing.T) {
				got, err := repo.ListDataQualityEvents(ctx, marketdata.DataQualityEventQuery{
					Exchange: "bybit",
					Symbol:   "BTCUSDT",
					Start:    now.Add(30 * time.Second),
					End:      now.Add(2 * time.Minute),
					Limit:    10,
				})
				if err != nil {
					t.Fatalf("list data quality events by time: %v", err)
				}
				if len(got) != 1 || got[0].EventType != marketdata.DataQualityEventStaleData {
					t.Fatalf("expected one STALE_DATA event, got %#v", got)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, tt.run)
	}
}

func cleanupDataQualityEvents(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	_, err := db.ExecContext(ctx, `
		DELETE FROM data_quality_events
		WHERE exchange = 'bybit' AND symbol = 'BTCUSDT'
	`)
	if err != nil {
		t.Fatalf("cleanup data quality events: %v", err)
	}
}

func testDataQualityEvent(createdAt time.Time, eventType, severity string) marketdata.DataQualityEvent {
	return marketdata.DataQualityEvent{
		Exchange:  "bybit",
		Symbol:    "BTCUSDT",
		Interval:  "1",
		EventType: eventType,
		Severity:  severity,
		Message:   "test data quality event",
		DataJSON:  []byte(`{"source":"test"}`),
		CreatedAt: createdAt,
	}
}
