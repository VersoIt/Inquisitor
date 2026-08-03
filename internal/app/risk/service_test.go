package risk_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	appRisk "github.com/VersoIt/Inquisitor/internal/app/risk"
	"github.com/VersoIt/Inquisitor/internal/clock"
	domainrisk "github.com/VersoIt/Inquisitor/internal/risk"
	"github.com/shopspring/decimal"
)

func TestServiceEvaluatePassesClockedSnapshotsToEngine(t *testing.T) {
	now := time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)
	engine := &fakeEngine{decision: domainrisk.Decision{IntentID: "intent_0001"}}
	service := appRisk.NewService(engine, appRisk.WithClock(clock.FixedClock{Time: now}))
	req := appRisk.EvaluateRequest{Intent: domainrisk.TradeIntent{IntentID: "intent_0001"}}

	got, err := service.Evaluate(context.Background(), req)
	if err != nil {
		t.Fatalf("evaluate risk: %v", err)
	}
	if got.IntentID != "intent_0001" || engine.calls != 1 || engine.input.EvaluatedAt != now {
		t.Fatalf("service orchestration mismatch: decision=%#v engine=%#v", got, engine)
	}
}

func TestServiceEvaluateLoadsPersistentKillSwitchAndAuditsDecision(t *testing.T) {
	now := time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)
	decision := rejectedRiskDecision(now)
	engine := &fakeEngine{decision: decision}
	auditRepo := &fakeDecisionAuditRepo{}
	killSwitchRepo := &fakeKillSwitchRepo{state: domainrisk.KillSwitchState{
		Active:    true,
		Reason:    "operator emergency stop",
		Source:    "operator",
		UpdatedAt: now.Add(-time.Minute),
	}}
	service := appRisk.NewService(
		engine,
		appRisk.WithClock(clock.FixedClock{Time: now}),
		appRisk.WithDecisionAuditRepository(auditRepo),
		appRisk.WithKillSwitchRepository(killSwitchRepo),
	)

	got, err := service.Evaluate(context.Background(), appRisk.EvaluateRequest{
		DecisionID: "decision_0001",
		Intent:     auditIntent(now),
		Runtime:    domainrisk.RuntimeState{TradingEnabled: true, Mode: domainrisk.ModePaper},
	})
	if err != nil {
		t.Fatalf("evaluate risk: %v", err)
	}
	if got.IntentID != decision.IntentID || engine.calls != 1 {
		t.Fatalf("engine call mismatch: decision=%#v engine=%#v", got, engine)
	}
	if !engine.input.Runtime.KillSwitchActive {
		t.Fatalf("expected persistent kill switch state to be passed into engine: %#v", engine.input.Runtime)
	}
	if auditRepo.calls != 1 {
		t.Fatalf("expected one audit write, got %d", auditRepo.calls)
	}
	if auditRepo.record.DecisionID != "decision_0001" || auditRepo.record.Mode != domainrisk.ModePaper || auditRepo.record.Symbol != "BTCUSDT" {
		t.Fatalf("audit record mismatch: %#v", auditRepo.record)
	}
	if !auditRepo.record.EntryPrice.Equal(decimal.RequireFromString("100000")) || !auditRepo.record.Leverage.Equal(decimal.RequireFromString("1")) ||
		auditRepo.record.Confidence != 80 || auditRepo.record.IntentReason != "signal confirmed" {
		t.Fatalf("audit intent snapshot mismatch: %#v", auditRepo.record)
	}
}

func TestServiceEvaluateRejectsDependenciesAndFailuresTableDriven(t *testing.T) {
	now := time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)
	engineErr := errors.New("engine failed")
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	tests := []struct {
		name       string
		ctx        context.Context
		service    *appRisk.Service
		wantErrSub string
	}{
		{"cancelled context", cancelled, appRisk.NewService(&fakeEngine{}, appRisk.WithClock(clock.FixedClock{Time: now})), "canceled"},
		{"missing engine", context.Background(), appRisk.NewService(nil, appRisk.WithClock(clock.FixedClock{Time: now})), "engine"},
		{"missing clock", context.Background(), appRisk.NewService(&fakeEngine{}, appRisk.WithClock(nil)), "clock"},
		{"engine error", context.Background(), appRisk.NewService(&fakeEngine{err: engineErr}, appRisk.WithClock(clock.FixedClock{Time: now})), engineErr.Error()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.service.Evaluate(tt.ctx, appRisk.EvaluateRequest{})
			if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
		})
	}
}

func TestServiceEvaluateFailsClosedOnRiskControlDependenciesTableDriven(t *testing.T) {
	now := time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)
	repoErr := errors.New("repo unavailable")
	tests := []struct {
		name       string
		request    appRisk.EvaluateRequest
		auditRepo  domainrisk.DecisionAuditRepository
		killRepo   domainrisk.KillSwitchRepository
		wantErrSub string
	}{
		{
			name:       "missing audit id when audit repo configured",
			request:    appRisk.EvaluateRequest{Intent: auditIntent(now), Runtime: domainrisk.RuntimeState{Mode: domainrisk.ModePaper}},
			auditRepo:  &fakeDecisionAuditRepo{},
			wantErrSub: "decision_id",
		},
		{
			name:       "audit repository failure",
			request:    appRisk.EvaluateRequest{DecisionID: "decision_0001", Intent: auditIntent(now), Runtime: domainrisk.RuntimeState{Mode: domainrisk.ModePaper}},
			auditRepo:  &fakeDecisionAuditRepo{err: repoErr},
			wantErrSub: repoErr.Error(),
		},
		{
			name:       "kill switch repository failure",
			request:    appRisk.EvaluateRequest{DecisionID: "decision_0001", Intent: auditIntent(now), Runtime: domainrisk.RuntimeState{Mode: domainrisk.ModePaper}},
			killRepo:   &fakeKillSwitchRepo{err: repoErr},
			wantErrSub: repoErr.Error(),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := appRisk.NewService(
				&fakeEngine{decision: rejectedRiskDecision(now)},
				appRisk.WithClock(clock.FixedClock{Time: now}),
				appRisk.WithDecisionAuditRepository(tt.auditRepo),
				appRisk.WithKillSwitchRepository(tt.killRepo),
			)

			_, err := service.Evaluate(context.Background(), tt.request)
			if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
		})
	}
}

func TestServiceKillSwitchCommandsAppendEventsTableDriven(t *testing.T) {
	now := time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name       string
		run        func(*appRisk.Service, context.Context, appRisk.KillSwitchRequest) (domainrisk.KillSwitchEvent, error)
		wantActive bool
	}{
		{
			name: "activate",
			run: func(s *appRisk.Service, ctx context.Context, req appRisk.KillSwitchRequest) (domainrisk.KillSwitchEvent, error) {
				return s.ActivateKillSwitch(ctx, req)
			},
			wantActive: true,
		},
		{
			name: "release",
			run: func(s *appRisk.Service, ctx context.Context, req appRisk.KillSwitchRequest) (domainrisk.KillSwitchEvent, error) {
				return s.ReleaseKillSwitch(ctx, req)
			},
			wantActive: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeKillSwitchRepo{}
			service := appRisk.NewService(
				&fakeEngine{},
				appRisk.WithClock(clock.FixedClock{Time: now}),
				appRisk.WithKillSwitchRepository(repo),
			)
			event, err := tt.run(service, context.Background(), appRisk.KillSwitchRequest{
				EventID: " risk_kill_switch_0001 ",
				Reason:  " operator command ",
				Source:  " OPERATOR ",
			})
			if err != nil {
				t.Fatalf("append kill switch event: %v", err)
			}
			if event.Active != tt.wantActive || event.EventID != "risk_kill_switch_0001" || event.Source != "operator" || event.CreatedAt != now {
				t.Fatalf("event mismatch: %#v", event)
			}
			if repo.appended.EventID != event.EventID || repo.appendCalls != 1 {
				t.Fatalf("repository event mismatch: calls=%d event=%#v", repo.appendCalls, repo.appended)
			}
		})
	}
}

func TestServiceKillSwitchCommandsRejectMissingDependencies(t *testing.T) {
	now := time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)
	service := appRisk.NewService(&fakeEngine{}, appRisk.WithClock(clock.FixedClock{Time: now}))
	_, err := service.ActivateKillSwitch(context.Background(), appRisk.KillSwitchRequest{
		EventID: "risk_kill_switch_0001",
		Reason:  "operator command",
		Source:  "operator",
	})
	if err == nil || !strings.Contains(err.Error(), "kill switch repository") {
		t.Fatalf("expected missing repository error, got %v", err)
	}
}

func TestServiceKillSwitchReadUseCasesTableDriven(t *testing.T) {
	now := time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)
	state := domainrisk.KillSwitchState{
		Active:    true,
		Reason:    "operator emergency stop",
		Source:    "operator",
		UpdatedAt: now,
	}
	events := []domainrisk.KillSwitchEvent{{
		EventID:   "risk_kill_switch_0001",
		Active:    true,
		Reason:    "operator emergency stop",
		Source:    "operator",
		CreatedAt: now,
	}}

	tests := []struct {
		name       string
		run        func(*appRisk.Service, context.Context) error
		assertRepo func(*testing.T, *fakeKillSwitchRepo)
		wantErrSub string
	}{
		{
			name: "current state",
			run: func(service *appRisk.Service, ctx context.Context) error {
				got, err := service.CurrentKillSwitchState(ctx)
				if err != nil {
					return err
				}
				if got != state {
					return fmt.Errorf("state mismatch: %#v", got)
				}
				return nil
			},
			assertRepo: func(t *testing.T, repo *fakeKillSwitchRepo) {
				t.Helper()
				if repo.currentCall != 1 {
					t.Fatalf("current calls mismatch: %d", repo.currentCall)
				}
			},
		},
		{
			name: "list events",
			run: func(service *appRisk.Service, ctx context.Context) error {
				got, err := service.ListKillSwitchEvents(ctx, domainrisk.KillSwitchEventQuery{Source: "operator", Limit: 5})
				if err != nil {
					return err
				}
				if len(got) != 1 || got[0] != events[0] {
					return fmt.Errorf("events mismatch: %#v", got)
				}
				return nil
			},
			assertRepo: func(t *testing.T, repo *fakeKillSwitchRepo) {
				t.Helper()
				if repo.listCalls != 1 || repo.query.Source != "operator" || repo.query.Limit != 5 {
					t.Fatalf("list query mismatch: calls=%d query=%#v", repo.listCalls, repo.query)
				}
			},
		},
		{
			name: "invalid list query",
			run: func(service *appRisk.Service, ctx context.Context) error {
				_, err := service.ListKillSwitchEvents(ctx, domainrisk.KillSwitchEventQuery{Source: "Operator"})
				return err
			},
			wantErrSub: "source",
		},
		{
			name: "repository error",
			run: func(service *appRisk.Service, ctx context.Context) error {
				_, err := service.CurrentKillSwitchState(ctx)
				return err
			},
			wantErrSub: "repo unavailable",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &fakeKillSwitchRepo{state: state, events: events}
			if tt.name == "repository error" {
				repo.err = errors.New("repo unavailable")
			}
			service := appRisk.NewService(nil, appRisk.WithKillSwitchRepository(repo))
			err := tt.run(service, context.Background())
			if tt.wantErrSub != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("run use case: %v", err)
			}
			if tt.assertRepo != nil {
				tt.assertRepo(t, repo)
			}
		})
	}
}

type fakeEngine struct {
	input    domainrisk.EvaluationInput
	decision domainrisk.Decision
	calls    int
	err      error
}

func (e *fakeEngine) Evaluate(input domainrisk.EvaluationInput) (domainrisk.Decision, error) {
	e.calls++
	e.input = input
	if e.err != nil {
		return domainrisk.Decision{}, e.err
	}
	return e.decision, nil
}

type fakeDecisionAuditRepo struct {
	record domainrisk.DecisionAuditRecord
	calls  int
	err    error
}

func (r *fakeDecisionAuditRepo) RecordDecision(_ context.Context, record domainrisk.DecisionAuditRecord) (domainrisk.DecisionAuditStats, error) {
	r.calls++
	r.record = record
	if r.err != nil {
		return domainrisk.DecisionAuditStats{}, r.err
	}
	return domainrisk.DecisionAuditStats{Inserted: 1}, nil
}

func (r *fakeDecisionAuditRepo) ListDecisions(context.Context, domainrisk.DecisionAuditQuery) ([]domainrisk.DecisionAuditRecord, error) {
	return nil, fmt.Errorf("not implemented")
}

type fakeKillSwitchRepo struct {
	state       domainrisk.KillSwitchState
	events      []domainrisk.KillSwitchEvent
	query       domainrisk.KillSwitchEventQuery
	appended    domainrisk.KillSwitchEvent
	currentCall int
	appendCalls int
	listCalls   int
	err         error
}

func (r *fakeKillSwitchRepo) AppendKillSwitchEvent(_ context.Context, event domainrisk.KillSwitchEvent) (domainrisk.KillSwitchStats, error) {
	r.appendCalls++
	r.appended = event
	if r.err != nil {
		return domainrisk.KillSwitchStats{}, r.err
	}
	return domainrisk.KillSwitchStats{Inserted: 1}, nil
}

func (r *fakeKillSwitchRepo) CurrentKillSwitchState(context.Context) (domainrisk.KillSwitchState, error) {
	r.currentCall++
	if r.err != nil {
		return domainrisk.KillSwitchState{}, r.err
	}
	return r.state, nil
}

func (r *fakeKillSwitchRepo) ListKillSwitchEvents(_ context.Context, query domainrisk.KillSwitchEventQuery) ([]domainrisk.KillSwitchEvent, error) {
	r.listCalls++
	r.query = query
	if r.err != nil {
		return nil, r.err
	}
	return append([]domainrisk.KillSwitchEvent(nil), r.events...), nil
}

func auditIntent(now time.Time) domainrisk.TradeIntent {
	return domainrisk.TradeIntent{
		IntentID:     "intent_0001",
		HypothesisID: "hypothesis_0001",
		StrategyName: "trend-momentum",
		Symbol:       "BTCUSDT",
		Side:         domainrisk.SideLong,
		Confidence:   80,
		EntryPrice:   decimal.RequireFromString("100000"),
		Leverage:     decimal.RequireFromString("1"),
		Reason:       "signal confirmed",
		CreatedAt:    now.Add(-time.Minute),
	}
}

func rejectedRiskDecision(now time.Time) domainrisk.Decision {
	return domainrisk.Decision{
		IntentID:  "intent_0001",
		Approved:  false,
		Reason:    "kill_switch_active",
		Checks:    []domainrisk.Check{{Name: "kill_switch_inactive", Passed: false, Reason: "kill_switch_active"}},
		CreatedAt: now,
		StopLoss:  decimal.Zero,
	}
}
