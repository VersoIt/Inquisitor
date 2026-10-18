package paper_test

import (
	"context"
	"errors"
	"testing"
	"time"

	apppaper "github.com/VersoIt/Inquisitor/internal/app/paper"
	"github.com/VersoIt/Inquisitor/internal/clock"
	"github.com/VersoIt/Inquisitor/internal/paper"
)

type entryGuard struct {
	calls int
	err   error
}

func (g *entryGuard) AuthorizePaperEntry(context.Context, paper.OrderTicket, apppaper.RecordOrderFillRequest) error {
	g.calls++
	return g.err
}

func TestSignalBackedFillRequiresGuardButExistingFillRecoveryDoesNot(t *testing.T) {
	for _, scenario := range []string{"missing guard", "authorization denied", "authorized", "already recorded", "legacy manual"} {
		t.Run(scenario, func(t *testing.T) {
			now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
			ticket := appOrderTicket(now)
			ticket.SourceSignalID, ticket.SourceApprovalID = "signal", "approval"
			record := testValidationRecord(t, "research_app_0001", now.Add(-2*time.Hour), paper.ValidationStatusRunning)
			record.ValidationID = ticket.ValidationID
			fills := &fakeOrderFillRepository{stats: paper.OrderFillStats{Inserted: 1}}
			guard := &entryGuard{}
			opts := []apppaper.Option{apppaper.WithValidationRecordRepository(&fakeValidationRecordRepository{records: []paper.ValidationRecord{record}}), apppaper.WithOrderTicketRepository(&fakeOrderTicketRepository{tickets: []paper.OrderTicket{ticket}}), apppaper.WithOrderFillRepository(fills), apppaper.WithKillSwitchRepository(&fakePaperKillSwitchRepository{}), apppaper.WithClock(clock.FixedClock{Time: now.Add(2 * time.Minute)})}
			if scenario != "missing guard" {
				opts = append(opts, apppaper.WithEntryAuthorization(guard))
			}
			switch scenario {
			case "authorization denied":
				guard.err = errors.New("revoked")
			case "already recorded":
				fills.fills = []paper.OrderFill{appOrderFill(now)}
				guard.err = errors.New("revoked after original fill")
			case "legacy manual":
				ticket.SourceSignalID, ticket.SourceApprovalID = "", ""
				opts = append(opts, apppaper.WithOrderTicketRepository(&fakeOrderTicketRepository{tickets: []paper.OrderTicket{ticket}}))
			}
			got, err := apppaper.NewService(nil, nil, opts...).RecordOrderFill(context.Background(), appOrderFillRequest(now))
			denied := scenario == "missing guard" || scenario == "authorization denied"
			if denied {
				if err == nil || got.Fill.FillID != "" || fills.calls != 0 {
					t.Fatalf("unauthorized fill %+v %v", got, err)
				}
			} else if err != nil || fills.calls != 1 {
				t.Fatalf("fill %+v %v", got, err)
			}
			wantCalls := 0
			if scenario == "authorized" || scenario == "authorization denied" {
				wantCalls = 1
			}
			if guard.calls != wantCalls {
				t.Fatalf("guard calls=%d want=%d", guard.calls, wantCalls)
			}
		})
	}
}
