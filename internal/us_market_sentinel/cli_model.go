//go:build darwin

package sentinel

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

type CLIModel struct{ Tool, Path, Model string }

type CLIModelOption struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

func (c CLIModel) executable() (string, error) {
	if c.Tool != "codex" && c.Tool != "claude" && c.Tool != "agy" && c.Tool != "opencode" {
		return "", errors.New("未知的本地模型工具")
	}
	if c.Path != "" {
		if filepath.IsAbs(c.Path) {
			if path, err := exec.LookPath(c.Path); err == nil {
				return path, nil
			}
		}
		return "", fmt.Errorf("%s 路径无效，请在设置中选择可执行文件", c.Tool)
	}
	if path, err := exec.LookPath(c.Tool); err == nil && filepath.IsAbs(path) {
		return path, nil
	}
	home, _ := os.UserHomeDir()
	candidates := []string{"/opt/homebrew/bin/" + c.Tool, "/usr/local/bin/" + c.Tool, filepath.Join(home, ".local", "bin", c.Tool)}
	switch c.Tool {
	case "codex":
		candidates = append(candidates, "/Applications/Codex.app/Contents/Resources/codex", "/Applications/ChatGPT.app/Contents/Resources/codex-cli/CodexCLI.app/Contents/MacOS/codex")
	case "claude":
		candidates = append(candidates, filepath.Join(home, ".claude", "local", "claude"))
	case "agy":
		candidates = append(candidates, filepath.Join(home, ".gemini", "antigravity", "bin", "agy"))
	case "opencode":
		candidates = append(candidates, filepath.Join(home, ".opencode", "bin", "opencode"), filepath.Join(home, ".bun", "bin", "opencode"), "/Applications/OpenCode.app/Contents/MacOS/opencode")
	}
	for _, candidate := range candidates {
		if path, err := exec.LookPath(candidate); err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("找不到 %s，请安装并登录，或在设置中填写可执行文件路径", c.Tool)
}

const cliOutputLimit = 192 << 10

// Drain all output to avoid a blocked writer, but retain at most one report.
type cliOutput struct {
	body     bytes.Buffer
	tooLarge bool
}

func (b *cliOutput) Write(p []byte) (int, error) {
	n := min(len(p), cliOutputLimit-b.body.Len())
	b.body.Write(p[:n])
	b.tooLarge = b.tooLarge || n < len(p)
	return len(p), nil
}

func (c CLIModel) Complete(ctx context.Context, in Input, feedback string) ([]byte, error) {
	path, err := c.executable()
	if err != nil {
		return nil, err
	}
	messages, err := analysisMessages(in, feedback)
	if err != nil {
		return nil, err
	}
	payload, err := json.Marshal(messages)
	if err != nil {
		return nil, err
	}
	dir, err := os.MkdirTemp("", "samuel-sentinel-cli-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	file := filepath.Join(dir, "report.json")
	var args []string
	var stdout cliOutput
	stdinData := payload
	switch c.Tool {
	case "codex":
		args = []string{"exec", "--skip-git-repo-check", "--ephemeral", "--sandbox", "read-only", "-c", `approval_policy="never"`,
			"-c", "features.shell_tool=false", "-c", "features.unified_exec=false", "-c", "features.plugins=false",
			"-c", "mcp_servers={}", "-c", `web_search="disabled"`, "--color", "never", "--output-last-message", file}
		if c.Model != "" {
			args = append(args, "-c", fmt.Sprintf("model=%q", c.Model))
		}
		args = append(args, "-")
	case "claude":
		args = []string{"-p", "--output-format", "text", "--tools", "", "--strict-mcp-config", "--mcp-config", `{"mcpServers":{}}`, "--no-session-persistence"}
		if c.Model != "" {
			args = append(args, "--model", c.Model)
		}
	case "agy":
		args = []string{"--input-format", "stream-json", "--output-format", "stream-json", "--disable-slash-commands", "--print-timeout", "90s", "--sandbox"}
		if c.Model != "" {
			args = append(args, "--model", c.Model)
		} else {
			args = append(args, "--effort", "low")
		}
		var envelope struct {
			Event   string `json:"event"`
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		}
		envelope.Event = "user"
		envelope.Message.Content = string(payload)
		line, err := json.Marshal(envelope)
		if err != nil {
			return nil, err
		}
		stdinData = append(line, '\n')
	case "opencode":
		args = []string{"run", "--archived", "--format", "json"}
		if c.Model != "" {
			args = append(args, "--model", c.Model)
		}
		stdinData = append(bytes.Clone(payload), '\n')
	}
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Dir = dir
	cmd.Stdin = bytes.NewReader(stdinData)
	var streamPipe io.ReadCloser
	var streamResult chan struct {
		body []byte
		err  error
	}
	switch c.Tool {
	case "claude":
		cmd.Stdout = &stdout
	case "agy", "opencode":
		streamPipe, err = cmd.StdoutPipe()
		if err != nil {
			return nil, err
		}
		streamResult = make(chan struct {
			body []byte
			err  error
		}, 1)
	default:
		cmd.Stdout = io.Discard
	}
	cmd.Stderr = io.Discard // model/tool diagnostics may contain credentials or the entire prompt
	cmd.Env = cliEnv(path)
	// CLI launchers may spawn a model worker. Cancel the complete process group.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if err == syscall.ESRCH {
			return os.ErrProcessDone
		}
		return err
	}
	cmd.WaitDelay = 2 * time.Second
	if c.Tool == "agy" || c.Tool == "opencode" {
		if err = cmd.Start(); err != nil {
			return nil, fmt.Errorf("%s 调用失败，请在终端检查 CLI 版本、登录状态与账户额度", c.Tool)
		}
		parser := parseAgyStreamJSON
		if c.Tool == "opencode" {
			parser = parseOpenCodeJSON
		}
		go func() {
			body, parseErr := parser(streamPipe)
			_, _ = io.Copy(io.Discard, streamPipe)
			streamResult <- struct {
				body []byte
				err  error
			}{body, parseErr}
		}()
		parsed := <-streamResult
		err = cmd.Wait()
		if err != nil {
			if ctx.Err() != nil {
				return nil, fmt.Errorf("%s 研判超时或已取消：%w", c.Tool, ctx.Err())
			}
			return nil, fmt.Errorf("%s 调用失败，请在终端检查 CLI 版本、登录状态与账户额度", c.Tool)
		}
		return parsed.body, parsed.err
	}
	if err = cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("%s 研判超时或已取消：%w", c.Tool, ctx.Err())
		}
		return nil, fmt.Errorf("%s 调用失败，请在终端检查 CLI 版本、登录状态与账户额度", c.Tool)
	}
	if c.Tool == "claude" {
		if stdout.tooLarge || stdout.body.Len() == 0 {
			return nil, errModelEnvelope
		}
		return stripCodeFence(stdout.body.Bytes()), nil
	}
	output, err := os.Open(file)
	if err != nil {
		return nil, errModelEnvelope
	}
	defer output.Close()
	raw, err := io.ReadAll(io.LimitReader(output, cliOutputLimit+1))
	if err != nil || len(raw) == 0 || len(raw) > cliOutputLimit {
		return nil, errModelEnvelope
	}
	return stripCodeFence(raw), nil
}

func cliEnv(path string) []string {
	var env []string
	for _, item := range os.Environ() {
		k, _, _ := strings.Cut(item, "=")
		switch k {
		case "PATH", "FRED_API_KEY", "SAMUEL_LLM_API_KEY", "OPENAI_API_KEY", "ANTHROPIC_API_KEY", "CODEX_API_KEY":
			continue
		}
		env = append(env, item)
	}
	// GUI apps often lack Homebrew in PATH. Node-based CLI launchers need the
	// interpreter in the same tools directory as the selected executable.
	paths := []string{filepath.Dir(path), "/opt/homebrew/bin", "/usr/local/bin", "/usr/bin", "/bin"}
	for _, entry := range filepath.SplitList(os.Getenv("PATH")) {
		if filepath.IsAbs(entry) {
			paths = append(paths, entry)
		}
	}
	return append(env, "PATH="+strings.Join(paths, string(os.PathListSeparator)))
}

func parseAgyStreamJSON(r io.Reader) ([]byte, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64<<10), 1<<20)
	var final []byte
	for scanner.Scan() {
		var ev struct {
			Event  string `json:"event"`
			Result *struct {
				Status   string `json:"status"`
				Response string `json:"response"`
			} `json:"result"`
		}
		if json.Unmarshal(scanner.Bytes(), &ev) == nil && ev.Event == "result" && ev.Result != nil {
			if ev.Result.Status != "SUCCESS" || len(ev.Result.Response) == 0 || len(ev.Result.Response) > cliOutputLimit {
				return nil, errModelEnvelope
			}
			final = stripCodeFence([]byte(ev.Result.Response))
		}
	}
	if scanner.Err() != nil || len(final) == 0 {
		return nil, errModelEnvelope
	}
	return final, nil
}

func parseOpenCodeJSON(r io.Reader) ([]byte, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64<<10), 1<<20)
	var out bytes.Buffer
	for scanner.Scan() {
		var ev struct {
			Type string `json:"type"`
			Part *struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"part"`
		}
		if json.Unmarshal(scanner.Bytes(), &ev) == nil && ev.Type == "text" && ev.Part != nil && ev.Part.Text != "" {
			if out.Len()+len(ev.Part.Text) > cliOutputLimit {
				return nil, errModelEnvelope
			}
			out.WriteString(ev.Part.Text)
		}
	}
	if scanner.Err() != nil || out.Len() == 0 {
		return nil, errModelEnvelope
	}
	return stripCodeFence(out.Bytes()), nil
}

func listCLIModels(ctx context.Context, tool, customPath string) []CLIModelOption {
	if tool != "agy" && tool != "opencode" {
		return []CLIModelOption{}
	}
	path, err := (CLIModel{Tool: tool, Path: customPath}).executable()
	if err != nil {
		return []CLIModelOption{}
	}
	queryCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	cmd := exec.CommandContext(queryCtx, path, "models")
	cmd.Env = cliEnv(path)
	cmd.Stderr = io.Discard
	var out cliOutput
	cmd.Stdout = &out
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if err == syscall.ESRCH {
			return os.ErrProcessDone
		}
		return err
	}
	if cmd.Run() != nil {
		return []CLIModelOption{}
	}
	return parseCLIModelList(out.body.Bytes())
}

func parseCLIModelList(raw []byte) []CLIModelOption {
	options := []CLIModelOption{}
	seen := map[string]bool{}
	for _, line := range strings.Split(string(raw), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "-") {
			continue
		}
		id, label, hasTab := strings.Cut(line, "\t")
		id = strings.TrimSpace(id)
		label = strings.TrimSpace(label)
		if !hasTab {
			fields := strings.Fields(line)
			if len(fields) == 0 {
				continue
			}
			id = fields[0]
			label = strings.TrimSpace(strings.TrimPrefix(line, id))
		}
		if id == "" || strings.ContainsAny(id, " \t\r\n") || strings.HasPrefix(id, "-") || seen[id] {
			continue
		}
		seen[id] = true
		if label == "" {
			label = id
		}
		options = append(options, CLIModelOption{ID: id, Label: label})
	}
	return options
}

func stripCodeFence(raw []byte) []byte {
	trimmed := bytes.TrimSpace(raw)
	if !bytes.HasPrefix(trimmed, []byte("```")) || !bytes.HasSuffix(trimmed, []byte("```")) {
		return trimmed
	}
	newline := bytes.IndexByte(trimmed, '\n')
	if newline < 0 {
		return trimmed
	}
	inner := trimmed[newline+1 : len(trimmed)-3]
	if bytes.Contains(inner, []byte("```")) {
		return trimmed
	}
	return bytes.TrimSpace(inner)
}
