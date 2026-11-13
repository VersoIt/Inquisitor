package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	appresearch "github.com/VersoIt/Inquisitor/internal/app/research"
	"github.com/shopspring/decimal"
)

func frozenSource(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", "docs", "experiments", "trend-spot-btc-1h-v1-result.json"))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestDiagnosticCommandFrozenDevelopmentIsDeterministicAndImmutable(t *testing.T) {
	dir := t.TempDir()
	source, output := filepath.Join(dir, "source.json"), filepath.Join(dir, "diagnostic.json")
	raw := frozenSource(t)
	if err := os.WriteFile(source, raw, 0600); err != nil {
		t.Fatal(err)
	}
	args := []string{"-source", source, "-output", output}
	var stdout bytes.Buffer
	if err := run(context.Background(), args, &stdout); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stdout.String(), "Trades=135") || !strings.Contains(stdout.String(), "No retuning") {
		t.Fatal(stdout.String())
	}
	result, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	var artifact diagnosticArtifact
	if err := json.Unmarshal(result, &artifact); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	if artifact.SourceArtifactSHA256 != hex.EncodeToString(sum[:]) || artifact.Diagnostics.Totals.Trades != 135 || !artifact.Diagnostics.Totals.NetPnL.Equal(decimal.RequireFromString("-14.52312223531766694124551061786")) {
		t.Fatal("wrong provenance or frozen result")
	}
	goldenRaw, err := os.ReadFile(filepath.Join("..", "..", "docs", "experiments", "trend-spot-btc-1h-v1-development-diagnostics.json"))
	if err != nil {
		t.Fatal(err)
	}
	var golden diagnosticArtifact
	if err := json.Unmarshal(goldenRaw, &golden); err != nil {
		t.Fatal(err)
	}
	actualDiagnostic, err := json.Marshal(artifact.Diagnostics)
	if err != nil {
		t.Fatal(err)
	}
	expectedDiagnostic, err := json.Marshal(golden.Diagnostics)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(actualDiagnostic, expectedDiagnostic) {
		t.Fatal("frozen development attribution changed")
	}
	if err := run(context.Background(), args, io.Discard); err == nil {
		t.Fatal("overwrote diagnostic evidence")
	}
	after, err := os.ReadFile(output)
	if err != nil || !bytes.Equal(result, after) {
		t.Fatal("output mutated")
	}
	if err := run(context.Background(), []string{"-source", source, "-output", source}, io.Discard); err == nil {
		t.Fatal("overwrote source")
	}
	after, err = os.ReadFile(source)
	if err != nil || !bytes.Equal(raw, after) {
		t.Fatal("source mutated")
	}
	repeat := filepath.Join(dir, "repeat.json")
	if err := run(context.Background(), []string{"-source", source, "-output", repeat}, io.Discard); err != nil {
		t.Fatal(err)
	}
	again, err := os.ReadFile(repeat)
	if err != nil || !bytes.Equal(result, again) {
		t.Fatal("non-deterministic artifact")
	}

	// Poison all non-development evidence: attribution must remain unchanged.
	var poisoned sourceArtifact
	if err := json.Unmarshal(raw, &poisoned); err != nil {
		t.Fatal(err)
	}
	poisoned.Report.Validation = appresearch.TrendExperimentPeriod{}
	poisoned.Report.Stress = appresearch.TrendExperimentPeriod{}
	poisoned.Report.ValidationBlocks = nil
	poisoned.Report.Verdict = "NOT_AN_APPROVAL"
	poisonRaw, err := json.Marshal(poisoned)
	if err != nil {
		t.Fatal(err)
	}
	poisonPath, poisonOut := filepath.Join(dir, "poison.json"), filepath.Join(dir, "poison-out.json")
	if err := os.WriteFile(poisonPath, poisonRaw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := run(context.Background(), []string{"-source", poisonPath, "-output", poisonOut}, io.Discard); err != nil {
		t.Fatal(err)
	}
	poisonResult, err := os.ReadFile(poisonOut)
	if err != nil {
		t.Fatal(err)
	}
	var poisonArtifact diagnosticArtifact
	if err := json.Unmarshal(poisonResult, &poisonArtifact); err != nil {
		t.Fatal(err)
	}
	a, _ := json.Marshal(artifact.Diagnostics)
	b, _ := json.Marshal(poisonArtifact.Diagnostics)
	if !bytes.Equal(a, b) || artifact.SourceArtifactSHA256 == poisonArtifact.SourceArtifactSHA256 {
		t.Fatal("validation leaked or provenance was lost")
	}
}

func TestDiagnosticCommandRejectsInvalidEvidenceWithoutOutput(t *testing.T) {
	for _, tc := range []struct {
		name string
		raw  func() []byte
	}{
		{"broken JSON", func() []byte { return []byte("{") }},
		{"unknown field", func() []byte { return []byte(`{"Unknown":1}`) }},
		{"trailing JSON", func() []byte { return append(frozenSource(t), []byte(` {}`)...) }},
		{"wrong model", func() []byte {
			return bytes.ReplaceAll(frozenSource(t), []byte("trend-spot-candle-v1"), []byte("unknown"))
		}},
		{"missing fingerprint", func() []byte {
			var a sourceArtifact
			if err := json.Unmarshal(frozenSource(t), &a); err != nil {
				t.Fatal(err)
			}
			a.Report.CandlesSHA256 = ""
			raw, _ := json.Marshal(a)
			return raw
		}},
		{"corrupted ledger", func() []byte {
			var a sourceArtifact
			if err := json.Unmarshal(frozenSource(t), &a); err != nil {
				t.Fatal(err)
			}
			a.Report.Development.Trades[0].Trade.NetPnL = decimal.Zero
			raw, _ := json.Marshal(a)
			return raw
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			source, output := filepath.Join(dir, "source.json"), filepath.Join(dir, "out.json")
			if err := os.WriteFile(source, tc.raw(), 0600); err != nil {
				t.Fatal(err)
			}
			if err := run(context.Background(), []string{"-source", source, "-output", output}, io.Discard); err == nil {
				t.Fatal("invalid source accepted")
			}
			if _, err := os.Stat(output); !os.IsNotExist(err) {
				t.Fatal("invalid evidence produced output")
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := run(ctx, nil, io.Discard); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := run(context.Background(), []string{"unexpected"}, io.Discard); err == nil {
		t.Fatal("positional arguments accepted")
	}
}
