package strategy

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"strings"
	"time"

	"github.com/VersoIt/Inquisitor/internal/marketdata"
	"github.com/VersoIt/Inquisitor/internal/risk"
)

var ErrSignalConflict = errors.New("strategy signal already exists with different payload")

type SignalAuditRecord struct {
	Signal     Signal
	Mode       risk.Mode
	Exchange   string
	Category   string
	ObservedAt time.Time
	Features   map[string]string
	RecordedAt time.Time
}

type SignalWriteStats struct{ Inserted, Skipped int }

func (s SignalWriteStats) Total() int { return s.Inserted + s.Skipped }

type SignalQuery struct {
	SignalID          string
	HypothesisID      string
	HypothesisVersion string
	Mode              risk.Mode
	Symbol            string
	Type              SignalType
	Start, End        time.Time
	Limit             int
}

type SignalRepository interface {
	RecordSignal(context.Context, SignalAuditRecord) (SignalWriteStats, error)
	ListSignals(context.Context, SignalQuery) ([]SignalAuditRecord, error)
}

// NewSignalAuditRecord takes an owned snapshot and uses PostgreSQL timestamp precision.
func NewSignalAuditRecord(record SignalAuditRecord) (SignalAuditRecord, error) {
	if err := validateSignalAuditRecord(record, false); err != nil {
		return SignalAuditRecord{}, err
	}
	record.Features = maps.Clone(record.Features)
	record.Signal.CreatedAt = auditTime(record.Signal.CreatedAt)
	record.ObservedAt, record.RecordedAt = auditTime(record.ObservedAt), auditTime(record.RecordedAt)
	if record.Signal.Trade != nil {
		trade := *record.Signal.Trade
		trade.CreatedAt, trade.ObservedAt = auditTime(trade.CreatedAt), auditTime(trade.ObservedAt)
		record.Signal.Trade = &trade
	}
	return record, nil
}

func auditTime(t time.Time) time.Time { return t.UTC().Truncate(time.Microsecond) }

func ValidateSignalAuditRecord(record SignalAuditRecord) error {
	return validateSignalAuditRecord(record, true)
}

func validateSignalAuditRecord(record SignalAuditRecord, persisted bool) error {
	s := record.Signal
	var problems []Problem
	for _, field := range []struct{ name, value string }{
		{"signal_id", s.SignalID}, {"hypothesis_id", s.HypothesisID}, {"hypothesis_version", s.HypothesisVersion},
		{"strategy_name", s.StrategyName}, {"symbol", s.Symbol}, {"interval", s.Interval}, {"reason", s.Reason},
		{"exchange", record.Exchange}, {"category", record.Category},
	} {
		problems = appendRequiredTrimmed(problems, field.name, field.value)
	}
	if !signalSymbolPattern.MatchString(s.Symbol) {
		problems = append(problems, Problem{"symbol", "must be uppercase alphanumeric"})
	}
	if _, err := marketdata.IntervalDuration(s.Interval); err != nil {
		problems = append(problems, Problem{"interval", "is unsupported"})
	}
	if !risk.KnownMode(record.Mode) {
		problems = append(problems, Problem{"mode", "must be PAPER or LIVE"})
	}
	if s.CreatedAt.IsZero() {
		problems = append(problems, Problem{"created_at", "is required"})
	}
	if record.RecordedAt.IsZero() || record.RecordedAt.Before(s.CreatedAt) {
		problems = append(problems, Problem{"recorded_at", "is required and must not precede created_at"})
	}
	if record.Features == nil {
		problems = append(problems, Problem{"features", "must be an object"})
	}
	for key := range record.Features {
		if strings.TrimSpace(key) == "" || key != strings.TrimSpace(key) {
			problems = append(problems, Problem{"features", "keys must be nonblank and trimmed"})
			break
		}
	}
	switch s.Type {
	case SignalNoTrade:
		if s.Trade != nil {
			problems = append(problems, Problem{"trade", "must be absent for NO_TRADE"})
		}
	case SignalBuy, SignalSell:
		if s.Trade == nil {
			problems = append(problems, Problem{"trade", "is required for BUY/SELL"})
			break
		}
		t := *s.Trade
		if err := ValidateTradeSignal(t); err != nil {
			problems = append(problems, Problem{"trade", err.Error()})
		}
		if t.SignalID != s.SignalID || t.HypothesisID != s.HypothesisID || t.HypothesisVersion != s.HypothesisVersion || t.StrategyName != s.StrategyName || t.Symbol != s.Symbol || t.Reason != s.Reason || !t.CreatedAt.Equal(s.CreatedAt) || !t.ObservedAt.Equal(record.ObservedAt) {
			problems = append(problems, Problem{"trade", "identity, reason and timestamps must match audit envelope"})
		}
		if (s.Type == SignalBuy && t.Side != SideLong) || (s.Type == SignalSell && t.Side != SideShort) {
			problems = append(problems, Problem{"trade", "side must match signal type"})
		}
	default:
		problems = append(problems, Problem{"type", "must be BUY, SELL or NO_TRADE"})
	}
	if persisted {
		times := []time.Time{s.CreatedAt, record.ObservedAt, record.RecordedAt}
		if s.Trade != nil {
			times = append(times, s.Trade.CreatedAt, s.Trade.ObservedAt)
		}
		for _, t := range times {
			_, offset := t.Zone()
			if !t.Equal(auditTime(t)) || offset != 0 {
				problems = append(problems, Problem{"timestamps", "must use UTC and microsecond precision"})
				break
			}
		}
	}
	if len(problems) > 0 {
		return ValidationError{Problems: problems}
	}
	return nil
}

func ValidateSignalQuery(q SignalQuery) error {
	for _, value := range []string{q.SignalID, q.HypothesisID, q.HypothesisVersion, q.Symbol} {
		if value != strings.TrimSpace(value) {
			return fmt.Errorf("signal query filters must be trimmed")
		}
	}
	if q.Mode != "" && !risk.KnownMode(q.Mode) {
		return fmt.Errorf("signal query mode must be PAPER or LIVE")
	}
	if q.Symbol != "" && !signalSymbolPattern.MatchString(q.Symbol) {
		return fmt.Errorf("signal query symbol must be uppercase alphanumeric")
	}
	if q.Type != "" && q.Type != SignalBuy && q.Type != SignalSell && q.Type != SignalNoTrade {
		return fmt.Errorf("signal query type is unsupported")
	}
	if !q.Start.IsZero() && !q.End.IsZero() && !q.End.After(q.Start) {
		return fmt.Errorf("signal query end must be after start")
	}
	if q.Limit < 0 || q.Limit > 1000 {
		return fmt.Errorf("signal query limit must be between 0 and 1000")
	}
	return nil
}
