package live_test

import (
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"github.com/VersoIt/Inquisitor/internal/live"
)

func TestNewAccountSnapshotNormalizesUnifiedAccount(t *testing.T) {
	observedAt := time.Date(2026, 7, 22, 12, 0, 0, 0, time.FixedZone("UTC+3", 3*60*60))

	got, err := live.NewAccountSnapshot(live.AccountSnapshotInput{
		Exchange:               " BYBIT ",
		AccountType:            " unified ",
		TotalEquity:            decimal.RequireFromString("50.25"),
		TotalWalletBalance:     decimal.RequireFromString("50.25"),
		TotalMarginBalance:     decimal.RequireFromString("50.25"),
		TotalAvailableBalance:  decimal.RequireFromString("50.25"),
		TotalPerpUPL:           decimal.Zero,
		TotalInitialMargin:     decimal.Zero,
		TotalMaintenanceMargin: decimal.Zero,
		Coins: []live.AccountCoinSnapshot{{
			Coin:             " usdt ",
			Equity:           decimal.RequireFromString("50.25"),
			USDValue:         decimal.RequireFromString("50.25"),
			WalletBalance:    decimal.RequireFromString("50.25"),
			MarginCollateral: true,
			CollateralSwitch: true,
		}},
		ObservedAt: observedAt,
	})
	if err != nil {
		t.Fatalf("new account snapshot: %v", err)
	}

	if got.Exchange != "bybit" || got.AccountType != live.AccountTypeUnified ||
		len(got.Coins) != 1 || got.Coins[0].Coin != "USDT" {
		t.Fatalf("snapshot not normalized: %#v", got)
	}
	if got.ObservedAt.Location() != time.UTC {
		t.Fatalf("snapshot observed_at must be UTC: %#v", got)
	}
}

func TestValidateAccountSnapshotQueryRejectsUnsafeInputsTableDriven(t *testing.T) {
	tests := []struct {
		name       string
		query      live.AccountSnapshotQuery
		wantErrSub string
	}{
		{name: "valid", query: validAccountSnapshotQuery()},
		{name: "missing exchange", query: mutateAccountSnapshotQuery(func(q *live.AccountSnapshotQuery) { q.Exchange = "" }), wantErrSub: "exchange"},
		{name: "uppercase exchange", query: mutateAccountSnapshotQuery(func(q *live.AccountSnapshotQuery) { q.Exchange = "BYBIT" }), wantErrSub: "exchange"},
		{name: "missing account type", query: mutateAccountSnapshotQuery(func(q *live.AccountSnapshotQuery) { q.AccountType = "" }), wantErrSub: "account_type"},
		{name: "lowercase account type", query: mutateAccountSnapshotQuery(func(q *live.AccountSnapshotQuery) { q.AccountType = "unified" }), wantErrSub: "account_type"},
		{name: "unknown account type", query: mutateAccountSnapshotQuery(func(q *live.AccountSnapshotQuery) { q.AccountType = "SPOT" }), wantErrSub: "account_type"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := live.ValidateAccountSnapshotQuery(tt.query)
			if tt.wantErrSub == "" {
				if err != nil {
					t.Fatalf("expected query to pass, got %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
		})
	}
}

func TestValidateAccountSnapshotRejectsUnsafeInputsTableDriven(t *testing.T) {
	tests := []struct {
		name       string
		mutate     func(*live.AccountSnapshot)
		wantErrSub string
	}{
		{"missing observed time", func(s *live.AccountSnapshot) { s.ObservedAt = time.Time{} }, "observed_at"},
		{"negative initial margin", func(s *live.AccountSnapshot) { s.TotalInitialMargin = decimal.RequireFromString("-1") }, "total_initial_margin"},
		{"negative maintenance margin", func(s *live.AccountSnapshot) { s.TotalMaintenanceMargin = decimal.RequireFromString("-1") }, "total_maintenance_margin"},
		{"missing coin", func(s *live.AccountSnapshot) { s.Coins[0].Coin = "" }, "coin"},
		{"lowercase coin", func(s *live.AccountSnapshot) { s.Coins[0].Coin = "usdt" }, "uppercase"},
		{"duplicate coin", func(s *live.AccountSnapshot) { s.Coins = append(s.Coins, s.Coins[0]) }, "unique"},
		{"negative locked", func(s *live.AccountSnapshot) { s.Coins[0].Locked = decimal.RequireFromString("-0.1") }, "locked"},
		{"negative borrow", func(s *live.AccountSnapshot) { s.Coins[0].BorrowAmount = decimal.RequireFromString("-0.1") }, "borrow_amount"},
		{"negative spot borrow", func(s *live.AccountSnapshot) { s.Coins[0].SpotBorrow = decimal.RequireFromString("-0.1") }, "spot_borrow"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			snapshot := validAccountSnapshot()
			tt.mutate(&snapshot)

			err := live.ValidateAccountSnapshot(snapshot)
			if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
		})
	}
}

func TestKnownAccountTypeCoversUnifiedAccount(t *testing.T) {
	if !live.KnownAccountType(live.AccountTypeUnified) {
		t.Fatalf("UNIFIED account type should be known")
	}
	if live.KnownAccountType("SPOT") {
		t.Fatalf("SPOT account type should not be accepted for live readiness")
	}
}

func TestAccountSnapshotStatsTotal(t *testing.T) {
	if got := (live.AccountSnapshotStats{Inserted: 2, Skipped: 3}).Total(); got != 5 {
		t.Fatalf("total mismatch: got %d", got)
	}
}

func validAccountSnapshotQuery() live.AccountSnapshotQuery {
	return live.AccountSnapshotQuery{
		Exchange:    "bybit",
		AccountType: live.AccountTypeUnified,
	}
}

func mutateAccountSnapshotQuery(mutate func(*live.AccountSnapshotQuery)) live.AccountSnapshotQuery {
	query := validAccountSnapshotQuery()
	mutate(&query)
	return query
}

func validAccountSnapshot() live.AccountSnapshot {
	return live.AccountSnapshot{
		Exchange:               "bybit",
		AccountType:            live.AccountTypeUnified,
		TotalEquity:            decimal.RequireFromString("50.25"),
		TotalWalletBalance:     decimal.RequireFromString("50.25"),
		TotalMarginBalance:     decimal.RequireFromString("50.25"),
		TotalAvailableBalance:  decimal.RequireFromString("50.25"),
		TotalPerpUPL:           decimal.Zero,
		TotalInitialMargin:     decimal.Zero,
		TotalMaintenanceMargin: decimal.Zero,
		Coins: []live.AccountCoinSnapshot{{
			Coin:             "USDT",
			Equity:           decimal.RequireFromString("50.25"),
			USDValue:         decimal.RequireFromString("50.25"),
			WalletBalance:    decimal.RequireFromString("50.25"),
			MarginCollateral: true,
			CollateralSwitch: true,
		}},
		ObservedAt: time.Date(2026, 7, 22, 12, 0, 0, 0, time.UTC),
	}
}
