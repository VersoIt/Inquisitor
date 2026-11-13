package paper_test

import (
	"context"
	"errors"
	"testing"
	"time"

	apppaper "github.com/VersoIt/Inquisitor/internal/app/paper"
	"github.com/VersoIt/Inquisitor/internal/clock"
	"github.com/VersoIt/Inquisitor/internal/paper"
	"github.com/VersoIt/Inquisitor/internal/testutil"
)

type accountReader struct {
	ledger paper.AccountLedger
	query  paper.AccountLedgerQuery
	calls  int
	err    error
	cancel func()
}

func (r *accountReader) LoadPaperAccountLedger(_ context.Context, q paper.AccountLedgerQuery) (paper.AccountLedger, error) {
	r.calls++
	r.query = q
	if r.cancel != nil {
		r.cancel()
	}
	return r.ledger, r.err
}

func TestAccountSnapshotServiceScopesAndTimestampsItsRead(t *testing.T) {
	f := testutil.PaperApproval(t)
	r := &accountReader{ledger: paper.AccountLedger{Validation: f.Evidence.Validation}}
	at := f.Now.Add(987 * time.Nanosecond)
	got, err := apppaper.NewAccountSnapshotService(r, clock.FixedClock{Time: at}).Snapshot(context.Background(), f.Grant.ValidationID)
	if err != nil || !got.FlatLedgerReady || got.Account == nil || r.calls != 1 || r.query.ValidationID != f.Grant.ValidationID || !r.query.AsOf.Equal(at.Truncate(time.Microsecond)) {
		t.Fatalf("snapshot %+v %v query=%+v", got, err, r.query)
	}
}

func TestAccountSnapshotServiceRejectsInvalidCallsAndStorageFailures(t *testing.T) {
	f := testutil.PaperApproval(t)
	for _, scenario := range []string{"empty id", "padded id", "wrong source", "storage failure", "cancelled before read", "cancelled after read"} {
		t.Run(scenario, func(t *testing.T) {
			r := &accountReader{ledger: paper.AccountLedger{Validation: f.Evidence.Validation}}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			id := f.Grant.ValidationID
			switch scenario {
			case "empty id":
				id = ""
			case "padded id":
				id = " id "
			case "wrong source":
				r.ledger.Validation.ValidationID = "other"
			case "storage failure":
				r.err = errors.New("storage unavailable")
			case "cancelled before read":
				cancel()
			case "cancelled after read":
				r.cancel = cancel
			}
			got, err := apppaper.NewAccountSnapshotService(r, clock.FixedClock{Time: f.Now}).Snapshot(ctx, id)
			if err == nil || got.Account != nil {
				t.Fatalf("invalid snapshot returned %+v %v", got, err)
			}
			if (scenario == "empty id" || scenario == "padded id" || scenario == "cancelled before read") && r.calls != 0 {
				t.Fatal("invalid invocation reached storage")
			}
		})
	}
	for _, s := range []*apppaper.AccountSnapshotService{nil, apppaper.NewAccountSnapshotService(nil, clock.SystemClock{}), apppaper.NewAccountSnapshotService(&accountReader{}, nil)} {
		if _, err := s.Snapshot(context.Background(), "id"); err == nil {
			t.Fatal("missing dependency accepted")
		}
	}
}
