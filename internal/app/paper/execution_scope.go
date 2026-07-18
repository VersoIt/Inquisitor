package paper

import (
	"fmt"
	"strings"
)

type paperExecutionCycleScope struct {
	ValidationID string
	Symbol       string
	Interval     string
}

func requirePaperExecutionCycleScope(validationIDValue string, symbolValue string, intervalValue string) (paperExecutionCycleScope, error) {
	validationID := strings.TrimSpace(validationIDValue)
	symbol := strings.ToUpper(strings.TrimSpace(symbolValue))
	interval := strings.TrimSpace(intervalValue)
	if validationID == "" {
		return paperExecutionCycleScope{}, fmt.Errorf("validation_id is required")
	}
	if symbol == "" {
		return paperExecutionCycleScope{}, fmt.Errorf("symbol is required")
	}
	if interval == "" {
		return paperExecutionCycleScope{}, fmt.Errorf("interval is required")
	}
	return paperExecutionCycleScope{ValidationID: validationID, Symbol: symbol, Interval: interval}, nil
}

func validatePaperExecutionCycleScanLimits(pendingScanLimit int, positionScanLimit int, quoteScanLimit int) error {
	if pendingScanLimit < 0 {
		return fmt.Errorf("pending_scan_limit must be greater than or equal to zero")
	}
	if positionScanLimit < 0 {
		return fmt.Errorf("position_scan_limit must be greater than or equal to zero")
	}
	if quoteScanLimit < 0 {
		return fmt.Errorf("quote_scan_limit must be greater than or equal to zero")
	}
	return nil
}
