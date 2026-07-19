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
	domainrisk "github.com/VersoIt/Inquisitor/internal/risk"
)

func TestServiceRecordOrderFillFromRunningTicket(t *testing.T) {
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	ticket := appOrderTicket(now)
	record := testValidationRecord(t, "research_app_0001", now.Add(-2*time.Hour), domainpaper.ValidationStatusRunning)
	record.ValidationID = ticket.ValidationID
	fills := &fakeOrderFillRepository{stats: domainpaper.OrderFillStats{Inserted: 1}}
	service := orderFillService(now, record, []domainpaper.OrderTicket{ticket}, fills)

	got, err := service.RecordOrderFill(context.Background(), apppaper.RecordOrderFillRequest{
		FillID:        " paper_fill_app_0001 ",
		TicketID:      " paper_ticket_app_0001 ",
		Liquidity:     " taker ",
		MidPrice:      decimal.RequireFromString("100000"),
		ExecutedPrice: decimal.RequireFromString("100050"),
		Fee:           decimal.RequireFromString("30.015"),
		FeeBPS:        decimal.RequireFromString("6"),
		SpreadBPS:     decimal.RequireFromString("2"),
		SlippageBPS:   decimal.RequireFromString("3"),
		FilledAt:      now.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("record order fill: %v", err)
	}

	if got.Record.ValidationID != record.ValidationID || got.Ticket.TicketID != ticket.TicketID {
		t.Fatalf("context mismatch: %#v", got)
	}
	if got.Fill.FillID != "paper_fill_app_0001" || got.Fill.TicketID != ticket.TicketID ||
		got.Fill.ValidationID != ticket.ValidationID || got.Fill.Liquidity != backtest.LiquidityTaker {
		t.Fatalf("fill identity mismatch: %#v", got.Fill)
	}
	if !got.Fill.Notional.Equal(decimal.RequireFromString("50025")) || !got.Fill.Quantity.Equal(ticket.Quantity) ||
		!got.Fill.Fee.Equal(decimal.RequireFromString("30.015")) {
		t.Fatalf("fill math mismatch: %#v", got.Fill)
	}
	if fills.calls != 1 || fills.fill.FillID != got.Fill.FillID || got.Stats.Inserted != 1 {
		t.Fatalf("fill repository mismatch: calls=%d fill=%#v stats=%#v", fills.calls, fills.fill, got.Stats)
	}
	if len(fills.queries) != 1 || fills.queries[0].ValidationID != ticket.ValidationID ||
		fills.queries[0].TicketID != ticket.TicketID || fills.queries[0].Limit != 2 {
		t.Fatalf("expected ticket fill lookup before write, got %#v", fills.queries)
	}
}

func TestServiceRecordOrderFillScopesExistingFillLookupToValidation(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	ticket := appOrderTicket(now)
	record := testValidationRecord(t, "research_app_0001", now.Add(-2*time.Hour), domainpaper.ValidationStatusRunning)
	record.ValidationID = ticket.ValidationID
	foreignFill := appOrderFill(now)
	foreignFill.FillID = "paper_fill_foreign_0001"
	foreignFill.ValidationID = "paper_validation_foreign_0001"
	foreignFill.TicketID = ticket.TicketID
	fills := &fakeOrderFillRepository{
		fills: []domainpaper.OrderFill{foreignFill},
		stats: domainpaper.OrderFillStats{Inserted: 1},
	}
	service := orderFillService(now, record, []domainpaper.OrderTicket{ticket}, fills)

	got, err := service.RecordOrderFill(context.Background(), appOrderFillRequest(now))
	if err != nil {
		t.Fatalf("record order fill with foreign fill present: %v", err)
	}

	if got.Fill.ValidationID != record.ValidationID || got.Fill.TicketID != ticket.TicketID || got.Stats.Inserted != 1 {
		t.Fatalf("scoped fill result mismatch: %#v", got)
	}
	if fills.calls != 1 || len(fills.queries) != 1 {
		t.Fatalf("fill repository call mismatch: calls=%d queries=%#v", fills.calls, fills.queries)
	}
	if fills.queries[0].ValidationID != record.ValidationID || fills.queries[0].TicketID != ticket.TicketID || fills.queries[0].Limit != 2 {
		t.Fatalf("fill lookup must be scoped to validation: %#v", fills.queries[0])
	}
}

func TestServiceRecordOrderFillRejectsUnsafeInputsTableDriven(t *testing.T) {
	now := time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)
	ticket := appOrderTicket(now)
	runningRecord := testValidationRecord(t, "research_app_0001", now.Add(-2*time.Hour), domainpaper.ValidationStatusRunning)
	runningRecord.ValidationID = ticket.ValidationID
	plannedRecord := runningRecord
	plannedRecord.Status = domainpaper.ValidationStatusPlanned
	plannedRecord.StartedAt = time.Time{}
	repositoryErr := errors.New("postgres unavailable")
	validReq := appOrderFillRequest(now)

	tests := []struct {
		name       string
		service    *apppaper.Service
		req        apppaper.RecordOrderFillRequest
		wantErrSub string
	}{
		{
			name:       "missing ticket id",
			service:    orderFillService(now, runningRecord, []domainpaper.OrderTicket{ticket}, &fakeOrderFillRepository{}),
			req:        func() apppaper.RecordOrderFillRequest { req := validReq; req.TicketID = ""; return req }(),
			wantErrSub: "ticket_id",
		},
		{
			name: "ticket not found",
			service: orderFillService(
				now,
				runningRecord,
				nil,
				&fakeOrderFillRepository{},
			),
			req:        validReq,
			wantErrSub: "not found",
		},
		{
			name: "ticket lookup ambiguous",
			service: orderFillService(
				now,
				runningRecord,
				[]domainpaper.OrderTicket{ticket, ticket},
				&fakeOrderFillRepository{},
			),
			req:        validReq,
			wantErrSub: "ambiguous",
		},
		{
			name:       "validation is not running",
			service:    orderFillService(now, plannedRecord, []domainpaper.OrderTicket{ticket}, &fakeOrderFillRepository{}),
			req:        validReq,
			wantErrSub: "RUNNING",
		},
		{
			name: "ticket already filled by another fill",
			service: orderFillService(
				now,
				runningRecord,
				[]domainpaper.OrderTicket{ticket},
				&fakeOrderFillRepository{fills: []domainpaper.OrderFill{func() domainpaper.OrderFill {
					fill := appOrderFill(now)
					fill.FillID = "paper_fill_existing_0001"
					return fill
				}()}},
			),
			req:        validReq,
			wantErrSub: "already has fill",
		},
		{
			name:    "improved long execution rejected",
			service: orderFillService(now, runningRecord, []domainpaper.OrderTicket{ticket}, &fakeOrderFillRepository{}),
			req: func() apppaper.RecordOrderFillRequest {
				req := validReq
				req.ExecutedPrice = decimal.RequireFromString("99950")
				req.Fee = decimal.RequireFromString("29.985")
				return req
			}(),
			wantErrSub: "LONG",
		},
		{
			name:    "fill before ticket creation",
			service: orderFillService(now, runningRecord, []domainpaper.OrderTicket{ticket}, &fakeOrderFillRepository{}),
			req: func() apppaper.RecordOrderFillRequest {
				req := validReq
				req.FilledAt = ticket.CreatedAt.Add(-time.Nanosecond)
				return req
			}(),
			wantErrSub: "ticket created_at",
		},
		{
			name:       "repository error",
			service:    orderFillService(now, runningRecord, []domainpaper.OrderTicket{ticket}, &fakeOrderFillRepository{err: repositoryErr}),
			req:        validReq,
			wantErrSub: repositoryErr.Error(),
		},
		{
			name: "missing fill repository",
			service: apppaper.NewService(
				&fakeRunRepository{},
				&fakeResultRepository{},
				apppaper.WithValidationRecordRepository(&fakeValidationRecordRepository{records: []domainpaper.ValidationRecord{runningRecord}}),
				apppaper.WithOrderTicketRepository(&fakeOrderTicketRepository{tickets: []domainpaper.OrderTicket{ticket}}),
				apppaper.WithClock(clock.FixedClock{Time: now.Add(2 * time.Minute)}),
			),
			req:        validReq,
			wantErrSub: "order fill repository",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := tt.service.RecordOrderFill(context.Background(), tt.req)
			if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
		})
	}
}

func TestServiceRecordOrderFillChecksKillSwitchBeforeNewFillTableDriven(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	ticket := appOrderTicket(now)
	record := testValidationRecord(t, "research_app_0001", now.Add(-2*time.Hour), domainpaper.ValidationStatusRunning)
	record.ValidationID = ticket.ValidationID
	existingFill := appOrderFill(now)
	repositoryErr := errors.New("postgres unavailable")
	activeKillSwitch := domainrisk.KillSwitchState{
		Active:    true,
		Reason:    "operator emergency stop",
		Source:    "operator",
		UpdatedAt: now.Add(time.Minute),
	}
	validReq := appOrderFillRequest(now)

	tests := []struct {
		name           string
		fills          *fakeOrderFillRepository
		killSwitch     *fakePaperKillSwitchRepository
		omitKillSwitch bool
		req            apppaper.RecordOrderFillRequest
		wantErrSub     string
		wantFillCalls  int
		wantKillCalls  int
	}{
		{
			name:          "inactive kill switch allows new fill",
			fills:         &fakeOrderFillRepository{stats: domainpaper.OrderFillStats{Inserted: 1}},
			killSwitch:    &fakePaperKillSwitchRepository{},
			req:           validReq,
			wantFillCalls: 1,
			wantKillCalls: 1,
		},
		{
			name:          "active kill switch blocks new fill before write",
			fills:         &fakeOrderFillRepository{},
			killSwitch:    &fakePaperKillSwitchRepository{state: activeKillSwitch},
			req:           validReq,
			wantErrSub:    "kill switch",
			wantFillCalls: 0,
			wantKillCalls: 1,
		},
		{
			name:          "kill switch lookup failure blocks new fill",
			fills:         &fakeOrderFillRepository{},
			killSwitch:    &fakePaperKillSwitchRepository{err: repositoryErr},
			req:           validReq,
			wantErrSub:    repositoryErr.Error(),
			wantFillCalls: 0,
			wantKillCalls: 1,
		},
		{
			name:           "missing kill switch repository fails closed",
			fills:          &fakeOrderFillRepository{},
			omitKillSwitch: true,
			req:            validReq,
			wantErrSub:     "kill switch repository",
			wantFillCalls:  0,
		},
		{
			name:          "exact idempotent fill retry skips kill switch",
			fills:         &fakeOrderFillRepository{fills: []domainpaper.OrderFill{existingFill}, stats: domainpaper.OrderFillStats{Skipped: 1}},
			killSwitch:    &fakePaperKillSwitchRepository{state: activeKillSwitch},
			req:           validReq,
			wantFillCalls: 1,
			wantKillCalls: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			options := []apppaper.Option{
				apppaper.WithValidationRecordRepository(&fakeValidationRecordRepository{records: []domainpaper.ValidationRecord{record}}),
				apppaper.WithOrderTicketRepository(&fakeOrderTicketRepository{tickets: []domainpaper.OrderTicket{ticket}}),
				apppaper.WithOrderFillRepository(tt.fills),
				apppaper.WithClock(clock.FixedClock{Time: now.Add(2 * time.Minute)}),
			}
			if !tt.omitKillSwitch {
				options = append(options, apppaper.WithKillSwitchRepository(tt.killSwitch))
			}
			service := apppaper.NewService(&fakeRunRepository{}, &fakeResultRepository{}, options...)

			got, err := service.RecordOrderFill(context.Background(), tt.req)
			if tt.wantErrSub != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
				}
			} else {
				if err != nil {
					t.Fatalf("record order fill: %v", err)
				}
				if got.Stats.Total() == 0 {
					t.Fatalf("expected repository stats on allowed fill, got %#v", got.Stats)
				}
			}
			if tt.fills.calls != tt.wantFillCalls {
				t.Fatalf("fill calls mismatch: got %d want %d", tt.fills.calls, tt.wantFillCalls)
			}
			if tt.killSwitch != nil && tt.killSwitch.currentCalls != tt.wantKillCalls {
				t.Fatalf("kill switch calls mismatch: got %d want %d", tt.killSwitch.currentCalls, tt.wantKillCalls)
			}
			if len(tt.fills.queries) != 1 {
				t.Fatalf("expected one fill-journal query before kill switch/write, got %#v", tt.fills.queries)
			}
			if tt.fills.queries[0].ValidationID != record.ValidationID || tt.fills.queries[0].TicketID != ticket.TicketID || tt.fills.queries[0].Limit != 2 {
				t.Fatalf("fill-journal query must be scoped to validation: %#v", tt.fills.queries[0])
			}
		})
	}
}

func orderFillService(
	now time.Time,
	record domainpaper.ValidationRecord,
	tickets []domainpaper.OrderTicket,
	fills *fakeOrderFillRepository,
) *apppaper.Service {
	return apppaper.NewService(
		&fakeRunRepository{},
		&fakeResultRepository{},
		apppaper.WithValidationRecordRepository(&fakeValidationRecordRepository{records: []domainpaper.ValidationRecord{record}}),
		apppaper.WithOrderTicketRepository(&fakeOrderTicketRepository{tickets: tickets}),
		apppaper.WithOrderFillRepository(fills),
		apppaper.WithKillSwitchRepository(&fakePaperKillSwitchRepository{}),
		apppaper.WithClock(clock.FixedClock{Time: now.Add(2 * time.Minute)}),
	)
}

type fakeOrderFillRepository struct {
	fill    domainpaper.OrderFill
	fills   []domainpaper.OrderFill
	queries []domainpaper.OrderFillQuery
	stats   domainpaper.OrderFillStats
	calls   int
	err     error
}

func (r *fakeOrderFillRepository) RecordOrderFill(_ context.Context, fill domainpaper.OrderFill) (domainpaper.OrderFillStats, error) {
	r.calls++
	r.fill = fill
	if r.err != nil {
		return domainpaper.OrderFillStats{}, r.err
	}
	if r.stats.Skipped == 0 {
		r.fills = append(r.fills, fill)
	}
	return r.stats, nil
}

func (r *fakeOrderFillRepository) ListOrderFills(_ context.Context, query domainpaper.OrderFillQuery) ([]domainpaper.OrderFill, error) {
	r.queries = append(r.queries, query)
	if r.err != nil {
		return nil, r.err
	}
	var out []domainpaper.OrderFill
	for _, fill := range r.fills {
		if query.ValidationID != "" && fill.ValidationID != query.ValidationID {
			continue
		}
		if query.TicketID != "" && fill.TicketID != query.TicketID {
			continue
		}
		if query.FillID != "" && fill.FillID != query.FillID {
			continue
		}
		out = append(out, fill)
		if query.Limit > 0 && len(out) == query.Limit {
			break
		}
	}
	return out, nil
}

type fakePaperKillSwitchRepository struct {
	state        domainrisk.KillSwitchState
	appended     domainrisk.KillSwitchEvent
	currentCalls int
	appendCalls  int
	events       []domainrisk.KillSwitchEvent
	err          error
}

func (r *fakePaperKillSwitchRepository) AppendKillSwitchEvent(_ context.Context, event domainrisk.KillSwitchEvent) (domainrisk.KillSwitchStats, error) {
	r.appendCalls++
	r.appended = event
	if r.err != nil {
		return domainrisk.KillSwitchStats{}, r.err
	}
	r.events = append(r.events, event)
	return domainrisk.KillSwitchStats{Inserted: 1}, nil
}

func (r *fakePaperKillSwitchRepository) CurrentKillSwitchState(context.Context) (domainrisk.KillSwitchState, error) {
	r.currentCalls++
	if r.err != nil {
		return domainrisk.KillSwitchState{}, r.err
	}
	return r.state, nil
}

func (r *fakePaperKillSwitchRepository) ListKillSwitchEvents(context.Context, domainrisk.KillSwitchEventQuery) ([]domainrisk.KillSwitchEvent, error) {
	if r.err != nil {
		return nil, r.err
	}
	return r.events, nil
}

func appOrderFillRequest(now time.Time) apppaper.RecordOrderFillRequest {
	return apppaper.RecordOrderFillRequest{
		FillID:        "paper_fill_app_0001",
		TicketID:      "paper_ticket_app_0001",
		Liquidity:     backtest.LiquidityTaker,
		MidPrice:      decimal.RequireFromString("100000"),
		ExecutedPrice: decimal.RequireFromString("100050"),
		Fee:           decimal.RequireFromString("30.015"),
		FeeBPS:        decimal.RequireFromString("6"),
		SpreadBPS:     decimal.RequireFromString("2"),
		SlippageBPS:   decimal.RequireFromString("3"),
		FilledAt:      now.Add(time.Minute),
	}
}

func appOrderTicket(now time.Time) domainpaper.OrderTicket {
	return domainpaper.OrderTicket{
		TicketID:     "paper_ticket_app_0001",
		ValidationID: "paper_validation_app_0001",
		DecisionID:   "risk_decision_app_0001",
		IntentID:     "risk_intent_app_0001",
		Exchange:     "bybit",
		Category:     "linear",
		Symbol:       "BTCUSDT",
		Interval:     "1",
		Side:         domainpaper.OrderSideLong,
		Quantity:     decimal.RequireFromString("0.5"),
		EntryPrice:   decimal.RequireFromString("100000"),
		StopLoss:     decimal.RequireFromString("99000"),
		TakeProfit:   decimal.RequireFromString("102000"),
		Leverage:     decimal.RequireFromString("1"),
		MaxLoss:      decimal.RequireFromString("500"),
		Confidence:   82,
		Reason:       "risk_checks_passed",
		CreatedAt:    now,
	}
}

func appOrderFill(now time.Time) domainpaper.OrderFill {
	ticket := appOrderTicket(now)
	filledAt := now.Add(time.Minute)
	return domainpaper.OrderFill{
		FillID:        "paper_fill_app_0001",
		TicketID:      ticket.TicketID,
		ValidationID:  ticket.ValidationID,
		DecisionID:    ticket.DecisionID,
		IntentID:      ticket.IntentID,
		Exchange:      ticket.Exchange,
		Category:      ticket.Category,
		Symbol:        ticket.Symbol,
		Interval:      ticket.Interval,
		Side:          ticket.Side,
		Liquidity:     backtest.LiquidityTaker,
		MidPrice:      decimal.RequireFromString("100000"),
		ExecutedPrice: decimal.RequireFromString("100050"),
		Quantity:      ticket.Quantity,
		Notional:      decimal.RequireFromString("50025"),
		Fee:           decimal.RequireFromString("30.015"),
		FeeBPS:        decimal.RequireFromString("6"),
		SpreadBPS:     decimal.RequireFromString("2"),
		SlippageBPS:   decimal.RequireFromString("3"),
		FilledAt:      filledAt,
		RecordedAt:    filledAt.Add(time.Minute),
	}
}
