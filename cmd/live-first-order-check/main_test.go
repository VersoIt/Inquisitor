package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
)

func TestBuildLiveFirstOrderCheckBundleTableDriven(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*liveFirstOrderCheckRequest)
		assert func(*testing.T, liveFirstOrderCheckBundle)
	}{
		{
			name: "explicit decision bundle",
			assert: func(t *testing.T, bundle liveFirstOrderCheckBundle) {
				assertLiveFirstOrderCommandNames(t, bundle.Commands, []string{
					"risk-kill-switch-state",
					"live-order-plan",
					"live-readiness",
					"live-loop-audit",
					"live-deploy-check",
					"live-handoff-verify",
					"live-ops-report",
				})
				killSwitch := liveFirstOrderCommandByName(t, bundle.Commands, "risk-kill-switch-state")
				assertLiveFirstOrderFlagValue(t, killSwitch.Args, "-config", "configs/live.local.yaml")
				assertLiveFirstOrderFlagValue(t, killSwitch.Args, "-action", "state")
				assertLiveFirstOrderFlagValue(t, killSwitch.Args, "-artifact-path", bundle.KillSwitchFile)

				plan := liveFirstOrderCommandByName(t, bundle.Commands, "live-order-plan")
				assertLiveFirstOrderFlagValue(t, plan.Args, "-decision-id", "risk_decision_live_first_order_001")
				assertLiveFirstOrderMissingFlag(t, plan.Args, "-select-pending")
				assertLiveFirstOrderFlagValue(t, plan.Args, "-artifact-path", bundle.PlanFile)

				readiness := liveFirstOrderCommandByName(t, bundle.Commands, "live-readiness")
				assertLiveFirstOrderFlagValue(t, readiness.Args, "-symbol", "BTCUSDT")
				assertLiveFirstOrderFlagValue(t, readiness.Args, "-plan-file", bundle.PlanFile)
				assertLiveFirstOrderFlagValue(t, readiness.Args, "-artifact-path", bundle.ReadinessFile)
				assertLiveFirstOrderFlag(t, readiness.Args, "-require-pending=true")

				deploy := liveFirstOrderCommandByName(t, bundle.Commands, "live-deploy-check")
				assertLiveFirstOrderFlagValue(t, deploy.Args, "-artifact-path", bundle.DeployCheckFile)
				assertLiveFirstOrderFlagValue(t, deploy.Args, "-micro-capital-limit-usdt", "100")
				assertLiveFirstOrderFlag(t, deploy.Args, "-execute")
				assertLiveFirstOrderFlag(t, deploy.Args, "-subaccount-confirmed")

				assertLiveFirstOrderFlagValue(t, bundle.SuggestedLiveLoop.Args, "-deploy-check-file", bundle.DeployCheckFile)
				assertLiveFirstOrderFlagValue(t, bundle.SuggestedLiveLoop.Args, "-ops-report-file", bundle.OpsReportFile)
				assertLiveFirstOrderFlagValue(t, bundle.SuggestedLiveLoop.Args, "-run-id", "live_loop_first_order_001")
				assertLiveFirstOrderFlag(t, bundle.SuggestedLiveLoop.Args, "-execute")

				verify := liveFirstOrderCommandByName(t, bundle.Commands, "live-handoff-verify")
				assertLiveFirstOrderFlagValue(t, verify.Args, "-kill-switch-file", bundle.KillSwitchFile)

				ops := liveFirstOrderCommandByName(t, bundle.Commands, "live-ops-report")
				assertLiveFirstOrderFlagValue(t, ops.Args, "-artifact-path", bundle.OpsReportFile)
				assertLiveFirstOrderFlagValue(t, ops.Args, "-symbol", "BTCUSDT")
				assertLiveFirstOrderFlag(t, ops.Args, "-fail-on-non-clear")

				assertLiveFirstOrderFlagValue(t, bundle.SuggestedPostOrderReview.Args, "-plan-file", bundle.PlanFile)
				assertLiveFirstOrderFlagValue(t, bundle.SuggestedPostOrderReview.Args, "-artifact-path", bundle.ReviewFile)
			},
		},
		{
			name: "select pending bundle normalizes symbol",
			mutate: func(req *liveFirstOrderCheckRequest) {
				req.DecisionID = ""
				req.SelectPending = true
				req.Symbol = "btcusdt"
			},
			assert: func(t *testing.T, bundle liveFirstOrderCheckBundle) {
				for _, command := range append(bundle.Commands, bundle.SuggestedLiveLoop) {
					if command.Name == "risk-kill-switch-state" || command.Name == "live-readiness" || command.Name == "live-loop-audit" || command.Name == "live-ops-report" {
						continue
					}
					assertLiveFirstOrderFlag(t, command.Args, "-select-pending")
					assertLiveFirstOrderFlagValue(t, command.Args, "-pending-symbol", "BTCUSDT")
					assertLiveFirstOrderMissingFlag(t, command.Args, "-decision-id")
				}
			},
		},
		{
			name: "limit plan preserves order instructions",
			mutate: func(req *liveFirstOrderCheckRequest) {
				req.OrderType = "limit"
				req.TimeInForce = "post_only"
				req.LimitPrice = "100000"
			},
			assert: func(t *testing.T, bundle liveFirstOrderCheckBundle) {
				plan := liveFirstOrderCommandByName(t, bundle.Commands, "live-order-plan")
				assertLiveFirstOrderFlagValue(t, plan.Args, "-order-type", "LIMIT")
				assertLiveFirstOrderFlagValue(t, plan.Args, "-time-in-force", "POST_ONLY")
				assertLiveFirstOrderFlagValue(t, plan.Args, "-limit-price", "100000")

				deploy := liveFirstOrderCommandByName(t, bundle.Commands, "live-deploy-check")
				assertLiveFirstOrderMissingFlag(t, deploy.Args, "-order-type")
				assertLiveFirstOrderMissingFlag(t, bundle.SuggestedLiveLoop.Args, "-order-type")
			},
		},
		{
			name: "readiness can be infrastructure only",
			mutate: func(req *liveFirstOrderCheckRequest) {
				req.RequirePending = false
			},
			assert: func(t *testing.T, bundle liveFirstOrderCheckBundle) {
				readiness := liveFirstOrderCommandByName(t, bundle.Commands, "live-readiness")
				assertLiveFirstOrderFlag(t, readiness.Args, "-require-pending=false")
			},
		},
		{
			name: "position drift opts into ops report exchange check",
			mutate: func(req *liveFirstOrderCheckRequest) {
				req.PositionDrift = true
				req.PositionDriftSymbols = "btcusdt,ethusdt"
				req.PositionDriftCurrentMaxAge = 3 * time.Second
				req.PositionDriftBaselineMaxAge = 7 * time.Minute
			},
			assert: func(t *testing.T, bundle liveFirstOrderCheckBundle) {
				ops := liveFirstOrderCommandByName(t, bundle.Commands, "live-ops-report")
				assertLiveFirstOrderFlag(t, ops.Args, "-position-drift")
				assertLiveFirstOrderFlagValue(t, ops.Args, "-position-drift-symbols", "BTCUSDT,ETHUSDT")
				assertLiveFirstOrderFlagValue(t, ops.Args, "-position-drift-current-max-age", "3s")
				assertLiveFirstOrderFlagValue(t, ops.Args, "-position-drift-baseline-max-age", "7m0s")
				assertLiveFirstOrderFlag(t, ops.Args, "-fail-on-non-clear")
			},
		},
		{
			name: "position drift symbols imply drift",
			mutate: func(req *liveFirstOrderCheckRequest) {
				req.PositionDriftSymbols = "solusdt"
			},
			assert: func(t *testing.T, bundle liveFirstOrderCheckBundle) {
				ops := liveFirstOrderCommandByName(t, bundle.Commands, "live-ops-report")
				assertLiveFirstOrderFlag(t, ops.Args, "-position-drift")
				assertLiveFirstOrderFlagValue(t, ops.Args, "-position-drift-symbols", "SOLUSDT")
				assertLiveFirstOrderFlagValue(t, ops.Args, "-position-drift-current-max-age", "5s")
				assertLiveFirstOrderFlagValue(t, ops.Args, "-position-drift-baseline-max-age", "10m0s")
			},
		},
		{
			name: "position drift kill switch activation implies drift",
			mutate: func(req *liveFirstOrderCheckRequest) {
				req.PositionDriftKillSwitch = true
			},
			assert: func(t *testing.T, bundle liveFirstOrderCheckBundle) {
				ops := liveFirstOrderCommandByName(t, bundle.Commands, "live-ops-report")
				assertLiveFirstOrderFlag(t, ops.Args, "-position-drift")
				assertLiveFirstOrderFlag(t, ops.Args, "-activate-kill-switch-on-position-drift-blocked")
				assertLiveFirstOrderFlag(t, ops.Args, "-fail-on-non-clear")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := validLiveFirstOrderCheckRequest()
			if tt.mutate != nil {
				tt.mutate(&req)
			}
			bundle, err := buildLiveFirstOrderCheckBundle(req)
			if err != nil {
				t.Fatalf("build bundle: %v", err)
			}
			if bundle.ArtifactDir != filepath.Clean(req.ArtifactDir) ||
				bundle.KillSwitchFile != filepath.Join(filepath.Clean(req.ArtifactDir), "risk-kill-switch-state.json") ||
				bundle.PlanFile != filepath.Join(filepath.Clean(req.ArtifactDir), "live-order-plan.json") ||
				bundle.ReadinessFile != filepath.Join(filepath.Clean(req.ArtifactDir), "live-readiness.json") ||
				bundle.AuditFile != filepath.Join(filepath.Clean(req.ArtifactDir), "live-loop-audit.json") ||
				bundle.DeployCheckFile != filepath.Join(filepath.Clean(req.ArtifactDir), "live-deploy-check.json") ||
				bundle.OpsReportFile != filepath.Join(filepath.Clean(req.ArtifactDir), "live-ops-report.json") ||
				bundle.ReviewFile != filepath.Join(filepath.Clean(req.ArtifactDir), "live-first-order-review.json") {
				t.Fatalf("artifact path mismatch: %#v", bundle)
			}
			tt.assert(t, bundle)
		})
	}
}

func TestBuildLiveFirstOrderCheckBundleRejectsUnsafeFlagsTableDriven(t *testing.T) {
	tests := []struct {
		name       string
		mutate     func(*liveFirstOrderCheckRequest)
		wantErrSub string
	}{
		{name: "missing source", mutate: func(req *liveFirstOrderCheckRequest) {
			req.DecisionID = ""
		}, wantErrSub: "decision-id is required"},
		{name: "mixed source", mutate: func(req *liveFirstOrderCheckRequest) {
			req.SelectPending = true
		}, wantErrSub: "decision-id must be empty"},
		{name: "missing subaccount confirmation", mutate: func(req *liveFirstOrderCheckRequest) {
			req.SubaccountConfirmed = false
		}, wantErrSub: "subaccount-confirmed"},
		{name: "missing execute mirror", mutate: func(req *liveFirstOrderCheckRequest) {
			req.Execute = false
		}, wantErrSub: "execute is required"},
		{name: "two iterations", mutate: func(req *liveFirstOrderCheckRequest) {
			req.MaxIterations = 2
		}, wantErrSub: "max-iterations must be 1"},
		{name: "timeout exceeds runtime", mutate: func(req *liveFirstOrderCheckRequest) {
			req.IterationTimeout = 16 * time.Second
		}, wantErrSub: "iteration-timeout"},
		{name: "invalid readiness pending limit", mutate: func(req *liveFirstOrderCheckRequest) {
			req.ReadinessPendingLimit = 0
		}, wantErrSub: "readiness-pending-limit"},
		{name: "invalid capital", mutate: func(req *liveFirstOrderCheckRequest) {
			req.MaxInitialLiveCapitalUSDT = decimal.Zero
		}, wantErrSub: "max-initial-live-capital-usdt"},
		{name: "invalid micro limit", mutate: func(req *liveFirstOrderCheckRequest) {
			req.MicroCapitalLimitUSDT = decimal.Zero
		}, wantErrSub: "micro-capital-limit-usdt"},
		{name: "blank artifact dir", mutate: func(req *liveFirstOrderCheckRequest) {
			req.ArtifactDir = " "
		}, wantErrSub: "artifact-dir"},
		{name: "untrimmed decision id", mutate: func(req *liveFirstOrderCheckRequest) {
			req.DecisionID = " risk_decision_live_first_order_001 "
		}, wantErrSub: "decision-id must be trimmed"},
		{name: "invalid ops report age", mutate: func(req *liveFirstOrderCheckRequest) {
			req.MaxOpsReportAge = 0
		}, wantErrSub: "max-ops-report-age"},
		{name: "invalid position drift current age", mutate: func(req *liveFirstOrderCheckRequest) {
			req.PositionDriftCurrentMaxAge = 0
		}, wantErrSub: "position-drift-current-max-age"},
		{name: "invalid position drift baseline age", mutate: func(req *liveFirstOrderCheckRequest) {
			req.PositionDriftBaselineMaxAge = 0
		}, wantErrSub: "position-drift-baseline-max-age"},
		{name: "position drift symbols reject item whitespace", mutate: func(req *liveFirstOrderCheckRequest) {
			req.PositionDriftSymbols = "BTCUSDT, ETHUSDT"
		}, wantErrSub: "position-drift-symbols"},
		{name: "position drift symbols reject duplicates", mutate: func(req *liveFirstOrderCheckRequest) {
			req.PositionDriftSymbols = "BTCUSDT,btcusdt"
		}, wantErrSub: "duplicates"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := validLiveFirstOrderCheckRequest()
			tt.mutate(&req)
			_, err := buildLiveFirstOrderCheckBundle(req)
			if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
		})
	}
}

func TestRunLiveFirstOrderCheckPrintOnlyDoesNotCreateArtifactsOrRunCommands(t *testing.T) {
	var output bytes.Buffer
	var ran bool
	err := runLiveFirstOrderCheck(context.Background(), []string{
		"-decision-id", "risk_decision_live_first_order_001",
		"-symbol", "btcusdt",
		"-artifact-dir", filepath.Join("artifacts", "first-order-print"),
		"-subaccount-confirmed",
		"-execute",
		"-print-only",
	}, liveFirstOrderCheckDependencies{
		output: &output,
		mkdirAll: func(string, os.FileMode) error {
			t.Fatalf("print-only must not create artifact directories")
			return nil
		},
		runCommand: func(context.Context, liveFirstOrderCommand, io.Writer) error {
			ran = true
			return nil
		},
	})
	if err != nil {
		t.Fatalf("run print-only: %v\nlogs:\n%s", err, output.String())
	}
	if ran {
		t.Fatalf("print-only must not run child commands")
	}
	for _, want := range []string{
		`"msg":"live first-order check planned"`,
		`"step":"risk-kill-switch-state"`,
		`"step":"live-order-plan"`,
		`"step":"live-deploy-check"`,
		`"msg":"live first-order check final command"`,
		`"msg":"live first-order check post-order command"`,
		`./cmd/live-loop`,
		`./cmd/live-first-order-review`,
	} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("expected output to contain %s, got\n%s", want, output.String())
		}
	}
}

func TestRunLiveFirstOrderCheckExecutesCommandsInOrder(t *testing.T) {
	artifactDir := filepath.Join("artifacts", "first-order-run")
	var output bytes.Buffer
	var mkdirPath string
	var ran []string

	err := runLiveFirstOrderCheck(context.Background(), []string{
		"-decision-id", "risk_decision_live_first_order_001",
		"-artifact-dir", artifactDir,
		"-subaccount-confirmed",
		"-execute",
	}, liveFirstOrderCheckDependencies{
		output: &output,
		mkdirAll: func(path string, perm os.FileMode) error {
			mkdirPath = path
			if perm != defaultLiveFirstOrderArtifactDirPerm {
				t.Fatalf("directory perm mismatch: got %#o want %#o", perm, os.FileMode(defaultLiveFirstOrderArtifactDirPerm))
			}
			return nil
		},
		runCommand: func(ctx context.Context, command liveFirstOrderCommand, output io.Writer) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			ran = append(ran, command.Name)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("run first-order check: %v\nlogs:\n%s", err, output.String())
	}
	if mkdirPath != filepath.Clean(artifactDir) {
		t.Fatalf("mkdir path mismatch: got %q want %q", mkdirPath, filepath.Clean(artifactDir))
	}
	want := []string{"risk-kill-switch-state", "live-order-plan", "live-readiness", "live-loop-audit", "live-deploy-check", "live-handoff-verify", "live-ops-report"}
	if !reflect.DeepEqual(ran, want) {
		t.Fatalf("command order mismatch: got %#v want %#v", ran, want)
	}
	if !strings.Contains(output.String(), `"msg":"live first-order check passed"`) {
		t.Fatalf("expected pass log, got\n%s", output.String())
	}
}

func TestRunLiveFirstOrderCheckStopsOnChildCommandFailure(t *testing.T) {
	var ran []string
	err := runLiveFirstOrderCheck(context.Background(), []string{
		"-decision-id", "risk_decision_live_first_order_001",
		"-subaccount-confirmed",
		"-execute",
	}, liveFirstOrderCheckDependencies{
		output: &bytes.Buffer{},
		runCommand: func(ctx context.Context, command liveFirstOrderCommand, output io.Writer) error {
			ran = append(ran, command.Name)
			if command.Name == "live-readiness" {
				return errors.New("database unavailable")
			}
			return nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "live-readiness") || !strings.Contains(err.Error(), "database unavailable") {
		t.Fatalf("expected live-readiness failure, got %v", err)
	}
	want := []string{"risk-kill-switch-state", "live-order-plan", "live-readiness"}
	if !reflect.DeepEqual(ran, want) {
		t.Fatalf("command order mismatch after failure: got %#v want %#v", ran, want)
	}
}

func TestParseLiveFirstOrderPositiveDecimalFlagTableDriven(t *testing.T) {
	tests := []struct {
		name       string
		value      string
		want       string
		wantErrSub string
	}{
		{name: "valid", value: "100", want: "100"},
		{name: "fraction", value: "0.1", want: "0.1"},
		{name: "blank", value: "", wantErrSub: "required"},
		{name: "untrimmed", value: " 100 ", wantErrSub: "trimmed"},
		{name: "not decimal", value: "abc", wantErrSub: "decimal"},
		{name: "zero", value: "0", wantErrSub: "positive"},
		{name: "negative", value: "-1", wantErrSub: "positive"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseLiveFirstOrderPositiveDecimalFlag("capital", tt.value)
			if tt.wantErrSub != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("parse decimal: %v", err)
			}
			if got.String() != tt.want {
				t.Fatalf("decimal mismatch: got %s want %s", got, tt.want)
			}
		})
	}
}

func validLiveFirstOrderCheckRequest() liveFirstOrderCheckRequest {
	return liveFirstOrderCheckRequest{
		GoBinary:                    "go",
		ConfigPath:                  "configs/live.local.yaml",
		ArtifactDir:                 filepath.Join("artifacts", "live-first-order"),
		DecisionID:                  "risk_decision_live_first_order_001",
		Symbol:                      "BTCUSDT",
		RunID:                       "live_loop_first_order_001",
		OrderType:                   "MARKET",
		SubaccountConfirmed:         true,
		Execute:                     true,
		RequirePending:              true,
		MaxInitialLiveCapitalUSDT:   decimal.RequireFromString("100"),
		MicroCapitalLimitUSDT:       decimal.RequireFromString("100"),
		ReadinessPendingLimit:       1,
		ReadinessAuditLimit:         10,
		AuditLimit:                  10,
		MaxPlanAge:                  10 * time.Minute,
		MaxReadinessAge:             10 * time.Minute,
		MaxAuditAge:                 10 * time.Minute,
		MaxDeployCheckAge:           10 * time.Minute,
		MaxOpsReportAge:             10 * time.Minute,
		PositionDriftCurrentMaxAge:  5 * time.Second,
		PositionDriftBaselineMaxAge: 10 * time.Minute,
		MaxIterations:               1,
		MaxRuntime:                  15 * time.Second,
		IterationTimeout:            10 * time.Second,
	}
}

func liveFirstOrderCommandByName(t *testing.T, commands []liveFirstOrderCommand, name string) liveFirstOrderCommand {
	t.Helper()
	for _, command := range commands {
		if command.Name == name {
			return command
		}
	}
	t.Fatalf("command %q not found in %#v", name, commands)
	return liveFirstOrderCommand{}
}

func assertLiveFirstOrderCommandNames(t *testing.T, commands []liveFirstOrderCommand, want []string) {
	t.Helper()
	got := make([]string, 0, len(commands))
	for _, command := range commands {
		got = append(got, command.Name)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("command names mismatch: got %#v want %#v", got, want)
	}
}

func assertLiveFirstOrderFlag(t *testing.T, args []string, flag string) {
	t.Helper()
	for _, arg := range args {
		if arg == flag {
			return
		}
	}
	t.Fatalf("flag %s not found in %s", flag, liveFirstOrderCommandLine(args))
}

func assertLiveFirstOrderMissingFlag(t *testing.T, args []string, flag string) {
	t.Helper()
	for _, arg := range args {
		if arg == flag {
			t.Fatalf("flag %s unexpectedly found in %s", flag, liveFirstOrderCommandLine(args))
		}
	}
}

func assertLiveFirstOrderFlagValue(t *testing.T, args []string, flag string, want string) {
	t.Helper()
	for i := 0; i < len(args)-1; i++ {
		if args[i] == flag {
			if args[i+1] != want {
				t.Fatalf("flag %s value mismatch: got %q want %q in %s", flag, args[i+1], want, liveFirstOrderCommandLine(args))
			}
			return
		}
	}
	t.Fatalf("flag %s not found in %s", flag, liveFirstOrderCommandLine(args))
}

func Example_liveFirstOrderCommandLine() {
	fmt.Println(liveFirstOrderCommandLine([]string{"go", "run", "./cmd/live-loop", "-config", "configs/live local.yaml"}))
	// Output:
	// go run ./cmd/live-loop -config "configs/live local.yaml"
}
