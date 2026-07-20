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
