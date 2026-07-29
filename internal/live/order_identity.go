package live

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

type LiveLoopOrderIdentity struct {
	RunID         string
	SubmissionID  string
	ClientOrderID string
}

type LiveLoopOrderIdentityExpectation struct {
	SubmissionID  string
	ClientOrderID string
}

func NewDeterministicLiveLoopOrderIdentity(decisionID string, runID string) (LiveLoopOrderIdentity, error) {
	trimmedDecisionID := strings.TrimSpace(decisionID)
	if trimmedDecisionID == "" {
		return LiveLoopOrderIdentity{}, fmt.Errorf("decision-id is required")
	}

	sum := sha256.Sum256([]byte(trimmedDecisionID))
	suffix := hex.EncodeToString(sum[:])[:24]
	identity := LiveLoopOrderIdentity{
		RunID:         "live_loop_" + suffix,
		SubmissionID:  "live_sub_" + suffix,
		ClientOrderID: "inq_live_" + suffix,
	}

	trimmedRunID := strings.TrimSpace(runID)
	if trimmedRunID != "" {
		if runID != trimmedRunID {
			return LiveLoopOrderIdentity{}, fmt.Errorf("run-id must be trimmed")
		}
		identity.RunID = trimmedRunID
	}
	return identity, nil
}

func ValidateLiveLoopOrderIdentityExpectation(
	identity LiveLoopOrderIdentity,
	expectation LiveLoopOrderIdentityExpectation,
) error {
	var problems []string
	expectedSubmissionID := strings.TrimSpace(expectation.SubmissionID)
	expectedClientOrderID := strings.TrimSpace(expectation.ClientOrderID)
	if expectation.SubmissionID != expectedSubmissionID {
		problems = append(problems, "expected_submission_id must be trimmed")
	}
	if expectation.ClientOrderID != expectedClientOrderID {
		problems = append(problems, "expected_client_order_id must be trimmed")
	}
	if expectedSubmissionID != "" && identity.SubmissionID != expectedSubmissionID {
		problems = append(problems, fmt.Sprintf("expected_submission_id %q does not match planned submission_id %q", expectedSubmissionID, identity.SubmissionID))
	}
	if expectedClientOrderID != "" && identity.ClientOrderID != expectedClientOrderID {
		problems = append(problems, fmt.Sprintf("expected_client_order_id %q does not match planned client_order_id %q", expectedClientOrderID, identity.ClientOrderID))
	}
	if len(problems) > 0 {
		return fmt.Errorf("live loop order identity expectation validation failed: %s", strings.Join(problems, "; "))
	}
	return nil
}
