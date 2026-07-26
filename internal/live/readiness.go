package live

import (
	"errors"
	"fmt"
	"strings"
)

type ReadinessCheckStatus string

const (
	ReadinessCheckStatusPass ReadinessCheckStatus = "PASS"
	ReadinessCheckStatusWarn ReadinessCheckStatus = "WARN"
	ReadinessCheckStatusFail ReadinessCheckStatus = "FAIL"
)

type ReadinessCheck struct {
	Name    string
	Status  ReadinessCheckStatus
	Details string
}

type ReadinessCheckSummary struct {
	Total  int
	Passed int
	Warned int
	Failed int
}

func NewReadinessCheck(name string, status ReadinessCheckStatus, details string) ReadinessCheck {
	return ReadinessCheck{
		Name:    strings.TrimSpace(name),
		Status:  status,
		Details: strings.TrimSpace(details),
	}
}

func SummarizeReadinessChecks(checks []ReadinessCheck) ReadinessCheckSummary {
	var summary ReadinessCheckSummary
	for _, check := range checks {
		summary.Total++
		switch check.Status {
		case ReadinessCheckStatusPass:
			summary.Passed++
		case ReadinessCheckStatusWarn:
			summary.Warned++
		case ReadinessCheckStatusFail:
			summary.Failed++
		}
	}
	return summary
}

func ReadinessChecksReady(checks []ReadinessCheck) bool {
	if len(checks) == 0 {
		return false
	}
	return SummarizeReadinessChecks(checks).Failed == 0
}

func ValidateReadinessCheck(check ReadinessCheck) error {
	var problems []string
	if strings.TrimSpace(check.Name) == "" {
		problems = append(problems, "name is required")
	}
	if check.Name != strings.TrimSpace(check.Name) {
		problems = append(problems, "name must be trimmed")
	}
	if !KnownReadinessCheckStatus(check.Status) {
		problems = append(problems, "status must be PASS, WARN, or FAIL")
	}
	if strings.TrimSpace(check.Details) == "" {
		problems = append(problems, "details are required")
	}
	if check.Details != strings.TrimSpace(check.Details) {
		problems = append(problems, "details must be trimmed")
	}
	if len(problems) > 0 {
		return errors.New("readiness check validation failed: " + strings.Join(problems, "; "))
	}
	return nil
}

func ValidateReadinessChecks(checks []ReadinessCheck) error {
	if len(checks) == 0 {
		return errors.New("readiness checks are required")
	}
	for index, check := range checks {
		if err := ValidateReadinessCheck(check); err != nil {
			return fmt.Errorf("readiness_check[%d]: %w", index, err)
		}
	}
	return nil
}

func KnownReadinessCheckStatus(status ReadinessCheckStatus) bool {
	switch status {
	case ReadinessCheckStatusPass, ReadinessCheckStatusWarn, ReadinessCheckStatusFail:
		return true
	default:
		return false
	}
}
