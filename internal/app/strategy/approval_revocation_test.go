package strategy_test

import (
	"context"
	"errors"
	"testing"
	"time"

	appstrategy "github.com/VersoIt/Inquisitor/internal/app/strategy"
	"github.com/VersoIt/Inquisitor/internal/clock"
	"github.com/VersoIt/Inquisitor/internal/strategy"
)

type revocationOnlyRepo struct {
	id         string
	revocation strategy.ApprovalRevocation
	stats      strategy.ApprovalStats
	err        error
	calls      int
}

func (r *revocationOnlyRepo) RevokePaperApproval(_ context.Context, id string, v strategy.ApprovalRevocation) (strategy.ApprovalStats, error) {
	r.calls++
	r.id, r.revocation = id, v
	return r.stats, r.err
}

func TestRevocationUseCaseDoesNotRequireResearchOrGeneration(t *testing.T) {
	at := time.Date(2026, 11, 13, 12, 0, 0, 123456789, time.FixedZone("operator", 3*3600))
	for _, stats := range []strategy.ApprovalStats{{Written: 1}, {Skipped: 1}} {
		r := &revocationOnlyRepo{stats: stats}
		got, err := appstrategy.NewApprovalRevocationService(r, clock.FixedClock{Time: at}).Revoke(context.Background(), "approval", "operator", "stop new entries")
		if err != nil || got != stats || r.id != "approval" || r.calls != 1 || !r.revocation.RevokedAt.Equal(at.UTC().Truncate(time.Microsecond)) || r.revocation.RevokedAt.Location() != time.UTC {
			t.Fatalf("revocation %+v %v repo=%+v", got, err, r)
		}
	}
}

func TestRevocationUseCaseFailsClosed(t *testing.T) {
	for _, tc := range []struct{ name, id, by, reason string }{
		{"empty id", "", "operator", "stop"}, {"padded id", " id ", "operator", "stop"},
		{"empty reviewer", "id", "", "stop"}, {"padded reviewer", "id", " operator ", "stop"},
		{"empty reason", "id", "operator", ""}, {"padded reason", "id", "operator", " stop "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &revocationOnlyRepo{stats: strategy.ApprovalStats{Written: 1}}
			if _, err := appstrategy.NewApprovalRevocationService(r, clock.SystemClock{}).Revoke(context.Background(), tc.id, tc.by, tc.reason); err == nil || r.calls != 0 {
				t.Fatal("invalid request reached write")
			}
		})
	}
	for _, stats := range []strategy.ApprovalStats{{}, {Written: 2}, {Written: 1, Skipped: 1}, {Skipped: -1}} {
		r := &revocationOnlyRepo{stats: stats}
		if _, err := appstrategy.NewApprovalRevocationService(r, clock.SystemClock{}).Revoke(context.Background(), "id", "operator", "stop"); err == nil {
			t.Fatal("invalid acknowledgement accepted")
		}
	}
	failure := errors.New("storage unavailable")
	r := &revocationOnlyRepo{err: failure}
	if _, err := appstrategy.NewApprovalRevocationService(r, clock.SystemClock{}).Revoke(context.Background(), "id", "operator", "stop"); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r.calls = 0
	if _, err := appstrategy.NewApprovalRevocationService(r, clock.SystemClock{}).Revoke(ctx, "id", "operator", "stop"); !errors.Is(err, context.Canceled) || r.calls != 0 {
		t.Fatal("cancellation ignored")
	}
	for _, s := range []*appstrategy.ApprovalRevocationService{nil, appstrategy.NewApprovalRevocationService(nil, clock.SystemClock{}), appstrategy.NewApprovalRevocationService(r, nil)} {
		if _, err := s.Revoke(context.Background(), "id", "operator", "stop"); err == nil {
			t.Fatal("missing dependency accepted")
		}
	}
}
