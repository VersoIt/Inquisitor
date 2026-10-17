package strategy_test

import (
	"context"
	"errors"
	"testing"
	"time"

	appstrategy "github.com/VersoIt/Inquisitor/internal/app/strategy"
	"github.com/VersoIt/Inquisitor/internal/clock"
	"github.com/VersoIt/Inquisitor/internal/risk"
	"github.com/VersoIt/Inquisitor/internal/strategy"
	"github.com/VersoIt/Inquisitor/internal/testutil"
)

type approvalRepo struct {
	authorization strategy.PaperAuthorization
	stats         strategy.ApprovalStats
	err           error
	writeErr      error
	reads, writes int
}

func (r *approvalRepo) LoadPaperApprovalEvidence(context.Context, string) (strategy.PaperApprovalEvidence, error) {
	r.reads++
	return r.authorization.Evidence, r.err
}
func (r *approvalRepo) RecordPaperApproval(_ context.Context, a strategy.PaperApproval) (strategy.ApprovalStats, error) {
	r.writes++
	if r.writeErr != nil {
		return strategy.ApprovalStats{}, r.writeErr
	}
	if r.err == nil {
		r.authorization.Grant = a
	}
	return r.stats, r.err
}
func (r *approvalRepo) LoadPaperAuthorization(context.Context, string) (strategy.PaperAuthorization, error) {
	r.reads++
	return r.authorization, r.err
}
func (r *approvalRepo) RevokePaperApproval(_ context.Context, _ string, v strategy.ApprovalRevocation) (strategy.ApprovalStats, error) {
	r.writes++
	if r.writeErr != nil {
		return strategy.ApprovalStats{}, r.writeErr
	}
	if r.err == nil {
		r.authorization.Revocation = v
	}
	return r.stats, r.err
}

func approvalService(t *testing.T, r *approvalRepo, f testutil.ApprovalFixture) *appstrategy.ApprovalService {
	t.Helper()
	s, err := appstrategy.NewApprovalService(r, clock.FixedClock{Time: f.Now}, f.Policy)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func approvalMarket(f testutil.ApprovalFixture) strategy.MarketContext {
	m, _ := generationInput()
	m.Now = f.Now
	m.ObservedAt = f.Now.Add(-time.Second)
	m.Regime.CloseTime = m.ObservedAt
	m.Regime.OpenTime = f.Now.Add(-time.Minute)
	m.Regime.CalculatedAt = m.ObservedAt
	return m
}

func TestApprovalServiceGrantsResolvesAndRevokesPaperOnly(t *testing.T) {
	f := testutil.PaperApproval(t)
	r := &approvalRepo{authorization: strategy.PaperAuthorization{Grant: f.Grant, Evidence: f.Evidence}, stats: strategy.ApprovalStats{Written: 1}}
	s := approvalService(t, r, f)
	a, stats, err := s.GrantPaper(context.Background(), appstrategy.GrantPaperRequest{ApprovalID: f.Grant.ApprovalID, ValidationID: f.Grant.ValidationID, StrategyName: f.Grant.StrategyName, Symbol: f.Grant.Symbol, Interval: f.Grant.Interval, ReviewedBy: "operator", Reason: "explicit paper review"})
	if err != nil || stats.Written != 1 || a.HypothesisSHA256 != f.Evidence.Hypothesis.ContentSHA256 || a.RunID != f.Evidence.Run.RunID || a.ReviewedBy != "operator" || !a.GrantedAt.Equal(f.Now) {
		t.Fatalf("grant %+v, %+v, %v", a, stats, err)
	}
	got, err := s.Resolve(context.Background(), a.ApprovalID, approvalMarket(f))
	if err != nil || !got.Enabled || got.ApprovalID != a.ApprovalID || got.ValidationID != a.ValidationID || got.RunID != a.RunID || got.HypothesisSHA256 != a.HypothesisSHA256 {
		t.Fatalf("resolve %+v, %v", got, err)
	}
	if _, err := s.Revoke(context.Background(), a.ApprovalID, "operator", "stop paper"); err != nil {
		t.Fatal(err)
	}
	got, err = s.Resolve(context.Background(), a.ApprovalID, approvalMarket(f))
	if !errors.Is(err, strategy.ErrApprovalUnavailable) || got.Enabled {
		t.Fatalf("revoked grant resolved %+v, %v", got, err)
	}
}

func TestApprovalServiceDeniesUntrustedInputsAndStorageFailures(t *testing.T) {
	storageErr := errors.New("database unavailable")
	for _, tt := range []struct {
		name   string
		mutate func(*approvalRepo, *strategy.MarketContext)
		want   error
		reads  int
	}{
		{"live mode", func(_ *approvalRepo, m *strategy.MarketContext) { m.Mode = risk.ModeLive }, strategy.ErrApprovalUnavailable, 0},
		{"repository failure", func(r *approvalRepo, _ *strategy.MarketContext) { r.err = storageErr }, storageErr, 1},
		{"wrong grant returned", func(r *approvalRepo, _ *strategy.MarketContext) { r.authorization.Grant.ApprovalID = "other" }, strategy.ErrApprovalUnavailable, 1},
		{"wrong exchange", func(_ *approvalRepo, m *strategy.MarketContext) { m.Regime.Exchange = "other" }, strategy.ErrApprovalUnavailable, 1},
		{"future evaluation", func(_ *approvalRepo, m *strategy.MarketContext) { m.Now = m.Now.Add(time.Second) }, nil, 0},
		{"cancelled grant", func(r *approvalRepo, _ *strategy.MarketContext) {
			r.authorization.Revocation = strategy.ApprovalRevocation{RevokedAt: r.authorization.Grant.GrantedAt, RevokedBy: "operator", Reason: "stop"}
		}, strategy.ErrApprovalUnavailable, 1},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := testutil.PaperApproval(t)
			r := &approvalRepo{authorization: strategy.PaperAuthorization{Grant: f.Grant, Evidence: f.Evidence}}
			m := approvalMarket(f)
			tt.mutate(r, &m)
			got, err := approvalService(t, r, f).Resolve(context.Background(), f.Grant.ApprovalID, m)
			if err == nil || (tt.want != nil && !errors.Is(err, tt.want)) || got.Enabled || r.reads != tt.reads {
				t.Fatalf("got %+v, %v, reads=%d", got, err, r.reads)
			}
		})
	}
	for _, review := range []string{"", " padded "} {
		f := testutil.PaperApproval(t)
		r := &approvalRepo{authorization: strategy.PaperAuthorization{Grant: f.Grant, Evidence: f.Evidence}, stats: strategy.ApprovalStats{Written: 1}}
		_, _, err := approvalService(t, r, f).GrantPaper(context.Background(), appstrategy.GrantPaperRequest{ApprovalID: f.Grant.ApprovalID, ValidationID: f.Grant.ValidationID, StrategyName: f.Grant.StrategyName, Symbol: f.Grant.Symbol, Interval: f.Grant.Interval, ReviewedBy: review, Reason: "review"})
		if err == nil || r.writes != 0 {
			t.Fatal("unattributed approval written")
		}
	}
}

func TestAuthorizedGenerationOverridesCallerApprovalAndJournalsProvenance(t *testing.T) {
	f := testutil.PaperApproval(t)
	r := &approvalRepo{authorization: strategy.PaperAuthorization{Grant: f.Grant, Evidence: f.Evidence}}
	m := approvalMarket(f)
	_, signal := generationInput()
	signal.HypothesisID, signal.HypothesisVersion, signal.StrategyName, signal.CreatedAt = f.Grant.HypothesisName, f.Grant.HypothesisVersion, f.Grant.StrategyName, f.Now
	signal.Trade.HypothesisID, signal.Trade.HypothesisVersion, signal.Trade.StrategyName, signal.Trade.CreatedAt, signal.Trade.ObservedAt = signal.HypothesisID, signal.HypothesisVersion, signal.StrategyName, f.Now, m.ObservedAt
	g, records := &fakeGenerator{signal: signal}, &fakeSignalRepo{stats: strategy.SignalWriteStats{Inserted: 1}}
	service := appstrategy.NewAuthorizedGenerationService(approvalService(t, r, f), appstrategy.NewGenerationService(g, records, clock.FixedClock{Time: f.Now}))
	m.Approval = strategy.ApprovalSnapshot{HypothesisID: "forged", Mode: risk.ModeLive, Enabled: true}
	got, err := service.GenerateAndRecord(context.Background(), f.Grant.ApprovalID, m)
	if err != nil || got.Signal.Trade == nil || records.calls != 1 || records.record.Features["approval.id"] != f.Grant.ApprovalID || records.record.Features["approval.hypothesis_sha256"] != f.Grant.HypothesisSHA256 || got.Signal.HypothesisID != "approval_test_hypothesis" {
		t.Fatalf("trusted generation %+v, %v, record=%+v", got, err, records.record)
	}
	r.authorization.Revocation = strategy.ApprovalRevocation{RevokedAt: f.Now, RevokedBy: "operator", Reason: "stop"}
	records.calls = 0
	g.calls = 0
	got, err = service.GenerateAndRecord(context.Background(), f.Grant.ApprovalID, m)
	if !errors.Is(err, strategy.ErrApprovalUnavailable) || got.Signal.Trade != nil || records.calls != 0 || g.calls != 0 {
		t.Fatalf("revoked generation %+v, %v", got, err)
	}
}

func TestApprovalServicesValidateDependenciesAndCancellation(t *testing.T) {
	f := testutil.PaperApproval(t)
	r := &approvalRepo{authorization: strategy.PaperAuthorization{Grant: f.Grant, Evidence: f.Evidence}}
	if _, err := appstrategy.NewApprovalService(nil, clock.FixedClock{Time: f.Now}, f.Policy); err == nil {
		t.Fatal("missing repository")
	}
	if _, err := appstrategy.NewApprovalService(r, nil, f.Policy); err == nil {
		t.Fatal("missing clock")
	}
	policy := f.Policy
	policy.Enabled = false
	if _, err := appstrategy.NewApprovalService(r, clock.FixedClock{Time: f.Now}, policy); err == nil {
		t.Fatal("disabled policy")
	}
	for _, s := range []*appstrategy.ApprovalService{nil, {}} {
		if _, err := s.Resolve(context.Background(), f.Grant.ApprovalID, approvalMarket(f)); err == nil {
			t.Fatal("missing service")
		}
		if _, _, err := s.GrantPaper(context.Background(), appstrategy.GrantPaperRequest{}); err == nil {
			t.Fatal("missing grant service")
		}
		if _, err := s.Revoke(context.Background(), "id", "by", "reason"); err == nil {
			t.Fatal("missing revoke service")
		}
	}
	s := approvalService(t, r, f)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Resolve(ctx, f.Grant.ApprovalID, approvalMarket(f)); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, _, err := s.GrantPaper(ctx, appstrategy.GrantPaperRequest{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := s.Revoke(ctx, "id", "by", "reason"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if r.reads != 0 || r.writes != 0 {
		t.Fatal("cancelled calls performed IO")
	}
}

func TestApprovalWritesFailClosedOnIOAndAcknowledgementErrors(t *testing.T) {
	for _, operation := range []string{"grant", "revoke"} {
		for _, scenario := range []string{"load error", "write error", "zero acknowledgement", "impossible acknowledgement", "invalid input"} {
			t.Run(operation+"/"+scenario, func(t *testing.T) {
				f := testutil.PaperApproval(t)
				failure := errors.New("unavailable")
				r := &approvalRepo{authorization: strategy.PaperAuthorization{Grant: f.Grant, Evidence: f.Evidence}, stats: strategy.ApprovalStats{Written: 1}}
				switch scenario {
				case "load error":
					r.err = failure
				case "write error":
					r.writeErr = failure
				case "zero acknowledgement":
					r.stats = strategy.ApprovalStats{}
				case "impossible acknowledgement":
					r.stats = strategy.ApprovalStats{Written: 1, Skipped: 1}
				}
				s := approvalService(t, r, f)
				var err error
				if operation == "grant" {
					req := appstrategy.GrantPaperRequest{ApprovalID: f.Grant.ApprovalID, ValidationID: f.Grant.ValidationID, StrategyName: f.Grant.StrategyName, Symbol: f.Grant.Symbol, Interval: f.Grant.Interval, ReviewedBy: "operator", Reason: "review"}
					if scenario == "invalid input" {
						req.ValidationID = " "
					}
					var got strategy.PaperApproval
					got, _, err = s.GrantPaper(context.Background(), req)
					if got.ApprovalID != "" {
						t.Fatal("failed grant returned usable record")
					}
				} else {
					id := f.Grant.ApprovalID
					if scenario == "invalid input" {
						id = " "
					}
					_, err = s.Revoke(context.Background(), id, "operator", "stop")
				}
				if err == nil {
					t.Fatal("write failure accepted")
				}
				if (scenario == "write error" || scenario == "load error") && !errors.Is(err, failure) {
					t.Fatalf("lost cause %v", err)
				}
			})
		}
	}
}
