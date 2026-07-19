package paper_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	apppaper "github.com/VersoIt/Inquisitor/internal/app/paper"
	"github.com/VersoIt/Inquisitor/internal/clock"
	domainpaper "github.com/VersoIt/Inquisitor/internal/paper"
)

func TestServiceListPendingOrderTicketsReturnsUnfilledTickets(t *testing.T) {
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	first := appOrderTicket(now)
	record := testValidationRecord(t, "research_app_0001", now.Add(-2*time.Hour), domainpaper.ValidationStatusRunning)
	record.ValidationID = first.ValidationID
	second := appOrderTicket(now.Add(time.Minute))
	second.TicketID = "paper_ticket_app_0002"
	second.DecisionID = "risk_decision_app_0002"
	second.IntentID = "risk_intent_app_0002"
	third := appOrderTicket(now.Add(2 * time.Minute))
	third.TicketID = "paper_ticket_app_0003"
	third.DecisionID = "risk_decision_app_0003"
	third.IntentID = "risk_intent_app_0003"
	fill := appOrderFill(now)
	fill.TicketID = second.TicketID
	fill.DecisionID = second.DecisionID
	fill.IntentID = second.IntentID
	tickets := &fakeOrderTicketRepository{tickets: []domainpaper.OrderTicket{first, second, third}}
	fills := &fakeOrderFillRepository{fills: []domainpaper.OrderFill{fill}}
	service := pendingOrderTicketService(now, record, tickets, fills)

	got, err := service.ListPendingOrderTickets(context.Background(), apppaper.ListPendingOrderTicketsRequest{
		ValidationID: first.ValidationID,
		Symbol:       "BTCUSDT",
		Interval:     "1",
		Limit:        10,
		ScanLimit:    10,
	})
	if err != nil {
		t.Fatalf("list pending tickets: %v", err)
	}

	if got.Record.ValidationID != record.ValidationID || got.ScannedTickets != 3 || got.FilledTickets != 1 || len(got.Tickets) != 2 {
		t.Fatalf("pending summary mismatch: %#v", got)
	}
	if got.Tickets[0].TicketID != first.TicketID || got.Tickets[1].TicketID != third.TicketID {
		t.Fatalf("pending tickets mismatch: %#v", got.Tickets)
	}
	if len(tickets.queries) != 1 || tickets.queries[0].ValidationID != first.ValidationID ||
		tickets.queries[0].Symbol != "BTCUSDT" || tickets.queries[0].Interval != "1" || tickets.queries[0].Limit != 10 {
		t.Fatalf("ticket query mismatch: %#v", tickets.queries)
	}
	if len(fills.queries) != 3 || fills.queries[0].ValidationID != first.ValidationID ||
		fills.queries[0].TicketID != first.TicketID || fills.queries[0].Limit != 2 ||
		fills.queries[1].ValidationID != first.ValidationID || fills.queries[1].TicketID != second.TicketID || fills.queries[1].Limit != 2 ||
		fills.queries[2].ValidationID != first.ValidationID || fills.queries[2].TicketID != third.TicketID || fills.queries[2].Limit != 2 {
		t.Fatalf("fill status queries mismatch: %#v", fills.queries)
	}
}

func TestServiceListPendingOrderTicketsScopesFillStatusToValidation(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	ticket := appOrderTicket(now)
	record := testValidationRecord(t, "research_app_0001", now.Add(-2*time.Hour), domainpaper.ValidationStatusRunning)
	record.ValidationID = ticket.ValidationID
	foreignFill := appOrderFill(now)
	foreignFill.FillID = "paper_fill_foreign_0001"
	foreignFill.ValidationID = "paper_validation_foreign_0001"
	foreignFill.TicketID = ticket.TicketID
	tickets := &fakeOrderTicketRepository{tickets: []domainpaper.OrderTicket{ticket}}
	fills := &fakeOrderFillRepository{fills: []domainpaper.OrderFill{foreignFill}}
	service := pendingOrderTicketService(now, record, tickets, fills)

	got, err := service.ListPendingOrderTickets(context.Background(), apppaper.ListPendingOrderTicketsRequest{
		ValidationID: ticket.ValidationID,
		Limit:        10,
		ScanLimit:    10,
	})
	if err != nil {
		t.Fatalf("list pending tickets with foreign fill present: %v", err)
	}

	if got.FilledTickets != 0 || len(got.Tickets) != 1 || got.Tickets[0].TicketID != ticket.TicketID {
		t.Fatalf("foreign fill must not mark ticket filled: %#v", got)
	}
	if len(fills.queries) != 1 || fills.queries[0].ValidationID != record.ValidationID ||
		fills.queries[0].TicketID != ticket.TicketID || fills.queries[0].Limit != 2 {
		t.Fatalf("fill status lookup must be scoped to validation: %#v", fills.queries)
	}
}

func TestServiceListPendingOrderTicketsStopsAtPendingLimit(t *testing.T) {
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	first := appOrderTicket(now)
	record := testValidationRecord(t, "research_app_0001", now.Add(-2*time.Hour), domainpaper.ValidationStatusRunning)
	record.ValidationID = first.ValidationID
	second := appOrderTicket(now.Add(time.Minute))
	second.TicketID = "paper_ticket_app_0002"
	tickets := &fakeOrderTicketRepository{tickets: []domainpaper.OrderTicket{first, second}}
	fills := &fakeOrderFillRepository{}
	service := pendingOrderTicketService(now, record, tickets, fills)

	got, err := service.ListPendingOrderTickets(context.Background(), apppaper.ListPendingOrderTicketsRequest{
		ValidationID: first.ValidationID,
		Limit:        1,
		ScanLimit:    2,
	})
	if err != nil {
		t.Fatalf("list pending tickets: %v", err)
	}

	if len(got.Tickets) != 1 || got.Tickets[0].TicketID != first.TicketID {
		t.Fatalf("pending limit mismatch: %#v", got)
	}
	if len(fills.queries) != 1 || fills.queries[0].ValidationID != first.ValidationID ||
		fills.queries[0].TicketID != first.TicketID || fills.queries[0].Limit != 2 {
		t.Fatalf("expected fill checks to stop at limit, got %#v", fills.queries)
	}
}

func TestServiceListPendingOrderTicketsRejectsUnsafeInputsTableDriven(t *testing.T) {
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	ticket := appOrderTicket(now)
	runningRecord := testValidationRecord(t, "research_app_0001", now.Add(-2*time.Hour), domainpaper.ValidationStatusRunning)
	runningRecord.ValidationID = ticket.ValidationID
	plannedRecord := runningRecord
	plannedRecord.Status = domainpaper.ValidationStatusPlanned
	plannedRecord.StartedAt = time.Time{}
	repositoryErr := errors.New("postgres unavailable")
	validReq := apppaper.ListPendingOrderTicketsRequest{
		ValidationID: ticket.ValidationID,
		Limit:        10,
		ScanLimit:    10,
	}

	tests := []struct {
		name       string
		service    *apppaper.Service
		req        apppaper.ListPendingOrderTicketsRequest
		wantErrSub string
	}{
		{
			name:       "missing validation id",
			service:    pendingOrderTicketService(now, runningRecord, &fakeOrderTicketRepository{tickets: []domainpaper.OrderTicket{ticket}}, &fakeOrderFillRepository{}),
			req:        func() apppaper.ListPendingOrderTicketsRequest { req := validReq; req.ValidationID = ""; return req }(),
			wantErrSub: "validation_id",
		},
		{
			name: "negative limit",
			service: pendingOrderTicketService(
				now,
				runningRecord,
				&fakeOrderTicketRepository{tickets: []domainpaper.OrderTicket{ticket}},
				&fakeOrderFillRepository{},
			),
			req:        func() apppaper.ListPendingOrderTicketsRequest { req := validReq; req.Limit = -1; return req }(),
			wantErrSub: "limit",
		},
		{
			name:       "scan limit below limit",
			service:    pendingOrderTicketService(now, runningRecord, &fakeOrderTicketRepository{tickets: []domainpaper.OrderTicket{ticket}}, &fakeOrderFillRepository{}),
			req:        func() apppaper.ListPendingOrderTicketsRequest { req := validReq; req.ScanLimit = 1; return req }(),
			wantErrSub: "scan_limit",
		},
		{
			name:       "validation is not running",
			service:    pendingOrderTicketService(now, plannedRecord, &fakeOrderTicketRepository{tickets: []domainpaper.OrderTicket{ticket}}, &fakeOrderFillRepository{}),
			req:        validReq,
			wantErrSub: "RUNNING",
		},
		{
			name: "ticket repository error",
			service: pendingOrderTicketService(
				now,
				runningRecord,
				&fakeOrderTicketRepository{err: repositoryErr},
				&fakeOrderFillRepository{},
			),
			req:        validReq,
			wantErrSub: repositoryErr.Error(),
		},
		{
			name: "fill repository error",
			service: pendingOrderTicketService(
				now,
				runningRecord,
				&fakeOrderTicketRepository{tickets: []domainpaper.OrderTicket{ticket}},
				&fakeOrderFillRepository{err: repositoryErr},
			),
			req:        validReq,
			wantErrSub: repositoryErr.Error(),
		},
		{
			name: "inconsistent fill journal",
			service: pendingOrderTicketService(
				now,
				runningRecord,
				&fakeOrderTicketRepository{tickets: []domainpaper.OrderTicket{ticket}},
				&fakeOrderFillRepository{fills: []domainpaper.OrderFill{appOrderFill(now), appOrderFill(now)}},
			),
			req:        validReq,
			wantErrSub: "inconsistent",
		},
		{
			name: "missing ticket repository",
			service: apppaper.NewService(
				&fakeRunRepository{},
				&fakeResultRepository{},
				apppaper.WithValidationRecordRepository(&fakeValidationRecordRepository{records: []domainpaper.ValidationRecord{runningRecord}}),
				apppaper.WithOrderFillRepository(&fakeOrderFillRepository{}),
			),
			req:        validReq,
			wantErrSub: "order ticket repository",
		},
		{
			name: "missing validation record repository",
			service: apppaper.NewService(
				&fakeRunRepository{},
				&fakeResultRepository{},
				apppaper.WithOrderTicketRepository(&fakeOrderTicketRepository{tickets: []domainpaper.OrderTicket{ticket}}),
				apppaper.WithOrderFillRepository(&fakeOrderFillRepository{}),
			),
			req:        validReq,
			wantErrSub: "validation record repository",
		},
		{
			name: "missing fill repository",
			service: apppaper.NewService(
				&fakeRunRepository{},
				&fakeResultRepository{},
				apppaper.WithValidationRecordRepository(&fakeValidationRecordRepository{records: []domainpaper.ValidationRecord{runningRecord}}),
				apppaper.WithOrderTicketRepository(&fakeOrderTicketRepository{tickets: []domainpaper.OrderTicket{ticket}}),
			),
			req:        validReq,
			wantErrSub: "order fill repository",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.service.ListPendingOrderTickets(context.Background(), tt.req)
			if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
		})
	}
}

func pendingOrderTicketService(
	now time.Time,
	record domainpaper.ValidationRecord,
	tickets *fakeOrderTicketRepository,
	fills *fakeOrderFillRepository,
) *apppaper.Service {
	return apppaper.NewService(
		&fakeRunRepository{},
		&fakeResultRepository{},
		apppaper.WithValidationRecordRepository(&fakeValidationRecordRepository{records: []domainpaper.ValidationRecord{record}}),
		apppaper.WithOrderTicketRepository(tickets),
		apppaper.WithOrderFillRepository(fills),
		apppaper.WithClock(clock.FixedClock{Time: now}),
	)
}
