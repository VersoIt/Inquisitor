package regime

import (
	"context"
	"fmt"
	"strings"
	"time"

	appfeatures "github.com/VersoIt/Inquisitor/internal/app/features"
)

type RunRequest struct {
	Exchange  string
	Category  string
	Symbols   []string
	Intervals []string
	Start     time.Time
	End       time.Time

	CandleLimit   int
	TradeLimit    int
	SnapshotLimit int
	Runtime       appfeatures.RuntimeState
}

type RunResult struct {
	Symbols    int
	Intervals  int
	Attempts   int
	Classified int
	Stored     int
	Inserted   int
	Updated    int
	NoTrade    int
}

func (s *Service) Run(ctx context.Context, req RunRequest) (RunResult, error) {
	normalized, err := normalizeRunRequest(req)
	if err != nil {
		return RunResult{}, err
	}
	if s.repository == nil {
		return RunResult{}, fmt.Errorf("regime repository is required")
	}

	result := RunResult{
		Symbols:   len(normalized.Symbols),
		Intervals: len(normalized.Intervals),
	}
	for _, symbol := range normalized.Symbols {
		for _, interval := range normalized.Intervals {
			result.Attempts++
			classification, err := s.ClassifyAndStore(ctx, appfeatures.ComputeRequest{
				Exchange:      normalized.Exchange,
				Category:      normalized.Category,
				Symbol:        symbol,
				Interval:      interval,
				Start:         normalized.Start,
				End:           normalized.End,
				CandleLimit:   normalized.CandleLimit,
				TradeLimit:    normalized.TradeLimit,
				SnapshotLimit: normalized.SnapshotLimit,
				Runtime:       normalized.Runtime,
			})
			if err != nil {
				return result, fmt.Errorf("classify and store regime %s %s: %w", symbol, interval, err)
			}

			result.Classified++
			result.Inserted += classification.Stored.Inserted
			result.Updated += classification.Stored.Updated
			result.Stored += classification.Stored.Total()
			if classification.Regime.NoTrade {
				result.NoTrade++
			}
		}
	}
	return result, nil
}

func normalizeRunRequest(req RunRequest) (RunRequest, error) {
	if strings.TrimSpace(req.Exchange) == "" {
		return RunRequest{}, fmt.Errorf("exchange is required")
	}
	if strings.TrimSpace(req.Category) == "" {
		return RunRequest{}, fmt.Errorf("category is required")
	}
	symbols, err := normalizeStringSet("symbols", req.Symbols)
	if err != nil {
		return RunRequest{}, err
	}
	intervals, err := normalizeStringSet("intervals", req.Intervals)
	if err != nil {
		return RunRequest{}, err
	}
	if req.Start.IsZero() {
		return RunRequest{}, fmt.Errorf("start is required")
	}
	if req.End.IsZero() {
		return RunRequest{}, fmt.Errorf("end is required")
	}
	if !req.End.After(req.Start) {
		return RunRequest{}, fmt.Errorf("end must be after start")
	}
	if req.CandleLimit < 0 || req.TradeLimit < 0 || req.SnapshotLimit < 0 {
		return RunRequest{}, fmt.Errorf("limits must be non-negative")
	}

	req.Exchange = strings.TrimSpace(req.Exchange)
	req.Category = strings.TrimSpace(req.Category)
	req.Symbols = symbols
	req.Intervals = intervals
	req.Start = req.Start.UTC()
	req.End = req.End.UTC()
	return req, nil
}

func normalizeStringSet(field string, values []string) ([]string, error) {
	if len(values) == 0 {
		return nil, fmt.Errorf("%s must not be empty", field)
	}

	normalized := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for index, value := range values {
		cleaned := strings.TrimSpace(value)
		if cleaned == "" {
			return nil, fmt.Errorf("%s[%d] must not be empty", field, index)
		}
		key := strings.ToUpper(cleaned)
		if _, exists := seen[key]; exists {
			return nil, fmt.Errorf("%s must not contain duplicates", field)
		}
		seen[key] = struct{}{}
		normalized = append(normalized, cleaned)
	}
	return normalized, nil
}
