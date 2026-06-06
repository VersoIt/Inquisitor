package gaps

import (
	"fmt"
	"sort"
	"time"

	"github.com/VersoIt/Inquisitor/internal/marketdata"
	"github.com/VersoIt/Inquisitor/internal/marketdata/validator"
)

type Gap struct {
	ExpectedOpenTime time.Time
	NextOpenTime     time.Time
	MissingCandles   int
}

func Detect(candles []marketdata.Candle, interval string) ([]Gap, error) {
	if len(candles) < 2 {
		return nil, nil
	}

	duration, err := marketdata.IntervalDuration(interval)
	if err != nil {
		return nil, err
	}
	if err := validator.ValidateCandles(candles); err != nil {
		return nil, err
	}
	if err := validateSingleSeries(candles, interval); err != nil {
		return nil, err
	}

	ordered := append([]marketdata.Candle(nil), candles...)
	sort.Slice(ordered, func(i, j int) bool {
		return ordered[i].OpenTime.Before(ordered[j].OpenTime)
	})

	var found []Gap
	for i := 1; i < len(ordered); i++ {
		expected := ordered[i-1].OpenTime.Add(duration)
		actual := ordered[i].OpenTime
		if actual.Equal(expected) {
			continue
		}
		if actual.Before(expected) {
			return nil, fmt.Errorf("candles overlap: expected %s, got %s", expected, actual)
		}

		missing := int(actual.Sub(expected) / duration)
		found = append(found, Gap{
			ExpectedOpenTime: expected,
			NextOpenTime:     actual,
			MissingCandles:   missing,
		})
	}

	return found, nil
}

func validateSingleSeries(candles []marketdata.Candle, interval string) error {
	first := candles[0]
	for i, candle := range candles {
		if candle.Exchange != first.Exchange {
			return fmt.Errorf("candles must belong to one exchange: candle[0]=%s candle[%d]=%s", first.Exchange, i, candle.Exchange)
		}
		if candle.Category != first.Category {
			return fmt.Errorf("candles must belong to one category: candle[0]=%s candle[%d]=%s", first.Category, i, candle.Category)
		}
		if candle.Symbol != first.Symbol {
			return fmt.Errorf("candles must belong to one symbol: candle[0]=%s candle[%d]=%s", first.Symbol, i, candle.Symbol)
		}
		if candle.Interval != interval {
			return fmt.Errorf("candle[%d] interval %q does not match requested interval %q", i, candle.Interval, interval)
		}
	}
	return nil
}
