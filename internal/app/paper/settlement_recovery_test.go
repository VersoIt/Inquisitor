package paper_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	apppaper "github.com/VersoIt/Inquisitor/internal/app/paper"
	"github.com/VersoIt/Inquisitor/internal/clock"
	domainpaper "github.com/VersoIt/Inquisitor/internal/paper"
	"github.com/shopspring/decimal"
)

var errSettlementConnectionLost = errors.New("simulated connection lost")

// These fakes retain committed rows even when the acknowledgement is lost and
// enforce the same immutable payload/unique-source contract as PostgreSQL.
type recoveryCloseRepository struct {
	fakePositionCloseRepository
	failBefore, failAfter bool
}

func (r *recoveryCloseRepository) RecordPositionClose(_ context.Context, close domainpaper.PositionClose) (domainpaper.PositionCloseStats, error) {
	r.calls++
	if err := domainpaper.ValidatePositionClose(close); err != nil {
		return domainpaper.PositionCloseStats{}, err
	}
	if r.failBefore {
		r.failBefore = false
		return domainpaper.PositionCloseStats{}, errSettlementConnectionLost
	}
	for _, old := range r.closes {
		if old.CloseID == close.CloseID {
			if !sameRecoveryPayload(old, close) {
				return domainpaper.PositionCloseStats{}, fmt.Errorf("close payload conflict")
			}
			return domainpaper.PositionCloseStats{Skipped: 1}, nil
		}
		if old.PositionID == close.PositionID {
			return domainpaper.PositionCloseStats{}, fmt.Errorf("position already closed")
		}
	}
	r.closes = append(r.closes, close)
	if r.failAfter {
		r.failAfter = false
		return domainpaper.PositionCloseStats{}, errSettlementConnectionLost
	}
	return domainpaper.PositionCloseStats{Inserted: 1}, nil
}

type recoveryEquityRepository struct {
	fakeEquityEventRepository
	failBefore, failAfter bool
}

func (r *recoveryEquityRepository) RecordEquityEvent(_ context.Context, event domainpaper.EquityEvent) (domainpaper.EquityEventStats, error) {
	r.calls++
	if err := domainpaper.ValidateEquityEvent(event); err != nil {
		return domainpaper.EquityEventStats{}, err
	}
	if r.failBefore {
		r.failBefore = false
		return domainpaper.EquityEventStats{}, errSettlementConnectionLost
	}
	for _, old := range r.events {
		if old.EventID == event.EventID {
			if !sameRecoveryPayload(old, event) {
				return domainpaper.EquityEventStats{}, fmt.Errorf("equity payload conflict")
			}
			return domainpaper.EquityEventStats{Skipped: 1}, nil
		}
		if old.CloseID == event.CloseID || old.Sequence == event.Sequence {
			return domainpaper.EquityEventStats{}, fmt.Errorf("equity source/sequence conflict")
		}
	}
	r.events = append(r.events, event)
	if r.failAfter {
		r.failAfter = false
		return domainpaper.EquityEventStats{}, errSettlementConnectionLost
	}
	return domainpaper.EquityEventStats{Inserted: 1}, nil
}

func sameRecoveryPayload(a, b any) bool {
	left, err := json.Marshal(a)
	if err != nil {
		return false
	}
	right, err := json.Marshal(b)
	return err == nil && bytes.Equal(left, right)
}

func recoverySettlementService(at time.Time, record domainpaper.ValidationRecord, position domainpaper.OpenPosition, closes domainpaper.PositionCloseRepository, equity domainpaper.EquityEventRepository) *apppaper.Service {
	return apppaper.NewService(&fakeRunRepository{}, &fakeResultRepository{},
		apppaper.WithValidationRecordRepository(&fakeValidationRecordRepository{records: []domainpaper.ValidationRecord{record}}),
		apppaper.WithOpenPositionRepository(&fakeOpenPositionRepository{positions: []domainpaper.OpenPosition{position}}),
		apppaper.WithPositionCloseRepository(closes), apppaper.WithEquityEventRepository(equity), apppaper.WithClock(clock.FixedClock{Time: at}))
}

func TestSettlementRecoversAtEveryPersistenceBoundaryOnALaterClock(t *testing.T) {
	for _, scenario := range []string{"completed", "close write failed", "close acknowledgement lost", "equity write failed", "equity acknowledgement lost"} {
		t.Run(scenario, func(t *testing.T) {
			now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
			position := appOpenPosition(now)
			record := testValidationRecord(t, "research_app_0001", now.Add(-2*time.Hour), domainpaper.ValidationStatusRunning)
			record.ValidationID = position.ValidationID
			closes := &recoveryCloseRepository{failBefore: scenario == "close write failed", failAfter: scenario == "close acknowledgement lost"}
			equity := &recoveryEquityRepository{failBefore: scenario == "equity write failed", failAfter: scenario == "equity acknowledgement lost"}
			req := apppaper.SettlePositionCloseRequest{EventID: "paper_equity_app_0001", Close: appClosePositionRequest(now)}
			req.Close.ClosedAt = req.Close.ClosedAt.Add(789 * time.Nanosecond)
			firstAt, laterAt := now.Add(5*time.Minute+987*time.Nanosecond), now.Add(24*time.Hour+654*time.Nanosecond)
			_, err := recoverySettlementService(firstAt, record, position, closes, equity).SettlePositionClose(context.Background(), req)
			if scenario == "completed" && err != nil {
				t.Fatal(err)
			}
			if scenario != "completed" && !errors.Is(err, errSettlementConnectionLost) {
				t.Fatalf("expected injected failure: %v", err)
			}
			closeCommitted, equityCommitted := len(closes.closes) == 1, len(equity.events) == 1
			got, err := recoverySettlementService(laterAt, record, position, closes, equity).SettlePositionClose(context.Background(), req)
			if err != nil {
				t.Fatalf("late recovery failed: %v", err)
			}
			wantCloseAt, wantEquityAt := laterAt.Truncate(time.Microsecond), laterAt.Truncate(time.Microsecond)
			if closeCommitted {
				wantCloseAt = firstAt.Truncate(time.Microsecond)
			}
			if equityCommitted {
				wantEquityAt = firstAt.Truncate(time.Microsecond)
			}
			if !got.Close.ClosedAt.Equal(req.Close.ClosedAt.Truncate(time.Microsecond)) || !got.Event.OccurredAt.Equal(got.Close.ClosedAt) {
				t.Fatal("journal timestamp precision changed on recovery")
			}
			if !got.Close.RecordedAt.Equal(wantCloseAt) || !got.Event.RecordedAt.Equal(wantEquityAt) || (got.CloseStats.Skipped == 1) != closeCommitted || (got.EquityStats.Skipped == 1) != equityCommitted {
				t.Fatalf("first-write history lost: %+v", got)
			}
			again, err := recoverySettlementService(laterAt.Add(time.Hour), record, position, closes, equity).SettlePositionClose(context.Background(), req)
			if err != nil || again.CloseStats.Skipped != 1 || again.EquityStats.Skipped != 1 || !sameRecoveryPayload(got.Close, again.Close) || !sameRecoveryPayload(got.Event, again.Event) {
				t.Fatalf("recovery not idempotent: %+v %v", again, err)
			}
			if len(closes.closes) != 1 || len(equity.events) != 1 || got.Event.Sequence != 1 || !got.Event.EquityAfter.Equal(record.InitialBalance.Add(got.Close.NetPnL)) {
				t.Fatal("duplicate close or double-counted PnL")
			}
		})
	}
}

func TestLateCloseRetryRejectsChangedPayloadWithoutAccounting(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*apppaper.SettlePositionCloseRequest)
	}{
		{"exit price", func(r *apppaper.SettlePositionCloseRequest) {
			r.Close.ExitMidPrice = r.Close.ExitMidPrice.Add(decimal.NewFromInt(100))
			r.Close.ExitPrice = r.Close.ExitPrice.Add(decimal.NewFromInt(100))
			r.Close.ExitFee = r.Close.ExitPrice.Mul(decimal.RequireFromString("0.5")).Mul(r.Close.ExitFeeBPS).Div(decimal.NewFromInt(10000))
		}},
		{"close reason", func(r *apppaper.SettlePositionCloseRequest) {
			r.Close.CloseReason = domainpaper.PositionCloseReasonManual
		}},
		{"close time", func(r *apppaper.SettlePositionCloseRequest) { r.Close.ClosedAt = r.Close.ClosedAt.Add(time.Second) }},
		{"spread evidence", func(r *apppaper.SettlePositionCloseRequest) {
			r.Close.SpreadBPS = r.Close.SpreadBPS.Add(decimal.NewFromInt(1))
		}},
		{"another close id", func(r *apppaper.SettlePositionCloseRequest) { r.Close.CloseID += "_other" }},
		{"another event id", func(r *apppaper.SettlePositionCloseRequest) { r.EventID += "_other" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
			position := appOpenPosition(now)
			record := testValidationRecord(t, "research_app_0001", now.Add(-2*time.Hour), domainpaper.ValidationStatusRunning)
			record.ValidationID = position.ValidationID
			closes, equity := &recoveryCloseRepository{}, &recoveryEquityRepository{}
			req := apppaper.SettlePositionCloseRequest{EventID: "paper_equity_app_0001", Close: appClosePositionRequest(now)}
			first, err := recoverySettlementService(now.Add(5*time.Minute), record, position, closes, equity).SettlePositionClose(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			equityCalls := equity.calls
			tc.mutate(&req)
			if _, err := recoverySettlementService(now.Add(24*time.Hour), record, position, closes, equity).SettlePositionClose(context.Background(), req); err == nil {
				t.Fatal("changed settlement accepted")
			}
			if equity.calls != equityCalls || len(closes.closes) != 1 || len(equity.events) != 1 || !sameRecoveryPayload(first.Close, closes.closes[0]) || !sameRecoveryPayload(first.Event, equity.events[0]) {
				t.Fatal("conflict changed durable journal or reached equity write")
			}
		})
	}
}

func TestEquityReplayVerifiesSourceBeforeWriting(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*domainpaper.EquityEvent)
	}{
		{"different pnl", func(e *domainpaper.EquityEvent) {
			e.NetPnL = e.NetPnL.Add(decimal.NewFromInt(1))
			e.EquityAfter = e.EquityBefore.Add(e.NetPnL)
		}},
		{"different fees", func(e *domainpaper.EquityEvent) { e.Fees = e.Fees.Add(decimal.NewFromInt(1)) }},
		{"different position", func(e *domainpaper.EquityEvent) { e.PositionID += "_other" }},
		{"different market", func(e *domainpaper.EquityEvent) { e.Symbol = "ETHUSDT" }},
		{"different timestamp", func(e *domainpaper.EquityEvent) { e.OccurredAt = e.OccurredAt.Add(-time.Second) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
			close, event := appPositionClose(now), appEquityEvent(now)
			tc.mutate(&event)
			record := testValidationRecord(t, "research_app_0001", now.Add(-2*time.Hour), domainpaper.ValidationStatusRunning)
			record.ValidationID = close.ValidationID
			equity := &fakeEquityEventRepository{events: []domainpaper.EquityEvent{event}, stats: domainpaper.EquityEventStats{Skipped: 1}}
			service := accountPositionCloseService(now.Add(24*time.Hour), record, []domainpaper.PositionClose{close}, equity)
			if _, err := service.AccountPositionClose(context.Background(), apppaper.AccountPositionCloseRequest{EventID: event.EventID, CloseID: close.CloseID}); err == nil || equity.calls != 0 {
				t.Fatalf("corrupt replay reached writer: calls=%d err=%v", equity.calls, err)
			}
		})
	}
}
