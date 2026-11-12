//go:build windows && (amd64 || arm64)

package market

import (
	"fmt"
	"unicode/utf16"
	"unicode/utf8"
	"unsafe"

	"github.com/hrthas-king/Samuel/internal/winapi"
)

var gbkToWide = winapi.DLL("kernel32.dll").NewProc("MultiByteToWideChar")

func decodeQuoteName(s string) (string, error) {
	if utf8.ValidString(s) {
		return s, nil
	}
	b := []byte(s)
	if len(b) == 0 {
		return "", fmt.Errorf("空名称")
	}
	n, _, _ := gbkToWide.Call(936, 8, uintptr(unsafe.Pointer(&b[0])), uintptr(len(b)), 0, 0)
	if n == 0 {
		return "", fmt.Errorf("名称编码无效")
	}
	w := make([]uint16, n)
	r, _, _ := gbkToWide.Call(936, 8, uintptr(unsafe.Pointer(&b[0])), uintptr(len(b)), uintptr(unsafe.Pointer(&w[0])), n)
	if r == 0 {
		return "", fmt.Errorf("名称编码无效")
	}
	return string(utf16.Decode(w)), nil
}
