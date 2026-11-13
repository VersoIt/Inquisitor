package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/VersoIt/Inquisitor/internal/clock"
	"github.com/VersoIt/Inquisitor/internal/config"
	"github.com/VersoIt/Inquisitor/internal/paper"
	"github.com/VersoIt/Inquisitor/internal/research"
	"github.com/VersoIt/Inquisitor/internal/strategy"
	"github.com/VersoIt/Inquisitor/internal/testutil"
)

type approvalStore struct {
	evidence               strategy.PaperApprovalEvidence
	stored                 strategy.PaperAuthorization
	reads, grants, revokes int
	err                    error
}

func (s *approvalStore) LoadPaperApprovalEvidence(context.Context, string) (strategy.PaperApprovalEvidence, error) {
	s.reads++
	return s.evidence, s.err
}
func (s *approvalStore) RecordPaperApproval(_ context.Context, a strategy.PaperApproval) (strategy.ApprovalStats, error) {
	s.grants++
	if s.err != nil {
		return strategy.ApprovalStats{}, s.err
	}
	if s.stored.Grant.ApprovalID != "" {
		left, _ := json.Marshal(a)
		right, _ := json.Marshal(s.stored.Grant)
		if !bytes.Equal(left, right) {
			return strategy.ApprovalStats{}, strategy.ErrApprovalConflict
		}
		return strategy.ApprovalStats{Skipped: 1}, nil
	}
	s.stored = strategy.PaperAuthorization{Grant: a, Evidence: s.evidence}
	return strategy.ApprovalStats{Written: 1}, nil
}
func (s *approvalStore) LoadPaperAuthorization(context.Context, string) (strategy.PaperAuthorization, error) {
	s.reads++
	return s.stored, s.err
}
func (s *approvalStore) RevokePaperApproval(_ context.Context, _ string, r strategy.ApprovalRevocation) (strategy.ApprovalStats, error) {
	s.revokes++
	if s.err != nil {
		return strategy.ApprovalStats{}, s.err
	}
	if s.stored.Grant.ApprovalID == "" {
		return strategy.ApprovalStats{}, strategy.ErrApprovalConflict
	}
	if !s.stored.Revocation.RevokedAt.IsZero() {
		if s.stored.Revocation.RevokedBy != r.RevokedBy || s.stored.Revocation.Reason != r.Reason {
			return strategy.ApprovalStats{}, strategy.ErrApprovalConflict
		}
		return strategy.ApprovalStats{Skipped: 1}, nil
	}
	s.stored.Revocation = r
	return strategy.ApprovalStats{Written: 1}, nil
}

func grantArgs(f testutil.ApprovalFixture) []string {
	return []string{"-action", "grant", "-approval-id", f.Grant.ApprovalID, "-validation-id", f.Grant.ValidationID, "-strategy", f.Grant.StrategyName, "-symbol", f.Grant.Symbol, "-interval", f.Grant.Interval, "-reviewed-by", "operator", "-reason", "review completed", "-confirm-review"}
}

func commandDeps(t *testing.T, f testutil.ApprovalFixture, store *approvalStore) dependencies {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Error(err)
		}
		_ = db.Close()
	})
	return dependencies{
		loadConfig: func(string) (*config.Config, error) {
			return &config.Config{Database: config.DatabaseConfig{DSN: "test-dsn"}, Trading: config.TradingConfig{Mode: "paper"}, Research: config.ResearchConfig{MinTrades: 100, MinProfitFactor: 1.15, MinExpectancyR: 0.05, MaxDrawdownPct: 15, RequireOutOfSample: true, RequireWalkForward: true, RequireRegimeAnalysis: true}}, nil
		},
		openDB: func(ctx context.Context, c config.DatabaseConfig) (*sql.DB, error) {
			if _, ok := ctx.Deadline(); !ok {
				t.Fatal("unbounded DB context")
			}
			if c.DSN != "test-dsn" {
				t.Fatal("unexpected DSN")
			}
			mock.ExpectClose()
			return db, nil
		},
		repository: func(*sql.DB) strategy.PaperApprovalRepository { return store },
		lookupEnv:  func(string) (string, bool) { return "test-dsn", true }, clock: clock.FixedClock{Time: f.Now},
	}
}

func TestApprovalCommandGrantShowRevokeAndImmutableRetries(t *testing.T) {
	f := testutil.PaperApproval(t)
	store := &approvalStore{evidence: f.Evidence}
	var output bytes.Buffer
	if err := run(context.Background(), grantArgs(f), &output, commandDeps(t, f, store)); err != nil {
		t.Fatal(err)
	}
	var grant commandResult
	if err := json.Unmarshal(output.Bytes(), &grant); err != nil {
		t.Fatal(err)
	}
	if grant.Grant == nil || grant.Stats.Written != 1 || !grant.Grant.GrantedAt.Equal(f.Now) || grant.Grant.HypothesisSHA256 != f.Grant.HypothesisSHA256 || !strings.Contains(grant.Warning, "not a current execution permit") {
		t.Fatalf("wrong grant output %+v", grant)
	}
	for _, action := range []string{"show", "revoke", "revoke"} {
		deps := commandDeps(t, f, store)
		deps.loadConfig = func(string) (*config.Config, error) { t.Fatal("show/revoke loaded app config"); return nil, nil }
		deps.clock = clock.FixedClock{Time: f.Now.Add(time.Hour)}
		args := []string{"-action", action, "-approval-id", f.Grant.ApprovalID}
		if action == "revoke" {
			args = append(args, "-reviewed-by", "operator", "-reason", "stop")
		}
		output.Reset()
		if err := run(context.Background(), args, &output, deps); err != nil {
			t.Fatal(err)
		}
		var result commandResult
		if err := json.Unmarshal(output.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if action == "show" && (result.Grant == nil || result.Grant.ApprovalID != f.Grant.ApprovalID || result.Stats != nil || store.revokes != 0 || store.grants != 1) {
			t.Fatal("show modified authorization")
		}
	}
	firstRevocation := store.stored.Revocation
	deps := commandDeps(t, f, store)
	deps.clock = clock.FixedClock{Time: f.Now.Add(2 * time.Hour)}
	if err := run(context.Background(), []string{"-action", "revoke", "-approval-id", f.Grant.ApprovalID, "-reviewed-by", "operator", "-reason", "stop"}, io.Discard, deps); err != nil || store.stored.Revocation != firstRevocation {
		t.Fatal("retry changed revocation")
	}
	deps = commandDeps(t, f, store)
	deps.clock = clock.FixedClock{Time: f.Now.Add(3 * time.Hour)}
	if err := run(context.Background(), grantArgs(f), io.Discard, deps); !errors.Is(err, strategy.ErrApprovalConflict) || store.stored.Revocation != firstRevocation || !store.stored.Grant.GrantedAt.Equal(f.Now) {
		t.Fatalf("later grant overwrote immutable/revoked record: %v", err)
	}
}

func TestApprovalCommandRejectsUnsafeFlagsBeforeAnyIO(t *testing.T) {
	f := testutil.PaperApproval(t)
	for _, args := range [][]string{
		nil, {"-action", "live", "-approval-id", "id"}, {"-approval-id", " id "}, {"-approval-id", "id", "extra"},
		{"-approval-id", "id", "-timeout", "0"}, {"-approval-id", "id", "-dsn-env", "bad-name"},
		{"-approval-id", "id", "-confirm-review"}, {"-approval-id", "id", "-config", "config.yaml"},
		{"-action", "revoke", "-approval-id", "id"}, {"-action", "revoke", "-approval-id", "id", "-reviewed-by", " op ", "-reason", "stop"},
		grantArgs(f)[:len(grantArgs(f))-1],
		append(grantArgs(f), "-strategy", "fake"), append(grantArgs(f), "-interval", "bad"), append(grantArgs(f), "-interval", " 1 "),
		append(grantArgs(f), "-validation-id", ""), append(grantArgs(f), "-symbol", "BTC/USDT"), append(grantArgs(f), "-symbol", "btcusdt"),
		append(grantArgs(f), "-dsn-env", "OTHER_DSN"),
	} {
		t.Run(fmt.Sprint(args), func(t *testing.T) {
			deps := dependencies{loadConfig: func(string) (*config.Config, error) { t.Fatal("invalid flags loaded config"); return nil, nil }, openDB: func(context.Context, config.DatabaseConfig) (*sql.DB, error) {
				t.Fatal("invalid flags opened DB")
				return nil, nil
			}, lookupEnv: func(string) (string, bool) { t.Fatal("invalid flags read environment"); return "", false }}
			if err := run(context.Background(), args, io.Discard, deps); err == nil {
				t.Fatal("invalid flags accepted")
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := run(ctx, []string{"-approval-id", "id"}, io.Discard, dependencies{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := run(context.Background(), []string{"-help"}, io.Discard, dependencies{}); !errors.Is(err, flag.ErrHelp) {
		t.Fatal(err)
	}
}

func TestApprovalGrantRejectsUnsafeConfigurationBeforeDB(t *testing.T) {
	f := testutil.PaperApproval(t)
	for _, tc := range []struct {
		name   string
		mutate func(*config.Config)
	}{
		{"live mode", func(c *config.Config) { c.Trading.Mode = "live" }}, {"live allowed", func(c *config.Config) { c.Trading.AllowLive = true }},
		{"withdrawals", func(c *config.Config) { c.Live.WithdrawalPermissionAllowed = true }}, {"weak sample", func(c *config.Config) { c.Research.MinTrades = 99 }},
		{"NaN PF", func(c *config.Config) { c.Research.MinProfitFactor = math.NaN() }}, {"missing OOS", func(c *config.Config) { c.Research.RequireOutOfSample = false }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deps := commandDeps(t, f, &approvalStore{})
			load := deps.loadConfig
			deps.loadConfig = func(path string) (*config.Config, error) { c, err := load(path); tc.mutate(c); return c, err }
			deps.openDB = func(context.Context, config.DatabaseConfig) (*sql.DB, error) {
				t.Fatal("unsafe config opened DB")
				return nil, nil
			}
			if err := run(context.Background(), grantArgs(f), io.Discard, deps); err == nil {
				t.Fatal("unsafe grant allowed")
			}
		})
	}
}

func TestGrantCommandCannotOverrideFailedPersistedEvidence(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*strategy.PaperApprovalEvidence)
	}{
		{"rejected result", func(e *strategy.PaperApprovalEvidence) { e.Result.Outcome = research.OutcomeRejected }},
		{"validation not running", func(e *strategy.PaperApprovalEvidence) {
			e.Validation.Status = paper.ValidationStatusPlanned
			e.Validation.StartedAt = time.Time{}
		}},
		{"missing hash", func(e *strategy.PaperApprovalEvidence) { e.Run.HypothesisContentSHA256 = strings.Repeat("0", 64) }},
		{"negative OOS", func(e *strategy.PaperApprovalEvidence) { e.Result.Metrics.OutOfSampleNetPnL = "-1" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := testutil.PaperApproval(t)
			store := &approvalStore{evidence: f.Evidence}
			tc.mutate(&store.evidence)
			var output bytes.Buffer
			if err := run(context.Background(), grantArgs(f), &output, commandDeps(t, f, store)); err == nil || store.grants != 0 || output.Len() != 0 {
				t.Fatalf("failed evidence granted permission: %v", err)
			}
		})
	}
}

type failedOutput struct{}

func (failedOutput) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestApprovalCommandStorageDeadlineAndOutputFailures(t *testing.T) {
	f := testutil.PaperApproval(t)
	store := &approvalStore{evidence: f.Evidence, stored: strategy.PaperAuthorization{Grant: f.Grant, Evidence: f.Evidence}}
	for _, action := range []string{"show", "revoke"} {
		t.Run(action+" storage error", func(t *testing.T) {
			failure := errors.New("storage unavailable")
			store.err = failure
			defer func() { store.err = nil }()
			args := []string{"-action", action, "-approval-id", f.Grant.ApprovalID}
			if action == "revoke" {
				args = append(args, "-reviewed-by", "operator", "-reason", "stop")
			}
			if err := run(context.Background(), args, io.Discard, commandDeps(t, f, store)); !errors.Is(err, failure) {
				t.Fatal(err)
			}
		})
	}
	deps := commandDeps(t, f, store)
	deps.openDB = func(context.Context, config.DatabaseConfig) (*sql.DB, error) {
		return nil, errors.New("parse postgres://user:SUPER_SECRET@localhost")
	}
	var output bytes.Buffer
	err := run(context.Background(), []string{"-approval-id", f.Grant.ApprovalID}, &output, deps)
	if err == nil || strings.Contains(err.Error(), "SUPER_SECRET") || output.Len() != 0 {
		t.Fatal("DSN leaked or connection error ignored")
	}
	deps = commandDeps(t, f, store)
	deps.lookupEnv = func(string) (string, bool) { return "", false }
	if err := run(context.Background(), []string{"-approval-id", f.Grant.ApprovalID}, io.Discard, deps); err == nil {
		t.Fatal("missing DSN accepted")
	}
	deps = commandDeps(t, f, store)
	deps.openDB = func(ctx context.Context, _ config.DatabaseConfig) (*sql.DB, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if err := run(context.Background(), []string{"-approval-id", f.Grant.ApprovalID, "-timeout", "1ms"}, io.Discard, deps); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	if err := run(context.Background(), []string{"-approval-id", f.Grant.ApprovalID}, failedOutput{}, commandDeps(t, f, store)); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatal("output failure ignored", err)
	}
}

func TestApprovalCommandRejectsMissingDependenciesAndMalformedReads(t *testing.T) {
	for _, scenario := range []string{"nil config", "config error", "nil DB", "nil repository", "wrong identity", "invalid grant", "incomplete revocation", "revocation before grant"} {
		t.Run(scenario, func(t *testing.T) {
			f := testutil.PaperApproval(t)
			store := &approvalStore{evidence: f.Evidence, stored: strategy.PaperAuthorization{Grant: f.Grant, Evidence: f.Evidence}}
			deps := commandDeps(t, f, store)
			args := []string{"-approval-id", f.Grant.ApprovalID}
			switch scenario {
			case "nil config":
				args = grantArgs(f)
				deps.loadConfig = func(string) (*config.Config, error) { return nil, nil }
			case "config error":
				args = grantArgs(f)
				deps.loadConfig = func(string) (*config.Config, error) { return nil, errors.New("config unavailable") }
			case "nil DB":
				deps.openDB = func(context.Context, config.DatabaseConfig) (*sql.DB, error) { return nil, nil }
			case "nil repository":
				deps.repository = func(*sql.DB) strategy.PaperApprovalRepository { return nil }
			case "wrong identity":
				store.stored.Grant.ApprovalID = "other"
			case "invalid grant":
				store.stored.Grant.HypothesisSHA256 = "bad"
			case "incomplete revocation":
				store.stored.Revocation.RevokedBy = "operator"
			case "revocation before grant":
				store.stored.Revocation = strategy.ApprovalRevocation{RevokedAt: f.Grant.GrantedAt.Add(-time.Second), RevokedBy: "operator", Reason: "stop"}
			}
			var output bytes.Buffer
			if err := run(context.Background(), args, &output, deps); err == nil || output.Len() != 0 || store.grants != 0 || store.revokes != 0 {
				t.Fatalf("malformed dependency/read produced success: %v", err)
			}
		})
	}
}
