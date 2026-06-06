package marketdata

import (
	"fmt"
	"strings"
	"time"
)

func IntervalDuration(interval string) (time.Duration, error) {
	switch strings.ToLower(strings.TrimSpace(interval)) {
	case "1", "1m":
		return time.Minute, nil
	case "3", "3m":
		return 3 * time.Minute, nil
	case "5", "5m":
		return 5 * time.Minute, nil
	case "15", "15m":
		return 15 * time.Minute, nil
	case "30", "30m":
		return 30 * time.Minute, nil
	case "60", "1h":
		return time.Hour, nil
	case "120", "2h":
		return 2 * time.Hour, nil
	case "240", "4h":
		return 4 * time.Hour, nil
	case "360", "6h":
		return 6 * time.Hour, nil
	case "720", "12h":
		return 12 * time.Hour, nil
	case "d", "1d":
		return 24 * time.Hour, nil
	default:
		return 0, fmt.Errorf("unsupported candle interval %q", interval)
	}
}
