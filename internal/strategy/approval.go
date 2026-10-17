package strategy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"github.com/VersoIt/Inquisitor/internal/hypothesis"
	"github.com/VersoIt/Inquisitor/internal/marketdata"
	"github.com/VersoIt/Inquisitor/internal/paper"
	"github.com/VersoIt/Inquisitor/internal/regime"
	"github.com/VersoIt/Inquisitor/internal/research"
	"github.com/VersoIt/Inquisitor/internal/risk"
	"github.com/shopspring/decimal"
)

var (
	ErrApprovalUnavailable = errors.New("paper strategy approval unavailable")
	ErrApprovalConflict    = errors.New("paper strategy approval conflicts with stored record")
)

type PaperApproval struct {
	ApprovalID                           string
	ValidationID                         string
	RunID                                string
	HypothesisName                       string
	HypothesisVersion                    string
	HypothesisSHA256                     string
	StrategyName                         string
	Exchange, Category, Symbol, Interval string
	ReviewedBy                           string
	Reason                               string
	GrantedAt                            time.Time
}

type ApprovalRevocation struct {
	RevokedAt         time.Time
	RevokedBy, Reason string
}
type PaperAuthorization struct {
	Grant      PaperApproval
	Revocation ApprovalRevocation
	Evidence   PaperApprovalEvidence
}
type PaperApprovalEvidence struct {
	Hypothesis hypothesis.Record
	Run        research.Run
	Result     research.Result
	Validation paper.ValidationRecord
}
type ApprovalStats struct{ Written, Skipped int }

type PaperApprovalRepository interface {
	LoadPaperApprovalEvidence(context.Context, string) (PaperApprovalEvidence, error)
	RecordPaperApproval(context.Context, PaperApproval) (ApprovalStats, error)
	LoadPaperAuthorization(context.Context, string) (PaperAuthorization, error)
	RevokePaperApproval(context.Context, string, ApprovalRevocation) (ApprovalStats, error)
}

func ValidatePaperApproval(a PaperApproval) error {
	for _, v := range []string{a.ApprovalID, a.ValidationID, a.RunID, a.HypothesisName, a.HypothesisVersion, a.ReviewedBy, a.Reason, a.Exchange, a.Category, a.Symbol, a.Interval} {
		if v == "" || v != strings.TrimSpace(v) {
			return fmt.Errorf("paper approval fields must be nonempty and trimmed")
		}
	}
	if a.StrategyName != NameTrend && a.StrategyName != NameMeanReversion && a.StrategyName != NameBreakout {
		return fmt.Errorf("paper approval strategy is unsupported")
	}
	if len(a.HypothesisSHA256) != 64 || strings.Trim(a.HypothesisSHA256, "0123456789abcdef") != "" {
		return fmt.Errorf("paper approval requires hypothesis SHA256")
	}
	if !signalSymbolPattern.MatchString(a.Symbol) {
		return fmt.Errorf("paper approval symbol is invalid")
	}
	if _, err := marketdata.IntervalDuration(a.Interval); err != nil {
		return err
	}
	_, offset := a.GrantedAt.Zone()
	if a.GrantedAt.IsZero() || !a.GrantedAt.Equal(auditTime(a.GrantedAt)) || offset != 0 {
		return fmt.Errorf("paper approval requires microsecond grant time")
	}
	return nil
}

func ValidateApprovalRevocation(r ApprovalRevocation) error {
	_, offset := r.RevokedAt.Zone()
	if r.RevokedAt.IsZero() || !r.RevokedAt.Equal(auditTime(r.RevokedAt)) || offset != 0 || r.RevokedBy == "" || r.RevokedBy != strings.TrimSpace(r.RevokedBy) || r.Reason == "" || r.Reason != strings.TrimSpace(r.Reason) {
		return fmt.Errorf("approval revocation requires timestamp, reviewer and reason")
	}
	return nil
}

func ValidatePaperApprovalPolicy(p research.ResultGatePolicy) error {
	if err := research.ValidateResultGatePolicy(p); err != nil {
		return err
	}
	if !p.Enabled || p.MinTrades < 100 || !p.MinProfitFactor.GreaterThan(decimal.NewFromInt(1)) || !p.MinExpectancy.IsPositive() || p.MaxDrawdownPct <= 0 || math.IsNaN(p.MaxDrawdownPct) || math.IsInf(p.MaxDrawdownPct, 0) || !p.RequireCosts || !p.RequireOutOfSample || !p.RequireWalkForward || !p.RequireRegimeAnalysis {
		return fmt.Errorf("paper approval requires enabled conservative research gates")
	}
	return nil
}

// CheckPaperApprovalEvidence validates current source data, not a copied CANDIDATE flag.
func CheckPaperApprovalEvidence(a PaperApproval, e PaperApprovalEvidence, policy research.ResultGatePolicy, at time.Time) error {
	if err := ValidatePaperApproval(a); err != nil {
		return err
	}
	if err := ValidatePaperApprovalPolicy(policy); err != nil {
		return err
	}
	if err := hypothesis.ValidateRecord(e.Hypothesis); err != nil {
		return err
	}
	if err := research.ValidateRun(e.Run); err != nil {
		return err
	}
	if err := research.ValidateResult(e.Result); err != nil {
		return err
	}
	if err := paper.ValidateValidationRecord(e.Validation); err != nil {
		return err
	}
	h, r, v, result := e.Hypothesis, e.Run, e.Validation, e.Result
	parsed, err := hypothesis.ParseYAML([]byte(h.RawYAML))
	if err != nil {
		return err
	}
	storedJSON, err := json.Marshal(h.Spec)
	if err != nil {
		return err
	}
	parsedJSON, err := json.Marshal(parsed)
	if err != nil {
		return err
	}
	if !bytes.Equal(storedJSON, parsedJSON) {
		return fmt.Errorf("hypothesis parsed specification differs from stored YAML")
	}
	compatible := false
	switch a.StrategyName {
	case NameTrend:
		compatible = slices.Contains(parsed.Regime.Allowed, string(regime.RegimeTrendUp)) && parsed.Direction != hypothesis.DirectionShort || slices.Contains(parsed.Regime.Allowed, string(regime.RegimeTrendDown)) && parsed.Direction != hypothesis.DirectionLong
	case NameMeanReversion:
		compatible = slices.Contains(parsed.Regime.Allowed, string(regime.RegimeRange))
	case NameBreakout:
		compatible = slices.Contains(parsed.Regime.Allowed, string(regime.RegimeBreakoutSetup))
	}
	if !compatible {
		return fmt.Errorf("strategy family is incompatible with hypothesis regimes/direction")
	}
	if a.HypothesisName != h.Name || a.HypothesisVersion != h.Version || a.HypothesisSHA256 != h.ContentSHA256 || r.HypothesisName != h.Name || r.HypothesisVersion != h.Version || r.HypothesisContentSHA256 != h.ContentSHA256 || a.RunID != r.RunID || result.RunID != r.RunID || v.RunID != r.RunID || a.ValidationID != v.ValidationID {
		return fmt.Errorf("paper approval source identities or hypothesis hash do not match")
	}
	if r.Status != research.StatusCompleted || result.FinalStatus != research.StatusCompleted || result.Outcome != research.OutcomeCandidate || v.Status != paper.ValidationStatusRunning {
		return fmt.Errorf("paper approval requires completed CANDIDATE research and RUNNING paper validation")
	}
	if a.Exchange != r.Exchange || a.Category != r.Category || a.Exchange != parsed.Market.Exchange || a.Category != parsed.Market.Category || !slices.Contains(r.Symbols, a.Symbol) || !slices.Contains(r.Intervals, a.Interval) || !slices.Contains(parsed.Market.Symbols, a.Symbol) || !slices.Contains(parsed.Market.Intervals, a.Interval) {
		return fmt.Errorf("paper approval market scope is outside validated hypothesis/run")
	}
	if at.IsZero() || at.Before(a.GrantedAt) || at.Before(v.StartedAt) || h.ImportedAt.After(at) || r.PlannedAt.After(at) || result.RecordedAt.Before(r.PlannedAt) || result.RecordedAt.After(v.PlannedAt) || v.PlannedAt.After(at) {
		return fmt.Errorf("paper approval evidence chronology is invalid")
	}
	if policy.MinTrades < parsed.Validation.MinTrades {
		policy.MinTrades = parsed.Validation.MinTrades
	}
	gates, err := research.EvaluateResultGates(result, policy)
	if err != nil {
		return err
	}
	m := result.Metrics
	if !gates.CandidateEligible || m.WalkForwardFolds <= 0 || m.WalkForwardPassedFolds != m.WalkForwardFolds || m.WalkForwardFailedFolds != 0 || m.WalkForwardTrades <= 0 {
		return fmt.Errorf("paper approval research gates or walk-forward folds failed")
	}
	pf, err := decimal.NewFromString(m.OutOfSampleProfitFactor)
	if err != nil || !m.OutOfSampleProfitFactorDefined || pf.LessThan(policy.MinProfitFactor) {
		return fmt.Errorf("paper approval out-of-sample profit factor failed")
	}
	pnl, err := decimal.NewFromString(m.OutOfSampleNetPnL)
	if err != nil || !pnl.IsPositive() || math.IsNaN(m.OutOfSampleMaxDrawdownPct) || math.IsInf(m.OutOfSampleMaxDrawdownPct, 0) || m.OutOfSampleMaxDrawdownPct > policy.MaxDrawdownPct {
		return fmt.Errorf("paper approval out-of-sample performance failed")
	}
	return nil
}

func ResolvePaperAuthorization(a PaperAuthorization, policy research.ResultGatePolicy, mode risk.Mode, symbol, interval string, current regime.Regime, at time.Time) (ApprovalSnapshot, error) {
	deny := func(err error) (ApprovalSnapshot, error) {
		return ApprovalSnapshot{}, fmt.Errorf("%w: %v", ErrApprovalUnavailable, err)
	}
	if mode != risk.ModePaper {
		return deny(fmt.Errorf("paper approval cannot authorize requested mode"))
	}
	if !a.Revocation.RevokedAt.IsZero() || a.Revocation.RevokedBy != "" || a.Revocation.Reason != "" {
		return deny(fmt.Errorf("approval is revoked"))
	}
	if err := CheckPaperApprovalEvidence(a.Grant, a.Evidence, policy, at); err != nil {
		return deny(err)
	}
	g, h := a.Grant, a.Evidence.Hypothesis.Spec
	if symbol != g.Symbol || interval != g.Interval || !regime.KnownRegime(current) || !slices.Contains(h.Regime.Allowed, string(current)) || slices.Contains(h.Regime.Blocked, string(current)) {
		return deny(fmt.Errorf("requested scope or regime is not approved"))
	}
	return ApprovalSnapshot{ApprovalID: g.ApprovalID, ValidationID: g.ValidationID, RunID: g.RunID, HypothesisSHA256: g.HypothesisSHA256, HypothesisID: g.HypothesisName, Version: g.HypothesisVersion, StrategyName: g.StrategyName, Mode: risk.ModePaper, Direction: h.Direction, Exchange: g.Exchange, Category: g.Category, Symbol: g.Symbol, Interval: g.Interval, MaxRiskPct: decimal.NewFromFloat(h.Risk.MaxRiskPerTradePct), MinConfidence: h.Risk.MinConfidence, Enabled: true}, nil
}
