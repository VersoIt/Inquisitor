package strategy

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/VersoIt/Inquisitor/internal/clock"
	"github.com/VersoIt/Inquisitor/internal/paper"
	"github.com/VersoIt/Inquisitor/internal/research"
	"github.com/VersoIt/Inquisitor/internal/risk"
	"github.com/VersoIt/Inquisitor/internal/strategy"
)

type PaperRunMarketSource interface {
	Snapshot(context.Context, strategy.PaperMarketScope) (strategy.PaperMarketSnapshot, error)
}
type PaperRunAccountSource interface {
	Snapshot(context.Context, string) (paper.AccountSnapshot, error)
}
type PaperRunApprovals interface {
	Resolve(context.Context, string, strategy.MarketContext) (strategy.ApprovalSnapshot, error)
}
type PaperRunCycle interface {
	Run(context.Context, RunPaperSignalCycleRequest) (RunPaperSignalCycleResult, error)
}

type PaperRunDependencies struct {
	Market    PaperRunMarketSource
	Account   PaperRunAccountSource
	Approvals PaperRunApprovals
	Cycle     PaperRunCycle
	Journal   strategy.PaperRunRepository
	Locker    strategy.PaperAdmissionLocker
	Tickets   paper.OrderTicketRepository
	Clock     clock.Clock
}

type PaperRunSettings struct {
	ConfigSHA256               string
	TradingEnabled, AllowShort bool
}

type RunSourcedPaperRequest struct {
	RunID, ApprovalID, ValidationID string
	Scope                           strategy.PaperMarketScope
}

type RunSourcedPaperResult struct {
	Receipt  strategy.PaperRunReceipt
	Replayed bool
}

type PaperRunService struct {
	deps     PaperRunDependencies
	settings PaperRunSettings
}

func NewPaperRunService(d PaperRunDependencies, s PaperRunSettings) (*PaperRunService, error) {
	hash, err := hex.DecodeString(s.ConfigSHA256)
	if d.Market == nil || d.Account == nil || d.Approvals == nil || d.Cycle == nil || d.Journal == nil || d.Locker == nil || d.Tickets == nil || d.Clock == nil || err != nil || len(hash) != 32 || strings.ToLower(s.ConfigSHA256) != s.ConfigSHA256 {
		return nil, fmt.Errorf("paper runner requires sources, approvals, cycle, journal, lock, tickets, clock and config checksum")
	}
	return &PaperRunService{deps: d, settings: s}, nil
}

// Run sources and freezes inputs before any signal write. Completed receipts are
// historical read-back only; unfinished runs never refresh their frozen inputs.
func (s *PaperRunService) Run(ctx context.Context, req RunSourcedPaperRequest) (RunSourcedPaperResult, error) {
	if err := ctx.Err(); err != nil {
		return RunSourcedPaperResult{}, err
	}
	if s == nil || s.deps.Clock == nil || s.deps.Market == nil || s.deps.Account == nil || s.deps.Approvals == nil || s.deps.Cycle == nil || s.deps.Journal == nil || s.deps.Locker == nil || s.deps.Tickets == nil {
		return RunSourcedPaperResult{}, fmt.Errorf("paper runner is required")
	}
	if err := strategy.ValidatePaperRunID(req.RunID); err != nil {
		return RunSourcedPaperResult{}, err
	}
	for _, id := range []string{req.ApprovalID, req.ValidationID} {
		if id == "" || id != strings.TrimSpace(id) || len(id) > 256 {
			return RunSourcedPaperResult{}, fmt.Errorf("paper runner requires bounded trimmed source IDs")
		}
	}
	if err := strategy.ValidatePaperMarketQuery(strategy.PaperMarketQuery{Scope: req.Scope, AsOf: s.deps.Clock.Now()}); err != nil {
		return RunSourcedPaperResult{}, err
	}
	var out RunSourcedPaperResult
	err := s.deps.Locker.WithPaperAdmissionLock(ctx, req.ValidationID, func(ctx context.Context) error {
		record, err := s.deps.Journal.LoadPaperRun(ctx, req.RunID)
		existing := err == nil
		if err != nil && !errors.Is(err, strategy.ErrPaperRunNotFound) {
			return err
		}
		if existing {
			if err := strategy.ValidatePaperRunInput(record.Input); err != nil {
				return err
			}
			in := record.Input
			if in.ApprovalID != req.ApprovalID || in.ValidationID != req.ValidationID || in.Market.Query.Scope != req.Scope || in.ConfigSHA256 != s.settings.ConfigSHA256 || in.Runtime.TradingEnabled != s.settings.TradingEnabled || in.AllowShort != s.settings.AllowShort {
				return strategy.ErrPaperRunConflict
			}
			if in.CreatedAt.After(s.deps.Clock.Now()) {
				return fmt.Errorf("paper run is from the future")
			}
			if record.Receipt != nil {
				if err := strategy.ValidatePaperRunReceipt(*record.Receipt, in); err != nil {
					return err
				}
				if record.Receipt.CompletedAt.After(s.deps.Clock.Now()) {
					return fmt.Errorf("paper receipt is from the future")
				}
				out = RunSourcedPaperResult{Receipt: *record.Receipt, Replayed: true}
				return ctx.Err()
			}
			if err := s.checkResumeAccount(ctx, in); err != nil {
				return err
			}
		} else {
			market, err := s.deps.Market.Snapshot(ctx, req.Scope)
			if err != nil {
				return fmt.Errorf("source paper market: %w", err)
			}
			account, err := s.deps.Account.Snapshot(ctx, req.ValidationID)
			if err != nil {
				return fmt.Errorf("source paper account: %w", err)
			}
			if market.Query.Scope != req.Scope {
				return fmt.Errorf("paper source returned foreign market")
			}
			in, err := strategy.NewPaperRunInput(strategy.PaperRunInput{RunID: req.RunID, ApprovalID: req.ApprovalID, ValidationID: req.ValidationID, ConfigSHA256: s.settings.ConfigSHA256,
				CreatedAt: s.now(), Market: market, Account: account, Runtime: risk.RuntimeState{Mode: risk.ModePaper, TradingEnabled: s.settings.TradingEnabled}, AllowShort: s.settings.AllowShort})
			if err != nil {
				return err
			}
			// Validate the binding before creating a durable run for this account.
			if err := s.checkApproval(ctx, in); err != nil {
				return err
			}
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := s.deps.Journal.RecordPaperRunInput(ctx, in); err != nil {
				return fmt.Errorf("freeze paper inputs: %w", err)
			}
			record.Input = in
		}
		in := record.Input
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := s.checkApproval(ctx, in); err != nil {
			return err
		}
		market, err := in.Market.StrategyContext(strategy.PaperRunSignalID(in.RunID), in.Runtime, in.AllowShort)
		if err != nil {
			return err
		}
		result, err := s.deps.Cycle.Run(ctx, RunPaperSignalCycleRequest{ApprovalID: in.ApprovalID, Market: market, Account: *in.Account.Account, RiskMarket: in.Market.RiskMarket})
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		receipt, err := paperRunReceipt(in, result, s.now())
		if err != nil {
			return err
		}
		saved, err := s.deps.Journal.RecordPaperRunReceipt(ctx, receipt)
		if err != nil {
			return fmt.Errorf("complete paper run: %w", err)
		}
		if err := strategy.ValidatePaperRunReceipt(saved, in); err != nil {
			return err
		}
		want := receipt
		want.CompletedAt = saved.CompletedAt
		if saved != want || saved.CompletedAt.After(s.deps.Clock.Now()) {
			return fmt.Errorf("invalid paper run completion acknowledgement")
		}
		out = RunSourcedPaperResult{Receipt: saved, Replayed: existing}
		return ctx.Err()
	})
	if err != nil {
		return RunSourcedPaperResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return RunSourcedPaperResult{}, err
	}
	if out.Receipt.RunID != req.RunID {
		return RunSourcedPaperResult{}, fmt.Errorf("paper admission lock returned without a completed callback")
	}
	return out, nil
}

func (s *PaperRunService) now() time.Time { return s.deps.Clock.Now().UTC().Truncate(time.Microsecond) }

func (s *PaperRunService) checkApproval(ctx context.Context, in strategy.PaperRunInput) error {
	a, err := s.deps.Approvals.Resolve(ctx, in.ApprovalID, strategy.MarketContext{Now: s.now(), Mode: risk.ModePaper, Regime: in.Market.Regime})
	if err != nil {
		return err
	}
	if !a.Enabled || a.ValidationID != in.ValidationID || a.StrategyName != "trend" || a.ApprovalID != in.ApprovalID {
		return fmt.Errorf("paper trend runner requires the matching validation's current trend approval")
	}
	return nil
}

func (s *PaperRunService) checkResumeAccount(ctx context.Context, in strategy.PaperRunInput) error {
	current, err := s.deps.Account.Snapshot(ctx, in.ValidationID)
	if err != nil {
		return err
	}
	if current.AsOf.IsZero() || current.AsOf.After(s.deps.Clock.Now()) || current.AsOf.Before(in.Account.AsOf) {
		return fmt.Errorf("invalid resumed account observation time")
	}
	want := in.Account
	want.AsOf = current.AsOf
	if !current.FlatLedgerReady {
		// Only our own committed, still-unfilled ticket can explain a changed
		// inventory after a lost completion acknowledgement. Never ignore others.
		if len(current.Blockers) != 1 || current.Blockers[0] != "pending_tickets" || current.Account != nil {
			return fmt.Errorf("paper run cannot resume against an exposed or blocked account")
		}
		rows, err := s.deps.Tickets.ListOrderTickets(ctx, paper.OrderTicketQuery{TicketID: strategy.PaperSignalTicketID(strategy.PaperRunSignalID(in.RunID)), Limit: 2})
		if err != nil {
			return err
		}
		if len(rows) != 1 {
			return fmt.Errorf("resumed pending inventory has no unique own ticket")
		}
		ticket := rows[0]
		if err := paper.ValidateOrderTicket(ticket); err != nil {
			return err
		}
		if ticket.SourceSignalID != strategy.PaperRunSignalID(in.RunID) || ticket.SourceApprovalID != in.ApprovalID || ticket.ValidationID != in.ValidationID || ticket.CreatedAt.Before(in.CreatedAt) || ticket.CreatedAt.After(s.deps.Clock.Now()) {
			return fmt.Errorf("resumed ticket identity or time mismatch")
		}
		want.Inventory.Tickets++
		want.Inventory.PendingTickets++
		want.FlatLedgerReady = false
		want.Account = nil
		want.Blockers = []string{"pending_tickets"}
	}
	a, err := research.ExperimentHash(want)
	if err != nil {
		return err
	}
	b, err := research.ExperimentHash(current)
	if err != nil {
		return err
	}
	if a != b {
		return fmt.Errorf("paper account changed since frozen run inputs; automatic resume denied")
	}
	return nil
}

func paperRunReceipt(in strategy.PaperRunInput, result RunPaperSignalCycleResult, at time.Time) (strategy.PaperRunReceipt, error) {
	r := strategy.PaperRunReceipt{RunID: in.RunID, InputSHA256: in.SHA256, SignalID: result.Generation.Signal.SignalID, Outcome: string(result.Outcome), CompletedAt: at}
	if result.Outcome == PaperSignalCycleNoTrade {
		if result.Generation.Signal.Type != strategy.SignalNoTrade || result.Submission != nil {
			return strategy.PaperRunReceipt{}, fmt.Errorf("invalid paper abstention acknowledgement")
		}
	} else {
		if result.Submission == nil || result.Generation.Signal.Trade == nil {
			return strategy.PaperRunReceipt{}, fmt.Errorf("paper run risk acknowledgement missing")
		}
		e := result.Submission.Evaluation
		if err := strategy.ValidatePaperRiskEvaluation(e); err != nil {
			return strategy.PaperRunReceipt{}, err
		}
		if e.SignalID != r.SignalID || e.ApprovalID != in.ApprovalID {
			return strategy.PaperRunReceipt{}, fmt.Errorf("paper run risk source mismatch")
		}
		r.DecisionID = e.Audit.DecisionID
		if result.Outcome == PaperSignalCycleTicketRecorded {
			ticket := result.Submission.Ticket
			if !e.Audit.Decision.Approved || ticket.ValidationID != in.ValidationID || ticket.SourceSignalID != r.SignalID || ticket.SourceApprovalID != in.ApprovalID {
				return strategy.PaperRunReceipt{}, fmt.Errorf("invalid paper ticket acknowledgement")
			}
			r.TicketID = ticket.TicketID
		} else if e.Audit.Decision.Approved || result.Submission.Ticket.TicketID != "" {
			return strategy.PaperRunReceipt{}, fmt.Errorf("invalid paper risk rejection acknowledgement")
		}
	}
	if err := strategy.ValidatePaperRunReceipt(r, in); err != nil {
		return strategy.PaperRunReceipt{}, err
	}
	return r, nil
}
