package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/shopspring/decimal"

	"github.com/VersoIt/Inquisitor/internal/config"
	domainlive "github.com/VersoIt/Inquisitor/internal/live"
)

func TestRunLiveFirstOrderReviewWritesReadyArtifact(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	plan := validLiveFirstOrderReviewCLIPlan(now)
	planFile := writeLiveFirstOrderReviewCLIPlanArtifact(t, plan)
	artifactPath := filepath.Join(t.TempDir(), "artifacts", "live-first-order-review.json")
	reader := &fakeLiveFirstOrderReviewCLIReader{evidence: validLiveFirstOrderReviewCLIEvidence(t, plan)}
	db, mock := newLiveFirstOrderReviewCLISQLMock(t)
	defer db.Close()

	var output bytes.Buffer
	err := runLiveFirstOrderReview(context.Background(), []string{
		"-config", "configs/live.local.yaml",
		"-plan-file", planFile,
		"-status-limit", "7",
		"-position-limit", "9",
		"-artifact-path", artifactPath,
	}, liveFirstOrderReviewDependencies{
		loadConfig: func(path string) (*config.Config, error) {
			if path != "configs/live.local.yaml" {
				t.Fatalf("config path mismatch: %q", path)
			}
			return &config.Config{App: config.AppConfig{LogLevel: "info"}}, nil
		},
		openDB: func(context.Context, config.DatabaseConfig) (*sql.DB, error) {
			return db, nil
		},
		newEvidenceReader: func(*sql.DB) domainlive.LiveFirstOrderReviewEvidenceReader {
			return reader
		},
		now:    func() time.Time { return now.Add(time.Minute) },
		output: &output,
	})
	if err != nil {
		t.Fatalf("run first-order review: %v\nlogs:\n%s", err, output.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
	}
	if reader.calls != 1 || reader.query.StatusLimit != 7 || reader.query.PositionLimit != 9 {
		t.Fatalf("reader query mismatch: calls=%d query=%#v", reader.calls, reader.query)
	}
	logs := output.String()
	for _, want := range []string{
		`"msg":"live first-order review report"`,
		`"ready":true`,
		`"msg":"live first-order review artifact written"`,
		`"msg":"live first-order review passed"`,
	} {
		if !strings.Contains(logs, want) {
			t.Fatalf("expected logs to contain %s, got\n%s", want, logs)
		}
	}

	artifact := readLiveFirstOrderReviewCLIArtifact(t, artifactPath)
	if err := domainlive.ValidateLiveFirstOrderReviewArtifact(artifact); err != nil {
		t.Fatalf("validate artifact: %v", err)
	}
	if !artifact.Ready || artifact.Evidence.StatusLimit != 7 || artifact.Evidence.PositionLimit != 9 {
		t.Fatalf("artifact summary mismatch: %#v", artifact)
	}
	if artifact.PlanFile.SHA256 != liveFirstOrderReviewCLIFileSHA256(t, planFile) {
		t.Fatalf("plan sha mismatch: got %q", artifact.PlanFile.SHA256)
	}
}

func TestRunLiveFirstOrderReviewWritesArtifactBeforeFailure(t *testing.T) {
	now := time.Date(2026, 8, 2, 12, 0, 0, 0, time.UTC)
	plan := validLiveFirstOrderReviewCLIPlan(now)
	planFile := writeLiveFirstOrderReviewCLIPlanArtifact(t, plan)
	artifactPath := filepath.Join(t.TempDir(), "artifacts", "live-first-order-review.json")
	evidence := validLiveFirstOrderReviewCLIEvidence(t, plan)
	evidence.PositionSnapshots = nil
	reader := &fakeLiveFirstOrderReviewCLIReader{evidence: evidence}
	db, mock := newLiveFirstOrderReviewCLISQLMock(t)
	defer db.Close()

	var output bytes.Buffer
	err := runLiveFirstOrderReview(context.Background(), []string{
		"-config", "configs/live.local.yaml",
		"-plan-file", planFile,
		"-artifact-path", artifactPath,
	}, liveFirstOrderReviewDependencies{
		loadConfig: func(string) (*config.Config, error) {
			return &config.Config{App: config.AppConfig{LogLevel: "info"}}, nil
		},
		openDB: func(context.Context, config.DatabaseConfig) (*sql.DB, error) {
			return db, nil
		},
		newEvidenceReader: func(*sql.DB) domainlive.LiveFirstOrderReviewEvidenceReader {
			return reader
		},
		now:    func() time.Time { return now.Add(time.Minute) },
		output: &output,
	})
	if err == nil || !strings.Contains(err.Error(), "live_position_snapshot") {
		t.Fatalf("expected position failure, got %v\nlogs:\n%s", err, output.String())
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatalf("sql expectations: %v", err)
	}
	if !strings.Contains(output.String(), `"msg":"live first-order review artifact written"`) {
		t.Fatalf("expected artifact write log, got\n%s", output.String())
	}

	artifact := readLiveFirstOrderReviewCLIArtifact(t, artifactPath)
	if err := domainlive.ValidateLiveFirstOrderReviewArtifact(artifact); err != nil {
		t.Fatalf("validate failed artifact: %v", err)
	}
	if artifact.Ready || !sameLiveFirstOrderReviewCLIStringSet(artifact.FailedChecks, []string{"live_position_snapshot"}) {
		t.Fatalf("failed artifact mismatch: %#v", artifact)
	}
}

func TestRunLiveFirstOrderReviewRejectsMissingPlanBeforeSideEffects(t *testing.T) {
	var loaded bool
	var opened bool
	err := runLiveFirstOrderReview(context.Background(), []string{
		"-config", "configs/live.local.yaml",
	}, liveFirstOrderReviewDependencies{
		loadConfig: func(string) (*config.Config, error) {
			loaded = true
			return &config.Config{}, nil
		},
		openDB: func(context.Context, config.DatabaseConfig) (*sql.DB, error) {
			opened = true
			return nil, nil
		},
		output: &bytes.Buffer{},
	})
	if err == nil || !strings.Contains(err.Error(), "plan-file") {
		t.Fatalf("expected plan-file error, got %v", err)
	}
	if loaded || opened {
		t.Fatalf("missing plan must stop before side effects: loaded=%t opened=%t", loaded, opened)
	}
}

type fakeLiveFirstOrderReviewCLIReader struct {
	query    domainlive.LiveFirstOrderReviewEvidenceQuery
	evidence domainlive.LiveFirstOrderReviewEvidence
	calls    int
}

func (r *fakeLiveFirstOrderReviewCLIReader) ReadLiveFirstOrderReviewEvidence(
	_ context.Context,
	query domainlive.LiveFirstOrderReviewEvidenceQuery,
) (domainlive.LiveFirstOrderReviewEvidence, error) {
	r.calls++
	r.query = query
	return r.evidence, nil
}

func validLiveFirstOrderReviewCLIPlan(now time.Time) domainlive.LiveOrderPlanArtifact {
	return domainlive.LiveOrderPlanArtifact{
		SchemaVersion:       domainlive.LiveOrderPlanArtifactSchemaVersion,
		Source:              domainlive.LiveOrderPlanArtifactSourceDecisionID,
		RunID:               "live_loop_first_review_cli_0001",
		DecisionID:          "risk_decision_first_review_cli_0001",
		SubmissionID:        "live_submission_first_review_cli_0001",
		ClientOrderID:       "live_client_first_review_cli_0001",
		Exchange:            "bybit",
		Category:            "linear",
		Symbol:              "BTCUSDT",
		Side:                domainlive.OrderSideLong,
		OrderType:           domainlive.OrderTypeMarket,
		TimeInForce:         domainlive.TimeInForceIOC,
		LimitPrice:          "0",
		Quantity:            "0.001",
		EntryPrice:          "100000",
		Notional:            "100",
		MaxLoss:             "1",
		StopLoss:            "99000",
		TakeProfit:          "102000",
		Leverage:            "1",
		Confidence:          82,
		DecisionCreatedAt:   now.Add(-2 * time.Minute),
		RecordedAt:          now.Add(-time.Minute),
		SubmissionCreatedAt: now,
	}
}

func validLiveFirstOrderReviewCLIEvidence(
	t *testing.T,
	plan domainlive.LiveOrderPlanArtifact,
) domainlive.LiveFirstOrderReviewEvidence {
	t.Helper()
	submission := liveFirstOrderReviewCLISubmission(t, plan, plan.SubmissionCreatedAt.Add(10*time.Second))
	ack := liveFirstOrderReviewCLIAck(t, plan, submission.CreatedAt.Add(time.Second))
	status := liveFirstOrderReviewCLIStatus(t, submission, ack, submission.CreatedAt.Add(2*time.Second))
	position := liveFirstOrderReviewCLIPosition(t, submission, status, submission.CreatedAt.Add(3*time.Second))
	run := liveFirstOrderReviewCLIRun(plan, submission.CreatedAt.Add(500*time.Millisecond), position.ObservedAt.Add(time.Second))
	return domainlive.LiveFirstOrderReviewEvidence{
		PlanArtifact:         plan,
		RunAudits:            []domainlive.LiveLoopRunAudit{run},
		Submissions:          []domainlive.OrderSubmission{submission},
		Acknowledgements:     []domainlive.OrderAcknowledgement{ack},
		OrderStatusSnapshots: []domainlive.OrderStatusSnapshot{status},
		PositionSnapshots:    []domainlive.PositionSnapshot{position},
	}
}

func liveFirstOrderReviewCLISubmission(
	t *testing.T,
	plan domainlive.LiveOrderPlanArtifact,
	createdAt time.Time,
) domainlive.OrderSubmission {
	t.Helper()
	submission, err := domainlive.NewOrderSubmission(domainlive.OrderSubmissionInput{
		SubmissionID:     plan.SubmissionID,
		ClientOrderID:    plan.ClientOrderID,
		DecisionID:       plan.DecisionID,
		DecisionApproved: true,
		IntentID:         "risk_intent_first_review_cli_0001",
		RiskMode:         domainlive.RiskModeLive,
		Exchange:         plan.Exchange,
		Category:         plan.Category,
		Symbol:           plan.Symbol,
		Side:             plan.Side,
		Type:             plan.OrderType,
		TimeInForce:      plan.TimeInForce,
		Quantity:         decimal.RequireFromString(plan.Quantity),
		ReferencePrice:   decimal.RequireFromString(plan.EntryPrice),
		LimitPrice:       decimal.RequireFromString(plan.LimitPrice),
		StopLoss:         decimal.RequireFromString(plan.StopLoss),
		TakeProfit:       decimal.RequireFromString(plan.TakeProfit),
		Leverage:         decimal.RequireFromString(plan.Leverage),
		MaxLoss:          decimal.RequireFromString(plan.MaxLoss),
		Confidence:       plan.Confidence,
		Reason:           "first-order CLI review fixture",
		CreatedAt:        createdAt,
	})
	if err != nil {
		t.Fatalf("new submission: %v", err)
	}
	return submission
}

func liveFirstOrderReviewCLIAck(
	t *testing.T,
	plan domainlive.LiveOrderPlanArtifact,
	receivedAt time.Time,
) domainlive.OrderAcknowledgement {
	t.Helper()
	ack, err := domainlive.NewOrderAcknowledgement(domainlive.OrderAcknowledgementInput{
		SubmissionID:    plan.SubmissionID,
		ClientOrderID:   plan.ClientOrderID,
		Exchange:        plan.Exchange,
		ExchangeOrderID: "bybit_order_first_review_cli_0001",
		Status:          domainlive.OrderStatusAccepted,
		ReceivedAt:      receivedAt,
	})
	if err != nil {
		t.Fatalf("new ack: %v", err)
	}
	return ack
}

func liveFirstOrderReviewCLIStatus(
	t *testing.T,
	submission domainlive.OrderSubmission,
	ack domainlive.OrderAcknowledgement,
	observedAt time.Time,
) domainlive.OrderStatusSnapshot {
	t.Helper()
	snapshot, err := domainlive.NewOrderStatusSnapshot(domainlive.OrderStatusSnapshotInput{
		ClientOrderID:              submission.ClientOrderID,
		ExchangeOrderID:            ack.ExchangeOrderID,
		Exchange:                   submission.Exchange,
		Category:                   submission.Category,
		Symbol:                     submission.Symbol,
		Side:                       submission.Side,
		Type:                       submission.Type,
		TimeInForce:                submission.TimeInForce,
		ExchangeStatus:             domainlive.ExchangeOrderStatusFilled,
		RejectReason:               "EC_NoError",
		Quantity:                   submission.Quantity,
		Price:                      submission.ReferencePrice,
		AveragePrice:               submission.ReferencePrice,
		LeavesQuantity:             decimal.Zero,
		CumulativeExecutedQuantity: submission.Quantity,
		CumulativeExecutedValue:    submission.Notional,
		CumulativeFee:              decimal.RequireFromString("0.055"),
		ExchangeCreatedAt:          observedAt.Add(-time.Second),
		ExchangeUpdatedAt:          observedAt,
		ObservedAt:                 observedAt,
	})
	if err != nil {
		t.Fatalf("new status: %v", err)
	}
	return snapshot
}

func liveFirstOrderReviewCLIPosition(
	t *testing.T,
	submission domainlive.OrderSubmission,
	status domainlive.OrderStatusSnapshot,
	observedAt time.Time,
) domainlive.PositionSnapshot {
	t.Helper()
	snapshot, err := domainlive.NewPositionSnapshot(domainlive.PositionSnapshotInput{
		Exchange:           submission.Exchange,
		Category:           submission.Category,
		Symbol:             submission.Symbol,
		Side:               submission.Side,
		Size:               status.CumulativeExecutedQuantity,
		AveragePrice:       status.AveragePrice,
		PositionValue:      status.CumulativeExecutedValue,
		MarkPrice:          status.AveragePrice,
		Leverage:           submission.Leverage,
		ExchangeStatus:     domainlive.ExchangePositionStatusNormal,
		ExchangeCreatedAt:  observedAt.Add(-time.Second),
		ExchangeUpdatedAt:  observedAt,
		ObservedAt:         observedAt,
		ExchangeReduceOnly: false,
	})
	if err != nil {
		t.Fatalf("new position: %v", err)
	}
	return snapshot
}

func liveFirstOrderReviewCLIRun(
	plan domainlive.LiveOrderPlanArtifact,
	startedAt time.Time,
	finishedAt time.Time,
) domainlive.LiveLoopRunAudit {
	return domainlive.LiveLoopRunAudit{
		RunID:                 plan.RunID,
		StartedAt:             startedAt,
		MaxIterations:         1,
		MaxRuntime:            15 * time.Second,
		IterationTimeout:      10 * time.Second,
		Status:                domainlive.LiveLoopRunStatusCompleted,
		FinishedAt:            finishedAt,
		PreflightChecked:      true,
		PreflightReady:        true,
		IterationsAttempted:   1,
		IterationsSucceeded:   1,
		StopReason:            "ITERATION_REQUESTED",
		StopDetails:           "live_order_submitted",
		CompletedWithinBounds: true,
		Iterations: []domainlive.LiveLoopIterationAudit{{
			RunID:             plan.RunID,
			RunStartedAt:      startedAt,
			Iteration:         1,
			Action:            domainlive.LiveLoopAuditIterationActionSubmitted,
			RequestStop:       true,
			Reason:            "live_order_submitted",
			DecisionID:        plan.DecisionID,
			SubmissionID:      plan.SubmissionID,
			ClientOrderID:     plan.ClientOrderID,
			ExchangeSubmitted: true,
			StartedAt:         startedAt.Add(time.Second),
			FinishedAt:        startedAt.Add(2 * time.Second),
		}},
	}
}

func writeLiveFirstOrderReviewCLIPlanArtifact(t *testing.T, artifact domainlive.LiveOrderPlanArtifact) string {
	t.Helper()
	if err := domainlive.ValidateLiveOrderPlanArtifact(artifact); err != nil {
		t.Fatalf("validate plan artifact: %v", err)
	}
	payload, err := json.MarshalIndent(artifact, "", "  ")
	if err != nil {
		t.Fatalf("marshal plan: %v", err)
	}
	path := filepath.Join(t.TempDir(), "live-order-plan.json")
	if err := os.WriteFile(path, append(payload, '\n'), 0o600); err != nil {
		t.Fatalf("write plan: %v", err)
	}
	return path
}

func readLiveFirstOrderReviewCLIArtifact(t *testing.T, path string) domainlive.LiveFirstOrderReviewArtifact {
	t.Helper()
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read artifact: %v", err)
	}
	var artifact domainlive.LiveFirstOrderReviewArtifact
	if err := json.Unmarshal(payload, &artifact); err != nil {
		t.Fatalf("decode artifact: %v", err)
	}
	return artifact
}

func liveFirstOrderReviewCLIFileSHA256(t *testing.T, path string) string {
	t.Helper()
	payload, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read for sha: %v", err)
	}
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func newLiveFirstOrderReviewCLISQLMock(t *testing.T) (*sql.DB, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	return db, mock
}

func sameLiveFirstOrderReviewCLIStringSet(left []string, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	counts := make(map[string]int, len(left))
	for _, item := range left {
		counts[item]++
	}
	for _, item := range right {
		counts[item]--
		if counts[item] < 0 {
			return false
		}
	}
	return true
}
