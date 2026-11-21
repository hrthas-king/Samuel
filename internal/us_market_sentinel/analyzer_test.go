//go:build darwin

package sentinel

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"
)

type modelFunc func(context.Context, Input, string) ([]byte, error)

func (f modelFunc) Complete(ctx context.Context, in Input, feedback string) ([]byte, error) {
	return f(ctx, in, feedback)
}
func modelReport(in Input) Report {
	r := quantReport(in)
	r.Summary = "根据传入快照评估。"
	r.Confidence = "high"
	return r
}
func runModel(t *testing.T, in Input, r Report) Result {
	t.Helper()
	return analyze(context.Background(), in, modelFunc(func(context.Context, Input, string) ([]byte, error) { return json.Marshal(r) }))
}
func TestFourGuardrails(t *testing.T) {
	in := scenario(1)
	r := modelReport(in)
	r.Signal = "strong_buy_dca"
	r.Multiplier = 2
	r.Adjustment = -30
	r.Base = 2
	r.Temperature = 1
	o := runModel(t, in, r)
	if o.Mode != "llm" || o.Base != 43 || o.Adjustment != -15 || o.Temperature != 28 || !o.Breaker || o.Regime != "recession_warning" || o.Signal != "cautious" || o.Multiplier != .5 || o.Throttle {
		t.Fatal(o)
	}
	if len(o.Guards) != 2 || !strings.HasPrefix(o.Guards[0], "A") || !strings.HasPrefix(o.Guards[1], "B") {
		t.Fatal(o.Guards)
	}
	in = scenario(0)
	in.Style = Right
	r = modelReport(in)
	r.Signal = "buy_confirmed"
	r.Multiplier = 2
	for _, state := range []struct{ v, ma *bool }{{truth(false), truth(true)}, {truth(true), truth(false)}, {nil, truth(true)}} {
		in.Market.VIXDeclining, in.Market.Reclaimed = state.v, state.ma
		o = runModel(t, in, r)
		if o.Signal != "watch_for_reversal" || o.Multiplier > 1 || len(o.Guards) == 0 {
			t.Fatal(o)
		}
	}
	in.Market.VIXDeclining, in.Market.Reclaimed = truth(true), truth(true)
	o = runModel(t, in, r)
	if o.Signal != "buy_confirmed" || o.Multiplier != 2 {
		t.Fatal(o)
	}
	for _, tc := range []struct {
		offset     time.Duration
		importance string
		want       bool
	}{{48 * time.Hour, "high", true}, {48*time.Hour + time.Second, "high", false}, {-time.Second, "high", false}, {time.Hour, "low", false}, {0, "high", true}} {
		in.Events = []Event{{in.AsOf.Add(tc.offset).Format(time.RFC3339), "FOMC", tc.importance, "mock"}}
		r.Throttle = true // a model cannot invent a throttle
		o = runModel(t, in, r)
		if o.Throttle != tc.want || (o.Throttle && (o.Multiplier != 1.2 || !strings.Contains(strings.Join(o.Risks, " "), "事件落地前分批执行"))) {
			t.Fatal(tc, o)
		}
	}
	in = scenario(2)
	r = modelReport(in)
	r.Adjustment = 29
	r.Temperature = 0
	r.Signal = "accumulate"
	r.Multiplier = 1.5
	o = runModel(t, in, r)
	if o.Adjustment != 15 || o.Temperature != 97 || o.Signal != "pause_or_trim" || o.Multiplier > .3 {
		t.Fatal(o)
	}
}
func TestMalformedSchemaRetriesAndTimeout(t *testing.T) {
	in := scenario(0)
	r := modelReport(in)
	valid, _ := json.Marshal(r)
	var raw map[string]any
	json.Unmarshal(valid, &raw)
	delete(raw, "event_throttle_applied")
	bad, _ := json.Marshal(raw)
	for _, s := range [][]byte{bad, append(slices.Clone(valid), []byte(" {}")...), []byte(strings.Replace(string(valid), "\"low\"", "null", 1) + "!"), []byte(strings.Replace(string(valid), "\"high\"", "\"certain\"", 1))} {
		if _, err := decodeReport(s); err == nil {
			t.Fatal("accepted invalid JSON", string(s))
		}
	}
	attempts := 0
	m := modelFunc(func(ctx context.Context, _ Input, feedback string) ([]byte, error) {
		attempts++
		if attempts > 1 && !strings.Contains(feedback, "event_throttle_applied") {
			t.Fatal("missing validator feedback", feedback)
		}
		if attempts < 3 {
			return bad, nil
		}
		return valid, nil
	})
	o := analyze(context.Background(), in, m)
	if attempts != 3 || o.Mode != "llm" {
		t.Fatal(o)
	}
	attempts = 0
	m = func(context.Context, Input, string) ([]byte, error) { attempts++; return bad, nil }
	o = analyze(context.Background(), in, m)
	if attempts != 3 || o.Mode != "quant_only" || o.Adjustment != 0 || o.Confidence != "low" || !strings.Contains(strings.Join(o.Warnings, " "), "event_throttle_applied") {
		t.Fatal(o)
	}
	m = func(context.Context, Input, string) ([]byte, error) { return nil, context.DeadlineExceeded }
	o = analyze(context.Background(), in, m)
	if o.Base != 20 || o.Temperature != 20 || o.Confidence != "low" || o.Adjustment != 0 {
		t.Fatal(o)
	}
	in.Previous = &Prior{Temperature: 16}
	o = analyze(context.Background(), in, nil)
	if o.Delta == nil || *o.Delta != 4 {
		t.Fatal(o)
	}
	if !strings.Contains(Brief(o), "失效条件") || !strings.Contains(Brief(o), "█") {
		t.Fatal(Brief(o))
	}
}

type roundTrip func(*http.Request) (*http.Response, error)

func (fn roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return fn(r) }
func reply(code int, body string) *http.Response {
	return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
}
func TestChatWireAndSecrets(t *testing.T) {
	in := scenario(0)
	key := "offline-secret"
	client := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		if r.Header.Get("Authorization") != "Bearer "+key || r.URL.Path != "/v1/chat/completions" {
			t.Fatal("invalid request")
		}
		var payload struct {
			Messages []struct{ Role, Content string }
			Model    string
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload.Model != "offline-model" || payload.Messages[0].Content != systemPrompt || !strings.Contains(payload.Messages[3].Content, "bad score") {
			t.Fatal("system prompt / correction")
		}
		var input map[string]json.RawMessage
		if err := json.Unmarshal([]byte(payload.Messages[2].Content), &input); err != nil {
			t.Fatal(err)
		}
		for _, k := range strings.Fields("strategy_style quant_base_score D1 D2 D3 index_preference_baseline previous_snapshot market_data macro_and_fed news_and_fed_headlines_48h upcoming_macro_calendar_72h") {
			if _, ok := input[k]; !ok {
				t.Fatal(k)
			}
		}
		if strings.Contains(payload.Messages[2].Content, key) {
			t.Fatal("key leaked")
		}
		return reply(200, `{"choices":[{"message":{"role":"assistant","content":"{}"}}]}`), nil
	})}
	c := ChatClient{client, "https://example.test/v1", key, "offline-model"}
	if data, err := c.Complete(context.Background(), in, "bad score"); err != nil || string(data) != "{}" {
		t.Fatal(string(data), err)
	}
	c.BaseURL = "http://example.test"
	if _, err := c.Complete(context.Background(), in, ""); err == nil {
		t.Fatal("unencrypted transport")
	}
	f := Fetcher{Client: &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) { return nil, errors.New("offline") })}, Now: in.AsOf, FREDKey: key, Dir: t.TempDir()}
	_, _, warning := f.loadSeries(context.Background(), "DFF", "2024-01-01", true)
	if warning == "" || strings.Contains(warning, key) {
		t.Fatal(warning)
	}
}
