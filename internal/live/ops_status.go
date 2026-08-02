package live

import (
	"errors"
)

type LiveOpsStatus string

const (
	LiveOpsStatusClear     LiveOpsStatus = "CLEAR"
	LiveOpsStatusAttention LiveOpsStatus = "ATTENTION"
	LiveOpsStatusBlocked   LiveOpsStatus = "BLOCKED"
)

func SummarizeLiveOpsStatus(checks []ReadinessCheck) (LiveOpsStatus, error) {
	if err := ValidateReadinessChecks(checks); err != nil {
		return "", err
	}
	summary := SummarizeReadinessChecks(checks)
	if summary.Failed > 0 {
		return LiveOpsStatusBlocked, nil
	}
	if summary.Warned > 0 {
		return LiveOpsStatusAttention, nil
	}
	return LiveOpsStatusClear, nil
}

func ValidateLiveOpsStatus(status LiveOpsStatus) error {
	if !KnownLiveOpsStatus(status) {
		return errors.New("live ops status must be CLEAR, ATTENTION, or BLOCKED")
	}
	return nil
}

func KnownLiveOpsStatus(status LiveOpsStatus) bool {
	switch status {
	case LiveOpsStatusClear, LiveOpsStatusAttention, LiveOpsStatusBlocked:
		return true
	default:
		return false
	}
}
