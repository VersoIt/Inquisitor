package postgres_test

import (
	"context"
	"database/sql/driver"
	"errors"
	"path/filepath"
	"testing"
	"time"

	apppaper "github.com/VersoIt/Inquisitor/internal/app/paper"
	appstrategy "github.com/VersoIt/Inquisitor/internal/app/strategy"
	"github.com/VersoIt/Inquisitor/internal/clock"
	"github.com/VersoIt/Inquisitor/internal/hypothesis"
	"github.com/VersoIt/Inquisitor/internal/marketdata"
	"github.com/VersoIt/Inquisitor/internal/paper"
	"github.com/VersoIt/Inquisitor/internal/research"
	"github.com/VersoIt/Inquisitor/internal/risk"
	"github.com/VersoIt/Inquisitor/internal/storage/postgres"
	"github.com/VersoIt/Inquisitor/internal/strategy"
	"github.com/VersoIt/Inquisitor/internal/testutil"
	"github.com/shopspring/decimal"
)

func sourceCandleValues(c marketdata.Candle) []driver.Value {
	return []driver.Value{c.Exchange, c.Category, c.Symbol, c.Interval, c.OpenTime, c.CloseTime, c.Open.String(), c.High.String(), c.Low.String(), c.Close.String(), c.Volume.String(), c.Turnover.String(), c.IsClosed}
}

func TestPaperMarketRepositoryFailureBoundaries(t *testing.T) {
	failure := errors.New("database unavailable")
	for _, scenario := range []string{"begin", "candles query", "candles iteration", "truncated history", "regime", "book", "instrument", "commit"} {
		t.Run(scenario, func(t *testing.T) {
			db, mock := newSQLMock(t)
			defer db.Close()
			q, _, f := testutil.PaperMarket()
			begin := mock.ExpectBegin()
			if scenario == "begin" {
				begin.WillReturnError(failure)
			} else {
				candles := mock.ExpectQuery("SELECT exchange,category,symbol,interval,open_time,close_time").WithArgs(q.Scope.Exchange, q.Scope.Category, q.Scope.Symbol, q.Scope.Interval, strategy.PaperMarketCandleCount)
				if scenario == "candles query" {
					candles.WillReturnError(failure)
				} else {
					rows := accountMockRows(13)
					if scenario != "truncated history" {
						for i := len(f.Candles) - 1; i >= 0; i-- {
							rows.AddRow(sourceCandleValues(f.Candles[i])...)
						}
					}
					if scenario == "candles iteration" {
						rows.RowError(0, failure)
					}
					candles.WillReturnRows(rows).RowsWillBeClosed()
					if scenario != "truncated history" && scenario != "candles iteration" {
						r := f.Regime
						query := mock.ExpectQuery("SELECT exchange,category,symbol,interval,open_time,close_time,calculated_at").WithArgs(q.Scope.Exchange, q.Scope.Category, q.Scope.Symbol, q.Scope.Interval)
						if scenario == "regime" {
							query.WillReturnError(failure)
						} else {
							query.WillReturnRows(accountMockRows(12, r.Exchange, r.Category, r.Symbol, r.Interval, r.OpenTime, r.CloseTime, r.CalculatedAt, string(r.Regime), string(r.CandidateRegime), r.Confidence, r.NoTrade, `[]`))
							book := mock.ExpectQuery("SELECT exchange,category,symbol,depth").WithArgs(q.Scope.Exchange, q.Scope.Category, q.Scope.Symbol, strategy.MaxPaperBookJSONBytes)
							if scenario == "book" {
								book.WillReturnError(failure)
							} else {
								b := f.Book
								book.WillReturnRows(accountMockRows(15, b.Exchange, b.Category, b.Symbol, b.Depth, `[["99.99","2000"]]`, `[["100.01","3000"]]`, b.BestBid.String(), b.BestAsk.String(), b.Spread.String(), b.SpreadBPS.String(), b.UpdateID, b.Sequence, b.ExchangeTime, b.MatchingEngineTime, b.CreatedAt))
								instrument := mock.ExpectQuery("SELECT exchange,category,symbol,base_coin").WithArgs(q.Scope.Exchange, q.Scope.Category, q.Scope.Symbol)
								if scenario == "instrument" {
									instrument.WillReturnError(failure)
								} else {
									i := f.Instrument
									instrument.WillReturnRows(accountMockRows(18, i.Exchange, i.Category, i.Symbol, i.BaseCoin, i.QuoteCoin, i.Status, i.TickSize.String(), i.QtyStep.String(), i.MinOrderQty.String(), i.MaxOrderQty.String(), i.MaxMarketOrderQty.String(), i.MinNotionalValue.String(), i.PriceScale, `{}`, `{}`, `{}`, `{}`, i.UpdatedAt))
								}
							}
						}
					}
				}
				if scenario == "commit" {
					mock.ExpectCommit().WillReturnError(failure)
				} else {
					mock.ExpectRollback()
				}
			}
			got, err := postgres.NewPaperMarketRepository(db).LoadPaperMarket(context.Background(), q)
			if err == nil || len(got.Candles) != 0 {
				t.Fatalf("partial market returned: %+v %v", got, err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPaperMarketRepositoryIntegration(t *testing.T) {
	for _, scenario := range []string{"fresh", "short history", "missing regime", "forming candle ignored", "future newest book", "duplicate newest book", "oversized newest book", "read only repeatable read"} {
		t.Run(scenario, func(t *testing.T) {
			db, _ := testutil.IsolatedPostgres(t)
			ctx := context.Background()
			if _, err := postgres.ApplyMigrations(ctx, db, filepath.Join("..", "..", "..", "migrations")); err != nil {
				t.Fatal(err)
			}
			q, p, f := testutil.PaperMarket()
			testutil.StorePaperMarket(t, db, f)
			switch scenario {
			case "short history":
				if _, err := db.ExecContext(ctx, "DELETE FROM candles WHERE open_time=$1", f.Candles[0].OpenTime); err != nil {
					t.Fatal(err)
				}
			case "missing regime":
				if _, err := db.ExecContext(ctx, "DELETE FROM regime_states"); err != nil {
					t.Fatal(err)
				}
			case "forming candle ignored":
				c := f.Candles[255]
				c.OpenTime, c.CloseTime, c.IsClosed = c.CloseTime, c.CloseTime.Add(time.Minute), false
				if _, err := postgres.NewCandleRepository(db).UpsertCandles(ctx, []marketdata.Candle{c}); err != nil {
					t.Fatal(err)
				}
			case "future newest book", "duplicate newest book":
				b := f.Book
				b.Sequence++
				if scenario == "future newest book" {
					b.ExchangeTime = q.AsOf.Add(time.Microsecond)
				} else {
					b.Bids = append([]marketdata.OrderbookLevel{b.Bids[0]}, b.Bids...)
				}
				if _, err := postgres.NewOrderbookSnapshotRepository(db).CreateOrderbookSnapshots(ctx, []marketdata.OrderbookSnapshot{b}); err != nil {
					t.Fatal(err)
				}
			case "oversized newest book":
				if _, err := db.ExecContext(ctx, "UPDATE orderbook_snapshots SET bids_json=jsonb_build_array(jsonb_build_array('99.99','2'||repeat('0',$1::int)))", strategy.MaxPaperBookJSONBytes+1); err != nil {
					t.Fatal(err)
				}
			case "read only repeatable read":
				if _, err := db.ExecContext(ctx, "ALTER TABLE orderbook_snapshots RENAME TO market_isolation_books"); err != nil {
					t.Fatal(err)
				}
				if _, err := db.ExecContext(ctx, `CREATE VIEW orderbook_snapshots AS SELECT * FROM market_isolation_books WHERE current_setting('transaction_isolation')='repeatable read' AND current_setting('transaction_read_only')='on'`); err != nil {
					t.Fatal(err)
				}
			}
			service, err := appstrategy.NewPaperMarketService(postgres.NewPaperMarketRepository(db), clock.FixedClock{Time: q.AsOf}, p)
			if err != nil {
				t.Fatal(err)
			}
			got, err := service.Snapshot(ctx, q.Scope)
			valid := scenario == "fresh" || scenario == "forming candle ignored" || scenario == "read only repeatable read"
			if (err == nil) != valid || !valid && got.SourceSHA256 != "" {
				t.Fatalf("scenario=%s snapshot=%+v err=%v", scenario, got, err)
			}
			if valid && (!got.Quote.Mid.Equal(decimal.NewFromInt(100)) || !got.Quote.LiquidityQuote.Equal(decimal.NewFromInt(199980))) {
				t.Fatal("source values changed")
			}
		})
	}
}

func TestPaperMarketRepositoryRejectsInvalidInvocationBeforeSQL(t *testing.T) {
	for _, scenario := range []string{"nil repository", "nil database", "cancelled", "invalid query"} {
		t.Run(scenario, func(t *testing.T) {
			db, mock := newSQLMock(t)
			defer db.Close()
			q, _, _ := testutil.PaperMarket()
			ctx := context.Background()
			repository := postgres.NewPaperMarketRepository(db)
			switch scenario {
			case "nil repository":
				repository = nil
			case "nil database":
				repository = postgres.NewPaperMarketRepository(nil)
			case "cancelled":
				cancelled, cancel := context.WithCancel(ctx)
				cancel()
				ctx = cancelled
			case "invalid query":
				q.Scope.Symbol = "btcUsdt"
			}
			got, err := repository.LoadPaperMarket(ctx, q)
			if err == nil || len(got.Candles) != 0 || scenario == "cancelled" && !errors.Is(err, context.Canceled) {
				t.Fatalf("invalid invocation returned facts: %+v %v", got, err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSourcedMarketAndAccountReachAuthorizedPaperTicket(t *testing.T) {
	db, _ := testutil.IsolatedPostgres(t)
	ctx := context.Background()
	if _, err := postgres.ApplyMigrations(ctx, db, filepath.Join("..", "..", "..", "migrations")); err != nil {
		t.Fatal(err)
	}
	q, p, facts := testutil.PaperMarket()
	testutil.StorePaperMarket(t, db, facts)
	f := testutil.PaperRisk(t)
	e := f.Approval.Evidence
	if _, err := postgres.NewHypothesisRepository(db).UpsertHypotheses(ctx, []hypothesis.Record{e.Hypothesis}); err != nil {
		t.Fatal(err)
	}
	runs := postgres.NewResearchRunRepository(db)
	if _, err := runs.UpsertRuns(ctx, []research.Run{e.Run}); err != nil {
		t.Fatal(err)
	}
	if _, err := runs.RecordResult(ctx, e.Run, e.Result); err != nil {
		t.Fatal(err)
	}
	validations := postgres.NewPaperValidationRepository(db)
	planned := e.Validation
	planned.Status = paper.ValidationStatusPlanned
	planned.StartedAt = time.Time{}
	if _, err := validations.RecordValidation(ctx, planned); err != nil {
		t.Fatal(err)
	}
	if _, err := validations.TransitionValidation(ctx, e.Validation, paper.ValidationStatusPlanned); err != nil {
		t.Fatal(err)
	}
	grants := postgres.NewStrategyApprovalRepository(db)
	if _, err := grants.RecordPaperApproval(ctx, f.Approval.Grant); err != nil {
		t.Fatal(err)
	}
	c := clock.FixedClock{Time: q.AsOf}
	marketSource, err := appstrategy.NewPaperMarketService(postgres.NewPaperMarketRepository(db), c, p)
	if err != nil {
		t.Fatal(err)
	}
	market, err := marketSource.Snapshot(ctx, q.Scope)
	if err != nil {
		t.Fatal(err)
	}
	account, err := apppaper.NewAccountSnapshotService(postgres.NewPaperAccountRepository(db), c).Snapshot(ctx, e.Validation.ValidationID)
	if err != nil || account.Account == nil {
		t.Fatalf("account %+v %v", account, err)
	}
	observation, err := market.StrategyContext("sourced_market_signal", risk.RuntimeState{Mode: risk.ModePaper, TradingEnabled: true}, false)
	if err != nil {
		t.Fatal(err)
	}
	approvals, err := appstrategy.NewApprovalService(grants, c, f.Approval.Policy)
	if err != nil {
		t.Fatal(err)
	}
	selection := strategy.SelectionPolicy{MinConfidence: 70, MaxDataAge: p.MaxDataAge}
	trend, err := strategy.NewTrendStrategy(strategy.TrendConfig{SelectionPolicy: selection, StopATRMultiple: decimal.NewFromInt(2), TakeProfitR: decimal.NewFromInt(2)})
	if err != nil {
		t.Fatal(err)
	}
	selector, err := strategy.NewSelector(selection, trend)
	if err != nil {
		t.Fatal(err)
	}
	signals := postgres.NewStrategySignalRepository(db)
	journal := postgres.NewPaperSignalRiskRepository(db)
	stops := postgres.NewRiskKillSwitchRepository(db)
	generation := appstrategy.NewAuthorizedGenerationService(approvals, appstrategy.NewGenerationService(selector, signals, c))
	engine, err := risk.NewTradeRiskEngine(f.Policy)
	if err != nil {
		t.Fatal(err)
	}
	evaluations, err := appstrategy.NewPaperRiskService(signals, approvals, journal, stops, engine, c)
	if err != nil {
		t.Fatal(err)
	}
	submission := appstrategy.NewPaperSignalSubmissionService(evaluations, approvals, postgres.NewPaperOrderTicketRepository(db), c)
	cycle := appstrategy.NewPaperSignalCycleService(generation, submission, stops, c)
	request := appstrategy.RunPaperSignalCycleRequest{ApprovalID: f.Approval.Grant.ApprovalID, Market: observation, Account: *account.Account, RiskMarket: market.RiskMarket}
	result, err := cycle.Run(ctx, request)
	if err != nil || result.Outcome != appstrategy.PaperSignalCycleTicketRecorded || result.Submission == nil || result.Submission.Ticket.SourceSignalID != observation.SignalID {
		t.Fatalf("sourced cycle %+v %v", result, err)
	}
	again, err := cycle.Run(ctx, request)
	if err != nil || again.Submission == nil || !again.Submission.Replayed || again.Submission.Ticket.TicketID != result.Submission.Ticket.TicketID {
		t.Fatalf("sourced retry %+v %v", again, err)
	}
	var count int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM paper_order_fills").Scan(&count); err != nil || count != 0 {
		t.Fatal("market source/cycle unexpectedly filled orders", err)
	}
}
