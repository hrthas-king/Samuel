//go:build darwin

package sentinel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

var settingKeys = []string{
	"FRED_API_KEY", "SAMUEL_LLM_BACKEND", "SAMUEL_LLM_BASE_URL", "SAMUEL_LLM_MODEL",
	"SAMUEL_LLM_API_KEY", "OPENAI_API_KEY",
	"SAMUEL_AGY_PATH", "SAMUEL_AGY_MODEL",
	"SAMUEL_OPENCODE_PATH", "SAMUEL_OPENCODE_MODEL",
	"SAMUEL_CODEX_PATH", "SAMUEL_CODEX_MODEL",
	"SAMUEL_CLAUDE_PATH", "SAMUEL_CLAUDE_MODEL",
	"SAMUEL_SENTINEL_STYLE",
}

func envText(dir string) (string, error) {
	f, err := os.Open(filepath.Join(dir, ".env"))
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, (64<<10)+1))
	if len(raw) > 64<<10 {
		return "", errors.New("设置文件超过 64 KiB")
	}
	return string(raw), err
}

// Finder, the preferences editor and the terminal use the same private file.
func settings(dir string) (map[string]string, error) {
	raw, err := envText(dir)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, line := range strings.Split(raw, "\n") {
		k, v, ok := strings.Cut(line, "=")
		k = strings.TrimSpace(k)
		if !ok || !slices.Contains(settingKeys, k) {
			continue
		}
		v = strings.TrimSpace(v)
		if len(v) >= 2 && (v[0] == '"' || v[0] == '\'') && v[len(v)-1] == v[0] {
			v = v[1 : len(v)-1]
		}
		out[k] = v
	}
	for _, k := range settingKeys {
		if v := strings.TrimSpace(os.Getenv(k)); v != "" {
			out[k] = v
		}
	}
	return out, nil
}

// Keys are write-only across the helper/UI boundary. An omitted key preserves
// the saved value, an explicit empty string clears it.
type modelPreferences struct {
	Backend              string `json:"backend"`
	BaseURL              string `json:"baseURL"`
	Model                string `json:"model"`
	AgyPath              string `json:"agyPath"`
	AgyModel             string `json:"agyModel"`
	OpenCodePath         string `json:"opencodePath"`
	OpenCodeModel        string `json:"opencodeModel"`
	CodexPath            string `json:"codexPath"`
	CodexModel           string `json:"codexModel"`
	ClaudePath           string `json:"claudePath"`
	ClaudeModel          string `json:"claudeModel"`
	HasFREDKey           bool   `json:"hasFREDKey"`
	HasAPIKey            bool   `json:"hasAPIKey"`
	EnvironmentOverrides bool   `json:"environmentOverrides"`
}

func preferences(env map[string]string) modelPreferences {
	p := modelPreferences{
		Backend: env["SAMUEL_LLM_BACKEND"], BaseURL: env["SAMUEL_LLM_BASE_URL"], Model: env["SAMUEL_LLM_MODEL"],
		AgyPath: env["SAMUEL_AGY_PATH"], AgyModel: env["SAMUEL_AGY_MODEL"],
		OpenCodePath: env["SAMUEL_OPENCODE_PATH"], OpenCodeModel: env["SAMUEL_OPENCODE_MODEL"],
		CodexPath: env["SAMUEL_CODEX_PATH"], CodexModel: env["SAMUEL_CODEX_MODEL"],
		ClaudePath: env["SAMUEL_CLAUDE_PATH"], ClaudeModel: env["SAMUEL_CLAUDE_MODEL"],
		HasFREDKey: env["FRED_API_KEY"] != "",
		HasAPIKey:  env["SAMUEL_LLM_API_KEY"] != "" || env["OPENAI_API_KEY"] != "",
	}
	if p.Backend == "" {
		p.Backend = "api"
	}
	if p.BaseURL == "" {
		p.BaseURL = "https://api.openai.com/v1"
	}
	for _, k := range settingKeys {
		if k != "SAMUEL_SENTINEL_STYLE" && strings.TrimSpace(os.Getenv(k)) != "" {
			p.EnvironmentOverrides = true
			break
		}
	}
	return p
}

func configuredModel(client *http.Client, env map[string]string) Model {
	p := preferences(env)
	switch p.Backend {
	case "agy":
		return CLIModel{Tool: "agy", Path: p.AgyPath, Model: p.AgyModel}
	case "opencode":
		return CLIModel{Tool: "opencode", Path: p.OpenCodePath, Model: p.OpenCodeModel}
	case "codex":
		return CLIModel{Tool: "codex", Path: p.CodexPath, Model: p.CodexModel}
	case "claude":
		return CLIModel{Tool: "claude", Path: p.ClaudePath, Model: p.ClaudeModel}
	case "api":
		if p.Model != "" && p.HasAPIKey {
			key := env["SAMUEL_LLM_API_KEY"]
			if key == "" {
				key = env["OPENAI_API_KEY"]
			}
			return ChatClient{client, p.BaseURL, key, p.Model}
		}
	}
	return nil
}

func settingsCommand(ctx context.Context, dir string, args []string, input io.Reader, output io.Writer) error {
	if len(args) != 1 || (args[0] != "get" && args[0] != "set") {
		return errors.New("settings get|set")
	}
	if args[0] == "set" {
		raw, err := io.ReadAll(io.LimitReader(input, (32<<10)+1))
		if err != nil || len(raw) > 32<<10 {
			return errors.New("设置输入无效或过长")
		}
		var patch map[string]*string
		if json.Unmarshal(raw, &patch) != nil || patch == nil {
			return errors.New("设置需要 JSON 对象")
		}
		changes := make(map[string]string, len(patch))
		for k, v := range patch {
			if v == nil {
				return errors.New("设置字段必须为字符串")
			}
			changes[k] = *v
		}
		if err = savePreferences(ctx, dir, changes); err != nil {
			return err
		}
	}
	env, err := settings(dir)
	if err != nil {
		return err
	}
	return json.NewEncoder(output).Encode(preferences(env))
}

func modelsCommand(ctx context.Context, dir string, args []string, output io.Writer) error {
	if len(args) < 1 || len(args) > 2 {
		return errors.New("models <agy|opencode|codex|claude> [path]")
	}
	tool := strings.TrimSpace(args[0])
	if tool != "agy" && tool != "opencode" && tool != "codex" && tool != "claude" {
		return errors.New("不支持的模型工具")
	}
	customPath := ""
	if len(args) == 2 {
		customPath = strings.TrimSpace(args[1])
	} else if env, err := settings(dir); err == nil {
		p := preferences(env)
		switch tool {
		case "agy":
			customPath = p.AgyPath
		case "opencode":
			customPath = p.OpenCodePath
		case "codex":
			customPath = p.CodexPath
		case "claude":
			customPath = p.ClaudePath
		}
	}
	return json.NewEncoder(output).Encode(listCLIModels(ctx, tool, customPath))
}

func savePreferences(ctx context.Context, dir string, changes map[string]string) error {
	fields := map[string]string{
		"backend":       "SAMUEL_LLM_BACKEND",
		"baseURL":       "SAMUEL_LLM_BASE_URL",
		"model":         "SAMUEL_LLM_MODEL",
		"agyPath":       "SAMUEL_AGY_PATH",
		"agyModel":      "SAMUEL_AGY_MODEL",
		"opencodePath":  "SAMUEL_OPENCODE_PATH",
		"opencodeModel": "SAMUEL_OPENCODE_MODEL",
		"codexPath":     "SAMUEL_CODEX_PATH",
		"codexModel":    "SAMUEL_CODEX_MODEL",
		"claudePath":    "SAMUEL_CLAUDE_PATH",
		"claudeModel":   "SAMUEL_CLAUDE_MODEL",
		"fredKey":       "FRED_API_KEY",
		"apiKey":        "SAMUEL_LLM_API_KEY",
	}
	updates := map[string]string{}
	for name, raw := range changes {
		k, ok := fields[name]
		if !ok || len(raw) > 4096 || strings.ContainsAny(raw, "\x00\r\n") {
			return errors.New("设置包含无效字段或换行")
		}
		v := strings.TrimSpace(raw)
		switch name {
		case "backend":
			if v != "api" && v != "agy" && v != "opencode" && v != "codex" && v != "claude" {
				return errors.New("请选择 API、Antigravity CLI、OpenCode CLI、Codex CLI 或 Claude CLI")
			}
		case "baseURL":
			if v != "" {
				u, err := url.Parse(v)
				if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
					return errors.New("API 提供商地址需为不含凭据或查询参数的 HTTPS 地址")
				}
			}
		case "agyPath", "opencodePath", "codexPath", "claudePath":
			if v != "" && !filepath.IsAbs(v) {
				return errors.New("CLI 路径需为绝对路径，留空可自动查找")
			}
		case "agyModel", "opencodeModel", "codexModel", "claudeModel":
			if v != "" && (strings.HasPrefix(v, "-") || strings.ContainsAny(v, " \t")) {
				return errors.New("CLI 模型名称不能包含空格或以 - 开头")
			}
		case "apiKey":
			if v == "" {
				updates["OPENAI_API_KEY"] = ""
			}
		}
		updates[k] = v
	}
	unlock, err := (Store{dir}).lock(ctx, "settings")
	if err != nil {
		return err
	}
	defer unlock()
	raw, err := envText(dir)
	if err != nil {
		return err
	}
	var result strings.Builder
	for _, line := range strings.Split(strings.TrimSuffix(raw, "\n"), "\n") {
		key, _, ok := strings.Cut(line, "=")
		if _, replaced := updates[strings.TrimSpace(key)]; ok && replaced {
			continue
		}
		if raw != "" {
			fmt.Fprintln(&result, line)
		}
	}
	for _, k := range settingKeys {
		if v, ok := updates[k]; ok {
			fmt.Fprintf(&result, "%s=\"%s\"\n", k, v)
		}
	}
	if result.Len() > 64<<10 {
		return errors.New("设置文件超过 64 KiB")
	}
	return atomicWrite(filepath.Join(dir, ".env"), func(w io.Writer) error { _, err := io.WriteString(w, result.String()); return err })
}
