package live_test

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	applive "github.com/VersoIt/Inquisitor/internal/app/live"
	"github.com/VersoIt/Inquisitor/internal/clock"
	domainlive "github.com/VersoIt/Inquisitor/internal/live"
	domainrisk "github.com/VersoIt/Inquisitor/internal/risk"
)

func TestServicePreflightLiveStartupApprovesExplicitSafeStartup(t *testing.T) {
	service := liveStartupService(&fakeLiveKillSwitchRepository{}, mapEnvironment{
		"TRADING_LIVE_CONFIRM": "true",
		"BYBIT_API_KEY":        "key-value-must-not-be-returned",
		"BYBIT_API_SECRET":     "secret-value-must-not-be-returned",
	})

	got, err := service.PreflightLiveStartup(context.Background(), validLiveStartupRequest())
	if err != nil {
		t.Fatalf("preflight live startup: %v", err)
	}

	if !got.Ready || len(got.Problems) != 0 {
		t.Fatalf("expected ready startup, got %#v", got)
	}
	if !got.TradingEnabled || got.TradingMode != "live" || !got.AllowLive ||
		!got.ConfirmationAccepted || !got.APIKeyPresent || !got.APISecretPresent ||
		!got.SubaccountConfirmed || !got.WithdrawalPermissionDenied || got.KillSwitchActive {
		t.Fatalf("startup safety flags mismatch: %#v", got)
	}
	if got.ConfirmationEnv != "TRADING_LIVE_CONFIRM" || got.APIKeyEnv != "BYBIT_API_KEY" ||
		got.APISecretEnv != "BYBIT_API_SECRET" {
		t.Fatalf("env names mismatch: %#v", got)
	}
	if !got.InitialLiveCapitalUSDT.Equal(decimal.RequireFromString("50")) ||
		!got.MaxInitialLiveCapitalUSDT.Equal(decimal.RequireFromString("100")) {
		t.Fatalf("capital limits mismatch: %#v", got)
	}
	formattedResult := fmt.Sprintf("%#v", got)
	if strings.Contains(formattedResult, "key-value") ||
		strings.Contains(formattedResult, "secret-value") {
		t.Fatalf("preflight result must not expose secret values: %#v", got)
	}
	if got.KillSwitchReason != "" || got.KillSwitchSource != "" {
		t.Fatalf("inactive kill switch metadata mismatch: %#v", got)
	}
}

func TestServicePreflightLiveStartupRecordsFreshFlatPosition(t *testing.T) {
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	query := validLiveStartupPositionQuery()
	reader := &fakeLivePositionSnapshotReader{
		snapshot: validLiveStartupFlatPositionSnapshot(t, query, now.Add(-time.Second)),
	}
	journal := &fakeLivePositionSnapshotJournal{stats: domainlive.PositionSnapshotStats{Inserted: 1}}
	service := liveStartupPositionService(now, reader, journal, &fakeLiveKillSwitchRepository{}, validLiveStartupEnvironment())

	got, err := service.PreflightLiveStartup(context.Background(), mutateLiveStartupRequest(func(req *applive.PreflightLiveStartupRequest) {
		req.ExpectedFlatPositions = []domainlive.PositionSnapshotQuery{query}
		req.MaxPositionSnapshotAge = 5 * time.Second
	}))
	if err != nil {
		t.Fatalf("preflight live startup with flat position: %v", err)
	}

	if !got.Ready || len(got.Problems) != 0 {
		t.Fatalf("expected ready startup, got %#v", got)
	}
	if reader.calls != 1 || reader.query != query {
		t.Fatalf("position reader mismatch: calls=%d query=%#v", reader.calls, reader.query)
	}
	if journal.calls != 1 || got.PositionSnapshotStats.Inserted != 1 || got.PositionSnapshotStats.Skipped != 0 {
		t.Fatalf("position journal/stats mismatch: calls=%d result=%#v", journal.calls, got.PositionSnapshotStats)
	}
	if len(got.PositionSnapshots) != 1 || got.PositionSnapshots[0].Open {
		t.Fatalf("flat snapshot result mismatch: %#v", got.PositionSnapshots)
	}
	if len(got.ExpectedFlatPositions) != 1 || got.ExpectedFlatPositions[0] != query ||
		got.MaxPositionSnapshotAge != 5*time.Second {
		t.Fatalf("position preflight metadata mismatch: %#v", got)
	}
}

func TestServicePreflightLiveStartupBlocksUnexpectedOpenPosition(t *testing.T) {
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	query := validLiveStartupPositionQuery()
	reader := &fakeLivePositionSnapshotReader{
		snapshot: validLiveStartupOpenPositionSnapshot(t, query, now.Add(-time.Second)),
	}
	journal := &fakeLivePositionSnapshotJournal{stats: domainlive.PositionSnapshotStats{Inserted: 1}}
	service := liveStartupPositionService(now, reader, journal, &fakeLiveKillSwitchRepository{}, validLiveStartupEnvironment())

	got, err := service.PreflightLiveStartup(context.Background(), mutateLiveStartupRequest(func(req *applive.PreflightLiveStartupRequest) {
		req.ExpectedFlatPositions = []domainlive.PositionSnapshotQuery{query}
		req.MaxPositionSnapshotAge = 5 * time.Second
	}))
	if err == nil || !strings.Contains(err.Error(), "must be flat") {
		t.Fatalf("expected flat-position error, got %v", err)
	}

	if got.Ready || len(got.PositionSnapshots) != 1 || !got.PositionSnapshots[0].Open {
		t.Fatalf("unexpected open position result mismatch: %#v", got)
	}
	if reader.calls != 1 || journal.calls != 1 {
		t.Fatalf("open position should still be read and recorded: reader=%d journal=%d", reader.calls, journal.calls)
	}
}

func TestServicePreflightLiveStartupChecksAccountReadiness(t *testing.T) {
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	query := validLiveStartupAccountQuery()
	reader := &fakeLiveAccountSnapshotReader{
		snapshot: validLiveStartupAccountSnapshot(t, query, now.Add(-time.Second)),
	}
	service := liveStartupAccountService(now, reader, &fakeLiveKillSwitchRepository{}, validLiveStartupEnvironment())

	got, err := service.PreflightLiveStartup(context.Background(), mutateLiveStartupRequest(func(req *applive.PreflightLiveStartupRequest) {
		req.ExpectedAccount = query
		req.AccountBaseCurrency = " usdt "
		req.MaxAccountSnapshotAge = 5 * time.Second
	}))
	if err != nil {
		t.Fatalf("preflight live startup with account readiness: %v", err)
	}

	if !got.Ready || len(got.Problems) != 0 {
		t.Fatalf("expected ready startup, got %#v", got)
	}
	if reader.calls != 1 || reader.query != query {
		t.Fatalf("account reader mismatch: calls=%d query=%#v", reader.calls, reader.query)
	}
	if got.ExpectedAccount != query || got.AccountBaseCurrency != "USDT" || got.MaxAccountSnapshotAge != 5*time.Second {
		t.Fatalf("account preflight metadata mismatch: %#v", got)
	}
	if got.AccountSnapshot.AccountType != domainlive.AccountTypeUnified ||
		!got.AccountSnapshot.TotalEquity.Equal(decimal.RequireFromString("50")) {
		t.Fatalf("account snapshot mismatch: %#v", got.AccountSnapshot)
	}
}

func TestServicePreflightLiveStartupRejectsUnsafeAccountReadinessTableDriven(t *testing.T) {
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	query := validLiveStartupAccountQuery()
	readerErr := errors.New("bybit unavailable")

	tests := []struct {
		name            string
		reader          *fakeLiveAccountSnapshotReader
		withReader      bool
		withClock       bool
		req             applive.PreflightLiveStartupRequest
		wantErrSub      string
		wantReaderCalls int
	}{
		{
			name:      "missing account reader",
			withClock: true,
			req: mutateLiveStartupRequest(func(req *applive.PreflightLiveStartupRequest) {
				req.ExpectedAccount = query
				req.AccountBaseCurrency = "USDT"
				req.MaxAccountSnapshotAge = 5 * time.Second
			}),
			wantErrSub: "account snapshot reader",
		},
		{
			name:       "missing clock",
			reader:     &fakeLiveAccountSnapshotReader{snapshot: validLiveStartupAccountSnapshot(t, query, now)},
			withReader: true,
			req: mutateLiveStartupRequest(func(req *applive.PreflightLiveStartupRequest) {
				req.ExpectedAccount = query
				req.AccountBaseCurrency = "USDT"
				req.MaxAccountSnapshotAge = 5 * time.Second
			}),
			wantErrSub: "clock",
		},
		{
			name:       "invalid account query",
			reader:     &fakeLiveAccountSnapshotReader{snapshot: validLiveStartupAccountSnapshot(t, query, now)},
			withReader: true,
			withClock:  true,
			req: mutateLiveStartupRequest(func(req *applive.PreflightLiveStartupRequest) {
				req.ExpectedAccount = domainlive.AccountSnapshotQuery{Exchange: "BYBIT", AccountType: domainlive.AccountTypeUnified}
				req.AccountBaseCurrency = "USDT"
				req.MaxAccountSnapshotAge = 5 * time.Second
			}),
			wantErrSub: "exchange",
		},
		{
			name:       "missing base currency",
			reader:     &fakeLiveAccountSnapshotReader{snapshot: validLiveStartupAccountSnapshot(t, query, now)},
			withReader: true,
			withClock:  true,
			req: mutateLiveStartupRequest(func(req *applive.PreflightLiveStartupRequest) {
				req.ExpectedAccount = query
				req.MaxAccountSnapshotAge = 5 * time.Second
			}),
			wantErrSub: "base currency",
		},
		{
			name:       "non positive max age",
			reader:     &fakeLiveAccountSnapshotReader{snapshot: validLiveStartupAccountSnapshot(t, query, now)},
			withReader: true,
			withClock:  true,
			req: mutateLiveStartupRequest(func(req *applive.PreflightLiveStartupRequest) {
				req.ExpectedAccount = query
				req.AccountBaseCurrency = "USDT"
			}),
			wantErrSub: "max account snapshot age",
		},
		{
			name:       "reader error",
			reader:     &fakeLiveAccountSnapshotReader{err: readerErr},
			withReader: true,
			withClock:  true,
			req: mutateLiveStartupRequest(func(req *applive.PreflightLiveStartupRequest) {
				req.ExpectedAccount = query
				req.AccountBaseCurrency = "USDT"
				req.MaxAccountSnapshotAge = 5 * time.Second
			}),
			wantErrSub:      readerErr.Error(),
			wantReaderCalls: 1,
		},
		{
			name:       "stale account snapshot",
			reader:     &fakeLiveAccountSnapshotReader{snapshot: validLiveStartupAccountSnapshot(t, query, now.Add(-10*time.Second))},
			withReader: true,
			withClock:  true,
			req: mutateLiveStartupRequest(func(req *applive.PreflightLiveStartupRequest) {
				req.ExpectedAccount = query
				req.AccountBaseCurrency = "USDT"
				req.MaxAccountSnapshotAge = time.Second
			}),
			wantErrSub:      "stale",
			wantReaderCalls: 1,
		},
		{
			name:       "future account snapshot",
			reader:     &fakeLiveAccountSnapshotReader{snapshot: validLiveStartupAccountSnapshot(t, query, now.Add(time.Second))},
			withReader: true,
			withClock:  true,
			req: mutateLiveStartupRequest(func(req *applive.PreflightLiveStartupRequest) {
				req.ExpectedAccount = query
				req.AccountBaseCurrency = "USDT"
				req.MaxAccountSnapshotAge = 5 * time.Second
			}),
			wantErrSub:      "future",
			wantReaderCalls: 1,
		},
		{
			name: "over max equity",
			reader: &fakeLiveAccountSnapshotReader{snapshot: mutateLiveStartupAccountSnapshot(validLiveStartupAccountSnapshot(t, query, now), func(s *domainlive.AccountSnapshot) {
				s.TotalEquity = decimal.RequireFromString("101")
			})},
			withReader: true,
			withClock:  true,
			req: mutateLiveStartupRequest(func(req *applive.PreflightLiveStartupRequest) {
				req.ExpectedAccount = query
				req.AccountBaseCurrency = "USDT"
				req.MaxAccountSnapshotAge = 5 * time.Second
			}),
			wantErrSub:      "exceeds",
			wantReaderCalls: 1,
		},
		{
			name: "zero total equity",
			reader: &fakeLiveAccountSnapshotReader{snapshot: mutateLiveStartupAccountSnapshot(validLiveStartupAccountSnapshot(t, query, now), func(s *domainlive.AccountSnapshot) {
				s.TotalEquity = decimal.Zero
			})},
			withReader: true,
			withClock:  true,
			req: mutateLiveStartupRequest(func(req *applive.PreflightLiveStartupRequest) {
				req.ExpectedAccount = query
				req.AccountBaseCurrency = "USDT"
				req.MaxAccountSnapshotAge = 5 * time.Second
			}),
			wantErrSub:      "total equity",
			wantReaderCalls: 1,
		},
		{
			name: "initial margin",
			reader: &fakeLiveAccountSnapshotReader{snapshot: mutateLiveStartupAccountSnapshot(validLiveStartupAccountSnapshot(t, query, now), func(s *domainlive.AccountSnapshot) {
				s.TotalInitialMargin = decimal.RequireFromString("1")
			})},
			withReader: true,
			withClock:  true,
			req: mutateLiveStartupRequest(func(req *applive.PreflightLiveStartupRequest) {
				req.ExpectedAccount = query
				req.AccountBaseCurrency = "USDT"
				req.MaxAccountSnapshotAge = 5 * time.Second
			}),
			wantErrSub:      "initial margin",
			wantReaderCalls: 1,
		},
		{
			name: "perp upl",
			reader: &fakeLiveAccountSnapshotReader{snapshot: mutateLiveStartupAccountSnapshot(validLiveStartupAccountSnapshot(t, query, now), func(s *domainlive.AccountSnapshot) {
				s.TotalPerpUPL = decimal.RequireFromString("-0.01")
			})},
			withReader: true,
			withClock:  true,
			req: mutateLiveStartupRequest(func(req *applive.PreflightLiveStartupRequest) {
				req.ExpectedAccount = query
				req.AccountBaseCurrency = "USDT"
				req.MaxAccountSnapshotAge = 5 * time.Second
			}),
			wantErrSub:      "perp UPL",
			wantReaderCalls: 1,
		},
		{
			name: "non base asset",
			reader: &fakeLiveAccountSnapshotReader{snapshot: mutateLiveStartupAccountSnapshot(validLiveStartupAccountSnapshot(t, query, now), func(s *domainlive.AccountSnapshot) {
				s.Coins = append(s.Coins, domainlive.AccountCoinSnapshot{
					Coin:          "BTC",
					Equity:        decimal.RequireFromString("0.001"),
					USDValue:      decimal.RequireFromString("100"),
					WalletBalance: decimal.RequireFromString("0.001"),
				})
			})},
			withReader: true,
			withClock:  true,
			req: mutateLiveStartupRequest(func(req *applive.PreflightLiveStartupRequest) {
				req.ExpectedAccount = query
				req.AccountBaseCurrency = "USDT"
				req.MaxAccountSnapshotAge = 5 * time.Second
			}),
			wantErrSub:      "non-base asset",
			wantReaderCalls: 1,
		},
		{
			name: "locked balance",
			reader: &fakeLiveAccountSnapshotReader{snapshot: mutateLiveStartupAccountSnapshot(validLiveStartupAccountSnapshot(t, query, now), func(s *domainlive.AccountSnapshot) {
				s.Coins[0].Locked = decimal.RequireFromString("1")
			})},
			withReader: true,
			withClock:  true,
			req: mutateLiveStartupRequest(func(req *applive.PreflightLiveStartupRequest) {
				req.ExpectedAccount = query
				req.AccountBaseCurrency = "USDT"
				req.MaxAccountSnapshotAge = 5 * time.Second
			}),
			wantErrSub:      "locked",
			wantReaderCalls: 1,
		},
		{
			name: "borrow amount",
			reader: &fakeLiveAccountSnapshotReader{snapshot: mutateLiveStartupAccountSnapshot(validLiveStartupAccountSnapshot(t, query, now), func(s *domainlive.AccountSnapshot) {
				s.Coins[0].BorrowAmount = decimal.RequireFromString("1")
			})},
			withReader: true,
			withClock:  true,
			req: mutateLiveStartupRequest(func(req *applive.PreflightLiveStartupRequest) {
				req.ExpectedAccount = query
				req.AccountBaseCurrency = "USDT"
				req.MaxAccountSnapshotAge = 5 * time.Second
			}),
			wantErrSub:      "borrow",
			wantReaderCalls: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			options := []applive.Option{
				applive.WithKillSwitchRepository(&fakeLiveKillSwitchRepository{}),
				applive.WithEnvironmentReader(validLiveStartupEnvironment()),
			}
			if tt.withReader {
				options = append(options, applive.WithAccountSnapshotReader(tt.reader))
			}
			if tt.withClock {
				options = append(options, applive.WithClock(clock.FixedClock{Time: now}))
			} else {
				options = append(options, applive.WithClock(nil))
			}
			service := applive.NewService(options...)

			got, err := service.PreflightLiveStartup(context.Background(), tt.req)
			if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
			if got.Ready {
				t.Fatalf("unsafe account startup must not be ready: %#v", got)
			}
			if tt.reader != nil && tt.reader.calls != tt.wantReaderCalls {
				t.Fatalf("account reader calls mismatch: got %d want %d", tt.reader.calls, tt.wantReaderCalls)
			}
		})
	}
}

func TestServicePreflightLiveStartupBlocksActiveKillSwitch(t *testing.T) {
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	killSwitch := &fakeLiveKillSwitchRepository{state: domainrisk.KillSwitchState{
		Active:    true,
		Reason:    "operator emergency stop",
		Source:    "operator",
		UpdatedAt: now,
	}}
	service := liveStartupService(killSwitch, validLiveStartupEnvironment())

	got, err := service.PreflightLiveStartup(context.Background(), validLiveStartupRequest())
	if err == nil || !strings.Contains(err.Error(), "kill switch") {
		t.Fatalf("expected kill switch error, got %v", err)
	}
	if got.Ready || !got.KillSwitchActive || got.KillSwitchReason != "operator emergency stop" ||
		got.KillSwitchSource != "operator" || killSwitch.currentCalls != 1 {
		t.Fatalf("active kill switch result mismatch: got=%#v calls=%d", got, killSwitch.currentCalls)
	}
}

func TestServicePreflightLiveStartupRejectsUnsafePositionChecksTableDriven(t *testing.T) {
	now := time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC)
	query := validLiveStartupPositionQuery()
	readerErr := errors.New("bybit unavailable")
	journalErr := errors.New("postgres unavailable")

	tests := []struct {
		name             string
		reader           *fakeLivePositionSnapshotReader
		journal          *fakeLivePositionSnapshotJournal
		withReader       bool
		withJournal      bool
		withClock        bool
		req              applive.PreflightLiveStartupRequest
		wantErrSub       string
		wantReaderCalls  int
		wantJournalCalls int
	}{
		{
			name:        "missing position reader",
			journal:     &fakeLivePositionSnapshotJournal{stats: domainlive.PositionSnapshotStats{Inserted: 1}},
			withJournal: true,
			withClock:   true,
			req: mutateLiveStartupRequest(func(req *applive.PreflightLiveStartupRequest) {
				req.ExpectedFlatPositions = []domainlive.PositionSnapshotQuery{query}
				req.MaxPositionSnapshotAge = 5 * time.Second
			}),
			wantErrSub: "position snapshot reader",
		},
		{
			name:       "missing position journal",
			reader:     &fakeLivePositionSnapshotReader{snapshot: validLiveStartupFlatPositionSnapshot(t, query, now)},
			withReader: true,
			withClock:  true,
			req: mutateLiveStartupRequest(func(req *applive.PreflightLiveStartupRequest) {
				req.ExpectedFlatPositions = []domainlive.PositionSnapshotQuery{query}
				req.MaxPositionSnapshotAge = 5 * time.Second
			}),
			wantErrSub: "position snapshot journal",
		},
		{
			name:        "missing clock",
			reader:      &fakeLivePositionSnapshotReader{snapshot: validLiveStartupFlatPositionSnapshot(t, query, now)},
			journal:     &fakeLivePositionSnapshotJournal{stats: domainlive.PositionSnapshotStats{Inserted: 1}},
			withReader:  true,
			withJournal: true,
			req: mutateLiveStartupRequest(func(req *applive.PreflightLiveStartupRequest) {
				req.ExpectedFlatPositions = []domainlive.PositionSnapshotQuery{query}
				req.MaxPositionSnapshotAge = 5 * time.Second
			}),
			wantErrSub: "clock",
		},
		{
			name:        "invalid position query",
			reader:      &fakeLivePositionSnapshotReader{snapshot: validLiveStartupFlatPositionSnapshot(t, query, now)},
			journal:     &fakeLivePositionSnapshotJournal{stats: domainlive.PositionSnapshotStats{Inserted: 1}},
			withReader:  true,
			withJournal: true,
			withClock:   true,
			req: mutateLiveStartupRequest(func(req *applive.PreflightLiveStartupRequest) {
				req.ExpectedFlatPositions = []domainlive.PositionSnapshotQuery{{
					Exchange: "BYBIT",
					Category: "linear",
					Symbol:   "BTCUSDT",
				}}
				req.MaxPositionSnapshotAge = 5 * time.Second
			}),
			wantErrSub: "exchange",
		},
		{
			name:        "stale position snapshot",
			reader:      &fakeLivePositionSnapshotReader{snapshot: validLiveStartupFlatPositionSnapshot(t, query, now.Add(-10*time.Second))},
			journal:     &fakeLivePositionSnapshotJournal{stats: domainlive.PositionSnapshotStats{Inserted: 1}},
			withReader:  true,
			withJournal: true,
			withClock:   true,
			req: mutateLiveStartupRequest(func(req *applive.PreflightLiveStartupRequest) {
				req.ExpectedFlatPositions = []domainlive.PositionSnapshotQuery{query}
				req.MaxPositionSnapshotAge = time.Second
			}),
			wantErrSub:       "stale",
			wantReaderCalls:  1,
			wantJournalCalls: 1,
		},
		{
			name:        "future observed position snapshot",
			reader:      &fakeLivePositionSnapshotReader{snapshot: validLiveStartupFlatPositionSnapshot(t, query, now.Add(time.Second))},
			journal:     &fakeLivePositionSnapshotJournal{stats: domainlive.PositionSnapshotStats{Inserted: 1}},
			withReader:  true,
			withJournal: true,
			withClock:   true,
			req: mutateLiveStartupRequest(func(req *applive.PreflightLiveStartupRequest) {
				req.ExpectedFlatPositions = []domainlive.PositionSnapshotQuery{query}
				req.MaxPositionSnapshotAge = 5 * time.Second
			}),
			wantErrSub:       "future",
			wantReaderCalls:  1,
			wantJournalCalls: 1,
		},
		{
			name:        "non positive max age",
			reader:      &fakeLivePositionSnapshotReader{snapshot: validLiveStartupFlatPositionSnapshot(t, query, now)},
			journal:     &fakeLivePositionSnapshotJournal{stats: domainlive.PositionSnapshotStats{Inserted: 1}},
			withReader:  true,
			withJournal: true,
			withClock:   true,
			req: mutateLiveStartupRequest(func(req *applive.PreflightLiveStartupRequest) {
				req.ExpectedFlatPositions = []domainlive.PositionSnapshotQuery{query}
			}),
			wantErrSub: "max position snapshot age",
		},
		{
			name:        "reader error",
			reader:      &fakeLivePositionSnapshotReader{err: readerErr},
			journal:     &fakeLivePositionSnapshotJournal{stats: domainlive.PositionSnapshotStats{Inserted: 1}},
			withReader:  true,
			withJournal: true,
			withClock:   true,
			req: mutateLiveStartupRequest(func(req *applive.PreflightLiveStartupRequest) {
				req.ExpectedFlatPositions = []domainlive.PositionSnapshotQuery{query}
				req.MaxPositionSnapshotAge = 5 * time.Second
			}),
			wantErrSub:      readerErr.Error(),
			wantReaderCalls: 1,
		},
		{
			name:        "journal error",
			reader:      &fakeLivePositionSnapshotReader{snapshot: validLiveStartupFlatPositionSnapshot(t, query, now)},
			journal:     &fakeLivePositionSnapshotJournal{err: journalErr},
			withReader:  true,
			withJournal: true,
			withClock:   true,
			req: mutateLiveStartupRequest(func(req *applive.PreflightLiveStartupRequest) {
				req.ExpectedFlatPositions = []domainlive.PositionSnapshotQuery{query}
				req.MaxPositionSnapshotAge = 5 * time.Second
			}),
			wantErrSub:       journalErr.Error(),
			wantReaderCalls:  1,
			wantJournalCalls: 1,
		},
		{
			name:        "journal zero stats",
			reader:      &fakeLivePositionSnapshotReader{snapshot: validLiveStartupFlatPositionSnapshot(t, query, now)},
			journal:     &fakeLivePositionSnapshotJournal{},
			withReader:  true,
			withJournal: true,
			withClock:   true,
			req: mutateLiveStartupRequest(func(req *applive.PreflightLiveStartupRequest) {
				req.ExpectedFlatPositions = []domainlive.PositionSnapshotQuery{query}
				req.MaxPositionSnapshotAge = 5 * time.Second
			}),
			wantErrSub:       "did not record",
			wantReaderCalls:  1,
			wantJournalCalls: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			options := []applive.Option{
				applive.WithKillSwitchRepository(&fakeLiveKillSwitchRepository{}),
				applive.WithEnvironmentReader(validLiveStartupEnvironment()),
			}
			if tt.withReader {
				options = append(options, applive.WithPositionSnapshotReader(tt.reader))
			}
			if tt.withJournal {
				options = append(options, applive.WithPositionSnapshotJournal(tt.journal))
			}
			if tt.withClock {
				options = append(options, applive.WithClock(clock.FixedClock{Time: now}))
			} else {
				options = append(options, applive.WithClock(nil))
			}
			service := applive.NewService(options...)

			got, err := service.PreflightLiveStartup(context.Background(), tt.req)
			if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
			if got.Ready {
				t.Fatalf("unsafe position startup must not be ready: %#v", got)
			}
			if tt.reader != nil && tt.reader.calls != tt.wantReaderCalls {
				t.Fatalf("position reader calls mismatch: got %d want %d", tt.reader.calls, tt.wantReaderCalls)
			}
			if tt.journal != nil && tt.journal.calls != tt.wantJournalCalls {
				t.Fatalf("position journal calls mismatch: got %d want %d", tt.journal.calls, tt.wantJournalCalls)
			}
		})
	}
}

func TestServicePreflightLiveStartupRejectsUnsafeInputsTableDriven(t *testing.T) {
	repositoryErr := errors.New("postgres unavailable")
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()

	tests := []struct {
		name          string
		ctx           context.Context
		service       *applive.Service
		killSwitch    *fakeLiveKillSwitchRepository
		env           applive.EnvironmentReader
		req           applive.PreflightLiveStartupRequest
		wantErrSub    string
		wantKillCalls int
	}{
		{
			name:       "cancelled context",
			ctx:        cancelled,
			killSwitch: &fakeLiveKillSwitchRepository{},
			env:        validLiveStartupEnvironment(),
			req:        validLiveStartupRequest(),
			wantErrSub: "canceled",
		},
		{
			name:       "missing kill switch repository",
			ctx:        context.Background(),
			service:    applive.NewService(applive.WithEnvironmentReader(validLiveStartupEnvironment())),
			req:        validLiveStartupRequest(),
			wantErrSub: "kill switch",
		},
		{
			name: "missing environment reader",
			ctx:  context.Background(),
			service: applive.NewService(
				applive.WithKillSwitchRepository(&fakeLiveKillSwitchRepository{}),
				applive.WithEnvironmentReader(nil),
			),
			req:        validLiveStartupRequest(),
			wantErrSub: "environment reader",
		},
		{
			name:          "trading disabled",
			ctx:           context.Background(),
			killSwitch:    &fakeLiveKillSwitchRepository{},
			env:           validLiveStartupEnvironment(),
			req:           mutateLiveStartupRequest(func(req *applive.PreflightLiveStartupRequest) { req.TradingEnabled = false }),
			wantErrSub:    "trading.enabled",
			wantKillCalls: 1,
		},
		{
			name:          "paper mode",
			ctx:           context.Background(),
			killSwitch:    &fakeLiveKillSwitchRepository{},
			env:           validLiveStartupEnvironment(),
			req:           mutateLiveStartupRequest(func(req *applive.PreflightLiveStartupRequest) { req.TradingMode = "paper" }),
			wantErrSub:    "trading.mode",
			wantKillCalls: 1,
		},
		{
			name:          "live not allowed",
			ctx:           context.Background(),
			killSwitch:    &fakeLiveKillSwitchRepository{},
			env:           validLiveStartupEnvironment(),
			req:           mutateLiveStartupRequest(func(req *applive.PreflightLiveStartupRequest) { req.AllowLive = false }),
			wantErrSub:    "allow_live",
			wantKillCalls: 1,
		},
		{
			name:          "confirmation env missing",
			ctx:           context.Background(),
			killSwitch:    &fakeLiveKillSwitchRepository{},
			env:           mapEnvironment{"BYBIT_API_KEY": "key", "BYBIT_API_SECRET": "secret"},
			req:           validLiveStartupRequest(),
			wantErrSub:    "confirmation",
			wantKillCalls: 1,
		},
		{
			name:          "confirmation env false",
			ctx:           context.Background(),
			killSwitch:    &fakeLiveKillSwitchRepository{},
			env:           mapEnvironment{"TRADING_LIVE_CONFIRM": "false", "BYBIT_API_KEY": "key", "BYBIT_API_SECRET": "secret"},
			req:           validLiveStartupRequest(),
			wantErrSub:    "confirmation",
			wantKillCalls: 1,
		},
		{
			name:          "api key missing",
			ctx:           context.Background(),
			killSwitch:    &fakeLiveKillSwitchRepository{},
			env:           mapEnvironment{"TRADING_LIVE_CONFIRM": "true", "BYBIT_API_SECRET": "secret"},
			req:           validLiveStartupRequest(),
			wantErrSub:    "API key",
			wantKillCalls: 1,
		},
		{
			name:          "api secret blank",
			ctx:           context.Background(),
			killSwitch:    &fakeLiveKillSwitchRepository{},
			env:           mapEnvironment{"TRADING_LIVE_CONFIRM": "true", "BYBIT_API_KEY": "key", "BYBIT_API_SECRET": " "},
			req:           validLiveStartupRequest(),
			wantErrSub:    "API secret",
			wantKillCalls: 1,
		},
		{
			name:          "subaccount not confirmed",
			ctx:           context.Background(),
			killSwitch:    &fakeLiveKillSwitchRepository{},
			env:           validLiveStartupEnvironment(),
			req:           mutateLiveStartupRequest(func(req *applive.PreflightLiveStartupRequest) { req.SubaccountConfirmed = false }),
			wantErrSub:    "subaccount",
			wantKillCalls: 1,
		},
		{
			name:          "withdrawal permission allowed",
			ctx:           context.Background(),
			killSwitch:    &fakeLiveKillSwitchRepository{},
			env:           validLiveStartupEnvironment(),
			req:           mutateLiveStartupRequest(func(req *applive.PreflightLiveStartupRequest) { req.WithdrawalPermissionAllowed = true }),
			wantErrSub:    "withdrawal",
			wantKillCalls: 1,
		},
		{
			name:          "zero initial capital",
			ctx:           context.Background(),
			killSwitch:    &fakeLiveKillSwitchRepository{},
			env:           validLiveStartupEnvironment(),
			req:           mutateLiveStartupRequest(func(req *applive.PreflightLiveStartupRequest) { req.InitialLiveCapitalUSDT = decimal.Zero }),
			wantErrSub:    "initial live capital",
			wantKillCalls: 1,
		},
		{
			name:       "initial capital exceeds max",
			ctx:        context.Background(),
			killSwitch: &fakeLiveKillSwitchRepository{},
			env:        validLiveStartupEnvironment(),
			req: mutateLiveStartupRequest(func(req *applive.PreflightLiveStartupRequest) {
				req.InitialLiveCapitalUSDT = decimal.RequireFromString("101")
			}),
			wantErrSub:    "must not exceed",
			wantKillCalls: 1,
		},
		{
			name:          "kill switch lookup failure",
			ctx:           context.Background(),
			killSwitch:    &fakeLiveKillSwitchRepository{err: repositoryErr},
			env:           validLiveStartupEnvironment(),
			req:           validLiveStartupRequest(),
			wantErrSub:    repositoryErr.Error(),
			wantKillCalls: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := tt.service
			if service == nil {
				service = liveStartupService(tt.killSwitch, tt.env)
			}

			got, err := service.PreflightLiveStartup(tt.ctx, tt.req)
			if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
			if got.Ready {
				t.Fatalf("unsafe startup must not be ready: %#v", got)
			}
			if tt.killSwitch != nil && tt.killSwitch.currentCalls != tt.wantKillCalls {
				t.Fatalf("kill switch calls mismatch: got %d want %d", tt.killSwitch.currentCalls, tt.wantKillCalls)
			}
		})
	}
}

func TestServicePreflightLiveStartupConfirmationValuesTableDriven(t *testing.T) {
	tests := []struct {
		name  string
		value string
	}{
		{name: "true", value: "true"},
		{name: "one", value: "1"},
		{name: "yes", value: "yes"},
		{name: "trimmed uppercase", value: " YES "},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := liveStartupService(&fakeLiveKillSwitchRepository{}, mapEnvironment{
				"TRADING_LIVE_CONFIRM": tt.value,
				"BYBIT_API_KEY":        "key",
				"BYBIT_API_SECRET":     "secret",
			})

			got, err := service.PreflightLiveStartup(context.Background(), validLiveStartupRequest())
			if err != nil {
				t.Fatalf("preflight live startup: %v", err)
			}
			if !got.Ready || !got.ConfirmationAccepted {
				t.Fatalf("expected confirmation accepted for %q: %#v", tt.value, got)
			}
		})
	}
}

type mapEnvironment map[string]string

func (e mapEnvironment) LookupEnv(name string) (string, bool) {
	value, ok := e[name]
	return value, ok
}

func validLiveStartupRequest() applive.PreflightLiveStartupRequest {
	return applive.PreflightLiveStartupRequest{
		TradingEnabled:              true,
		TradingMode:                 " LIVE ",
		AllowLive:                   true,
		RequireEnvConfirmation:      true,
		ConfirmationEnv:             " TRADING_LIVE_CONFIRM ",
		APIKeyEnv:                   " BYBIT_API_KEY ",
		APISecretEnv:                " BYBIT_API_SECRET ",
		RequireSubaccount:           true,
		SubaccountConfirmed:         true,
		WithdrawalPermissionAllowed: false,
		InitialLiveCapitalUSDT:      decimal.RequireFromString("50"),
		MaxInitialLiveCapitalUSDT:   decimal.RequireFromString("100"),
	}
}

func mutateLiveStartupRequest(mutate func(*applive.PreflightLiveStartupRequest)) applive.PreflightLiveStartupRequest {
	req := validLiveStartupRequest()
	mutate(&req)
	return req
}

func validLiveStartupEnvironment() mapEnvironment {
	return mapEnvironment{
		"TRADING_LIVE_CONFIRM": "true",
		"BYBIT_API_KEY":        "key",
		"BYBIT_API_SECRET":     "secret",
	}
}

func liveStartupService(killSwitch *fakeLiveKillSwitchRepository, env applive.EnvironmentReader) *applive.Service {
	return applive.NewService(
		applive.WithKillSwitchRepository(killSwitch),
		applive.WithEnvironmentReader(env),
	)
}

func liveStartupPositionService(
	now time.Time,
	reader domainlive.PositionSnapshotReader,
	journal domainlive.PositionSnapshotJournal,
	killSwitch *fakeLiveKillSwitchRepository,
	env applive.EnvironmentReader,
) *applive.Service {
	return applive.NewService(
		applive.WithKillSwitchRepository(killSwitch),
		applive.WithEnvironmentReader(env),
		applive.WithPositionSnapshotReader(reader),
		applive.WithPositionSnapshotJournal(journal),
		applive.WithClock(clock.FixedClock{Time: now}),
	)
}

func liveStartupAccountService(
	now time.Time,
	reader domainlive.AccountSnapshotReader,
	killSwitch *fakeLiveKillSwitchRepository,
	env applive.EnvironmentReader,
) *applive.Service {
	return applive.NewService(
		applive.WithKillSwitchRepository(killSwitch),
		applive.WithEnvironmentReader(env),
		applive.WithAccountSnapshotReader(reader),
		applive.WithClock(clock.FixedClock{Time: now}),
	)
}

type fakeLiveAccountSnapshotReader struct {
	query    domainlive.AccountSnapshotQuery
	snapshot domainlive.AccountSnapshot
	calls    int
	err      error
}

func (r *fakeLiveAccountSnapshotReader) GetAccountSnapshot(_ context.Context, query domainlive.AccountSnapshotQuery) (domainlive.AccountSnapshot, error) {
	r.calls++
	r.query = query
	if r.err != nil {
		return domainlive.AccountSnapshot{}, r.err
	}
	return r.snapshot, nil
}

func validLiveStartupAccountQuery() domainlive.AccountSnapshotQuery {
	return domainlive.AccountSnapshotQuery{
		Exchange:    "bybit",
		AccountType: domainlive.AccountTypeUnified,
	}
}

func validLiveStartupAccountSnapshot(t *testing.T, query domainlive.AccountSnapshotQuery, observedAt time.Time) domainlive.AccountSnapshot {
	t.Helper()

	snapshot, err := domainlive.NewAccountSnapshot(domainlive.AccountSnapshotInput{
		Exchange:               query.Exchange,
		AccountType:            query.AccountType,
		TotalEquity:            decimal.RequireFromString("50"),
		TotalWalletBalance:     decimal.RequireFromString("50"),
		TotalMarginBalance:     decimal.RequireFromString("50"),
		TotalAvailableBalance:  decimal.RequireFromString("50"),
		TotalPerpUPL:           decimal.Zero,
		TotalInitialMargin:     decimal.Zero,
		TotalMaintenanceMargin: decimal.Zero,
		Coins: []domainlive.AccountCoinSnapshot{{
			Coin:             "USDT",
			Equity:           decimal.RequireFromString("50"),
			USDValue:         decimal.RequireFromString("50"),
			WalletBalance:    decimal.RequireFromString("50"),
			MarginCollateral: true,
			CollateralSwitch: true,
		}},
		ObservedAt: observedAt,
	})
	if err != nil {
		t.Fatalf("new startup account snapshot: %v", err)
	}
	return snapshot
}

func mutateLiveStartupAccountSnapshot(snapshot domainlive.AccountSnapshot, mutate func(*domainlive.AccountSnapshot)) domainlive.AccountSnapshot {
	mutate(&snapshot)
	return snapshot
}

func validLiveStartupPositionQuery() domainlive.PositionSnapshotQuery {
	return domainlive.PositionSnapshotQuery{
		Exchange: "bybit",
		Category: "linear",
		Symbol:   "BTCUSDT",
	}
}

func validLiveStartupFlatPositionSnapshot(t *testing.T, query domainlive.PositionSnapshotQuery, observedAt time.Time) domainlive.PositionSnapshot {
	t.Helper()

	snapshot, err := domainlive.NewPositionSnapshot(domainlive.PositionSnapshotInput{
		Exchange:       query.Exchange,
		Category:       query.Category,
		Symbol:         query.Symbol,
		Size:           decimal.Zero,
		MarkPrice:      decimal.RequireFromString("100000"),
		ExchangeStatus: domainlive.ExchangePositionStatusNormal,
		PositionIndex:  0,
		Sequence:       -1,
		ObservedAt:     observedAt,
	})
	if err != nil {
		t.Fatalf("new flat startup position snapshot: %v", err)
	}
	return snapshot
}

func validLiveStartupOpenPositionSnapshot(t *testing.T, query domainlive.PositionSnapshotQuery, observedAt time.Time) domainlive.PositionSnapshot {
	t.Helper()

	snapshot, err := domainlive.NewPositionSnapshot(domainlive.PositionSnapshotInput{
		Exchange:          query.Exchange,
		Category:          query.Category,
		Symbol:            query.Symbol,
		Side:              domainlive.OrderSideLong,
		Size:              decimal.RequireFromString("0.001"),
		AveragePrice:      decimal.RequireFromString("100000"),
		PositionValue:     decimal.RequireFromString("100"),
		MarkPrice:         decimal.RequireFromString("100000"),
		LiquidationPrice:  decimal.RequireFromString("99000"),
		Leverage:          decimal.RequireFromString("1"),
		ExchangeStatus:    domainlive.ExchangePositionStatusNormal,
		PositionIndex:     0,
		Sequence:          123,
		ExchangeCreatedAt: observedAt.Add(-time.Minute),
		ExchangeUpdatedAt: observedAt,
		ObservedAt:        observedAt,
	})
	if err != nil {
		t.Fatalf("new open startup position snapshot: %v", err)
	}
	return snapshot
}
