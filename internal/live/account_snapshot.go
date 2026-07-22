package live

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

type AccountType string

const (
	AccountTypeUnified AccountType = "UNIFIED"
)

type AccountSnapshotQuery struct {
	Exchange    string
	AccountType AccountType
}

type AccountSnapshot struct {
	Exchange               string
	AccountType            AccountType
	TotalEquity            decimal.Decimal
	TotalWalletBalance     decimal.Decimal
	TotalMarginBalance     decimal.Decimal
	TotalAvailableBalance  decimal.Decimal
	TotalPerpUPL           decimal.Decimal
	TotalInitialMargin     decimal.Decimal
	TotalMaintenanceMargin decimal.Decimal
	Coins                  []AccountCoinSnapshot
	ObservedAt             time.Time
}

type AccountCoinSnapshot struct {
	Coin                  string
	Equity                decimal.Decimal
	USDValue              decimal.Decimal
	WalletBalance         decimal.Decimal
	Locked                decimal.Decimal
	BorrowAmount          decimal.Decimal
	AccruedInterest       decimal.Decimal
	TotalOrderIM          decimal.Decimal
	TotalPositionIM       decimal.Decimal
	TotalPositionMM       decimal.Decimal
	UnrealisedPnL         decimal.Decimal
	CumulativeRealisedPnL decimal.Decimal
	SpotBorrow            decimal.Decimal
	MarginCollateral      bool
	CollateralSwitch      bool
}

type AccountSnapshotInput struct {
	Exchange               string
	AccountType            AccountType
	TotalEquity            decimal.Decimal
	TotalWalletBalance     decimal.Decimal
	TotalMarginBalance     decimal.Decimal
	TotalAvailableBalance  decimal.Decimal
	TotalPerpUPL           decimal.Decimal
	TotalInitialMargin     decimal.Decimal
	TotalMaintenanceMargin decimal.Decimal
	Coins                  []AccountCoinSnapshot
	ObservedAt             time.Time
}

type AccountSnapshotReader interface {
	GetAccountSnapshot(ctx context.Context, query AccountSnapshotQuery) (AccountSnapshot, error)
}

func NewAccountSnapshot(input AccountSnapshotInput) (AccountSnapshot, error) {
	coins := make([]AccountCoinSnapshot, 0, len(input.Coins))
	for _, coin := range input.Coins {
		coin.Coin = strings.ToUpper(strings.TrimSpace(coin.Coin))
		coins = append(coins, coin)
	}

	snapshot := AccountSnapshot{
		Exchange:               strings.ToLower(strings.TrimSpace(input.Exchange)),
		AccountType:            normalizeAccountType(input.AccountType),
		TotalEquity:            input.TotalEquity,
		TotalWalletBalance:     input.TotalWalletBalance,
		TotalMarginBalance:     input.TotalMarginBalance,
		TotalAvailableBalance:  input.TotalAvailableBalance,
		TotalPerpUPL:           input.TotalPerpUPL,
		TotalInitialMargin:     input.TotalInitialMargin,
		TotalMaintenanceMargin: input.TotalMaintenanceMargin,
		Coins:                  coins,
		ObservedAt:             input.ObservedAt.UTC(),
	}
	if err := ValidateAccountSnapshot(snapshot); err != nil {
		return AccountSnapshot{}, err
	}
	return snapshot, nil
}

func ValidateAccountSnapshotQuery(query AccountSnapshotQuery) error {
	var problems []string
	if strings.TrimSpace(query.Exchange) == "" {
		problems = append(problems, "exchange is required")
	}
	if strings.TrimSpace(query.Exchange) != "" && query.Exchange != strings.ToLower(strings.TrimSpace(query.Exchange)) {
		problems = append(problems, "exchange must be lowercase and trimmed")
	}
	if strings.TrimSpace(string(query.AccountType)) == "" {
		problems = append(problems, "account_type is required")
	}
	if strings.TrimSpace(string(query.AccountType)) != "" && AccountType(strings.ToUpper(strings.TrimSpace(string(query.AccountType)))) != query.AccountType {
		problems = append(problems, "account_type must be uppercase and trimmed")
	}
	if strings.TrimSpace(string(query.AccountType)) != "" && !KnownAccountType(query.AccountType) {
		problems = append(problems, "account_type is unknown")
	}
	if len(problems) > 0 {
		return errors.New("live account snapshot query validation failed: " + strings.Join(problems, "; "))
	}
	return nil
}

func ValidateAccountSnapshot(snapshot AccountSnapshot) error {
	var problems []string
	if err := ValidateAccountSnapshotQuery(AccountSnapshotQuery{
		Exchange:    snapshot.Exchange,
		AccountType: snapshot.AccountType,
	}); err != nil {
		problems = append(problems, err.Error())
	}
	if snapshot.TotalInitialMargin.IsNegative() {
		problems = append(problems, "total_initial_margin must be non-negative")
	}
	if snapshot.TotalMaintenanceMargin.IsNegative() {
		problems = append(problems, "total_maintenance_margin must be non-negative")
	}
	if snapshot.ObservedAt.IsZero() {
		problems = append(problems, "observed_at is required")
	}

	seenCoins := make(map[string]struct{}, len(snapshot.Coins))
	for index, coin := range snapshot.Coins {
		prefix := fmt.Sprintf("coin[%d]", index)
		if strings.TrimSpace(coin.Coin) == "" {
			problems = append(problems, prefix+" coin is required")
		}
		if strings.TrimSpace(coin.Coin) != "" && coin.Coin != strings.ToUpper(strings.TrimSpace(coin.Coin)) {
			problems = append(problems, prefix+" coin must be uppercase and trimmed")
		}
		if _, exists := seenCoins[coin.Coin]; exists {
			problems = append(problems, prefix+" coin must be unique")
		}
		seenCoins[coin.Coin] = struct{}{}

		for _, item := range []struct {
			name  string
			value decimal.Decimal
		}{
			{"locked", coin.Locked},
			{"borrow_amount", coin.BorrowAmount},
			{"accrued_interest", coin.AccruedInterest},
			{"total_order_im", coin.TotalOrderIM},
			{"total_position_im", coin.TotalPositionIM},
			{"total_position_mm", coin.TotalPositionMM},
			{"spot_borrow", coin.SpotBorrow},
		} {
			if item.value.IsNegative() {
				problems = append(problems, prefix+" "+item.name+" must be non-negative")
			}
		}
	}

	if len(problems) > 0 {
		return errors.New("live account snapshot validation failed: " + strings.Join(problems, "; "))
	}
	return nil
}

func KnownAccountType(accountType AccountType) bool {
	switch accountType {
	case AccountTypeUnified:
		return true
	default:
		return false
	}
}

func normalizeAccountType(accountType AccountType) AccountType {
	return AccountType(strings.ToUpper(strings.TrimSpace(string(accountType))))
}
