package live

import (
	"context"
	"errors"
	"fmt"
	"strings"

	domainrisk "github.com/VersoIt/Inquisitor/internal/risk"
)

type PendingLiveDecision struct {
	Decision domainrisk.DecisionAuditRecord
}

type PendingLiveDecisionQuery struct {
	Symbol string
	Limit  int
}

type PendingLiveDecisionReader interface {
	ListPendingLiveDecisions(ctx context.Context, query PendingLiveDecisionQuery) ([]PendingLiveDecision, error)
}

func ValidatePendingLiveDecision(candidate PendingLiveDecision) error {
	var problems []string
	if err := domainrisk.ValidateDecisionAuditRecord(candidate.Decision); err != nil {
		problems = append(problems, err.Error())
	}
	if candidate.Decision.Mode != domainrisk.ModeLive {
		problems = append(problems, "decision mode must be LIVE")
	}
	if !candidate.Decision.Decision.Approved {
		problems = append(problems, "decision must be approved")
	}
	if len(problems) > 0 {
		return errors.New("pending live decision validation failed: " + strings.Join(problems, "; "))
	}
	return nil
}

func ValidatePendingLiveDecisions(candidates []PendingLiveDecision) error {
	for index, candidate := range candidates {
		if err := ValidatePendingLiveDecision(candidate); err != nil {
			return fmt.Errorf("pending_live_decision[%d]: %w", index, err)
		}
	}
	return nil
}

func ValidatePendingLiveDecisionQuery(query PendingLiveDecisionQuery) error {
	var problems []string
	if query.Symbol != strings.ToUpper(strings.TrimSpace(query.Symbol)) {
		problems = append(problems, "symbol must be uppercase and trimmed")
	}
	if query.Limit < 0 {
		problems = append(problems, "limit must be greater than or equal to zero")
	}
	if query.Limit > 100 {
		problems = append(problems, "limit must be no more than 100")
	}
	if len(problems) > 0 {
		return errors.New("pending live decision query validation failed: " + strings.Join(problems, "; "))
	}
	return nil
}
