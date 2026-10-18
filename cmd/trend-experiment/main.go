package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	appresearch "github.com/VersoIt/Inquisitor/internal/app/research"
	"github.com/VersoIt/Inquisitor/internal/exchanges"
	"github.com/VersoIt/Inquisitor/internal/exchanges/bybit/rest"
	"github.com/VersoIt/Inquisitor/internal/marketdata"
	"github.com/VersoIt/Inquisitor/internal/research"
)

const publicDataURL = "https://api.bybit.com"

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	if err := run(ctx, os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("trend-experiment", flag.ContinueOnError)
	planPath := flags.String("plan", "experiments/trend-spot-btc-1h-v1.json", "pre-registered experiment JSON")
	candlesPath := flags.String("candles", "tmp/experiments/trend-v1-candles.json", "immutable candle cache")
	reportPath := flags.String("report", "tmp/experiments/trend-v1-report.json", "new report path; never overwritten")
	download := flags.Bool("download", false, "download public mainnet spot candles, without account keys")
	sourceRoot := flags.String("source-root", ".", "repository root for implementation fingerprint")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments")
	}
	var p research.TrendExperimentPlan
	if err := readJSON(*planPath, &p); err != nil {
		return err
	}
	if err := research.ValidateTrendExperimentPlan(p); err != nil {
		return err
	}
	if p.ValidationEnd.After(time.Now().UTC()) {
		return fmt.Errorf("experiment cannot request a future validation window")
	}
	if _, err := os.Stat(*reportPath); !os.IsNotExist(err) {
		return fmt.Errorf("report path must be new: %s", *reportPath)
	}
	sourceHash, err := experimentSourceHash(*sourceRoot)
	if err != nil {
		return err
	}
	var candles []marketdata.Candle
	if *download {
		if _, err := os.Stat(*candlesPath); !os.IsNotExist(err) {
			return fmt.Errorf("candle cache path must be new: %s", *candlesPath)
		}
		client, err := rest.New(publicDataURL)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "Downloading public spot candles for %s; no account or order API is used.\n", p.ID)
		candles, err = downloadCandles(ctx, client, p, 200*time.Millisecond)
		if err != nil {
			return err
		}
		if err := writeNewJSON(*candlesPath, candles); err != nil {
			return err
		}
	} else if err := readJSON(*candlesPath, &candles); err != nil {
		return err
	}
	report, err := appresearch.RunTrendExperiment(ctx, p, candles)
	if err != nil {
		return err
	}
	report.SourceSHA256 = sourceHash
	artifact := struct {
		GeneratedAt  time.Time
		PublicSource string
		Report       appresearch.TrendExperimentReport
	}{time.Now().UTC(), publicDataURL + "/v5/market/kline", report}
	if err := writeNewJSON(*reportPath, artifact); err != nil {
		return err
	}
	fmt.Fprintf(out, "%s: %s\nCandles: %d\nValidation: trades=%d net=%s PF=%s defined=%t marked_drawdown_pct=%s\nStress net=%s; buy/hold net=%s; cash net=0\nReasons: %s\nReport: %s\nPlan SHA256: %s\nData SHA256: %s\nSource SHA256: %s\n",
		p.ID, report.Verdict, report.CandleCount, report.Validation.Summary.Trades, report.Validation.Summary.NetPnL.String(), report.Validation.Summary.ProfitFactor.String(), report.Validation.Summary.ProfitFactorDefined, report.Validation.MarkedDrawdownPct.String(), report.Stress.Summary.NetPnL.String(), report.BuyAndHold.NetPnL.String(), strings.Join(report.Reasons, ", "), *reportPath, report.PlanSHA256, report.CandlesSHA256, report.SourceSHA256)
	return nil
}

type klineReader interface {
	GetKlines(context.Context, exchanges.KlinesRequest) ([]marketdata.Candle, error)
}

func downloadCandles(ctx context.Context, client klineReader, p research.TrendExperimentPlan, delay time.Duration) ([]marketdata.Candle, error) {
	if err := research.ValidateTrendExperimentPlan(p); err != nil {
		return nil, err
	}
	var result []marketdata.Candle
	for cursor := p.DataStart; cursor.Before(p.ValidationEnd); {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		end := cursor.Add(1000 * time.Hour)
		if end.After(p.ValidationEnd) {
			end = p.ValidationEnd
		}
		rows, err := client.GetKlines(ctx, exchanges.KlinesRequest{Category: p.Category, Symbol: p.Symbol, Interval: p.Interval, Start: cursor, End: end.Add(-time.Millisecond), Limit: 1000})
		if err != nil {
			return nil, fmt.Errorf("download %s: %w", cursor.Format(time.RFC3339), err)
		}
		if len(rows) != int(end.Sub(cursor)/time.Hour) {
			return nil, fmt.Errorf("incomplete public candle page at %s", cursor.Format(time.RFC3339))
		}
		for i, c := range rows {
			if !c.OpenTime.Equal(cursor.Add(time.Duration(i) * time.Hour)) {
				return nil, fmt.Errorf("unordered or missing public candle at %s", cursor.Format(time.RFC3339))
			}
		}
		result = append(result, rows...)
		cursor = end
		if cursor.Before(p.ValidationEnd) && delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, ctx.Err()
			case <-timer.C:
			}
		}
	}
	if err := research.ValidateExperimentCandles(p, result); err != nil {
		return nil, err
	}
	return result, nil
}

func readJSON(path string, target any) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	d := json.NewDecoder(f)
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		return fmt.Errorf("decode %s: %w", path, err)
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("trailing JSON in %s", path)
	}
	return nil
}

func writeNewJSON(path string, value any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	e := json.NewEncoder(f)
	e.SetIndent("", "  ")
	if err := e.Encode(value); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

func experimentSourceHash(root string) (string, error) {
	paths := []string{"go.mod", "go.sum"}
	for _, dir := range []string{"cmd/trend-experiment", "internal/app/research", "internal/research", "internal/strategy", "internal/features", "internal/backtest", "internal/marketdata", "internal/exchanges"} {
		err := filepath.WalkDir(filepath.Join(root, dir), func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			if strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
				rel, err := filepath.Rel(root, path)
				if err != nil {
					return err
				}
				paths = append(paths, filepath.ToSlash(rel))
			}
			return nil
		})
		if err != nil {
			return "", err
		}
	}
	sort.Strings(paths)
	hash := sha256.New()
	for _, path := range paths {
		raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		if err != nil {
			return "", err
		}
		fmt.Fprintf(hash, "%s\x00", path)
		hash.Write(raw)
		hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}
