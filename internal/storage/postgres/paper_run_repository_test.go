package postgres_test

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/DATA-DOG/go-sqlmock"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/VersoIt/Inquisitor/internal/storage/postgres"
	"github.com/VersoIt/Inquisitor/internal/strategy"
	"github.com/VersoIt/Inquisitor/internal/testutil"
)

func TestPaperRunRepositoryRoundTripAndFirstWriteWins(t *testing.T) {
	db, _ := testutil.IsolatedPostgres(t)
	ctx := context.Background()
	if _, err := postgres.ApplyMigrations(ctx, db, filepath.Join("..", "..", "..", "migrations")); err != nil {
		t.Fatal(err)
	}
	testutil.StorePaperApproval(t, db, testutil.PaperApproval(t))
	repo := postgres.NewPaperRunRepository(db)
	in := testutil.PaperRun(t)
	if _, err := repo.LoadPaperRun(ctx, in.RunID); !errors.Is(err, strategy.ErrPaperRunNotFound) {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := repo.RecordPaperRunInput(ctx, in); err != nil {
			t.Fatal(err)
		}
	}
	got, err := repo.LoadPaperRun(ctx, in.RunID)
	if err != nil || got.Input.SHA256 != in.SHA256 || got.Receipt != nil {
		t.Fatalf("roundtrip %+v %v", got, err)
	}
	changed := in
	changed.ConfigSHA256 = strings.Repeat("b", 64)
	changed, err = strategy.NewPaperRunInput(changed)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.RecordPaperRunInput(ctx, changed); !errors.Is(err, strategy.ErrPaperRunConflict) {
		t.Fatal("overwritten frozen input", err)
	}
	r := strategy.PaperRunReceipt{RunID: in.RunID, InputSHA256: in.SHA256, SignalID: strategy.PaperRunSignalID(in.RunID), Outcome: "NO_TRADE", CompletedAt: in.CreatedAt}
	if _, err := repo.RecordPaperRunReceipt(ctx, r); err != nil {
		t.Fatal(err)
	}
	retry := r
	retry.CompletedAt = retry.CompletedAt.Add(time.Hour)
	if saved, err := repo.RecordPaperRunReceipt(ctx, retry); err != nil || saved != r {
		t.Fatalf("completion time refreshed %+v %v", saved, err)
	}
	conflict := r
	conflict.Outcome = "RISK_REJECTED"
	_, conflict.DecisionID = strategy.PaperRiskIDs(conflict.SignalID)
	if _, err := repo.RecordPaperRunReceipt(ctx, conflict); !errors.Is(err, strategy.ErrPaperRunConflict) {
		t.Fatal("receipt overwritten", err)
	}
	for _, statement := range []string{
		`UPDATE paper_signal_runs SET input_json='{}'::jsonb`,
		`UPDATE paper_signal_runs SET receipt_json='{}'::jsonb`,
		`UPDATE paper_signal_runs SET input_sha256='bad'`,
		`UPDATE paper_signal_runs SET completed_at=created_at-interval '1 second'`,
	} {
		if _, err := db.ExecContext(ctx, statement); err == nil {
			t.Fatalf("raw malformed envelope accepted: %s", statement)
		}
	}
	// A well-shaped but changed body is rejected by checksum, not silently used.
	if _, err := db.ExecContext(ctx, `UPDATE paper_signal_runs SET input_json=jsonb_set(input_json,'{Market,SourceSHA256}',to_jsonb($1::text))`, strings.Repeat("b", 64)); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.LoadPaperRun(ctx, in.RunID); err == nil {
		t.Fatal("corrupt source body accepted")
	}
}

func TestPaperAdmissionLockIsValidationWideAndReleasedOnEveryExit(t *testing.T) {
	db, _ := testutil.IsolatedPostgres(t)
	repo := postgres.NewPaperRunRepository(db)
	ctx := context.Background()
	failure := errors.New("interrupted")
	for _, scenario := range []string{"success", "failure", "cancelled", "panic"} {
		t.Run(scenario, func(t *testing.T) {
			callCtx, cancel := context.WithCancel(ctx)
			defer cancel()
			calls := 0
			var err error
			func() {
				defer func() {
					if value := recover(); value != nil && scenario != "panic" {
						panic(value)
					}
				}()
				err = repo.WithPaperAdmissionLock(callCtx, "validation-a", func(context.Context) error {
					calls++
					if err := repo.WithPaperAdmissionLock(ctx, "validation-a", func(context.Context) error { t.Fatal("same validation lock entered"); return nil }); !errors.Is(err, strategy.ErrPaperAdmissionBusy) {
						t.Fatal("same validation did not serialize", err)
					}
					// The outer lock plus one failed nested acquisition must not leak a
					// connection; another validation can acquire the second connection.
					if err := repo.WithPaperAdmissionLock(ctx, "validation-b", func(context.Context) error { return nil }); err != nil {
						t.Fatal("unrelated validation blocked", err)
					}
					switch scenario {
					case "failure":
						return failure
					case "cancelled":
						cancel()
						return nil
					case "panic":
						panic("test callback panic")
					}
					return nil
				})
			}()
			if calls != 1 || scenario == "success" && err != nil || scenario == "failure" && !errors.Is(err, failure) || scenario == "cancelled" && !errors.Is(err, context.Canceled) {
				t.Fatalf("lock exit scenario=%s err=%v calls=%d", scenario, err, calls)
			}
			// Context rollback can finish asynchronously; bounded acquisition retries
			// check release without assuming a scheduler-specific timing.
			deadline := time.Now().Add(2 * time.Second)
			for {
				err := repo.WithPaperAdmissionLock(ctx, "validation-a", func(context.Context) error { return nil })
				if err == nil {
					break
				}
				if !errors.Is(err, strategy.ErrPaperAdmissionBusy) || time.Now().After(deadline) {
					t.Fatal("lock leaked", err)
				}
				time.Sleep(time.Millisecond)
			}
		})
	}
}

func TestPaperRunRepositoryGuardsBeforeSQL(t *testing.T) {
	db, mock := newSQLMock(t)
	defer db.Close()
	repo := postgres.NewPaperRunRepository(db)
	if _, err := repo.LoadPaperRun(context.Background(), " padded "); err == nil {
		t.Fatal("invalid ID accepted")
	}
	if err := repo.WithPaperAdmissionLock(context.Background(), "", func(context.Context) error { return nil }); err == nil {
		t.Fatal("empty validation")
	}
	if err := repo.WithPaperAdmissionLock(context.Background(), "validation", nil); err == nil {
		t.Fatal("nil callback")
	}
	db.SetMaxOpenConns(1)
	if err := repo.WithPaperAdmissionLock(context.Background(), "validation", func(context.Context) error { t.Fatal("unsafe pool entered"); return nil }); err == nil {
		t.Fatal("single connection pool accepted")
	}
	var absent *postgres.PaperRunRepository
	if _, err := absent.LoadPaperRun(context.Background(), "run"); err == nil {
		t.Fatal("nil repo")
	}
	c, cancel := context.WithCancel(context.Background())
	cancel()
	if err := repo.RecordPaperRunInput(c, testutil.PaperRun(t)); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestPaperAdmissionLockSQLFailuresReleaseTransaction(t *testing.T) {
	failure := errors.New("connection lost")
	for _, scenario := range []string{"begin", "query", "busy", "callback", "commit"} {
		t.Run(scenario, func(t *testing.T) {
			db, mock := newSQLMock(t)
			defer db.Close()
			begin := mock.ExpectBegin()
			if scenario == "begin" {
				begin.WillReturnError(failure)
			} else {
				query := mock.ExpectQuery("SELECT pg_try_advisory_xact_lock").WithArgs("validation")
				if scenario == "query" {
					query.WillReturnError(failure)
				} else {
					query.WillReturnRows(sqlmock.NewRows([]string{"locked"}).AddRow(scenario != "busy"))
				}
				if scenario == "commit" {
					mock.ExpectCommit().WillReturnError(failure)
				} else {
					mock.ExpectRollback()
				}
			}
			calls := 0
			err := postgres.NewPaperRunRepository(db).WithPaperAdmissionLock(context.Background(), "validation", func(context.Context) error {
				calls++
				if scenario == "callback" {
					return failure
				}
				return nil
			})
			if err == nil || scenario == "busy" && !errors.Is(err, strategy.ErrPaperAdmissionBusy) || (scenario == "callback" || scenario == "commit") && calls != 1 || (scenario == "begin" || scenario == "query" || scenario == "busy") && calls != 0 {
				t.Fatalf("lock boundary %s err=%v calls=%d", scenario, err, calls)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPaperRunRepositorySourceReadAndWriteFailures(t *testing.T) {
	failure := errors.New("database offline")
	for _, scenario := range []string{"read", "scan", "bad JSON", "bad checksum", "envelope mismatch", "incomplete completion", "insert", "insert acknowledgement"} {
		t.Run(scenario, func(t *testing.T) {
			db, mock := newSQLMock(t)
			defer db.Close()
			repo := postgres.NewPaperRunRepository(db)
			in := testutil.PaperRun(t)
			if scenario == "insert" || scenario == "insert acknowledgement" {
				exec := mock.ExpectExec("INSERT INTO paper_signal_runs")
				if scenario == "insert" {
					exec.WillReturnError(failure)
				} else {
					exec.WillReturnResult(sqlmock.NewResult(0, 1))
					mock.ExpectQuery("SELECT validation_id,approval_id").WillReturnError(failure)
				}
				if err := repo.RecordPaperRunInput(context.Background(), in); err == nil {
					t.Fatal("failed input write accepted")
				}
			} else {
				query := mock.ExpectQuery("SELECT validation_id,approval_id")
				if scenario == "read" {
					query.WillReturnError(failure)
				} else {
					raw, _ := json.Marshal(in)
					hash := in.SHA256
					var completed any
					switch scenario {
					case "bad JSON":
						raw = []byte(`{`)
					case "bad checksum":
						in.Market.Version = "changed"
						raw, _ = json.Marshal(in)
					case "envelope mismatch":
						hash = strings.Repeat("b", 64)
					case "incomplete completion":
						completed = in.CreatedAt
					}
					rows := accountMockRows(7)
					if scenario == "scan" {
						rows.AddRow(nil, nil, nil, nil, nil, nil, nil)
					} else {
						rows.AddRow(in.ValidationID, in.ApprovalID, hash, in.CreatedAt, string(raw), nil, completed)
					}
					query.WillReturnRows(rows)
				}
				if got, err := repo.LoadPaperRun(context.Background(), in.RunID); err == nil || got.Input.RunID != "" {
					t.Fatalf("partial/corrupt run returned %+v %v", got, err)
				}
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}
