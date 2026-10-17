package strategy_test

import (
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/VersoIt/Inquisitor/internal/paper"
	"github.com/VersoIt/Inquisitor/internal/regime"
	"github.com/VersoIt/Inquisitor/internal/research"
	"github.com/VersoIt/Inquisitor/internal/risk"
	"github.com/VersoIt/Inquisitor/internal/strategy"
	"github.com/VersoIt/Inquisitor/internal/testutil"
	"github.com/shopspring/decimal"
)

func TestPaperApprovalResolvesCurrentPersistedEvidence(t *testing.T) {
	f := testutil.PaperApproval(t)
	got, err := strategy.ResolvePaperAuthorization(strategy.PaperAuthorization{Grant: f.Grant, Evidence: f.Evidence}, f.Policy, risk.ModePaper, "BTCUSDT", "1", regime.RegimeTrendUp, f.Now)
	if err != nil {
		t.Fatal(err)
	}
	if got.Mode != risk.ModePaper || !got.Enabled || got.HypothesisID != f.Grant.HypothesisName || got.Version != f.Grant.HypothesisVersion || got.StrategyName != strategy.NameTrend || !got.MaxRiskPct.Equal(decimal.RequireFromString("0.25")) || got.MinConfidence != 70 {
		t.Fatalf("incorrect persisted snapshot %+v", got)
	}
}

func TestPaperApprovalRejectsChangedOrInsufficientEvidence(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*testutil.ApprovalFixture)
	}{
		{"grant hash", func(f *testutil.ApprovalFixture) { f.Grant.HypothesisSHA256 = strings.Repeat("0", 64) }},
		{"incompatible strategy family", func(f *testutil.ApprovalFixture) { f.Grant.StrategyName = strategy.NameMeanReversion }},
		{"missing reviewer", func(f *testutil.ApprovalFixture) { f.Grant.ReviewedBy = "" }},
		{"missing reason", func(f *testutil.ApprovalFixture) { f.Grant.Reason = "" }},
		{"unsupported strategy", func(f *testutil.ApprovalFixture) { f.Grant.StrategyName = "other" }},
		{"non UTC grant", func(f *testutil.ApprovalFixture) {
			f.Grant.GrantedAt = f.Grant.GrantedAt.In(time.FixedZone("offset", 3600))
		}},
		{"run hash", func(f *testutil.ApprovalFixture) { f.Evidence.Run.HypothesisContentSHA256 = strings.Repeat("0", 64) }},
		{"run version", func(f *testutil.ApprovalFixture) { f.Evidence.Run.HypothesisVersion = "v2" }},
		{"result identity", func(f *testutil.ApprovalFixture) { f.Evidence.Result.RunID = "other_run_0001" }},
		{"validation identity", func(f *testutil.ApprovalFixture) { f.Grant.ValidationID = "other_validation" }},
		{"changed parsed specification", func(f *testutil.ApprovalFixture) { f.Evidence.Hypothesis.Spec.Risk.MaxRiskPerTradePct = 0.5 }},
		{"tampered raw YAML", func(f *testutil.ApprovalFixture) { f.Evidence.Hypothesis.RawYAML += "\n#changed" }},
		{"failed run", func(f *testutil.ApprovalFixture) { f.Evidence.Run.Status = research.StatusFailed }},
		{"rejected result", func(f *testutil.ApprovalFixture) { f.Evidence.Result.Outcome = research.OutcomeRejected }},
		{"stopped paper", func(f *testutil.ApprovalFixture) {
			f.Evidence.Validation.Status = paper.ValidationStatusCancelled
			f.Evidence.Validation.CancelledAt = f.Now
			f.Evidence.Validation.StatusReason = "stopped"
		}},
		{"scope not researched", func(f *testutil.ApprovalFixture) { f.Grant.Symbol = "ETHUSDT" }},
		{"interval not researched", func(f *testutil.ApprovalFixture) { f.Grant.Interval = "5" }},
		{"exchange mismatch", func(f *testutil.ApprovalFixture) { f.Grant.Exchange = "other" }},
		{"result after paper plan", func(f *testutil.ApprovalFixture) { f.Evidence.Result.RecordedAt = f.Now }},
		{"future paper start", func(f *testutil.ApprovalFixture) { f.Evidence.Validation.StartedAt = f.Now.Add(time.Second) }},
		{"future grant", func(f *testutil.ApprovalFixture) { f.Grant.GrantedAt = f.Now.Add(time.Second) }},
		{"too few trades", func(f *testutil.ApprovalFixture) { f.Policy.MinTrades = 101 }},
		{"weak overall PF", func(f *testutil.ApprovalFixture) { f.Evidence.Result.Metrics.ProfitFactor = "1.01" }},
		{"weak overall expectancy", func(f *testutil.ApprovalFixture) { f.Evidence.Result.Metrics.Expectancy = "0" }},
		{"weak OOS PF", func(f *testutil.ApprovalFixture) { f.Evidence.Result.Metrics.OutOfSampleProfitFactor = "1" }},
		{"undefined OOS PF", func(f *testutil.ApprovalFixture) { f.Evidence.Result.Metrics.OutOfSampleProfitFactorDefined = false }},
		{"negative OOS PnL", func(f *testutil.ApprovalFixture) { f.Evidence.Result.Metrics.OutOfSampleNetPnL = "-1" }},
		{"OOS drawdown exceeded", func(f *testutil.ApprovalFixture) { f.Evidence.Result.Metrics.OutOfSampleMaxDrawdownPct = 16 }},
		{"nonfinite OOS drawdown", func(f *testutil.ApprovalFixture) { f.Evidence.Result.Metrics.OutOfSampleMaxDrawdownPct = math.NaN() }},
		{"failed fold", func(f *testutil.ApprovalFixture) {
			f.Evidence.Result.Metrics.WalkForwardPassedFolds = 2
			f.Evidence.Result.Metrics.WalkForwardFailedFolds = 1
		}},
		{"missing folds", func(f *testutil.ApprovalFixture) {
			f.Evidence.Result.Metrics.WalkForwardFolds = 0
			f.Evidence.Result.Metrics.WalkForwardPassedFolds = 0
		}},
		{"missing costs", func(f *testutil.ApprovalFixture) { f.Evidence.Result.Metrics.SlippageIncluded = false }},
		{"disabled gates", func(f *testutil.ApprovalFixture) { f.Policy.Enabled = false }},
		{"weak policy PF", func(f *testutil.ApprovalFixture) { f.Policy.MinProfitFactor = decimal.NewFromInt(1) }},
		{"nonfinite policy", func(f *testutil.ApprovalFixture) { f.Policy.MaxDrawdownPct = math.NaN() }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := testutil.PaperApproval(t)
			tt.mutate(&f)
			_, err := strategy.ResolvePaperAuthorization(strategy.PaperAuthorization{Grant: f.Grant, Evidence: f.Evidence}, f.Policy, risk.ModePaper, "BTCUSDT", "1", regime.RegimeTrendUp, f.Now)
			if !errors.Is(err, strategy.ErrApprovalUnavailable) {
				t.Fatalf("expected denied approval, got %v", err)
			}
		})
	}
}

func TestPaperApprovalCannotAuthorizeLiveRevocationOrOtherScope(t *testing.T) {
	f := testutil.PaperApproval(t)
	for _, tt := range []struct {
		name             string
		mode             risk.Mode
		symbol, interval string
		regime           regime.Regime
		revocation       strategy.ApprovalRevocation
	}{
		{"live", risk.ModeLive, "BTCUSDT", "1", regime.RegimeTrendUp, strategy.ApprovalRevocation{}},
		{"wrong symbol", risk.ModePaper, "ETHUSDT", "1", regime.RegimeTrendUp, strategy.ApprovalRevocation{}},
		{"wrong timeframe", risk.ModePaper, "BTCUSDT", "5", regime.RegimeTrendUp, strategy.ApprovalRevocation{}},
		{"blocked regime", risk.ModePaper, "BTCUSDT", "1", regime.RegimeChaos, strategy.ApprovalRevocation{}},
		{"revoked", risk.ModePaper, "BTCUSDT", "1", regime.RegimeTrendUp, strategy.ApprovalRevocation{RevokedAt: f.Now, RevokedBy: "operator", Reason: "stop"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, err := strategy.ResolvePaperAuthorization(strategy.PaperAuthorization{Grant: f.Grant, Evidence: f.Evidence, Revocation: tt.revocation}, f.Policy, tt.mode, tt.symbol, tt.interval, tt.regime, f.Now)
			if !errors.Is(err, strategy.ErrApprovalUnavailable) || got.Enabled || got.Mode != "" {
				t.Fatalf("unsafe snapshot %+v, %v", got, err)
			}
		})
	}
}
