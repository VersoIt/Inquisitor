package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/VersoIt/Inquisitor/internal/clock"
	"github.com/VersoIt/Inquisitor/internal/config"
	"github.com/VersoIt/Inquisitor/internal/paper"
	"github.com/VersoIt/Inquisitor/internal/testutil"
)

type ledgerReader struct {
	ledger paper.AccountLedger
	calls  int
	err    error
}

func (r *ledgerReader) LoadPaperAccountLedger(_ context.Context, q paper.AccountLedgerQuery) (paper.AccountLedger, error) {
	r.calls++
	return r.ledger, r.err
}

func commandDependencies(t *testing.T, r *ledgerReader) dependencies {
	t.Helper()
	f := testutil.PaperApproval(t)
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Error(err)
		}
		_ = db.Close()
	})
	return dependencies{openDB: func(ctx context.Context, c config.DatabaseConfig) (*sql.DB, error) {
		if _, ok := ctx.Deadline(); !ok {
			t.Fatal("missing deadline")
		}
		if c.DSN != "test-dsn" {
			t.Fatal("wrong DSN")
		}
		mock.ExpectClose()
		return db, nil
	}, reader: func(*sql.DB) paper.AccountLedgerReader { return r }, lookupEnv: func(string) (string, bool) { return "test-dsn", true }, clock: clock.FixedClock{Time: f.Now}}
}

func TestPaperAccountCommandReadOnlyReadyAndBlockedModes(t *testing.T) {
	for _, tc := range []struct {
		name                 string
		pending, requireFlat bool
		wantError            bool
	}{
		{"flat", false, false, false}, {"flat required", false, true, false}, {"blocked inspect", true, false, false}, {"blocked require flat", true, true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := testutil.PaperApproval(t)
			r := &ledgerReader{ledger: paper.AccountLedger{Validation: f.Evidence.Validation}}
			if tc.pending {
				r.ledger.Inventory.Tickets = 1
				r.ledger.Inventory.PendingTickets = 1
			}
			args := []string{"-validation-id", f.Grant.ValidationID}
			if tc.requireFlat {
				args = append(args, "-require-flat")
			}
			var output bytes.Buffer
			err := run(context.Background(), args, &output, commandDependencies(t, r))
			if (err != nil) != tc.wantError || tc.wantError && !errors.Is(err, errLedgerNotFlat) {
				t.Fatalf("wrong exit semantics: %v", err)
			}
			var artifact struct {
				Snapshot paper.AccountSnapshot
				Warning  string
			}
			if err := json.Unmarshal(output.Bytes(), &artifact); err != nil {
				t.Fatal(err)
			}
			if artifact.Snapshot.FlatLedgerReady == tc.pending || (artifact.Snapshot.Account == nil) != tc.pending || r.calls != 1 || !strings.Contains(artifact.Warning, "not global") {
				t.Fatalf("misleading account output: %s", output.String())
			}
			if strings.Contains(output.String(), "test-dsn") {
				t.Fatal("DSN leaked")
			}
		})
	}
}

func TestPaperAccountCommandInvalidFlagsStopBeforeIO(t *testing.T) {
	for _, args := range [][]string{nil, {"-validation-id", " padded "}, {"-validation-id", "id", "-timeout", "0"}, {"-validation-id", "id", "-dsn-env", "invalid-name"}, {"-validation-id", "id", "extra"}, {"-validation-id", "id", "-mode", "live"}} {
		deps := dependencies{lookupEnv: func(string) (string, bool) { t.Fatal("invalid flags read environment"); return "", false }, openDB: func(context.Context, config.DatabaseConfig) (*sql.DB, error) {
			t.Fatal("invalid flags opened DB")
			return nil, nil
		}}
		if err := run(context.Background(), args, io.Discard, deps); err == nil {
			t.Fatalf("invalid flags accepted %v", args)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := run(ctx, []string{"-validation-id", "id"}, io.Discard, dependencies{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := run(context.Background(), []string{"-help"}, io.Discard, dependencies{}); !errors.Is(err, flag.ErrHelp) {
		t.Fatal(err)
	}
}

type brokenOutput struct{}

func (brokenOutput) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestPaperAccountCommandFailuresAndCredentialRedaction(t *testing.T) {
	f := testutil.PaperApproval(t)
	for _, scenario := range []string{"missing DSN", "connection failure", "nil database", "nil reader", "storage failure", "wrong validation", "deadline", "output failure"} {
		t.Run(scenario, func(t *testing.T) {
			r := &ledgerReader{ledger: paper.AccountLedger{Validation: f.Evidence.Validation}}
			deps := commandDependencies(t, r)
			args := []string{"-validation-id", f.Grant.ValidationID}
			var output bytes.Buffer
			var writer io.Writer = &output
			switch scenario {
			case "missing DSN":
				deps.lookupEnv = func(string) (string, bool) { return "", false }
			case "connection failure":
				deps.openDB = func(context.Context, config.DatabaseConfig) (*sql.DB, error) {
					return nil, errors.New("DSN contains SUPER_SECRET")
				}
			case "nil database":
				deps.openDB = func(context.Context, config.DatabaseConfig) (*sql.DB, error) { return nil, nil }
			case "nil reader":
				deps.reader = func(*sql.DB) paper.AccountLedgerReader { return nil }
			case "storage failure":
				r.err = errors.New("storage unavailable")
			case "wrong validation":
				r.ledger.Validation.ValidationID = "other"
			case "deadline":
				args = append(args, "-timeout", "1ms")
				deps.openDB = func(ctx context.Context, _ config.DatabaseConfig) (*sql.DB, error) {
					<-ctx.Done()
					return nil, ctx.Err()
				}
			case "output failure":
				writer = brokenOutput{}
			}
			err := run(context.Background(), args, writer, deps)
			if err == nil || strings.Contains(err.Error(), "SUPER_SECRET") || output.Len() != 0 {
				t.Fatalf("failure leaked usable output/credentials: %v %s", err, output.String())
			}
		})
	}
}
