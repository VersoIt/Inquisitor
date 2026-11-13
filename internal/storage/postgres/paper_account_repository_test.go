package postgres_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/VersoIt/Inquisitor/internal/paper"
	"github.com/VersoIt/Inquisitor/internal/storage/postgres"
	"github.com/VersoIt/Inquisitor/internal/testutil"
)

func accountMockRows(count int, values ...driver.Value) *sqlmock.Rows {
	columns := make([]string, count)
	for i := range columns {
		columns[i] = fmt.Sprintf("column_%d", i)
	}
	rows := sqlmock.NewRows(columns)
	if len(values) > 0 {
		rows.AddRow(values...)
	}
	return rows
}

func TestAccountRepositoryCommitsOnlyCoherentReadSnapshots(t *testing.T) {
	failure := errors.New("database unavailable")
	for _, scenario := range []string{"flat", "blocked pending", "begin error", "validation error", "event query error", "event iteration error", "close query error", "close iteration error", "inventory error", "bad inventory", "commit error"} {
		t.Run(scenario, func(t *testing.T) {
			db, mock := newSQLMock(t)
			defer db.Close()
			f := testutil.PaperApproval(t)
			q := paper.AccountLedgerQuery{ValidationID: f.Grant.ValidationID, AsOf: f.Now}
			begin := mock.ExpectBegin()
			if scenario == "begin error" {
				begin.WillReturnError(failure)
			} else {
				validation := mock.ExpectQuery("SELECT validation_id,run_id").WithArgs(q.ValidationID)
				if scenario == "validation error" {
					validation.WillReturnError(failure)
				} else {
					validation.WillReturnRows(accountMockRows(12, paperValidationSQLArgs(t, f.Evidence.Validation)...))
					events := mock.ExpectQuery("SELECT event_id,validation_id").WithArgs(q.ValidationID, paper.MaxAccountLedgerRows+1)
					if scenario == "event query error" {
						events.WillReturnError(failure)
					} else {
						rows := accountMockRows(15)
						if scenario == "event iteration error" {
							rows = accountMockRows(15, paperEquityEventRowValues(testPaperEquityEvent(f.Now))...)
							rows.RowError(0, failure)
						}
						events.WillReturnRows(rows).RowsWillBeClosed()
						if scenario != "event iteration error" {
							closes := mock.ExpectQuery("SELECT close_id,position_id").WithArgs(q.ValidationID, paper.MaxAccountLedgerRows+1)
							if scenario == "close query error" {
								closes.WillReturnError(failure)
							} else {
								rows := accountMockRows(32)
								if scenario == "close iteration error" {
									rows = accountMockRows(32, paperPositionCloseRowValues(testPaperPositionClose(f.Now))...)
									rows.RowError(0, failure)
								}
								closes.WillReturnRows(rows).RowsWillBeClosed()
								if scenario != "close iteration error" {
									inventory := mock.ExpectQuery("SELECT \\(SELECT count").WithArgs(q.ValidationID, q.AsOf)
									if scenario == "inventory error" {
										inventory.WillReturnError(failure)
									} else {
										values := []driver.Value{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
										if scenario == "blocked pending" {
											values[0] = 1
											values[5] = 1
										}
										if scenario == "bad inventory" {
											values[9] = 1
										}
										inventory.WillReturnRows(accountMockRows(11, values...))
									}
								}
							}
						}
					}
				}
				if scenario == "flat" || scenario == "blocked pending" {
					mock.ExpectCommit()
				} else if scenario == "commit error" {
					mock.ExpectCommit().WillReturnError(failure)
				} else {
					mock.ExpectRollback()
				}
			}
			ledger, err := postgres.NewPaperAccountRepository(db).LoadPaperAccountLedger(context.Background(), q)
			if scenario == "flat" || scenario == "blocked pending" {
				if err != nil || ledger.Validation.ValidationID != q.ValidationID {
					t.Fatalf("read snapshot %+v %v", ledger, err)
				}
				snapshot, err := paper.BuildAccountSnapshot(ledger, q.AsOf)
				if err != nil || snapshot.FlatLedgerReady != (scenario == "flat") {
					t.Fatalf("snapshot %+v %v", snapshot, err)
				}
			} else if err == nil || ledger.Validation.ValidationID != "" {
				t.Fatalf("failed read returned usable ledger %+v %v", ledger, err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestAccountRepositoryRejectsInvalidCallsWithoutSQL(t *testing.T) {
	db, mock := newSQLMock(t)
	defer db.Close()
	repo := postgres.NewPaperAccountRepository(db)
	for _, q := range []paper.AccountLedgerQuery{{}, {ValidationID: " padded ", AsOf: time.Now()}, {ValidationID: "id"}} {
		if _, err := repo.LoadPaperAccountLedger(context.Background(), q); err == nil {
			t.Fatal("bad query accepted")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := repo.LoadPaperAccountLedger(ctx, paper.AccountLedgerQuery{ValidationID: "id", AsOf: time.Now()}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	for _, r := range []*postgres.PaperAccountRepository{nil, postgres.NewPaperAccountRepository(nil)} {
		if _, err := r.LoadPaperAccountLedger(context.Background(), paper.AccountLedgerQuery{ValidationID: "id", AsOf: time.Now()}); err == nil {
			t.Fatal("missing DB accepted")
		}
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
