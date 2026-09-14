package corectl

import (
	"fmt"
	"io"
	"os"
	"strings"
	"unicode/utf8"
)

const maxLogBytes = 64 << 10

func Tail(path string, lines int) (string, error) {
	if lines < 1 || lines > 1000 {
		return "", fmt.Errorf("lines must be 1..1000")
	}
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	defer f.Close()
	st, err := f.Stat()
	if err != nil {
		return "", err
	}
	if !st.Mode().IsRegular() {
		return "", fmt.Errorf("log is not a regular file")
	}
	pos := st.Size()
	buf := []byte{}
	for pos > 0 && len(buf) < maxLogBytes && strings.Count(string(buf), "\n") <= lines {
		n := min(int64(4096), pos, int64(maxLogBytes-len(buf)))
		pos -= n
		block := make([]byte, n)
		if _, err = f.ReadAt(block, pos); err != nil && err != io.EOF {
			return "", err
		}
		buf = append(block, buf...)
	}
	// A long line or UTF-8 character may straddle the bounded read window.
	for len(buf) > 0 && !utf8.RuneStart(buf[0]) {
		buf = buf[1:]
	}
	text := strings.TrimSuffix(string(buf), "\n")
	parts := strings.Split(text, "\n")
	if len(parts) > lines {
		parts = parts[len(parts)-lines:]
	}
	return strings.Join(parts, "\n"), nil
}
