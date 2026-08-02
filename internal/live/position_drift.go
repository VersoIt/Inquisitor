package live

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

const DefaultPositionDriftCurrentMaxAge = 5 * time.Second

const DefaultPositionDriftBaselineMaxAge = 10 * time.Minute

type PositionDriftComparisonRequest struct {
	Query          PositionSnapshotQuery
	Current        PositionSnapshot
	HasBaseline    bool
	Baseline       PositionSnapshot
	Now            time.Time
	CurrentMaxAge  time.Duration
	BaselineMaxAge time.Duration
}

type PositionDriftComparison struct {
	Query       PositionSnapshotQuery
	Current     PositionSnapshot
	HasBaseline bool
	Baseline    PositionSnapshot
	Checks      []ReadinessCheck
	Status      LiveOpsStatus
}

func BuildPositionDriftComparison(req PositionDriftComparisonRequest) (PositionDriftComparison, error) {
	if req.CurrentMaxAge == 0 {
		req.CurrentMaxAge = DefaultPositionDriftCurrentMaxAge
	}
	if req.BaselineMaxAge == 0 {
		req.BaselineMaxAge = DefaultPositionDriftBaselineMaxAge
	}
	if req.Now.IsZero() {
		return PositionDriftComparison{}, errors.New("position drift comparison requires now")
	}
	if req.CurrentMaxAge <= 0 {
		return PositionDriftComparison{}, errors.New("position drift comparison requires positive current max age")
	}
	if req.BaselineMaxAge <= 0 {
		return PositionDriftComparison{}, errors.New("position drift comparison requires positive baseline max age")
	}
	if err := ValidatePositionSnapshotQuery(req.Query); err != nil {
		return PositionDriftComparison{}, err
	}
	if err := EnsurePositionSnapshotMatchesQuery(req.Current, req.Query); err != nil {
		return PositionDriftComparison{}, fmt.Errorf("current snapshot: %w", err)
	}
	if req.HasBaseline {
		if err := EnsurePositionSnapshotMatchesQuery(req.Baseline, req.Query); err != nil {
			return PositionDriftComparison{}, fmt.Errorf("baseline snapshot: %w", err)
		}
	}

	comparison := PositionDriftComparison{
		Query:       req.Query,
		Current:     req.Current,
		HasBaseline: req.HasBaseline,
		Baseline:    req.Baseline,
	}
	comparison.Checks = append(comparison.Checks,
		positionDriftCurrentSnapshotCheck(req.Current, req.Now, req.CurrentMaxAge),
		positionDriftBaselineSnapshotCheck(req.HasBaseline, req.Baseline, req.Now, req.BaselineMaxAge),
		positionDriftExchangeStatusCheck(req.Current),
		positionDriftExposureCheck(req.HasBaseline, req.Baseline, req.Current),
	)
	if err := ValidateReadinessChecks(comparison.Checks); err != nil {
		return PositionDriftComparison{}, err
	}
	status, err := SummarizeLiveOpsStatus(comparison.Checks)
	if err != nil {
		return PositionDriftComparison{}, err
	}
	comparison.Status = status
	return comparison, nil
}

func ValidatePositionSnapshotFreshness(snapshot PositionSnapshot, now time.Time, maxAge time.Duration) error {
	if err := ValidatePositionSnapshot(snapshot); err != nil {
		return err
	}
	var problems []string
	if now.IsZero() {
		problems = append(problems, "now is required")
	}
	if maxAge <= 0 {
		problems = append(problems, "max_age must be positive")
	}
	if len(problems) == 0 {
		age := now.UTC().Sub(snapshot.ObservedAt.UTC())
		if age < 0 {
			problems = append(problems, "observed_at must not be in the future")
		}
		if age > maxAge {
			problems = append(problems, fmt.Sprintf("snapshot is stale: age=%s max=%s", age, maxAge))
		}
	}
	if len(problems) > 0 {
		return errors.New("live position snapshot freshness validation failed: " + strings.Join(problems, "; "))
	}
	return nil
}

func EnsurePositionSnapshotMatchesQuery(snapshot PositionSnapshot, query PositionSnapshotQuery) error {
	if err := ValidatePositionSnapshot(snapshot); err != nil {
		return err
	}
	if err := ValidatePositionSnapshotQuery(query); err != nil {
		return err
	}
	var problems []string
	if snapshot.Exchange != query.Exchange {
		problems = append(problems, fmt.Sprintf("exchange %q does not match query exchange %q", snapshot.Exchange, query.Exchange))
	}
	if snapshot.Category != query.Category {
		problems = append(problems, fmt.Sprintf("category %q does not match query category %q", snapshot.Category, query.Category))
	}
	if snapshot.Symbol != query.Symbol {
		problems = append(problems, fmt.Sprintf("symbol %q does not match query symbol %q", snapshot.Symbol, query.Symbol))
	}
	if len(problems) > 0 {
		return errors.New("live position snapshot query mismatch: " + strings.Join(problems, "; "))
	}
	return nil
}

func PositionExposureDriftProblems(baseline PositionSnapshot, current PositionSnapshot) []string {
	var problems []string
	if baseline.Exchange != current.Exchange {
		problems = append(problems, fmt.Sprintf("exchange drift: db=%s current=%s", baseline.Exchange, current.Exchange))
	}
	if baseline.Category != current.Category {
		problems = append(problems, fmt.Sprintf("category drift: db=%s current=%s", baseline.Category, current.Category))
	}
	if baseline.Symbol != current.Symbol {
		problems = append(problems, fmt.Sprintf("symbol drift: db=%s current=%s", baseline.Symbol, current.Symbol))
	}
	if baseline.Open != current.Open {
		problems = append(problems, fmt.Sprintf("open drift: db=%t current=%t", baseline.Open, current.Open))
	}
	if baseline.PositionIndex != current.PositionIndex {
		problems = append(problems, fmt.Sprintf("position_index drift: db=%d current=%d", baseline.PositionIndex, current.PositionIndex))
	}
	if baseline.ExchangeReduceOnly != current.ExchangeReduceOnly {
		problems = append(problems, fmt.Sprintf("reduce_only drift: db=%t current=%t", baseline.ExchangeReduceOnly, current.ExchangeReduceOnly))
	}
	if !baseline.Size.Equal(current.Size) {
		problems = append(problems, fmt.Sprintf("size drift: db=%s current=%s", baseline.Size, current.Size))
	}
	if baseline.Open || current.Open {
		if baseline.Side != current.Side {
			problems = append(problems, fmt.Sprintf("side drift: db=%s current=%s", baseline.Side, current.Side))
		}
		if !baseline.AveragePrice.Equal(current.AveragePrice) {
			problems = append(problems, fmt.Sprintf("average_price drift: db=%s current=%s", baseline.AveragePrice, current.AveragePrice))
		}
		if !baseline.Leverage.Equal(current.Leverage) {
			problems = append(problems, fmt.Sprintf("leverage drift: db=%s current=%s", baseline.Leverage, current.Leverage))
		}
		if baseline.ExchangeStatus != current.ExchangeStatus {
			problems = append(problems, fmt.Sprintf("exchange_status drift: db=%s current=%s", baseline.ExchangeStatus, current.ExchangeStatus))
		}
		if !sameOptionalTime(baseline.ExchangeCreatedAt, current.ExchangeCreatedAt) {
			problems = append(problems, fmt.Sprintf("exchange_created_at drift: db=%s current=%s", baseline.ExchangeCreatedAt.UTC().Format(time.RFC3339Nano), current.ExchangeCreatedAt.UTC().Format(time.RFC3339Nano)))
		}
	}
	return problems
}

func positionDriftCurrentSnapshotCheck(snapshot PositionSnapshot, now time.Time, maxAge time.Duration) ReadinessCheck {
	if err := ValidatePositionSnapshotFreshness(snapshot, now, maxAge); err != nil {
		return NewReadinessCheck(
			"current_position_snapshot",
			ReadinessCheckStatusFail,
			fmt.Sprintf("%s: %v", snapshot.Symbol, err),
		)
	}
	return NewReadinessCheck(
		"current_position_snapshot",
		ReadinessCheckStatusPass,
		fmt.Sprintf("%s current exchange position snapshot is fresh", snapshot.Symbol),
	)
}

func positionDriftBaselineSnapshotCheck(hasBaseline bool, snapshot PositionSnapshot, now time.Time, maxAge time.Duration) ReadinessCheck {
	if !hasBaseline {
		return NewReadinessCheck("db_position_baseline", ReadinessCheckStatusWarn, "no persisted DB position baseline is available")
	}
	age := now.UTC().Sub(snapshot.ObservedAt.UTC())
	if age < 0 {
		return NewReadinessCheck(
			"db_position_baseline",
			ReadinessCheckStatusFail,
			fmt.Sprintf("%s DB position baseline observed_at is in the future", snapshot.Symbol),
		)
	}
	if age > maxAge {
		return NewReadinessCheck(
			"db_position_baseline",
			ReadinessCheckStatusWarn,
			fmt.Sprintf("%s DB position baseline is stale: age=%s max=%s", snapshot.Symbol, age, maxAge),
		)
	}
	return NewReadinessCheck(
		"db_position_baseline",
		ReadinessCheckStatusPass,
		fmt.Sprintf("%s DB position baseline is fresh", snapshot.Symbol),
	)
}

func positionDriftExchangeStatusCheck(snapshot PositionSnapshot) ReadinessCheck {
	if snapshot.ExchangeStatus == "" || snapshot.ExchangeStatus == ExchangePositionStatusNormal {
		return NewReadinessCheck(
			"position_exchange_status",
			ReadinessCheckStatusPass,
			fmt.Sprintf("%s exchange position status is normal", snapshot.Symbol),
		)
	}
	return NewReadinessCheck(
		"position_exchange_status",
		ReadinessCheckStatusFail,
		fmt.Sprintf("%s exchange position status is %s", snapshot.Symbol, snapshot.ExchangeStatus),
	)
}

func positionDriftExposureCheck(hasBaseline bool, baseline PositionSnapshot, current PositionSnapshot) ReadinessCheck {
	if !hasBaseline {
		return NewReadinessCheck("position_exposure_drift", ReadinessCheckStatusWarn, "cannot compare exposure drift without DB baseline")
	}
	problems := PositionExposureDriftProblems(baseline, current)
	if len(problems) > 0 {
		return NewReadinessCheck(
			"position_exposure_drift",
			ReadinessCheckStatusFail,
			fmt.Sprintf("%s exposure drift detected: %s", current.Symbol, strings.Join(problems, "; ")),
		)
	}
	return NewReadinessCheck(
		"position_exposure_drift",
		ReadinessCheckStatusPass,
		fmt.Sprintf("%s current exchange exposure matches DB baseline", current.Symbol),
	)
}

func sameOptionalTime(left time.Time, right time.Time) bool {
	if left.IsZero() || right.IsZero() {
		return left.IsZero() && right.IsZero()
	}
	return left.UTC().Equal(right.UTC())
}
