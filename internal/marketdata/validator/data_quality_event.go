package validator

import (
	"fmt"
	"strings"

	"github.com/VersoIt/Inquisitor/internal/marketdata"
)

func ValidateDataQualityEvent(event marketdata.DataQualityEvent) error {
	var problems []Problem

	add := func(field, code, message string) {
		problems = append(problems, Problem{Field: field, Code: code, Message: message})
	}

	if strings.TrimSpace(event.Exchange) == "" {
		add("exchange", "required", "exchange is required")
	}
	if strings.TrimSpace(event.Symbol) == "" {
		add("symbol", "required", "symbol is required")
	}
	if strings.TrimSpace(event.EventType) == "" {
		add("event_type", "required", "event_type is required")
	} else if !isKnownDataQualityEventType(event.EventType) {
		add("event_type", "unsupported", "event_type is unsupported")
	}
	if strings.TrimSpace(event.Severity) == "" {
		add("severity", "required", "severity is required")
	} else if !isKnownDataQualitySeverity(event.Severity) {
		add("severity", "unsupported", "severity is unsupported")
	}
	if strings.TrimSpace(event.Message) == "" {
		add("message", "required", "message is required")
	}
	if event.CreatedAt.IsZero() {
		add("created_at", "required", "created_at is required")
	}

	if len(problems) > 0 {
		return CandleValidationError{Problems: problems}
	}
	return nil
}

func isKnownDataQualityEventType(eventType string) bool {
	switch eventType {
	case marketdata.DataQualityEventCandleGap,
		marketdata.DataQualityEventOrderbookInvalid,
		marketdata.DataQualityEventSpreadTooWide,
		marketdata.DataQualityEventStaleData:
		return true
	default:
		return false
	}
}

func isKnownDataQualitySeverity(severity string) bool {
	switch severity {
	case marketdata.DataQualitySeverityInfo, marketdata.DataQualitySeverityWarning, marketdata.DataQualitySeverityCritical:
		return true
	default:
		return false
	}
}

func ValidateDataQualityEvents(events []marketdata.DataQualityEvent) error {
	for i, event := range events {
		if err := ValidateDataQualityEvent(event); err != nil {
			return fmt.Errorf("data_quality_event[%d]: %w", i, err)
		}
	}
	return nil
}
