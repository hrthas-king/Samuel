//go:build darwin

package sentinel

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestPublicFREDSevenSeriesAndNetLiquidity(t *testing.T) {
	rows := map[string]string{
		"WALCL": "2025-03-05,7000000\n2025-03-12,7100000", "WTREGEN": "2025-03-05,800000\n2025-03-12,850000",
		"RRPONTSYD": "2025-03-05,90\n2025-03-06,95\n2025-03-12,100", "DGS2": "2025-03-12,4.2",
		"DFF": "2025-03-12,4.3", "DFII10": "2025-03-12,1.7", "BAMLH0A0HYM2": "2025-03-12,3.52",
	}
	var api, csv atomic.Int32
	client := &http.Client{Transport: roundTrip(func(req *http.Request) (*http.Response, error) {
		if req.URL.Host == "api.stlouisfed.org" {
			api.Add(1)
			return reply(403, ""), nil
		}
		csv.Add(1)
		id := req.URL.Query().Get("id")
		if req.URL.Host != "fred.stlouisfed.org" || req.URL.Path != "/graph/fredgraph.csv" || req.URL.Query().Get("cosd") != "2025-03-01" ||
			req.URL.Query().Has("api_key") || strings.Contains(req.UserAgent(), "Mozilla") || rows[id] == "" {
			t.Errorf("unexpected CSV request for series %s", id)
		}
		return reply(200, "\ufeffobservation_date,"+id+"\n2025-02-28,999\n"+rows[id]+"\n2025-03-13,.\n2025-03-14,NaN\n2025-03-17,Inf\n2025-04-01,123\n"), nil
	})}
	f := Fetcher{Dir: t.TempDir(), Now: time.Date(2025, 3, 18, 12, 0, 0, 0, ny), Client: client}
	ctx := context.Background()
	data, sources, w := f.history(ctx, nil, fredSymbols, "2025-03-01")
	if len(w) != 0 || api.Load() != 0 || csv.Load() != 7 {
		t.Fatal(w, api.Load(), csv.Load())
	}
	for _, s := range fredSymbols {
		if sources[s] != "FRED CSV / "+s || data[s]["2025-03-12"] == nil {
			t.Fatal(s, sources[s])
		}
		for _, d := range []string{"2025-02-28", "2025-03-13", "2025-03-14", "2025-03-17", "2025-04-01"} {
			if _, ok := data[s][d]; ok {
				t.Fatal("accepted invalid observation", s, d)
			}
		}
	}
	days, net := liquidity(data, "2025-03-18")
	near(t, net[slices.Index(days, "2025-03-06")], 6105, 1e-8)
	m := macroIndicators(data, "2025-03-18").Measures
	near(t, m["net_liquidity_billions"].Level, 6150, 1e-8)
	near(t, m["hy_oas_pct"].Level, 3.52, 1e-8)
	near(t, m["us2y_minus_dff_bps"].Level, -10, 1e-8)
	f.history(ctx, nil, fredSymbols, "2025-03-01")
	if csv.Load() != 7 {
		t.Fatal("CSV cache not reused")
	}
	f.FREDKey, f.Force = "offline-fred-key", true
	_, src, issue := f.loadSeries(ctx, "BAMLH0A0HYM2", "2025-03-01", true)
	if api.Load() != 1 || csv.Load() != 8 || issue != "" || src != "FRED CSV / BAMLH0A0HYM2" {
		t.Fatal("API failure should fall through silently", src, issue)
	}
	if _, err := parseFREDCSV([]byte("DATE,DFF\n2025-03-01,3"), "WALCL", "2025-03-01", "2025-03-18"); err == nil {
		t.Fatal("wrong series")
	}
	f.FREDKey = ""
	f.Client = &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Query().Has("cosd") {
			return reply(502, ""), nil
		}
		return reply(200, "DATE,DGS2\n1990-03-12,8\n2025-03-12,4.2\n"), nil
	})}
	full, _, issue := f.loadSeries(ctx, "DGS2", "2025-03-01", true)
	if issue != "" || len(full) != 1 {
		t.Fatal("canonical full CSV fallback", full, issue)
	}
	f.Client = &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) { return reply(403, ""), nil })}
	for _, key := range []string{"", "offline-fred-key"} {
		f.FREDKey = key
		_, _, w = f.history(ctx, nil, fredSymbols, "2025-03-01")
		if len(w) != 1 || !strings.Contains(w[0], "7 项") || !strings.Contains(w[0], "403") || !strings.Contains(w[0], "FRED_API_KEY") || strings.Contains(w[0], "offline-fred-key") {
			t.Fatal(w)
		}
		for _, s := range fredSymbols {
			if !strings.Contains(w[0], s) {
				t.Fatal("missing series name", s)
			}
		}
	}
}

func TestCreditGuardEAndObservedDrivers(t *testing.T) {
	for _, tc := range []struct {
		credit   *float64
		coverage float64
		limited  bool
	}{{nil, .54, true}, {nil, .95, true}, {number(3), .59999, true}, {number(3), .60, false}} {
		in := scenario(0)
		in.Macro = MacroData{Measures: Measures{"hy_oas_pct": {Level: tc.credit}}}
		in.Base, in.Coverage = 3, tc.coverage
		in.Market.Measures["vix_vix3m_ratio"] = Metric{Level: number(1.16)}
		in.News = []Headline{{Title: "Published Fed headline", Source: "Federal Reserve", Published: in.AsOf}}
		for _, mode := range []string{"quant_only", "llm"} {
			for _, style := range []string{Left, Right} {
				in.Style = style
				in.Market.VIXDeclining, in.Market.Reclaimed = truth(true), truth(true)
				r := quantReport(in)
				if r.Multiplier != 1.75 {
					t.Fatal("panic fixture", r.Multiplier)
				}
				r.Summary = "盲目加码"
				out := applyGuardrails(in, r, mode)
				if out.DataThrottle != tc.limited || out.CreditMissing != (tc.credit == nil) {
					t.Fatal("guard state", out)
				}
				if tc.limited && (out.Signal != "cautious" || out.Multiplier != 1 || !strings.Contains(out.Summary, "暂停倍投") || !strings.Contains(strings.Join(out.Guards, " "), "E ·")) {
					t.Fatal("unguarded buy", out)
				}
				if tc.credit == nil && (!strings.Contains(out.Summary, "无法排除衰退风险") || !strings.Contains(Brief(out), "缺信用数据")) {
					t.Fatal(out.Summary)
				}
				if !tc.limited && out.Multiplier != 1.75 {
					t.Fatal("0.60 boundary")
				}
				if len(out.Headlines) != 1 || out.Headlines[0].Source != "Federal Reserve" {
					t.Fatal("raw headlines lost")
				}
				if len(out.Drivers) == 0 || !strings.Contains(strings.Join(out.Drivers, " "), "1.16") {
					t.Fatal(out.Drivers)
				}
				for _, fact := range out.Drivers {
					if strings.Contains(fact, "—") || strings.TrimSpace(fact) == "" {
						t.Fatal(fact)
					}
				}
			}
		}
	}
	in := scenario(1)
	in.Coverage = .3
	o := applyGuardrails(in, modelReport(in), "llm")
	if !o.Breaker || !o.DataThrottle || o.Multiplier > .5 {
		t.Fatal("E weakened B", o)
	}
	in = scenario(0)
	in.Macro = MacroData{Measures: Measures{}}
	in.Base, in.Coverage = 50, .54
	neutral := applyGuardrails(in, quantReport(in), "quant_only")
	if !neutral.DataThrottle || !neutral.CreditMissing || neutral.Signal != "hold_normal_dca" || neutral.Multiplier != 1 ||
		!strings.Contains(neutral.Summary, "暂停倍投") || !strings.Contains(Brief(neutral), "仅含趋势项，缺信用数据") {
		t.Fatal("neutral hold under E", neutral)
	}
	empty := quantReport(Input{})
	if len(empty.Drivers) != 0 || strings.Contains(empty.Dimensions.Fed.Summary, "—") {
		t.Fatal("empty observations became facts", empty)
	}
	emptyOut := applyGuardrails(Input{}, empty, "quant_only")
	if !strings.Contains(Brief(emptyOut), "系统性信用  —（缺信用数据）") {
		t.Fatal("missing credit score display", Brief(emptyOut))
	}
}

func TestSettingsUseSamePrivateStoreAndRedactKeys(t *testing.T) {
	dir := t.TempDir()
	for _, k := range settingKeys {
		t.Setenv(k, "")
	}
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte("# user note\nSAMUEL_SENTINEL_STYLE=right_confirm\nOPENAI_API_KEY=legacy-secret\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	cmd := func(op, payload string) error {
		b.Reset()
		return settingsCommand(context.Background(), dir, []string{op}, strings.NewReader(payload), &b)
	}
	if err := cmd("set", `{"fredKey":"private-fred","baseURL":"https://example.test/v1","model":"test","backend":"api"}`); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(b.String(), "private-fred") || strings.Contains(b.String(), "legacy-secret") {
		t.Fatal("secret in settings output")
	}
	var p modelPreferences
	if err := json.Unmarshal(b.Bytes(), &p); err != nil || !p.HasFREDKey || !p.HasAPIKey {
		t.Fatal(p, err)
	}
	env, _ := settings(dir)
	api, ok := configuredModel(nil, env).(ChatClient)
	if !ok || api.Key != "legacy-secret" || api.BaseURL != "https://example.test/v1" || api.Model != "test" || env["SAMUEL_SENTINEL_STYLE"] != Right {
		t.Fatal("API config not reused")
	}
	info, _ := os.Stat(path)
	raw, _ := os.ReadFile(path)
	if info.Mode().Perm() != 0600 || !strings.Contains(string(raw), "# user note") {
		t.Fatal("permissions or unrelated settings lost")
	}
	if err := cmd("set", `{"backend":"agy","agyPath":"/usr/local/bin/agy","agyModel":"gemini-3.8-flash-high","apiKey":""}`); err != nil {
		t.Fatal(err)
	}
	env, _ = settings(dir)
	if cli, ok := configuredModel(nil, env).(CLIModel); !ok || cli.Tool != "agy" || cli.Path != "/usr/local/bin/agy" || cli.Model != "gemini-3.8-flash-high" || env["FRED_API_KEY"] != "private-fred" || preferences(env).HasAPIKey {
		t.Fatal("agy CLI selection or model patch", cli)
	}
	if err := cmd("set", `{"backend":"opencode","opencodePath":"/usr/local/bin/opencode","opencodeModel":"opencode/deepseek-v4.1-flash-free"}`); err != nil {
		t.Fatal(err)
	}
	env, _ = settings(dir)
	if cli, ok := configuredModel(nil, env).(CLIModel); !ok || cli.Tool != "opencode" || cli.Path != "/usr/local/bin/opencode" || cli.Model != "opencode/deepseek-v4.1-flash-free" {
		t.Fatal("opencode CLI selection", cli)
	}
	if err := cmd("set", `{"backend":"claude","claudeModel":"sonnet"}`); err != nil {
		t.Fatal(err)
	}
	env, _ = settings(dir)
	if cli, ok := configuredModel(nil, env).(CLIModel); !ok || cli.Tool != "claude" || cli.Model != "sonnet" {
		t.Fatal("CLI selection", cli)
	}
	t.Setenv("FRED_API_KEY", "external-secret")
	env, _ = settings(dir)
	if env["FRED_API_KEY"] != "external-secret" || !preferences(env).EnvironmentOverrides {
		t.Fatal("environment precedence")
	}
	before, _ := os.ReadFile(path)
	for _, invalid := range []string{`{"baseURL":"http://example.test"}`, `{"model":"x\nOPENAI_API_KEY=fake"}`, `{"backend":"shell"}`, `{"agyPath":"relative"}`, `{"opencodePath":"relative"}`, `{"codexPath":"relative"}`, `{"agyModel":"-bad"}`, `{"opencodeModel":"has space"}`, `{"apiKey":null}`} {
		if err := cmd("set", invalid); err == nil {
			t.Fatal("accepted invalid settings")
		}
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("partial settings write")
	}
}

func TestLocalCLITransportsUseStdinAndValidatedReports(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("SAMUEL_MOCK_CLI_DIR", dir)
	t.Setenv("OPENAI_API_KEY", "must-not-reach-cli")
	in := scenario(0)
	in.Macro.Measures["hy_oas_pct"] = Metric{}
	report, _ := json.Marshal(modelReport(in))
	if err := os.WriteFile(filepath.Join(dir, "reply"), report, 0600); err != nil {
		t.Fatal(err)
	}
	script := `#!/bin/sh
set -eu
test -z "${OPENAI_API_KEY:-}"
if [ "${1:-}" = "models" ]; then
  printf 'gemini-3.8-flash-high\tGemini 3.8 Flash (High)\nopencode/deepseek-v4.1-flash-free\n'
  exit 0
fi
cat > "$SAMUEL_MOCK_CLI_DIR/prompt"
printf '%s\n' "$@" > "$SAMUEL_MOCK_CLI_DIR/args"
if [ "$1" = "exec" ]; then
  while [ "$1" != "--output-last-message" ]; do shift; done
  cp "$SAMUEL_MOCK_CLI_DIR/reply" "$2"
  echo "Codex progress is not part of the answer"
elif [ "$1" = "run" ]; then
  echo '{"type":"step_start"}'
  printf '{"type":"text","part":{"type":"text","text":'
  cat "$SAMUEL_MOCK_CLI_DIR/reply_json_string"
  printf '}}\n'
  echo '{"type":"step_finish"}'
elif [ "$1" = "--input-format" ]; then
  echo '{"event":"init","conversation_id":"mock"}'
  echo '{"event":"step_update","step_update":{"state":"ACTIVE"}}'
  printf '{"event":"result","result":{"status":"SUCCESS","response":'
  cat "$SAMUEL_MOCK_CLI_DIR/reply_json_string"
  printf '}}\n'
else
  cat "$SAMUEL_MOCK_CLI_DIR/reply"
fi
`
	writeReply := func(raw []byte) {
		if err := os.WriteFile(filepath.Join(dir, "reply"), raw, 0600); err != nil {
			t.Fatal(err)
		}
		quoted, _ := json.Marshal(string(raw))
		if err := os.WriteFile(filepath.Join(dir, "reply_json_string"), quoted, 0600); err != nil {
			t.Fatal(err)
		}
	}
	writeReply(report)
	path := filepath.Join(dir, "mock CLI with spaces")
	if err := os.WriteFile(path, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	models := listCLIModels(context.Background(), "agy", path)
	if len(models) != 2 || models[0].ID != "gemini-3.8-flash-high" || models[0].Label != "Gemini 3.8 Flash (High)" || models[1].ID != "opencode/deepseek-v4.1-flash-free" {
		t.Fatal("model list parsing", models)
	}
	for _, tc := range []struct {
		tool, model, wantArg string
	}{
		{"agy", "gemini-3.8-flash-high", "gemini-3.8-flash-high"},
		{"opencode", "opencode/deepseek-v4.1-flash-free", "opencode/deepseek-v4.1-flash-free"},
		{"codex", "gpt-5", `model="gpt-5"`},
		{"claude", "sonnet", "sonnet"},
	} {
		model := CLIModel{Tool: tc.tool, Path: path, Model: tc.model}
		out := analyze(context.Background(), in, model)
		if out.Mode != "llm" || out.Signal != "cautious" || !out.DataThrottle || out.Multiplier > 1 {
			t.Fatal(tc.tool, out)
		}
		if _, err := model.Complete(context.Background(), in, "schema correction"); err != nil {
			t.Fatal(err)
		}
		raw, _ := os.ReadFile(filepath.Join(dir, "prompt"))
		var msgs []message
		if tc.tool == "agy" {
			var env struct {
				Event   string `json:"event"`
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			}
			if err := json.Unmarshal(bytes.TrimSpace(raw), &env); err != nil || env.Event != "user" {
				t.Fatal("invalid agy stream-json input", err, string(raw))
			}
			raw = []byte(env.Message.Content)
		}
		if err := json.Unmarshal(raw, &msgs); err != nil || len(msgs) != 4 || msgs[0].Content != systemPrompt ||
			!strings.Contains(msgs[2].Content, "news_and_fed_headlines_48h") || !strings.Contains(msgs[3].Content, "schema correction") {
			t.Fatal("incomplete stdin prompt", tc.tool, err)
		}
		args, _ := os.ReadFile(filepath.Join(dir, "args"))
		argText := string(args)
		if strings.Contains(argText, "news_and_fed_headlines_48h") || !strings.Contains(argText, tc.wantArg) {
			t.Fatal("unexpected argv", tc.tool, argText)
		}
		if tc.tool == "agy" && strings.Contains(argText, "--effort") {
			t.Fatal("explicit agy model should not be overridden by --effort low", argText)
		}
	}
	if _, err := (CLIModel{Tool: "agy", Path: path}).Complete(context.Background(), in, ""); err != nil {
		t.Fatal(err)
	}
	if args, _ := os.ReadFile(filepath.Join(dir, "args")); !strings.Contains(string(args), "--effort\nlow") {
		t.Fatal("default agy should pass --effort low", string(args))
	}
	fenced := append([]byte("```json\n"), report...)
	fenced = append(fenced, []byte("\n```\n")...)
	writeReply(fenced)
	for _, tool := range []string{"agy", "opencode", "codex", "claude"} {
		out := analyze(context.Background(), in, CLIModel{Tool: tool, Path: path})
		if out.Mode != "llm" || out.Signal != "cautious" || !out.DataThrottle || out.Multiplier > 1 {
			t.Fatal("fenced CLI reply", tool, out)
		}
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\nsleep 20\n"), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := (CLIModel{Tool: "claude", Path: path}).Complete(ctx, in, ""); err == nil || time.Since(start) > 4*time.Second {
		t.Fatal("CLI timeout", err)
	}
	if o := analyze(context.Background(), in, CLIModel{Tool: "codex", Path: filepath.Join(dir, "missing")}); o.Mode != "quant_only" || !o.DataThrottle {
		t.Fatal("CLI failure bypassed fallback", o)
	}
}
