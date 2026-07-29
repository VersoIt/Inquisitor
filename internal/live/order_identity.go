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
