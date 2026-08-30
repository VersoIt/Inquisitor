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
)

func TestBuildLivePostOrderCheckBundleTableDriven(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*livePostOrderCheckRequest)
		assert func(*testing.T, livePostOrderCheckBundle)
	}{
		{
			name: "default first-order artifact bundle",
			assert: func(t *testing.T, bundle livePostOrderCheckBundle) {
				assertLivePostOrderCommandNames(t, bundle.Commands, []string{"live-first-order-review", "live-ops-report"})

				review := livePostOrderCommandByName(t, bundle.Commands, "live-first-order-review")
				assertLivePostOrderFlagValue(t, review.Args, "-config", "configs/live.local.yaml")
				assertLivePostOrderFlagValue(t, review.Args, "-plan-file", bundle.PlanFile)
				assertLivePostOrderFlagValue(t, review.Args, "-status-limit", "5")
				assertLivePostOrderFlagValue(t, review.Args, "-position-limit", "5")
				assertLivePostOrderFlagValue(t, review.Args, "-artifact-path", bundle.ReviewFile)

				ops := livePostOrderCommandByName(t, bundle.Commands, "live-ops-report")
				assertLivePostOrderFlagValue(t, ops.Args, "-config", "configs/live.local.yaml")
				assertLivePostOrderFlagValue(t, ops.Args, "-pending-limit", "10")
				assertLivePostOrderFlagValue(t, ops.Args, "-audit-limit", "10")
				assertLivePostOrderFlagValue(t, ops.Args, "-first-order-review-file", bundle.ReviewFile)
				assertLivePostOrderFlagValue(t, ops.Args, "-max-first-order-review-age", "24h0m0s")
				assertLivePostOrderFlag(t, ops.Args, "-allow-no-pending")
				assertLivePostOrderFlagValue(t, ops.Args, "-artifact-path", bundle.OpsReportFile)
				assertLivePostOrderFlag(t, ops.Args, "-fail-on-non-clear")
				assertLivePostOrderMissingFlag(t, ops.Args, "-position-drift")

				if bundle.ReviewCommand.Name != "live-first-order-review" || bundle.OpsCommand.Name != "live-ops-report" {
					t.Fatalf("named command mismatch: %#v", bundle)
				}
			},
		},
		{
			name: "explicit paths and symbol are normalized",
			mutate: func(req *livePostOrderCheckRequest) {
				req.PlanFile = filepath.Join("artifacts", "custom", "plan.json")
				req.ReviewFile = filepath.Join("artifacts", "custom", "review.json")
				req.OpsReportFile = filepath.Join("artifacts", "custom", "ops.json")
				req.Symbol = "btcusdt"
				req.StatusLimit = 7
				req.PositionLimit = 8
				req.PendingLimit = 9
				req.AuditLimit = 11
				req.MaxFirstOrderReviewAge = 3 * time.Hour
			},
			assert: func(t *testing.T, bundle livePostOrderCheckBundle) {
				if bundle.PlanFile != filepath.Join("artifacts", "custom", "plan.json") ||
					bundle.ReviewFile != filepath.Join("artifacts", "custom", "review.json") ||
					bundle.OpsReportFile != filepath.Join("artifacts", "custom", "ops.json") {
					t.Fatalf("explicit artifact paths mismatch: %#v", bundle)
				}
				review := livePostOrderCommandByName(t, bundle.Commands, "live-first-order-review")
				assertLivePostOrderFlagValue(t, review.Args, "-status-limit", "7")
				assertLivePostOrderFlagValue(t, review.Args, "-position-limit", "8")

				ops := livePostOrderCommandByName(t, bundle.Commands, "live-ops-report")
				assertLivePostOrderFlagValue(t, ops.Args, "-symbol", "BTCUSDT")
				assertLivePostOrderFlagValue(t, ops.Args, "-pending-limit", "9")
				assertLivePostOrderFlagValue(t, ops.Args, "-audit-limit", "11")
				assertLivePostOrderFlagValue(t, ops.Args, "-max-first-order-review-age", "3h0m0s")
			},
		},
		{
			name: "position drift opts into post-order exchange check",
			mutate: func(req *livePostOrderCheckRequest) {
				req.PositionDrift = true
				req.PositionDriftSymbols = "btcusdt,ethusdt"
				req.PositionDriftCurrentMaxAge = 3 * time.Second
				req.PositionDriftBaselineMaxAge = 7 * time.Minute
			},
			assert: func(t *testing.T, bundle livePostOrderCheckBundle) {
				ops := livePostOrderCommandByName(t, bundle.Commands, "live-ops-report")
				assertLivePostOrderFlag(t, ops.Args, "-position-drift")
				assertLivePostOrderFlagValue(t, ops.Args, "-position-drift-symbols", "BTCUSDT,ETHUSDT")
				assertLivePostOrderFlagValue(t, ops.Args, "-position-drift-current-max-age", "3s")
				assertLivePostOrderFlagValue(t, ops.Args, "-position-drift-baseline-max-age", "7m0s")
				if !bundle.PositionDrift || bundle.KillSwitchable {
					t.Fatalf("drift flags mismatch: %#v", bundle)
				}
			},
		},
		{
			name: "position drift symbols imply drift",
			mutate: func(req *livePostOrderCheckRequest) {
				req.PositionDriftSymbols = "solusdt"
			},
			assert: func(t *testing.T, bundle livePostOrderCheckBundle) {
				ops := livePostOrderCommandByName(t, bundle.Commands, "live-ops-report")
				assertLivePostOrderFlag(t, ops.Args, "-position-drift")
				assertLivePostOrderFlagValue(t, ops.Args, "-position-drift-symbols", "SOLUSDT")
				assertLivePostOrderFlagValue(t, ops.Args, "-position-drift-current-max-age", "5s")
				assertLivePostOrderFlagValue(t, ops.Args, "-position-drift-baseline-max-age", "10m0s")
				if !bundle.PositionDrift {
					t.Fatalf("symbols must imply position drift: %#v", bundle)
				}
			},
		},
		{
			name: "position drift kill switch activation implies drift",
			mutate: func(req *livePostOrderCheckRequest) {
				req.PositionDriftKillSwitch = true
			},
			assert: func(t *testing.T, bundle livePostOrderCheckBundle) {
				ops := livePostOrderCommandByName(t, bundle.Commands, "live-ops-report")
				assertLivePostOrderFlag(t, ops.Args, "-position-drift")
				assertLivePostOrderFlag(t, ops.Args, "-activate-kill-switch-on-position-drift-blocked")
				if !bundle.PositionDrift || !bundle.KillSwitchable {
					t.Fatalf("kill switch drift flags mismatch: %#v", bundle)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := validLivePostOrderCheckRequest()
			if tt.mutate != nil {
				tt.mutate(&req)
			}
			bundle, err := buildLivePostOrderCheckBundle(req)
			if err != nil {
				t.Fatalf("build bundle: %v", err)
			}
			if bundle.ArtifactDir != filepath.Clean(req.ArtifactDir) ||
				bundle.PlanFile == "" ||
				bundle.ReviewFile == "" ||
				bundle.OpsReportFile == "" {
				t.Fatalf("artifact path mismatch: %#v", bundle)
			}
			tt.assert(t, bundle)
		})
	}
}

func TestBuildLivePostOrderCheckBundleRejectsUnsafeFlagsTableDriven(t *testing.T) {
	tests := []struct {
		name       string
		mutate     func(*livePostOrderCheckRequest)
		wantErrSub string
	}{
		{name: "missing go", mutate: func(req *livePostOrderCheckRequest) {
			req.GoBinary = ""
		}, wantErrSub: "go is required"},
		{name: "blank config", mutate: func(req *livePostOrderCheckRequest) {
			req.ConfigPath = " "
		}, wantErrSub: "config"},
		{name: "blank artifact dir", mutate: func(req *livePostOrderCheckRequest) {
			req.ArtifactDir = " "
		}, wantErrSub: "artifact-dir"},
		{name: "untrimmed plan path", mutate: func(req *livePostOrderCheckRequest) {
			req.PlanFile = " artifacts/live-order-plan.json "
		}, wantErrSub: "plan-file"},
		{name: "untrimmed review path", mutate: func(req *livePostOrderCheckRequest) {
			req.ReviewFile = " artifacts/live-first-order-review.json "
		}, wantErrSub: "review-artifact"},
		{name: "untrimmed ops path", mutate: func(req *livePostOrderCheckRequest) {
			req.OpsReportFile = " artifacts/live-post-order-ops-report.json "
		}, wantErrSub: "ops-artifact"},
		{name: "invalid status limit", mutate: func(req *livePostOrderCheckRequest) {
			req.StatusLimit = 0
		}, wantErrSub: "status-limit"},
		{name: "status limit above max", mutate: func(req *livePostOrderCheckRequest) {
			req.StatusLimit = 101
		}, wantErrSub: "status-limit"},
		{name: "invalid position limit", mutate: func(req *livePostOrderCheckRequest) {
			req.PositionLimit = 0
		}, wantErrSub: "position-limit"},
		{name: "invalid pending limit", mutate: func(req *livePostOrderCheckRequest) {
			req.PendingLimit = 0
		}, wantErrSub: "pending-limit"},
		{name: "invalid audit limit", mutate: func(req *livePostOrderCheckRequest) {
			req.AuditLimit = 0
		}, wantErrSub: "audit-limit"},
		{name: "invalid review age", mutate: func(req *livePostOrderCheckRequest) {
			req.MaxFirstOrderReviewAge = 0
		}, wantErrSub: "max-first-order-review-age"},
		{name: "invalid position drift current age", mutate: func(req *livePostOrderCheckRequest) {
			req.PositionDriftCurrentMaxAge = 0
		}, wantErrSub: "position-drift-current-max-age"},
		{name: "invalid position drift baseline age", mutate: func(req *livePostOrderCheckRequest) {
			req.PositionDriftBaselineMaxAge = 0
		}, wantErrSub: "position-drift-baseline-max-age"},
		{name: "position drift symbols reject item whitespace", mutate: func(req *livePostOrderCheckRequest) {
			req.PositionDriftSymbols = "BTCUSDT, ETHUSDT"
		}, wantErrSub: "position-drift-symbols"},
		{name: "position drift symbols reject duplicates", mutate: func(req *livePostOrderCheckRequest) {
			req.PositionDriftSymbols = "BTCUSDT,btcusdt"
		}, wantErrSub: "duplicates"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := validLivePostOrderCheckRequest()
			tt.mutate(&req)
			_, err := buildLivePostOrderCheckBundle(req)
			if err == nil || !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Fatalf("expected error containing %q, got %v", tt.wantErrSub, err)
			}
		})
	}
}

func TestRunLivePostOrderCheckPrintOnlyDoesNotCreateArtifactsOrRunCommands(t *testing.T) {
	var output bytes.Buffer
	var ran bool
	err := runLivePostOrderCheck(context.Background(), []string{
		"-artifact-dir", filepath.Join("artifacts", "post-order-print"),
		"-symbol", "btcusdt",
		"-print-only",
	}, livePostOrderCheckDependencies{
		output: &output,
		mkdirAll: func(string, os.FileMode) error {
			t.Fatalf("print-only must not create artifact directories")
			return nil
		},
		runCommand: func(context.Context, livePostOrderCommand, io.Writer) error {
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
		`"msg":"live post-order check planned"`,
		`"step":"live-first-order-review"`,
		`"step":"live-ops-report"`,
		`./cmd/live-first-order-review`,
		`./cmd/live-ops-report`,
		`-allow-no-pending`,
		`"commands":2`,
	} {
		if !strings.Contains(output.String(), want) {
			t.Fatalf("expected output to contain %s, got\n%s", want, output.String())
		}
	}
}

func TestRunLivePostOrderCheckExecutesCommandsInOrder(t *testing.T) {
	artifactDir := filepath.Join("artifacts", "post-order-run")
	var output bytes.Buffer
	var mkdirPath string
	var ran []string

	err := runLivePostOrderCheck(context.Background(), []string{
		"-artifact-dir", artifactDir,
		"-symbol", "BTCUSDT",
	}, livePostOrderCheckDependencies{
		output: &output,
		mkdirAll: func(path string, perm os.FileMode) error {
			mkdirPath = path
			if perm != defaultLivePostOrderArtifactDirPerm {
				t.Fatalf("directory perm mismatch: got %#o want %#o", perm, os.FileMode(defaultLivePostOrderArtifactDirPerm))
			}
			return nil
		},
		runCommand: func(ctx context.Context, command livePostOrderCommand, output io.Writer) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			ran = append(ran, command.Name)
			return nil
		},
	})
	if err != nil {
		t.Fatalf("run post-order check: %v\nlogs:\n%s", err, output.String())
	}
	if mkdirPath != filepath.Clean(artifactDir) {
		t.Fatalf("mkdir path mismatch: got %q want %q", mkdirPath, filepath.Clean(artifactDir))
	}
	want := []string{"live-first-order-review", "live-ops-report"}
	if !reflect.DeepEqual(ran, want) {
		t.Fatalf("command order mismatch: got %#v want %#v", ran, want)
	}
	if !strings.Contains(output.String(), `"msg":"live post-order check passed"`) {
		t.Fatalf("expected pass log, got\n%s", output.String())
	}
}

func TestRunLivePostOrderCheckStopsOnChildCommandFailure(t *testing.T) {
	var ran []string
	err := runLivePostOrderCheck(context.Background(), []string{
		"-symbol", "BTCUSDT",
	}, livePostOrderCheckDependencies{
		output: &bytes.Buffer{},
		mkdirAll: func(string, os.FileMode) error {
			return nil
		},
		runCommand: func(ctx context.Context, command livePostOrderCommand, output io.Writer) error {
			ran = append(ran, command.Name)
			if command.Name == "live-first-order-review" {
				return errors.New("first-order evidence incomplete")
			}
			return nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "live-first-order-review") || !strings.Contains(err.Error(), "first-order evidence incomplete") {
		t.Fatalf("expected live-first-order-review failure, got %v", err)
	}
	want := []string{"live-first-order-review"}
	if !reflect.DeepEqual(ran, want) {
		t.Fatalf("command order mismatch after failure: got %#v want %#v", ran, want)
	}
}

func TestRunLivePostOrderCheckRejectsUnsafeFlagsBeforeSideEffects(t *testing.T) {
	var madeDir bool
	var ran bool
	err := runLivePostOrderCheck(context.Background(), []string{
		"-timeout", "0s",
	}, livePostOrderCheckDependencies{
		output: &bytes.Buffer{},
		mkdirAll: func(string, os.FileMode) error {
			madeDir = true
			return nil
		},
		runCommand: func(context.Context, livePostOrderCommand, io.Writer) error {
			ran = true
			return nil
		},
	})
	if err == nil || !strings.Contains(err.Error(), "timeout") {
		t.Fatalf("expected timeout error, got %v", err)
	}
	if madeDir || ran {
		t.Fatalf("unsafe flags must stop before side effects: mkdir=%t ran=%t", madeDir, ran)
	}
}

func validLivePostOrderCheckRequest() livePostOrderCheckRequest {
	return livePostOrderCheckRequest{
		GoBinary:                    "go",
		ConfigPath:                  "configs/live.local.yaml",
		ArtifactDir:                 filepath.Join("artifacts", "live-first-order"),
		StatusLimit:                 5,
		PositionLimit:               5,
		PendingLimit:                10,
		AuditLimit:                  10,
		MaxFirstOrderReviewAge:      24 * time.Hour,
		PositionDriftCurrentMaxAge:  5 * time.Second,
		PositionDriftBaselineMaxAge: 10 * time.Minute,
	}
}

func livePostOrderCommandByName(t *testing.T, commands []livePostOrderCommand, name string) livePostOrderCommand {
	t.Helper()
	for _, command := range commands {
		if command.Name == name {
			return command
		}
	}
	t.Fatalf("command %q not found in %#v", name, commands)
	return livePostOrderCommand{}
}

func assertLivePostOrderCommandNames(t *testing.T, commands []livePostOrderCommand, want []string) {
	t.Helper()
	got := make([]string, 0, len(commands))
	for _, command := range commands {
		got = append(got, command.Name)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("command names mismatch: got %#v want %#v", got, want)
	}
}

func assertLivePostOrderFlag(t *testing.T, args []string, flag string) {
	t.Helper()
	for _, arg := range args {
		if arg == flag {
			return
		}
	}
	t.Fatalf("flag %s not found in %s", flag, livePostOrderCommandLine(args))
}

func assertLivePostOrderMissingFlag(t *testing.T, args []string, flag string) {
	t.Helper()
	for _, arg := range args {
		if arg == flag {
			t.Fatalf("flag %s unexpectedly found in %s", flag, livePostOrderCommandLine(args))
		}
	}
}

func assertLivePostOrderFlagValue(t *testing.T, args []string, flag string, want string) {
	t.Helper()
	for i := 0; i < len(args)-1; i++ {
		if args[i] == flag {
			if args[i+1] != want {
				t.Fatalf("flag %s value mismatch: got %q want %q in %s", flag, args[i+1], want, livePostOrderCommandLine(args))
			}
			return
		}
	}
	t.Fatalf("flag %s not found in %s", flag, livePostOrderCommandLine(args))
}

func Example_livePostOrderCommandLine() {
	fmt.Println(livePostOrderCommandLine([]string{"go", "run", "./cmd/live-post-order-check", "-artifact-dir", "artifacts/live first order"}))
	// Output:
	// go run ./cmd/live-post-order-check -artifact-dir "artifacts/live first order"
}
