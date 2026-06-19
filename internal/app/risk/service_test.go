package risk_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	appRisk "github.com/VersoIt/Inquisitor/internal/app/risk"
	"github.com/VersoIt/Inquisitor/internal/clock"
	domainrisk "github.com/VersoIt/Inquisitor/internal/risk"
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
