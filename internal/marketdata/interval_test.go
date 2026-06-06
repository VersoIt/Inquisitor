package marketdata_test

import (
	"testing"
	"time"

	"github.com/VersoIt/Inquisitor/internal/marketdata"
)

func TestIntervalDurationTableDriven(t *testing.T) {
	tests := []struct {
		name     string
		interval string
		want     time.Duration
		wantErr  bool
	}{
		{name: "one minute numeric", interval: "1", want: time.Minute},
		{name: "one minute suffix", interval: "1m", want: time.Minute},
		{name: "five minutes", interval: "5", want: 5 * time.Minute},
		{name: "one hour numeric", interval: "60", want: time.Hour},
		{name: "one hour suffix", interval: "1h", want: time.Hour},
		{name: "one day", interval: "D", want: 24 * time.Hour},
		{name: "trims whitespace", interval: " 15 ", want: 15 * time.Minute},
		{name: "rejects unsupported interval", interval: "7", wantErr: true},
		{name: "rejects empty interval", interval: "", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := marketdata.IntervalDuration(tt.interval)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("expected valid interval, got %v", err)
			}
			if got != tt.want {
				t.Fatalf("expected %s, got %s", tt.want, got)
			}
		})
	}
}
