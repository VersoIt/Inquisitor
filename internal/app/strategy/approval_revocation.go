package strategy

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/VersoIt/Inquisitor/internal/clock"
	"github.com/VersoIt/Inquisitor/internal/strategy"
)

type PaperApprovalRevoker interface {
	RevokePaperApproval(context.Context, string, strategy.ApprovalRevocation) (strategy.ApprovalStats, error)
}

// Revocation must remain possible without loading research evidence or policy.
type ApprovalRevocationService struct {
	repository PaperApprovalRevoker
	clock      clock.Clock
}

func NewApprovalRevocationService(repository PaperApprovalRevoker, value clock.Clock) *ApprovalRevocationService {
	return &ApprovalRevocationService{repository: repository, clock: value}
}

func (s *ApprovalRevocationService) Revoke(ctx context.Context, id, reviewedBy, reason string) (strategy.ApprovalStats, error) {
	if err := ctx.Err(); err != nil {
		return strategy.ApprovalStats{}, err
	}
	if s == nil || s.repository == nil || s.clock == nil {
		return strategy.ApprovalStats{}, fmt.Errorf("approval revocation requires repository and clock")
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
