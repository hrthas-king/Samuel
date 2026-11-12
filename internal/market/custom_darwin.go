//go:build darwin && (amd64 || arm64)

package market

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

var quoteNames struct {
	sync.Mutex
	values map[string]string
}

func decodeQuoteName(s string) (string, error) {
	if utf8.ValidString(s) {
		return s, nil
	}
	if len(s) > 256 {
		return "", fmt.Errorf("名称编码无效")
	}
	quoteNames.Lock()
	defer quoteNames.Unlock()
	if decoded, ok := quoteNames.values[s]; ok {
		return decoded, nil
	}
	// macOS ships iconv. Convert each distinct provider name once, without
	// requiring cgo or a third-party text package.
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/bin/iconv", "-f", "GB18030", "-t", "UTF-8")
	cmd.Stdin = strings.NewReader(s)
	raw, err := cmd.Output()
	if err != nil || len(raw) == 0 || !utf8.Valid(raw) {
		return "", fmt.Errorf("名称编码无效")
	}
	if quoteNames.values == nil || len(quoteNames.values) >= 64 {
		quoteNames.values = make(map[string]string)
	}
	decoded := string(raw)
	quoteNames.values[s] = decoded
	return decoded, nil
}
