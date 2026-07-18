package paper_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	apppaper "github.com/VersoIt/Inquisitor/internal/app/paper"
	"github.com/VersoIt/Inquisitor/internal/backtest"
	"github.com/VersoIt/Inquisitor/internal/clock"
	domainpaper "github.com/VersoIt/Inquisitor/internal/paper"
)

func TestServiceClosePositionFromOpenPosition(t *testing.T) {
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	position := appOpenPosition(now)
	record := testValidationRecord(t, "research_app_0001", now.Add(-2*time.Hour), domainpaper.ValidationStatusRunning)
	record.ValidationID = position.ValidationID
	closes := &fakePositionCloseRepository{stats: domainpaper.PositionCloseStats{Inserted: 1}}
	service := closePositionService(now, record, []domainpaper.OpenPosition{position}, closes)

	got, err := service.ClosePosition(context.Background(), apppaper.ClosePositionRequest{
		CloseID:      " paper_close_app_0001 ",
		PositionID:   " paper_position_app_0001 ",
		Liquidity:    " taker ",
		ExitMidPrice: decimal.RequireFromString("101000"),
		ExitPrice:    decimal.RequireFromString("100950"),
		ExitFee:      decimal.RequireFromString("30.285"),
		ExitFeeBPS:   decimal.RequireFromString("6"),
		SpreadBPS:    decimal.RequireFromString("2"),
		SlippageBPS:  decimal.RequireFromString("3"),
		CloseReason:  " take_profit ",
		ClosedAt:     position.OpenedAt.Add(2 * time.Minute),
	})
	if err != nil {
		t.Fatalf("close position: %v", err)
	}

	if got.Record.ValidationID != record.ValidationID || got.Position.PositionID != position.PositionID {
		t.Fatalf("context mismatch: %#v", got)
	}
	if got.Close.CloseID != "paper_close_app_0001" || got.Close.PositionID != position.PositionID ||
		got.Close.EntryFillID != position.FillID || got.Close.Liquidity != backtest.LiquidityTaker ||
		got.Close.CloseReason != domainpaper.PositionCloseReasonTakeProfit {
		t.Fatalf("close identity mismatch: %#v", got.Close)
	}
	if !got.Close.ExitNotional.Equal(decimal.RequireFromString("50475")) || !got.Close.GrossPnL.Equal(decimal.RequireFromString("450")) ||
		!got.Close.NetPnL.Equal(decimal.RequireFromString("389.7")) {
		t.Fatalf("close accounting mismatch: %#v", got.Close)
	}
	if closes.calls != 1 || closes.close.CloseID != got.Close.CloseID || got.Stats.Inserted != 1 {
		t.Fatalf("close repository mismatch: calls=%d close=%#v stats=%#v", closes.calls, closes.close, got.Stats)
	}
	if len(closes.queries) != 1 || closes.queries[0].PositionID != position.PositionID || closes.queries[0].Limit != 2 {
		t.Fatalf("expected position close lookup before write, got %#v", closes.queries)
	}
}

func TestServiceClosePositionAllowsExactCloseRerun(t *testing.T) {
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	position := appOpenPosition(now)
	record := testValidationRecord(t, "research_app_0001", now.Add(-2*time.Hour), domainpaper.ValidationStatusRunning)
	record.ValidationID = position.ValidationID
	existingClose := appPositionClose(now)
	closes := &fakePositionCloseRepository{
		closes: []domainpaper.PositionClose{existingClose},
		stats:  domainpaper.PositionCloseStats{Skipped: 1},
	}
	service := closePositionService(now, record, []domainpaper.OpenPosition{position}, closes)

	got, err := service.ClosePosition(context.Background(), appClosePositionRequest(now))
	if err != nil {
		t.Fatalf("close position rerun: %v", err)
	}
	if got.Stats.Skipped != 1 || closes.calls != 1 {
		t.Fatalf("idempotent close mismatch: stats=%#v calls=%d", got.Stats, closes.calls)
	}
}

func TestServiceClosePositionRejectsUnsafeInputsTableDriven(t *testing.T) {
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	position := appOpenPosition(now)
	runningRecord := testValidationRecord(t, "research_app_0001", now.Add(-2*time.Hour), domainpaper.ValidationStatusRunning)
	runningRecord.ValidationID = position.ValidationID
	plannedRecord := runningRecord
	plannedRecord.Status = domainpaper.ValidationStatusPlanned
	plannedRecord.StartedAt = time.Time{}
	repositoryErr := errors.New("postgres unavailable")
	validReq := appClosePositionRequest(now)

	tests := []struct {
		name       string
		service    *apppaper.Service
		req        apppaper.ClosePositionRequest
		wantErrSub string
	}{
		{
			name:       "missing position id",
			service:    closePositionService(now, runningRecord, []domainpaper.OpenPosition{position}, &fakePositionCloseRepository{}),
			req:        func() apppaper.ClosePositionRequest { req := validReq; req.PositionID = ""; return req }(),
			wantErrSub: "position_id",
		},
		{
			name:       "position not found",
			service:    closePositionService(now, runningRecord, nil, &fakePositionCloseRepository{}),
			req:        validReq,
			wantErrSub: "not found",
		},
		{
			name:       "position lookup ambiguous",
			service:    closePositionService(now, runningRecord, []domainpaper.OpenPosition{position, position}, &fakePositionCloseRepository{}),
			req:        validReq,
			wantErrSub: "ambiguous",
		},
		{
			name:       "validation is not running",
			service:    closePositionService(now, plannedRecord, []domainpaper.OpenPosition{position}, &fakePositionCloseRepository{}),
			req:        validReq,
			wantErrSub: "RUNNING",
		},
		{
			name: "position already closed by another close",
			service: closePositionService(
				now,
				runningRecord,
				[]domainpaper.OpenPosition{position},
				&fakePositionCloseRepository{closes: []domainpaper.PositionClose{func() domainpaper.PositionClose {
					close := appPositionClose(now)
					close.CloseID = "paper_close_existing_0001"
					return close
				}()}},
			),
			req:        validReq,
			wantErrSub: "already has close",
		},
		{
			name:    "improved long exit rejected",
			service: closePositionService(now, runningRecord, []domainpaper.OpenPosition{position}, &fakePositionCloseRepository{}),
			req: func() apppaper.ClosePositionRequest {
				req := validReq
				req.ExitPrice = decimal.RequireFromString("101050")
				req.ExitFee = decimal.RequireFromString("30.315")
				return req
			}(),
			wantErrSub: "LONG",
		},
		{
			name:    "close before open rejected",
			service: closePositionService(now, runningRecord, []domainpaper.OpenPosition{position}, &fakePositionCloseRepository{}),
			req: func() apppaper.ClosePositionRequest {
				req := validReq
				req.ClosedAt = position.OpenedAt
				return req
			}(),
			wantErrSub: "closed_at",
		},
		{
			name:       "close lookup error",
			service:    closePositionService(now, runningRecord, []domainpaper.OpenPosition{position}, &fakePositionCloseRepository{listErr: repositoryErr}),
			req:        validReq,
			wantErrSub: repositoryErr.Error(),
		},
		{
			name:       "record close error",
			service:    closePositionService(now, runningRecord, []domainpaper.OpenPosition{position}, &fakePositionCloseRepository{recordErr: repositoryErr}),
			req:        validReq,
			wantErrSub: repositoryErr.Error(),
		},
		{
			name: "missing close repository",
			service: apppaper.NewService(
				&fakeRunRepository{},
				&fakeResultRepository{},
				apppaper.WithValidationRecordRepository(&fakeValidationRecordRepository{records: []domainpaper.ValidationRecord{runningRecord}}),
				apppaper.WithOpenPositionRepository(&fakeOpenPositionRepository{positions: []domainpaper.OpenPosition{position}}),
				apppaper.WithClock(clock.FixedClock{Time: now.Add(4 * time.Minute)}),
			),
			req:        validReq,
			wantErrSub: "position close repository",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.service.ClosePosition(context.Background(), tt.req)
			if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
		})
	}
}

func closePositionService(
	now time.Time,
	record domainpaper.ValidationRecord,
	positions []domainpaper.OpenPosition,
	closes *fakePositionCloseRepository,
) *apppaper.Service {
	return apppaper.NewService(
		&fakeRunRepository{},
		&fakeResultRepository{},
		apppaper.WithValidationRecordRepository(&fakeValidationRecordRepository{records: []domainpaper.ValidationRecord{record}}),
		apppaper.WithOpenPositionRepository(&fakeOpenPositionRepository{positions: positions}),
		apppaper.WithPositionCloseRepository(closes),
		apppaper.WithClock(clock.FixedClock{Time: now.Add(4 * time.Minute)}),
	)
}

type fakePositionCloseRepository struct {
	close     domainpaper.PositionClose
	closes    []domainpaper.PositionClose
	queries   []domainpaper.PositionCloseQuery
	stats     domainpaper.PositionCloseStats
	calls     int
	listErr   error
	recordErr error
}

func (r *fakePositionCloseRepository) RecordPositionClose(_ context.Context, close domainpaper.PositionClose) (domainpaper.PositionCloseStats, error) {
	r.calls++
	r.close = close
	if r.recordErr != nil {
		return domainpaper.PositionCloseStats{}, r.recordErr
	}
	if r.stats.Skipped == 0 {
		r.closes = append(r.closes, close)
	}
	return r.stats, nil
}

func (r *fakePositionCloseRepository) ListPositionCloses(_ context.Context, query domainpaper.PositionCloseQuery) ([]domainpaper.PositionClose, error) {
	r.queries = append(r.queries, query)
	if r.listErr != nil {
		return nil, r.listErr
	}
	var out []domainpaper.PositionClose
	for _, close := range r.closes {
		if query.ValidationID != "" && close.ValidationID != query.ValidationID {
			continue
		}
		if query.PositionID != "" && close.PositionID != query.PositionID {
			continue
		}
		if query.CloseID != "" && close.CloseID != query.CloseID {
			continue
		}
		if query.Symbol != "" && close.Symbol != query.Symbol {
			continue
		}
		if query.Interval != "" && close.Interval != query.Interval {
			continue
		}
		out = append(out, close)
		if query.Limit > 0 && len(out) == query.Limit {
			break
		}
	}
	return out, nil
}

func appClosePositionRequest(now time.Time) apppaper.ClosePositionRequest {
	position := appOpenPosition(now)
	return apppaper.ClosePositionRequest{
		CloseID:      "paper_close_app_0001",
		PositionID:   "paper_position_app_0001",
		Liquidity:    backtest.LiquidityTaker,
		ExitMidPrice: decimal.RequireFromString("101000"),
		ExitPrice:    decimal.RequireFromString("100950"),
		ExitFee:      decimal.RequireFromString("30.285"),
		ExitFeeBPS:   decimal.RequireFromString("6"),
		SpreadBPS:    decimal.RequireFromString("2"),
		SlippageBPS:  decimal.RequireFromString("3"),
		CloseReason:  domainpaper.PositionCloseReasonTakeProfit,
		ClosedAt:     position.OpenedAt.Add(2 * time.Minute),
	}
}

func appPositionClose(now time.Time) domainpaper.PositionClose {
	position := appOpenPosition(now)
	closedAt := position.OpenedAt.Add(2 * time.Minute)
	return domainpaper.PositionClose{
		CloseID:       "paper_close_app_0001",
		PositionID:    position.PositionID,
		EntryFillID:   position.FillID,
		TicketID:      position.TicketID,
		ValidationID:  position.ValidationID,
		DecisionID:    position.DecisionID,
		IntentID:      position.IntentID,
		Exchange:      position.Exchange,
		Category:      position.Category,
		Symbol:        position.Symbol,
		Interval:      position.Interval,
		Side:          position.Side,
		Liquidity:     backtest.LiquidityTaker,
		Quantity:      position.Quantity,
		EntryPrice:    position.EntryPrice,
		ExitMidPrice:  decimal.RequireFromString("101000"),
		ExitPrice:     decimal.RequireFromString("100950"),
		EntryNotional: position.EntryNotional,
		ExitNotional:  decimal.RequireFromString("50475"),
		EntryFee:      position.EntryFee,
		ExitFee:       decimal.RequireFromString("30.285"),
		ExitFeeBPS:    decimal.RequireFromString("6"),
		SpreadBPS:     decimal.RequireFromString("2"),
		SlippageBPS:   decimal.RequireFromString("3"),
		Fees:          decimal.RequireFromString("60.3"),
		GrossPnL:      decimal.RequireFromString("450"),
		NetPnL:        decimal.RequireFromString("389.7"),
		Return:        decimal.RequireFromString("0.0077901049475262"),
		CloseReason:   domainpaper.PositionCloseReasonTakeProfit,
		OpenedAt:      position.OpenedAt,
		ClosedAt:      closedAt,
		RecordedAt:    now.Add(4 * time.Minute),
	}
}
