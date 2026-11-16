package strategy

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/VersoIt/Inquisitor/internal/paper"
	"github.com/VersoIt/Inquisitor/internal/research"
	"github.com/VersoIt/Inquisitor/internal/risk"
)

var (
	ErrPaperRunNotFound   = errors.New("paper run not found")
	ErrPaperRunConflict   = errors.New("paper run conflicts with immutable input or receipt")
	ErrPaperAdmissionBusy = errors.New("paper admission is already running for this validation")
	paperRunIDPattern     = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:-]{0,127}$`)
	paperRunHashPattern   = regexp.MustCompile(`^[a-f0-9]{64}$`)
)

type PaperRunInput struct {
	RunID, ApprovalID, ValidationID, ConfigSHA256, SHA256 string
	CreatedAt                                             time.Time
	Market                                                PaperMarketSnapshot
	Account                                               paper.AccountSnapshot
	Runtime                                               risk.RuntimeState
	AllowShort                                            bool
}

type PaperRunReceipt struct {
	RunID, InputSHA256, Outcome, SignalID, DecisionID, TicketID string
	CompletedAt                                                 time.Time
}

type PaperRunRecord struct {
	Input   PaperRunInput
	Receipt *PaperRunReceipt `json:",omitempty"`
}

type PaperRunRepository interface {
	LoadPaperRun(context.Context, string) (PaperRunRecord, error)
	RecordPaperRunInput(context.Context, PaperRunInput) error
	RecordPaperRunReceipt(context.Context, PaperRunReceipt) (PaperRunReceipt, error)
}

type PaperAdmissionLocker interface {
	WithPaperAdmissionLock(context.Context, string, func(context.Context) error) error
}

func ValidatePaperRunID(id string) error {
	if !paperRunIDPattern.MatchString(id) {
		return fmt.Errorf("paper run ID requires 1-128 ASCII identifier characters")
	}
	return nil
}

func PaperRunSignalID(id string) string {
	return fmt.Sprintf("paper_run_%x", sha256.Sum256([]byte(id)))
}

func NewPaperRunInput(in PaperRunInput) (PaperRunInput, error) {
	in.SHA256 = ""
	// Freeze an owned JSON-canonical value, including nested feature slices and
	// the optional account pointer, before computing its durable identity.
	raw, err := json.Marshal(in)
	if err != nil {
		return PaperRunInput{}, err
	}
	var owned PaperRunInput
	if err := json.Unmarshal(raw, &owned); err != nil {
		return PaperRunInput{}, err
	}
	in = owned
	hash, err := research.ExperimentHash(in)
	if err != nil {
		return PaperRunInput{}, err
	}
	in.SHA256 = hash
	if err := ValidatePaperRunInput(in); err != nil {
		return PaperRunInput{}, err
	}
	return in, nil
}

func ValidatePaperRunInput(in PaperRunInput) error {
	if err := ValidatePaperRunID(in.RunID); err != nil {
		return err
	}
	for _, id := range []string{in.ApprovalID, in.ValidationID} {
		if id == "" || id != strings.TrimSpace(id) || len(id) > 256 {
			return fmt.Errorf("paper run requires bounded trimmed approval and validation IDs")
		}
	}
	if !paperRunHashPattern.MatchString(in.ConfigSHA256) || !paperRunHashPattern.MatchString(in.SHA256) || in.CreatedAt.IsZero() || !in.CreatedAt.Equal(in.CreatedAt.UTC().Truncate(time.Microsecond)) || in.Runtime.Mode != risk.ModePaper || in.Runtime.KillSwitchActive {
		return fmt.Errorf("invalid paper run config, clock or runtime envelope")
	}
	m, a := in.Market, in.Account
	if err := ValidatePaperMarketQuery(m.Query); err != nil {
		return err
	}
	if err := ValidatePaperMarketPolicy(m.Policy); err != nil {
		return err
	}
	if m.Version != PaperMarketVersion || !paperRunHashPattern.MatchString(m.SourceSHA256) || m.Query.AsOf.After(in.CreatedAt) || a.ValidationID != in.ValidationID || a.AsOf.IsZero() || a.AsOf.After(in.CreatedAt) || !a.FlatLedgerReady || a.Account == nil || len(a.Blockers) != 0 || a.Inventory.PendingTickets != 0 || a.Inventory.UnopenedFills != 0 || a.Inventory.ActivePositions != 0 || a.Inventory.LegacyTrades != 0 || a.Inventory.BrokenLinks != 0 || a.Inventory.FutureRecords != 0 || !a.RealizedEquity.Equal(a.Account.Equity) || !a.RealizedEquity.IsPositive() {
		return fmt.Errorf("paper run requires identified market observations and a reconciled flat account")
	}
	if _, err := m.StrategyContext(PaperRunSignalID(in.RunID), in.Runtime, in.AllowShort); err != nil {
		return err
	}
	want := in.SHA256
	in.SHA256 = ""
	got, err := research.ExperimentHash(in)
	if err != nil || got != want {
		return fmt.Errorf("paper run input checksum mismatch")
	}
	return nil
}

func ValidatePaperRunReceipt(r PaperRunReceipt, in PaperRunInput) error {
	if err := ValidatePaperRunInput(in); err != nil {
		return err
	}
	_, decisionID := PaperRiskIDs(PaperRunSignalID(in.RunID))
	if r.RunID != in.RunID || r.InputSHA256 != in.SHA256 || r.SignalID != PaperRunSignalID(in.RunID) || r.CompletedAt.IsZero() || r.CompletedAt.Before(in.CreatedAt) || !r.CompletedAt.Equal(r.CompletedAt.UTC().Truncate(time.Microsecond)) {
		return fmt.Errorf("paper run receipt identity or chronology mismatch")
	}
	switch r.Outcome {
	case "NO_TRADE":
		if r.DecisionID != "" || r.TicketID != "" {
			return fmt.Errorf("abstention receipt cannot contain risk or ticket IDs")
		}
	case "RISK_REJECTED":
		if r.DecisionID != decisionID || r.TicketID != "" {
			return fmt.Errorf("risk rejection receipt has invalid provenance")
		}
	case "TICKET_RECORDED":
		if r.DecisionID != decisionID || r.TicketID != PaperSignalTicketID(r.SignalID) {
			return fmt.Errorf("ticket receipt has invalid provenance")
		}
	default:
		return fmt.Errorf("unknown paper run outcome")
	}
	return nil
}
