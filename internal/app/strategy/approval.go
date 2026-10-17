package strategy

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/VersoIt/Inquisitor/internal/clock"
	"github.com/VersoIt/Inquisitor/internal/research"
	"github.com/VersoIt/Inquisitor/internal/risk"
	"github.com/VersoIt/Inquisitor/internal/strategy"
)

type ApprovalService struct {
	repository strategy.PaperApprovalRepository
	clock      clock.Clock
	policy     research.ResultGatePolicy
}

func NewApprovalService(repository strategy.PaperApprovalRepository, value clock.Clock, policy research.ResultGatePolicy) (*ApprovalService, error) {
	if repository == nil || value == nil {
		return nil, fmt.Errorf("approval service requires repository and clock")
	}
	if err := strategy.ValidatePaperApprovalPolicy(policy); err != nil {
		return nil, err
	}
	return &ApprovalService{repository: repository, clock: value, policy: policy}, nil
}

type GrantPaperRequest struct{ ApprovalID, ValidationID, StrategyName, Symbol, Interval, ReviewedBy, Reason string }

func (s *ApprovalService) GrantPaper(ctx context.Context, req GrantPaperRequest) (strategy.PaperApproval, strategy.ApprovalStats, error) {
	if err := ctx.Err(); err != nil {
		return strategy.PaperApproval{}, strategy.ApprovalStats{}, err
	}
	if s == nil || s.repository == nil || s.clock == nil {
		return strategy.PaperApproval{}, strategy.ApprovalStats{}, fmt.Errorf("approval service is required")
	}
	if req.ValidationID == "" || req.ValidationID != strings.TrimSpace(req.ValidationID) {
		return strategy.PaperApproval{}, strategy.ApprovalStats{}, fmt.Errorf("validation ID must be nonempty and trimmed")
	}
	e, err := s.repository.LoadPaperApprovalEvidence(ctx, req.ValidationID)
	if err != nil {
		return strategy.PaperApproval{}, strategy.ApprovalStats{}, fmt.Errorf("load paper approval evidence: %w", err)
	}
	a := strategy.PaperApproval{ApprovalID: req.ApprovalID, ValidationID: req.ValidationID, RunID: e.Run.RunID, HypothesisName: e.Hypothesis.Name, HypothesisVersion: e.Hypothesis.Version, HypothesisSHA256: e.Hypothesis.ContentSHA256,
		StrategyName: req.StrategyName, Exchange: e.Run.Exchange, Category: e.Run.Category, Symbol: req.Symbol, Interval: req.Interval, ReviewedBy: req.ReviewedBy, Reason: req.Reason, GrantedAt: s.clock.Now().UTC().Truncate(time.Microsecond)}
	if err := strategy.CheckPaperApprovalEvidence(a, e, s.policy, a.GrantedAt); err != nil {
		return strategy.PaperApproval{}, strategy.ApprovalStats{}, err
	}
	stats, err := s.repository.RecordPaperApproval(ctx, a)
	if err != nil {
		return strategy.PaperApproval{}, strategy.ApprovalStats{}, err
	}
	if !validApprovalStats(stats) {
		return strategy.PaperApproval{}, strategy.ApprovalStats{}, fmt.Errorf("invalid approval write acknowledgement")
	}
	return a, stats, nil
}

func (s *ApprovalService) Resolve(ctx context.Context, id string, market strategy.MarketContext) (strategy.ApprovalSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return strategy.ApprovalSnapshot{}, err
	}
	if s == nil || s.repository == nil || s.clock == nil {
		return strategy.ApprovalSnapshot{}, fmt.Errorf("approval service is required")
	}
	if market.Mode != risk.ModePaper {
		return strategy.ApprovalSnapshot{}, fmt.Errorf("%w: only PAPER authorization is supported", strategy.ErrApprovalUnavailable)
	}
	if id == "" || id != strings.TrimSpace(id) || market.Now.IsZero() || market.Now.After(s.clock.Now()) {
		return strategy.ApprovalSnapshot{}, fmt.Errorf("invalid approval request ID or evaluation time")
	}
	a, err := s.repository.LoadPaperAuthorization(ctx, id)
	if err != nil {
		return strategy.ApprovalSnapshot{}, fmt.Errorf("load paper authorization: %w", err)
	}
	if a.Grant.ApprovalID != id || a.Grant.Exchange != market.Regime.Exchange || a.Grant.Category != market.Regime.Category {
		return strategy.ApprovalSnapshot{}, fmt.Errorf("%w: approval identity or market mismatch", strategy.ErrApprovalUnavailable)
	}
	snapshot, err := strategy.ResolvePaperAuthorization(a, s.policy, market.Mode, market.Regime.Symbol, market.Regime.Interval, market.Regime.Regime, market.Now)
	if err != nil {
		return strategy.ApprovalSnapshot{}, err
	}
	return snapshot, nil
}

func (s *ApprovalService) Revoke(ctx context.Context, id, reviewedBy, reason string) (strategy.ApprovalStats, error) {
	if err := ctx.Err(); err != nil {
		return strategy.ApprovalStats{}, err
	}
	if s == nil || s.repository == nil || s.clock == nil {
		return strategy.ApprovalStats{}, fmt.Errorf("approval service is required")
	}
	if id == "" || id != strings.TrimSpace(id) {
		return strategy.ApprovalStats{}, fmt.Errorf("approval ID must be nonempty and trimmed")
	}
	r := strategy.ApprovalRevocation{RevokedAt: s.clock.Now().UTC().Truncate(time.Microsecond), RevokedBy: reviewedBy, Reason: reason}
	if err := strategy.ValidateApprovalRevocation(r); err != nil {
		return strategy.ApprovalStats{}, err
	}
	stats, err := s.repository.RevokePaperApproval(ctx, id, r)
	if err != nil {
		return strategy.ApprovalStats{}, err
	}
	if !validApprovalStats(stats) {
		return strategy.ApprovalStats{}, fmt.Errorf("invalid revocation write acknowledgement")
	}
	return stats, nil
}

func validApprovalStats(s strategy.ApprovalStats) bool {
	return s.Written == 1 && s.Skipped == 0 || s.Written == 0 && s.Skipped == 1
}

type AuthorizedGenerationService struct {
	approvals  *ApprovalService
	generation *GenerationService
}

func NewAuthorizedGenerationService(approvals *ApprovalService, generation *GenerationService) *AuthorizedGenerationService {
	return &AuthorizedGenerationService{approvals: approvals, generation: generation}
}
func (s *AuthorizedGenerationService) GenerateAndRecord(ctx context.Context, approvalID string, market strategy.MarketContext) (GenerateResult, error) {
	if err := ctx.Err(); err != nil {
		return GenerateResult{}, err
	}
	if s == nil || s.approvals == nil || s.generation == nil {
		return GenerateResult{}, fmt.Errorf("authorized generation requires approval and generation services")
	}
	approval, err := s.approvals.Resolve(ctx, approvalID, market)
	if err != nil {
		return GenerateResult{}, err
	}
	market.Approval = approval
	return s.generation.GenerateAndRecord(ctx, market)
}
