package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	appresearch "github.com/VersoIt/Inquisitor/internal/app/research"
)

type sourceArtifact struct {
	GeneratedAt  time.Time
	PublicSource string
	Report       appresearch.TrendExperimentReport
}

type diagnosticArtifact struct {
	SourceArtifactSHA256                                string
	SourceModelVersion, SourceCodeSHA256, CandlesSHA256 string
	Diagnostics                                         appresearch.DevelopmentDiagnostics
}

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// Offline only: consumes frozen evidence and cannot submit orders or retune it.
func run(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("trend-diagnose", flag.ContinueOnError)
	source := flags.String("source", "docs/experiments/trend-spot-btc-1h-v1-result.json", "frozen experiment artifact")
	output := flags.String("output", "tmp/experiments/trend-v1-development-diagnostics.json", "new diagnostic artifact; never overwritten")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	raw, err := os.ReadFile(*source)
	if err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var sourceReport sourceArtifact
	if err := decoder.Decode(&sourceReport); err != nil {
		return err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("trailing source JSON")
	}
	r := sourceReport.Report
	if r.ModelVersion != "trend-spot-candle-v1" {
		return fmt.Errorf("unsupported source model")
	}
	for _, fingerprint := range []string{r.SourceSHA256, r.CandlesSHA256} {
		decoded, err := hex.DecodeString(fingerprint)
		if err != nil || len(decoded) != sha256.Size {
			return fmt.Errorf("missing or invalid source fingerprint")
		}
	}
	d, err := appresearch.DiagnoseDevelopment(ctx, appresearch.DevelopmentDiagnosticInput{Plan: r.Plan, PlanSHA256: r.PlanSHA256, Development: r.Development})
	if err != nil {
		return err
	}
	sum := sha256.Sum256(raw)
	artifact := diagnosticArtifact{SourceArtifactSHA256: hex.EncodeToString(sum[:]), SourceModelVersion: r.ModelVersion, SourceCodeSHA256: r.SourceSHA256, CandlesSHA256: r.CandlesSHA256, Diagnostics: d}
	payload, err := json.MarshalIndent(artifact, "", "  ")
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(*output), 0755); err != nil {
		return err
	}
	f, err := os.OpenFile(*output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	_, writeErr := f.Write(append(payload, '\n'))
	closeErr := f.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	_, err = fmt.Fprintf(out, "Development only: %s to %s\nTrades=%d mid-price PnL=%s entry impact=%s exit impact=%s fees=%s net=%s\nOutput: %s\nNo retuning or paper/live approval.\n", d.Start.Format(time.RFC3339), d.End.Format(time.RFC3339), d.Totals.Trades, d.Totals.MidPricePnL, d.Totals.EntryImpact, d.Totals.ExitImpact, d.Totals.Fees, d.Totals.NetPnL, *output)
	return err
}
