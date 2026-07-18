package paper_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	apppaper "github.com/VersoIt/Inquisitor/internal/app/paper"
	"github.com/VersoIt/Inquisitor/internal/clock"
	domainpaper "github.com/VersoIt/Inquisitor/internal/paper"
)

func TestServiceOpenPositionFromOrderFill(t *testing.T) {
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	ticket := appOrderTicket(now)
	fill := appOrderFill(now)
	record := testValidationRecord(t, "research_app_0001", now.Add(-2*time.Hour), domainpaper.ValidationStatusRunning)
	record.ValidationID = ticket.ValidationID
	positions := &fakeOpenPositionRepository{stats: domainpaper.OpenPositionStats{Inserted: 1}}
	service := openPositionService(now, record, []domainpaper.OrderTicket{ticket}, []domainpaper.OrderFill{fill}, positions)

	got, err := service.OpenPosition(context.Background(), apppaper.OpenPositionRequest{
		PositionID: " paper_position_app_0001 ",
		FillID:     " paper_fill_app_0001 ",
	})
	if err != nil {
		t.Fatalf("open position: %v", err)
	}

	if got.Record.ValidationID != record.ValidationID || got.Ticket.TicketID != ticket.TicketID || got.Fill.FillID != fill.FillID {
		t.Fatalf("context mismatch: %#v", got)
	}
	if got.Position.PositionID != "paper_position_app_0001" || got.Position.FillID != fill.FillID ||
		got.Position.TicketID != ticket.TicketID || got.Position.ValidationID != ticket.ValidationID {
		t.Fatalf("position identity mismatch: %#v", got.Position)
	}
	if !got.Position.EntryNotional.Equal(fill.Notional) || !got.Position.OpenRisk.Equal(decimal.RequireFromString("525")) ||
		!got.Position.PlannedMaxLoss.Equal(ticket.MaxLoss) {
		t.Fatalf("position accounting mismatch: %#v", got.Position)
	}
	if positions.calls != 1 || positions.position.PositionID != got.Position.PositionID || got.Stats.Inserted != 1 {
		t.Fatalf("position repository mismatch: calls=%d position=%#v stats=%#v", positions.calls, positions.position, got.Stats)
	}
	if len(positions.queries) != 1 || positions.queries[0].FillID != fill.FillID || positions.queries[0].Limit != 2 {
		t.Fatalf("expected fill position lookup before write, got %#v", positions.queries)
	}
}

func TestServiceOpenPositionRejectsUnsafeInputsTableDriven(t *testing.T) {
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	ticket := appOrderTicket(now)
	fill := appOrderFill(now)
	runningRecord := testValidationRecord(t, "research_app_0001", now.Add(-2*time.Hour), domainpaper.ValidationStatusRunning)
	runningRecord.ValidationID = ticket.ValidationID
	plannedRecord := runningRecord
	plannedRecord.Status = domainpaper.ValidationStatusPlanned
	plannedRecord.StartedAt = time.Time{}
	repositoryErr := errors.New("postgres unavailable")
	validReq := apppaper.OpenPositionRequest{
		PositionID: "paper_position_app_0001",
		FillID:     fill.FillID,
	}

	tests := []struct {
		name       string
		service    *apppaper.Service
		req        apppaper.OpenPositionRequest
		wantErrSub string
	}{
		{
			name:       "missing fill id",
			service:    openPositionService(now, runningRecord, []domainpaper.OrderTicket{ticket}, []domainpaper.OrderFill{fill}, &fakeOpenPositionRepository{}),
			req:        func() apppaper.OpenPositionRequest { req := validReq; req.FillID = ""; return req }(),
			wantErrSub: "fill_id",
		},
		{
			name:       "fill not found",
			service:    openPositionService(now, runningRecord, []domainpaper.OrderTicket{ticket}, nil, &fakeOpenPositionRepository{}),
			req:        validReq,
			wantErrSub: "not found",
		},
		{
			name:       "fill lookup ambiguous",
			service:    openPositionService(now, runningRecord, []domainpaper.OrderTicket{ticket}, []domainpaper.OrderFill{fill, fill}, &fakeOpenPositionRepository{}),
			req:        validReq,
			wantErrSub: "ambiguous",
		},
		{
			name:       "ticket not found",
			service:    openPositionService(now, runningRecord, nil, []domainpaper.OrderFill{fill}, &fakeOpenPositionRepository{}),
			req:        validReq,
			wantErrSub: "not found",
		},
		{
			name:       "validation is not running",
			service:    openPositionService(now, plannedRecord, []domainpaper.OrderTicket{ticket}, []domainpaper.OrderFill{fill}, &fakeOpenPositionRepository{}),
			req:        validReq,
			wantErrSub: "RUNNING",
		},
		{
			name: "fill already opened another position",
			service: openPositionService(
				now,
				runningRecord,
				[]domainpaper.OrderTicket{ticket},
				[]domainpaper.OrderFill{fill},
				&fakeOpenPositionRepository{positions: []domainpaper.OpenPosition{func() domainpaper.OpenPosition {
					position := appOpenPosition(now)
					position.PositionID = "paper_position_existing_0001"
					return position
				}()}},
			),
			req:        validReq,
			wantErrSub: "already opened position",
		},
		{
			name: "mismatched fill and ticket",
			service: openPositionService(
				now,
				runningRecord,
				[]domainpaper.OrderTicket{ticket},
				[]domainpaper.OrderFill{func() domainpaper.OrderFill {
					mismatched := fill
					mismatched.Symbol = "ETHUSDT"
					return mismatched
				}()},
				&fakeOpenPositionRepository{},
			),
			req:        validReq,
			wantErrSub: "market scope",
		},
		{
			name:       "repository error",
			service:    openPositionService(now, runningRecord, []domainpaper.OrderTicket{ticket}, []domainpaper.OrderFill{fill}, &fakeOpenPositionRepository{err: repositoryErr}),
			req:        validReq,
			wantErrSub: repositoryErr.Error(),
		},
		{
			name: "missing position repository",
			service: apppaper.NewService(
				&fakeRunRepository{},
				&fakeResultRepository{},
				apppaper.WithValidationRecordRepository(&fakeValidationRecordRepository{records: []domainpaper.ValidationRecord{runningRecord}}),
				apppaper.WithOrderTicketRepository(&fakeOrderTicketRepository{tickets: []domainpaper.OrderTicket{ticket}}),
				apppaper.WithOrderFillRepository(&fakeOrderFillRepository{fills: []domainpaper.OrderFill{fill}}),
				apppaper.WithClock(clock.FixedClock{Time: now.Add(3 * time.Minute)}),
			),
			req:        validReq,
			wantErrSub: "open position repository",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.service.OpenPosition(context.Background(), tt.req)
			if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
		})
	}
}

func openPositionService(
	now time.Time,
	record domainpaper.ValidationRecord,
	tickets []domainpaper.OrderTicket,
	fills []domainpaper.OrderFill,
	positions *fakeOpenPositionRepository,
) *apppaper.Service {
	return apppaper.NewService(
		&fakeRunRepository{},
		&fakeResultRepository{},
		apppaper.WithValidationRecordRepository(&fakeValidationRecordRepository{records: []domainpaper.ValidationRecord{record}}),
		apppaper.WithOrderTicketRepository(&fakeOrderTicketRepository{tickets: tickets}),
		apppaper.WithOrderFillRepository(&fakeOrderFillRepository{fills: fills}),
		apppaper.WithOpenPositionRepository(positions),
		apppaper.WithClock(clock.FixedClock{Time: now.Add(3 * time.Minute)}),
	)
}

type fakeOpenPositionRepository struct {
	position  domainpaper.OpenPosition
	positions []domainpaper.OpenPosition
	queries   []domainpaper.OpenPositionQuery
	stats     domainpaper.OpenPositionStats
	calls     int
	err       error
}

func (r *fakeOpenPositionRepository) RecordOpenPosition(_ context.Context, position domainpaper.OpenPosition) (domainpaper.OpenPositionStats, error) {
	r.calls++
	r.position = position
	if r.err != nil {
		return domainpaper.OpenPositionStats{}, r.err
	}
	if r.stats.Skipped == 0 {
		r.positions = append(r.positions, position)
	}
	return r.stats, nil
}

func (r *fakeOpenPositionRepository) ListOpenPositions(_ context.Context, query domainpaper.OpenPositionQuery) ([]domainpaper.OpenPosition, error) {
	r.queries = append(r.queries, query)
	if r.err != nil {
		return nil, r.err
	}
	var out []domainpaper.OpenPosition
	for _, position := range r.positions {
		if query.ValidationID != "" && position.ValidationID != query.ValidationID {
			continue
		}
		if query.TicketID != "" && position.TicketID != query.TicketID {
			continue
		}
		if query.FillID != "" && position.FillID != query.FillID {
			continue
		}
		if query.PositionID != "" && position.PositionID != query.PositionID {
			continue
		}
		if query.Symbol != "" && position.Symbol != query.Symbol {
			continue
		}
		if query.Interval != "" && position.Interval != query.Interval {
			continue
		}
		out = append(out, position)
		if query.Limit > 0 && len(out) == query.Limit {
			break
		}
	}
	return out, nil
}

func appOpenPosition(now time.Time) domainpaper.OpenPosition {
	ticket := appOrderTicket(now)
	fill := appOrderFill(now)
	return domainpaper.OpenPosition{
		PositionID:     "paper_position_app_0001",
		FillID:         fill.FillID,
		TicketID:       ticket.TicketID,
		ValidationID:   ticket.ValidationID,
		DecisionID:     ticket.DecisionID,
		IntentID:       ticket.IntentID,
		Exchange:       ticket.Exchange,
		Category:       ticket.Category,
		Symbol:         ticket.Symbol,
		Interval:       ticket.Interval,
		Side:           ticket.Side,
		Quantity:       fill.Quantity,
		EntryPrice:     fill.ExecutedPrice,
		EntryNotional:  fill.Notional,
		EntryFee:       fill.Fee,
		StopLoss:       ticket.StopLoss,
		TakeProfit:     ticket.TakeProfit,
		Leverage:       ticket.Leverage,
		PlannedMaxLoss: ticket.MaxLoss,
		OpenRisk:       fill.ExecutedPrice.Sub(ticket.StopLoss).Abs().Mul(fill.Quantity),
		OpenedAt:       fill.FilledAt,
		RecordedAt:     fill.RecordedAt.Add(time.Minute),
	}
}
