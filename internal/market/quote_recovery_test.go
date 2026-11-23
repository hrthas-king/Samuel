package market

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestYahooColdTimeoutUsesChartOnNextPoll(t *testing.T) {
	var recovery yahooQuoteRecovery
	authAttempts, chartAttempts := 0, 0
	symbols := []string{"VOO"}
	mainQuote := &Quote{Symbol: "VOO", UpdateMode: "yahoo_post"}
	chartQuote := &Quote{Symbol: "VOO", UpdateMode: "yahoo_regular"}
	primary := func(ctx context.Context, _ []string) (map[string]SymbolResult, error) {
		authAttempts++
		if authAttempts == 1 {
			<-ctx.Done()
			return nil, ctx.Err()
		}
		return map[string]SymbolResult{"VOO": {Quote: mainQuote}}, nil
	}
	chart := func(ctx context.Context, _ []string) (map[string]SymbolResult, error) {
		chartAttempts++
		return map[string]SymbolResult{"VOO": {Quote: chartQuote}}, ctx.Err()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := recovery.fetch(ctx, symbols, primary, chart); !errors.Is(err, context.DeadlineExceeded) || chartAttempts != 0 {
		t.Fatalf("first timed-out poll: error=%v chart calls=%d", err, chartAttempts)
	}
	batch, err := recovery.fetch(context.Background(), symbols, primary, chart)
	if err != nil || batch["VOO"].Quote != chartQuote || authAttempts != 1 || chartAttempts != 1 {
		t.Fatalf("next poll did not get a separate chart attempt: batch=%v error=%v auth=%d chart=%d", batch, err, authAttempts, chartAttempts)
	}

	recovery.retryV7At = time.Now().Add(-time.Second)
	batch, err = recovery.fetch(context.Background(), symbols, primary, chart)
	if err != nil || batch["VOO"].Quote != mainQuote || authAttempts != 2 || !recovery.retryV7At.IsZero() {
		t.Fatalf("primary quote did not recover: batch=%v error=%v auth=%d", batch, err, authAttempts)
	}
}

func TestYahooChartBatchDoesNotLetOneSymbolStarveTheOther(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	qqqStarted := make(chan struct{})
	batch, err := fetchChartBatch(ctx, []string{"VOO", "QQQ"}, func(ctx context.Context, symbol string) (Quote, error) {
		if symbol == "QQQ" {
			close(qqqStarted)
			return Quote{Symbol: symbol, Price: 676.50}, nil
		}
		select {
		case <-qqqStarted:
		case <-ctx.Done():
		}
		return Quote{}, context.DeadlineExceeded
	})
	if err != nil || batch["QQQ"].Quote == nil || batch["VOO"].Err == "" {
		t.Fatalf("a slow VOO request blocked QQQ: batch=%v error=%v", batch, err)
	}
}

func TestYahooPrimaryPartialBatchFetchesOnlyMissingSymbol(t *testing.T) {
	var recovery yahooQuoteRecovery
	primaryQuote := &Quote{Symbol: "VOO", UpdateMode: "yahoo_post"}
	chartQuote := &Quote{Symbol: "QQQ", UpdateMode: "yahoo_regular"}
	primary := func(context.Context, []string) (map[string]SymbolResult, error) {
		return map[string]SymbolResult{"VOO": {Quote: primaryQuote}, "QQQ": {Err: "missing quote"}}, nil
	}
	chart := func(_ context.Context, missing []string) (map[string]SymbolResult, error) {
		if len(missing) != 1 || missing[0] != "QQQ" {
			t.Fatalf("chart requested the wrong symbols: %v", missing)
		}
		return map[string]SymbolResult{"QQQ": {Quote: chartQuote}}, nil
	}
	batch, err := recovery.fetch(context.Background(), []string{"VOO", "QQQ"}, primary, chart)
	if err != nil || batch["VOO"].Quote != primaryQuote || batch["QQQ"].Quote != chartQuote {
		t.Fatalf("primary was overwritten or missing symbol was not filled: batch=%v error=%v", batch, err)
	}
}

func TestYahooUnauthorizedPrimaryFallsBackOnTheSamePoll(t *testing.T) {
	var recovery yahooQuoteRecovery
	chartQuote := &Quote{Symbol: "VOO", UpdateMode: "yahoo_regular"}
	primary := func(context.Context, []string) (map[string]SymbolResult, error) {
		return nil, HTTPError{StatusCode: 401}
	}
	chart := func(context.Context, []string) (map[string]SymbolResult, error) {
		return map[string]SymbolResult{"VOO": {Quote: chartQuote}}, nil
	}
	batch, err := recovery.fetch(context.Background(), []string{"VOO"}, primary, chart)
	if err != nil || batch["VOO"].Quote != chartQuote || recovery.retryV7At.IsZero() {
		t.Fatalf("401 blocked a usable public quote: batch=%v error=%v", batch, err)
	}
}

func TestYahooRateLimitBacksOffWithoutDroppingVerifiedResults(t *testing.T) {
	for _, source := range []string{"primary", "chart"} {
		t.Run(source, func(t *testing.T) {
			var recovery yahooQuoteRecovery
			calls := 0
			symbols := []string{"VOO", "QQQ"}
			limited := HTTPError{StatusCode: 429}
			primary := func(context.Context, []string) (map[string]SymbolResult, error) {
				calls++
				if source == "primary" {
					return nil, limited
				}
				return nil, HTTPError{StatusCode: 401}
			}
			chart := func(ctx context.Context, missing []string) (map[string]SymbolResult, error) {
				calls++
				return fetchChartBatch(ctx, missing, func(_ context.Context, symbol string) (Quote, error) {
					if symbol == "QQQ" {
						return Quote{Symbol: symbol, Price: 676.50}, nil
					}
					return Quote{}, limited
				})
			}
			batch, err := recovery.fetch(context.Background(), symbols, primary, chart)
			if source == "primary" && (!yahooRateLimited(err) || calls != 1) {
				t.Fatalf("429 triggered another Yahoo request: calls=%d, err=%v", calls, err)
			}
			if source == "chart" && (err != nil || batch["QQQ"].Quote == nil || batch["VOO"].Err == "" || calls != 2) {
				t.Fatalf("429 discarded the partial chart batch: calls=%d, batch=%v, err=%v", calls, batch, err)
			}
			before := calls
			_, err = recovery.fetch(context.Background(), symbols, primary, chart)
			if !yahooRateLimited(err) || calls != before {
				t.Fatalf("429 cooldown did not suppress requests: calls=%d, err=%v", calls, err)
			}
		})
	}
}
