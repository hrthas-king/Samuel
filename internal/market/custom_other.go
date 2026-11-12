//go:build !windows && !darwin

package market

import (
	"fmt"
	"unicode/utf8"
)

func decodeQuoteName(s string) (string, error) {
	if !utf8.ValidString(s) {
		return "", fmt.Errorf("GBK解码需要Windows")
	}
	return s, nil
}
