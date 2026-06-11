package postgres_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	domainregime "github.com/VersoIt/Inquisitor/internal/regime"
	"github.com/VersoIt/Inquisitor/internal/storage/postgres"
)

func TestRegimeStateRepositoryIntegrationTableDriven(t *testing.T) {
	db := openTestPostgres(t)
	ctx := context.Background()
	applyMigrations(t, ctx, db)
	cleanupRegimeStates(t, ctx, db)
	t.Cleanup(func() {
		cleanupRegimeStates(t, context.Background(), db)
	})

	repo := postgres.NewRegimeStateRepository(db)
	closeTime := time.Date(2026, 6, 11, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		run  func(t *testing.T)
	}{
		{
			name: "upserts and lists latest regime state",
			run: func(t *testing.T) {
				first := testRegimeState(closeTime, domainregime.RegimeTrendUp, domainregime.RegimeTrendUp, false)
				stats, err := repo.UpsertStates(ctx, []domainregime.State{first})
				if err != nil {
					t.Fatalf("insert regime state: %v", err)
				}
				if stats.Inserted != 1 || stats.Updated != 0 {
					t.Fatalf("insert stats mismatch: %#v", stats)
				}

				updated := testRegimeState(closeTime, domainregime.RegimeNoTrade, domainregime.RegimeTrendUp, true)
				updated.Confidence = 64
				updated.Reasons = []string{"low_confidence"}
				stats, err = repo.UpsertStates(ctx, []domainregime.State{updated})
				if err != nil {
					t.Fatalf("update regime state: %v", err)
				}
				if stats.Inserted != 0 || stats.Updated != 1 {
					t.Fatalf("update stats mismatch: %#v", stats)
				}

				got, err := repo.ListStates(ctx, domainregime.StateQuery{
					Exchange: "bybit",
					Category: "linear",
					Symbol:   "BTCUSDT",
					Interval: "1",
					Limit:    10,
				})
				if err != nil {
					t.Fatalf("list regime states: %v", err)
				}
				if len(got) != 1 {
					t.Fatalf("expected one upserted state, got %d", len(got))
				}
				if got[0].Regime != domainregime.RegimeNoTrade || !got[0].NoTrade {
					t.Fatalf("expected updated no-trade state, got %#v", got[0])
				}
				if len(got[0].Reasons) != 1 || got[0].Reasons[0] != "low_confidence" {
					t.Fatalf("reasons mismatch: %#v", got[0].Reasons)
				}
			},
		},
		{
			name: "filters by regime and close-time window",
			run: func(t *testing.T) {
				second := testRegimeState(closeTime.Add(time.Minute), domainregime.RegimeRange, domainregime.RegimeRange, false)
				if _, err := repo.UpsertStates(ctx, []domainregime.State{second}); err != nil {
					t.Fatalf("insert range state: %v", err)
				}

				got, err := repo.ListStates(ctx, domainregime.StateQuery{
					Exchange: "bybit",
					Category: "linear",
					Symbol:   "BTCUSDT",
					Interval: "1",
					Regime:   domainregime.RegimeRange,
					Start:    closeTime.Add(30 * time.Second),
					End:      closeTime.Add(2 * time.Minute),
					Limit:    10,
				})
				if err != nil {
					t.Fatalf("list filtered regime states: %v", err)
				}
				if len(got) != 1 || got[0].Regime != domainregime.RegimeRange {
					t.Fatalf("expected one RANGE state, got %#v", got)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, tt.run)
	}
}

func cleanupRegimeStates(t *testing.T, ctx context.Context, db *sql.DB) {
	t.Helper()
	_, err := db.ExecContext(ctx, `
		DELETE FROM regime_states
		WHERE exchange = 'bybit' AND category = 'linear' AND symbol = 'BTCUSDT' AND interval = '1'
	`)
	if err != nil {
		t.Fatalf("cleanup regime states: %v", err)
	}
}
