package postgres_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"

	domainregime "github.com/VersoIt/Inquisitor/internal/regime"
	"github.com/VersoIt/Inquisitor/internal/storage/postgres"
)

func TestRegimeStateRepositorySQLMockTableDriven(t *testing.T) {
	ctx := context.Background()
	closeTime := time.Date(2026, 6, 11, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		run  func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock)
	}{
		{
			name: "upsert inserts new regime state",
			run: func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock) {
				state := testRegimeState(closeTime, domainregime.RegimeTrendUp, domainregime.RegimeTrendUp, false)
				mock.ExpectBegin()
				mock.ExpectPrepare("INSERT INTO regime_states")
				mock.ExpectPrepare("UPDATE regime_states")
				mock.ExpectExec("INSERT INTO regime_states").
					WithArgs(regimeStateSQLArgs(state)...).
					WillReturnResult(sqlmock.NewResult(1, 1))
				mock.ExpectCommit()

				stats, err := postgres.NewRegimeStateRepository(db).UpsertStates(ctx, []domainregime.State{state})
				if err != nil {
					t.Fatalf("upsert regime states: %v", err)
				}
				if stats.Inserted != 1 || stats.Updated != 0 {
					t.Fatalf("stats mismatch: got %#v want inserted=1 updated=0", stats)
				}
			},
		},
		{
			name: "upsert updates existing regime state on conflict",
			run: func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock) {
				state := testRegimeState(closeTime, domainregime.RegimeNoTrade, domainregime.RegimeTrendUp, true)
				state.Confidence = 62
				state.Reasons = []string{"low_confidence"}
				mock.ExpectBegin()
				mock.ExpectPrepare("INSERT INTO regime_states")
				mock.ExpectPrepare("UPDATE regime_states")
				mock.ExpectExec("INSERT INTO regime_states").
					WithArgs(regimeStateSQLArgs(state)...).
					WillReturnResult(sqlmock.NewResult(0, 0))
				mock.ExpectExec("UPDATE regime_states").
					WithArgs(regimeStateSQLArgs(state)...).
					WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectCommit()

				stats, err := postgres.NewRegimeStateRepository(db).UpsertStates(ctx, []domainregime.State{state})
				if err != nil {
					t.Fatalf("upsert existing regime state: %v", err)
				}
				if stats.Inserted != 0 || stats.Updated != 1 {
					t.Fatalf("stats mismatch: got %#v want inserted=0 updated=1", stats)
				}
			},
		},
		{
			name: "list scans regime states and reasons",
			run: func(t *testing.T, db *sql.DB, mock sqlmock.Sqlmock) {
				state := testRegimeState(closeTime, domainregime.RegimeNoTrade, domainregime.RegimeTrendUp, true)
				state.Confidence = 64
				state.Reasons = []string{"low_confidence", "data_quality:stale_data"}
				rows := sqlmock.NewRows([]string{
					"exchange", "category", "symbol", "interval", "open_time", "close_time", "calculated_at",
					"regime", "candidate_regime", "confidence", "no_trade", "reasons_json",
				}).AddRow(
					state.Exchange,
					state.Category,
					state.Symbol,
					state.Interval,
					state.OpenTime,
					state.CloseTime,
					state.CalculatedAt,
					string(state.Regime),
					string(state.CandidateRegime),
					state.Confidence,
					state.NoTrade,
					`["low_confidence","data_quality:stale_data"]`,
				)
				mock.ExpectQuery("SELECT exchange, category, symbol, interval").
					WithArgs("bybit", "linear", "BTCUSDT", "1", string(domainregime.RegimeNoTrade), closeTime.Add(-time.Hour), closeTime.Add(time.Hour), 20).
					WillReturnRows(rows)

				got, err := postgres.NewRegimeStateRepository(db).ListStates(ctx, domainregime.StateQuery{
					Exchange: "bybit",
					Category: "linear",
					Symbol:   "BTCUSDT",
					Interval: "1",
					Regime:   domainregime.RegimeNoTrade,
					Start:    closeTime.Add(-time.Hour),
					End:      closeTime.Add(time.Hour),
					Limit:    20,
				})
				if err != nil {
					t.Fatalf("list regime states: %v", err)
				}
				if len(got) != 1 {
					t.Fatalf("expected one regime state, got %d", len(got))
				}
				if got[0].Regime != domainregime.RegimeNoTrade || !got[0].NoTrade {
					t.Fatalf("unexpected state: %#v", got[0])
				}
				if len(got[0].Reasons) != 2 || got[0].Reasons[1] != "data_quality:stale_data" {
					t.Fatalf("reasons did not round-trip: %#v", got[0].Reasons)
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

func TestRegimeStateRepositoryRejectsInvalidInputsBeforeSQLTableDriven(t *testing.T) {
	ctx := context.Background()
	closeTime := time.Date(2026, 6, 11, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		run  func(db *sql.DB) error
	}{
		{
			name: "upsert rejects invalid state before transaction",
			run: func(db *sql.DB) error {
				state := testRegimeState(closeTime, domainregime.RegimeTrendUp, domainregime.RegimeTrendUp, false)
				state.Confidence = 140
				_, err := postgres.NewRegimeStateRepository(db).UpsertStates(ctx, []domainregime.State{state})
				return err
			},
		},
		{
			name: "list rejects invalid query before SQL",
			run: func(db *sql.DB) error {
				_, err := postgres.NewRegimeStateRepository(db).ListStates(ctx, domainregime.StateQuery{
					Exchange: "bybit",
					Category: "linear",
					Symbol:   "BTCUSDT",
				})
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

func testRegimeState(closeTime time.Time, stateRegime, candidate domainregime.Regime, noTrade bool) domainregime.State {
	return domainregime.State{
		Exchange:        "bybit",
		Category:        "linear",
		Symbol:          "BTCUSDT",
		Interval:        "1",
		OpenTime:        closeTime.Add(-time.Minute),
		CloseTime:       closeTime,
		CalculatedAt:    closeTime.Add(250 * time.Millisecond),
		Regime:          stateRegime,
		CandidateRegime: candidate,
		Confidence:      82,
		NoTrade:         noTrade,
		Reasons:         []string{"candidate:trend_up", "adx_trend"},
	}
}

func regimeStateSQLArgs(state domainregime.State) []driver.Value {
	return []driver.Value{
		state.Exchange,
		state.Category,
		state.Symbol,
		state.Interval,
		state.OpenTime.UTC(),
		state.CloseTime.UTC(),
		state.CalculatedAt.UTC(),
		string(state.Regime),
		string(state.CandidateRegime),
		state.Confidence,
		state.NoTrade,
		mustRegimeReasonsJSON(state.Reasons),
	}
}

func mustRegimeReasonsJSON(reasons []string) string {
	if reasons == nil {
		reasons = []string{}
	}
	raw, err := json.Marshal(reasons)
	if err != nil {
		panic(err)
	}
	return string(raw)
}
