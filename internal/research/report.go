package research

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const ReportSchemaVersion = "research-report/v1"

type ReportFormat string

const (
	ReportFormatJSON     ReportFormat = "json"
	ReportFormatMarkdown ReportFormat = "markdown"
)

type ReportInput struct {
	Run         Run
	Result      Result
	GeneratedAt time.Time
}

type Report struct {
	SchemaVersion string       `json:"schema_version"`
	GeneratedAt   time.Time    `json:"generated_at"`
	Run           ReportRun    `json:"run"`
	Result        ReportResult `json:"result"`
	Safety        ReportSafety `json:"safety"`
}

type ReportRun struct {
	RunID                   string    `json:"run_id"`
	HypothesisName          string    `json:"hypothesis_name"`
	HypothesisVersion       string    `json:"hypothesis_version"`
	HypothesisContentSHA256 string    `json:"hypothesis_content_sha256"`
	Exchange                string    `json:"exchange"`
	Category                string    `json:"category"`
	Status                  Status    `json:"status"`
	WindowStart             time.Time `json:"window_start"`
	WindowEnd               time.Time `json:"window_end"`
	PlannedAt               time.Time `json:"planned_at"`
	Symbols                 []string  `json:"symbols"`
	Intervals               []string  `json:"intervals"`
	Notes                   []string  `json:"notes,omitempty"`
}

type ReportResult struct {
	FinalStatus Status    `json:"final_status"`
	Outcome     Outcome   `json:"outcome"`
	Summary     string    `json:"summary"`
	Metrics     Metrics   `json:"metrics"`
	Reasons     []string  `json:"reasons"`
	RecordedAt  time.Time `json:"recorded_at"`
}

type ReportSafety struct {
	LiveTradingAllowed        bool     `json:"live_trading_allowed"`
	CandidateOutcomeReached   bool     `json:"candidate_outcome_reached"`
	BlockingValidationGaps    []string `json:"blocking_validation_gaps"`
	ConservativeCostsIncluded bool     `json:"conservative_costs_included"`
}

func BuildReport(input ReportInput) (Report, error) {
	if err := ValidateRun(input.Run); err != nil {
		return Report{}, err
	}
	if err := ValidateResult(input.Result); err != nil {
		return Report{}, err
	}
	if input.Run.RunID != input.Result.RunID {
		return Report{}, errors.New("research report validation failed: result run_id must match research run")
	}
	if input.GeneratedAt.IsZero() {
		return Report{}, errors.New("research report validation failed: generated_at is required")
	}

	return Report{
		SchemaVersion: ReportSchemaVersion,
		GeneratedAt:   input.GeneratedAt.UTC(),
		Run: ReportRun{
			RunID:                   input.Run.RunID,
			HypothesisName:          input.Run.HypothesisName,
			HypothesisVersion:       input.Run.HypothesisVersion,
			HypothesisContentSHA256: input.Run.HypothesisContentSHA256,
			Exchange:                input.Run.Exchange,
			Category:                input.Run.Category,
			Status:                  input.Run.Status,
			WindowStart:             input.Run.WindowStart.UTC(),
			WindowEnd:               input.Run.WindowEnd.UTC(),
			PlannedAt:               input.Run.PlannedAt.UTC(),
			Symbols:                 append([]string(nil), input.Run.Symbols...),
			Intervals:               append([]string(nil), input.Run.Intervals...),
			Notes:                   append([]string(nil), input.Run.Notes...),
		},
		Result: ReportResult{
			FinalStatus: input.Result.FinalStatus,
			Outcome:     input.Result.Outcome,
			Summary:     input.Result.Summary,
			Metrics:     input.Result.Metrics,
			Reasons:     append([]string(nil), input.Result.Reasons...),
			RecordedAt:  input.Result.RecordedAt.UTC(),
		},
		Safety: reportSafety(input.Result),
	}, nil
}

func ParseReportFormat(value string) (ReportFormat, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", string(ReportFormatJSON):
		return ReportFormatJSON, nil
	case "md", string(ReportFormatMarkdown):
		return ReportFormatMarkdown, nil
	default:
		return "", fmt.Errorf("unsupported research report format %q", value)
	}
}

func RenderReport(input ReportInput, format ReportFormat) ([]byte, error) {
	report, err := BuildReport(input)
	if err != nil {
		return nil, err
	}
	switch format {
	case ReportFormatJSON:
		return RenderReportJSON(report)
	case ReportFormatMarkdown:
		return RenderReportMarkdown(report)
	default:
		return nil, fmt.Errorf("unsupported research report format %q", format)
	}
}

func RenderReportJSON(report Report) ([]byte, error) {
	if err := ValidateReport(report); err != nil {
		return nil, err
	}
	out, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("render research report json: %w", err)
	}
	return append(out, '\n'), nil
}

func RenderReportMarkdown(report Report) ([]byte, error) {
	if err := ValidateReport(report); err != nil {
		return nil, err
	}

	var builder strings.Builder
	builder.WriteString("# Research Report\n\n")
	builder.WriteString("Generated at: ")
	builder.WriteString(formatReportTime(report.GeneratedAt))
	builder.WriteString("\n\n")

	builder.WriteString("## Run\n\n")
	writeMarkdownTable(&builder, []markdownRow{
		{"Run ID", report.Run.RunID},
		{"Hypothesis", report.Run.HypothesisName + " " + report.Run.HypothesisVersion},
		{"Status", string(report.Run.Status)},
		{"Exchange", report.Run.Exchange},
		{"Category", report.Run.Category},
		{"Symbols", strings.Join(report.Run.Symbols, ", ")},
		{"Intervals", strings.Join(report.Run.Intervals, ", ")},
		{"Window", formatReportTime(report.Run.WindowStart) + " to " + formatReportTime(report.Run.WindowEnd)},
		{"Planned at", formatReportTime(report.Run.PlannedAt)},
		{"Content SHA256", report.Run.HypothesisContentSHA256},
	})
	builder.WriteString("\n")

	builder.WriteString("## Decision\n\n")
	writeMarkdownTable(&builder, []markdownRow{
		{"Final status", string(report.Result.FinalStatus)},
		{"Outcome", string(report.Result.Outcome)},
		{"Recorded at", formatReportTime(report.Result.RecordedAt)},
		{"Summary", report.Result.Summary},
	})
	builder.WriteString("\n")

	builder.WriteString("## Metrics\n\n")
	rows := []markdownRow{
		{"Trades", strconv.Itoa(report.Result.Metrics.Trades)},
		{"Regime coverage", formatFloat(report.Result.Metrics.RegimeCoveragePct) + "%"},
		{"Regime states", fmt.Sprintf("%d/%d", report.Result.Metrics.RegimeStates, report.Result.Metrics.ExpectedRegimeStates)},
		{"Net PnL", markdownMetric(report.Result.Metrics.NetPnL)},
		{"Total fees", markdownMetric(report.Result.Metrics.TotalFees)},
		{"Expectancy", markdownMetric(report.Result.Metrics.Expectancy)},
		{"Profit factor", markdownMetric(report.Result.Metrics.ProfitFactor)},
		{"Profit factor defined", strconv.FormatBool(report.Result.Metrics.ProfitFactorDefined)},
		{"Win rate", formatFloat(report.Result.Metrics.WinRatePct) + "%"},
		{"Max drawdown", formatFloat(report.Result.Metrics.MaxDrawdownPct) + "%"},
		{"Initial equity", markdownMetric(report.Result.Metrics.InitialEquity)},
		{"Final equity", markdownMetric(report.Result.Metrics.FinalEquity)},
		{"Fees included", strconv.FormatBool(report.Result.Metrics.FeesIncluded)},
		{"Spread included", strconv.FormatBool(report.Result.Metrics.SpreadIncluded)},
		{"Slippage included", strconv.FormatBool(report.Result.Metrics.SlippageIncluded)},
		{"Out of sample", strconv.FormatBool(report.Result.Metrics.OutOfSample)},
		{"Walk forward", strconv.FormatBool(report.Result.Metrics.WalkForward)},
	}
	if report.Result.Metrics.OutOfSample || report.Result.Metrics.InSampleTrades+report.Result.Metrics.OutOfSampleTrades > 0 {
		rows = append(rows,
			markdownRow{"In-sample trades", strconv.Itoa(report.Result.Metrics.InSampleTrades)},
			markdownRow{"In-sample net PnL", markdownMetric(report.Result.Metrics.InSampleNetPnL)},
			markdownRow{"In-sample profit factor", markdownMetric(report.Result.Metrics.InSampleProfitFactor)},
			markdownRow{"In-sample max drawdown", formatFloat(report.Result.Metrics.InSampleMaxDrawdownPct) + "%"},
			markdownRow{"Out-of-sample trades", strconv.Itoa(report.Result.Metrics.OutOfSampleTrades)},
			markdownRow{"Out-of-sample net PnL", markdownMetric(report.Result.Metrics.OutOfSampleNetPnL)},
			markdownRow{"Out-of-sample profit factor", markdownMetric(report.Result.Metrics.OutOfSampleProfitFactor)},
			markdownRow{"Out-of-sample max drawdown", formatFloat(report.Result.Metrics.OutOfSampleMaxDrawdownPct) + "%"},
		)
	}
	if report.Result.Metrics.WalkForwardFolds > 0 {
		rows = append(rows,
			markdownRow{"Walk-forward folds", strconv.Itoa(report.Result.Metrics.WalkForwardFolds)},
			markdownRow{"Walk-forward passed folds", strconv.Itoa(report.Result.Metrics.WalkForwardPassedFolds)},
			markdownRow{"Walk-forward failed folds", strconv.Itoa(report.Result.Metrics.WalkForwardFailedFolds)},
			markdownRow{"Walk-forward trades", strconv.Itoa(report.Result.Metrics.WalkForwardTrades)},
		)
	}
	writeMarkdownTable(&builder, rows)
	builder.WriteString("\n")

	builder.WriteString("## Safety\n\n")
	writeMarkdownTable(&builder, []markdownRow{
		{"Live trading allowed", strconv.FormatBool(report.Safety.LiveTradingAllowed)},
		{"Candidate outcome reached", strconv.FormatBool(report.Safety.CandidateOutcomeReached)},
		{"Conservative costs included", strconv.FormatBool(report.Safety.ConservativeCostsIncluded)},
		{"Blocking validation gaps", strings.Join(report.Safety.BlockingValidationGaps, ", ")},
	})
	builder.WriteString("\n")

	builder.WriteString("## Reasons\n\n")
	if len(report.Result.Reasons) == 0 {
		builder.WriteString("- none\n")
	} else {
		for _, reason := range report.Result.Reasons {
			builder.WriteString("- ")
			builder.WriteString(markdownText(reason))
			builder.WriteString("\n")
		}
	}
	return []byte(builder.String()), nil
}

func ValidateReport(report Report) error {
	var problems []string
	if report.SchemaVersion != ReportSchemaVersion {
		problems = append(problems, "schema_version is unsupported")
	}
	if report.GeneratedAt.IsZero() {
		problems = append(problems, "generated_at is required")
	}
	if strings.TrimSpace(report.Run.RunID) == "" {
		problems = append(problems, "run.run_id is required")
	}
	if strings.TrimSpace(report.Result.Summary) == "" {
		problems = append(problems, "result.summary is required")
	}
	if report.Result.RecordedAt.IsZero() {
		problems = append(problems, "result.recorded_at is required")
	}
	if len(problems) > 0 {
		return errors.New("research report validation failed: " + strings.Join(problems, "; "))
	}
	return nil
}

type markdownRow struct {
	Field string
	Value string
}

func writeMarkdownTable(builder *strings.Builder, rows []markdownRow) {
	builder.WriteString("| Field | Value |\n")
	builder.WriteString("| --- | --- |\n")
	for _, row := range rows {
		builder.WriteString("| ")
		builder.WriteString(markdownTableCell(row.Field))
		builder.WriteString(" | ")
		builder.WriteString(markdownTableCell(row.Value))
		builder.WriteString(" |\n")
	}
}

func markdownMetric(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}

func markdownTableCell(value string) string {
	value = markdownText(value)
	value = strings.ReplaceAll(value, "|", `\|`)
	if value == "" {
		return "-"
	}
	return value
}

func markdownText(value string) string {
	value = strings.TrimSpace(value)
	value = strings.ReplaceAll(value, "\r\n", " ")
	value = strings.ReplaceAll(value, "\n", " ")
	value = strings.ReplaceAll(value, "\r", " ")
	return value
}

func formatReportTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339)
}

func formatFloat(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}

func reportSafety(result Result) ReportSafety {
	metrics := result.Metrics
	gaps := []string{"live_execution_not_implemented"}
	if result.Outcome != OutcomeCandidate {
		gaps = append(gaps, "candidate_outcome_not_reached")
	}
	if !metrics.OutOfSample {
		gaps = append(gaps, "out_of_sample_validation_missing")
	}
	if !metrics.WalkForward {
		gaps = append(gaps, "walk_forward_validation_missing")
	}
	return ReportSafety{
		LiveTradingAllowed:        false,
		CandidateOutcomeReached:   result.Outcome == OutcomeCandidate,
		BlockingValidationGaps:    gaps,
		ConservativeCostsIncluded: metrics.FeesIncluded && metrics.SpreadIncluded && metrics.SlippageIncluded,
	}
}
