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
	mainQuote := &Quote{Symbol: "VOO", UpdateMode: "yahoo_post"}
	chartQuote := &Quote{Symbol: "VOO", UpdateMode: "yahoo_regular"}
	primary := func(ctx context.Context) (map[string]SymbolResult, error) {
		authAttempts++
		if authAttempts == 1 {
			<-ctx.Done()
			return nil, ctx.Err()
		}
		return map[string]SymbolResult{"VOO": {Quote: mainQuote}}, nil
	}
	chart := func(ctx context.Context) (map[string]SymbolResult, error) {
		chartAttempts++
		return map[string]SymbolResult{"VOO": {Quote: chartQuote}}, ctx.Err()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := recovery.fetch(ctx, primary, chart); !errors.Is(err, context.DeadlineExceeded) || chartAttempts != 0 {
		t.Fatalf("first timed-out poll: error=%v chart calls=%d", err, chartAttempts)
	}
	batch, err := recovery.fetch(context.Background(), primary, chart)
	if err != nil || batch["VOO"].Quote != chartQuote || authAttempts != 1 || chartAttempts != 1 {
		t.Fatalf("next poll did not get a separate chart attempt: batch=%v error=%v auth=%d chart=%d", batch, err, authAttempts, chartAttempts)
	}

	recovery.retryV7At = time.Now().Add(-time.Second)
	batch, err = recovery.fetch(context.Background(), primary, chart)
	if err != nil || batch["VOO"].Quote != mainQuote || authAttempts != 2 || !recovery.retryV7At.IsZero() {
		t.Fatalf("primary quote did not recover: batch=%v error=%v auth=%d", batch, err, authAttempts)
	}
}

func TestYahooChartBatchKeepsQuoteWhenAnotherSymbolTimesOut(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	calls := 0
	batch, err := fetchChartBatch(ctx, []string{"VOO", "QQQ"}, func(ctx context.Context, symbol string) (Quote, error) {
		calls++
		if symbol != "VOO" {
			t.Fatalf("started a request after the deadline: %s", symbol)
		}
		<-ctx.Done()
		return Quote{Symbol: "VOO", Price: 676.50}, nil
	})
	if err != nil || calls != 1 || batch["VOO"].Quote == nil || batch["QQQ"].Err == "" {
		t.Fatalf("the verified quote was discarded: calls=%d batch=%v error=%v", calls, batch, err)
	}
}
