package testutil

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/VersoIt/Inquisitor/internal/hypothesis"
	"github.com/VersoIt/Inquisitor/internal/paper"
	"github.com/VersoIt/Inquisitor/internal/research"
	"github.com/VersoIt/Inquisitor/internal/risk"
	"github.com/VersoIt/Inquisitor/internal/storage/postgres"
	"github.com/VersoIt/Inquisitor/internal/strategy"
)

func PaperRun(t testing.TB) strategy.PaperRunInput {
	t.Helper()
	q, p, f := PaperMarket()
	m, err := strategy.BuildPaperMarketSnapshot(q, p, f)
	if err != nil {
		t.Fatal(err)
	}
	a := PaperApproval(t)
	account, err := paper.BuildAccountSnapshot(paper.AccountLedger{Validation: a.Evidence.Validation}, q.AsOf)
	if err != nil {
		t.Fatal(err)
	}
	in, err := strategy.NewPaperRunInput(strategy.PaperRunInput{RunID: "test.paper.run", ApprovalID: a.Grant.ApprovalID, ValidationID: a.Grant.ValidationID, ConfigSHA256: strings.Repeat("a", 64), CreatedAt: q.AsOf, Market: m, Account: account, Runtime: risk.RuntimeState{Mode: risk.ModePaper, TradingEnabled: true}})
	if err != nil {
		t.Fatal(err)
	}
	return in
}

func StorePaperApproval(t testing.TB, db *sql.DB, f ApprovalFixture) {
	t.Helper()
	ctx := context.Background()
	e := f.Evidence
	if _, err := postgres.NewHypothesisRepository(db).UpsertHypotheses(ctx, []hypothesis.Record{e.Hypothesis}); err != nil {
		t.Fatal(err)
	}
	runs := postgres.NewResearchRunRepository(db)
	if _, err := runs.UpsertRuns(ctx, []research.Run{e.Run}); err != nil {
		t.Fatal(err)
	}
	if _, err := runs.RecordResult(ctx, e.Run, e.Result); err != nil {
		t.Fatal(err)
	}
	v := postgres.NewPaperValidationRepository(db)
	planned := e.Validation
	planned.Status = paper.ValidationStatusPlanned
	planned.StartedAt = time.Time{}
	if _, err := v.RecordValidation(ctx, planned); err != nil {
		t.Fatal(err)
	}
	if _, err := v.TransitionValidation(ctx, e.Validation, paper.ValidationStatusPlanned); err != nil {
		t.Fatal(err)
	}
	if _, err := postgres.NewStrategyApprovalRepository(db).RecordPaperApproval(ctx, f.Grant); err != nil {
		t.Fatal(err)
	}
}
