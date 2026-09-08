// Package outputtail keeps bounded command output for deployment diagnostics.
package outputtail

import (
	"strings"
	"sync"
	"unicode/utf8"
)

const Marker = "...[truncated]...\n"

// Buffer is a concurrency-safe writer that retains only the last maxBytes
// bytes. It can be read while a command is still writing stdout/stderr.
type Buffer struct {
	mu        sync.Mutex
	data      []byte
	maxBytes  int
	truncated bool
}

func New(maxBytes int) *Buffer {
	if maxBytes <= 0 {
		maxBytes = 1
	}
	return &Buffer{maxBytes: maxBytes}
}

func (b *Buffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := len(p)
	if n == 0 {
		return 0, nil
	}
	if b.data == nil {
		b.data = make([]byte, 0, b.maxBytes)
	}
	if n >= b.maxBytes {
		b.truncated = b.truncated || len(b.data) > 0 || n > b.maxBytes
		b.data = append(b.data[:0], p[n-b.maxBytes:]...)
	} else {
		if overflow := len(b.data) + n - b.maxBytes; overflow > 0 {
			b.data = b.data[:copy(b.data, b.data[overflow:])]
			b.truncated = true
		}
		b.data = append(b.data, p...)
	}
	return n, nil
}

func (b *Buffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return tail(string(b.data), b.maxBytes, 0, b.truncated)
}

// Tail returns valid UTF-8 containing at most maxBytes bytes and the last
// maxLines log lines (zero means no line limit). A truncation marker, when
// present, is included in the byte budget, in addition to the log lines.
func Tail(s string, maxBytes, maxLines int) string {
	s, truncated := strings.CutPrefix(s, Marker)
	return tail(s, maxBytes, maxLines, truncated)
}

func tail(s string, maxBytes, maxLines int, truncated bool) string {
	if maxBytes <= 0 {
		return ""
	}
	// Commands may emit non-UTF-8 bytes or stop in the middle of a character.
	// Keep database TEXT values valid without expanding their byte count.
	s = strings.ToValidUTF8(s, "?")
	if maxLines > 0 {
		end := len(s)
		if end > 0 && s[end-1] == '\n' {
			end--
		}
		for i, lines := end-1, 1; i >= 0; i-- {
			if s[i] == '\n' {
				if lines == maxLines {
					s, truncated = s[i+1:], true
					break
				}
				lines++
			}
		}
	}
	truncated = truncated || len(s) > maxBytes
	if !truncated {
		return s
	}
	marker := Marker
	if len(marker) >= maxBytes {
		// Tiny explicit limits leave no room for a marker and useful output.
		marker = ""
	}
	budget := maxBytes - len(marker)
	if len(s) > budget {
		start := len(s) - budget
		for start < len(s) && !utf8.RuneStart(s[start]) {
			start++
		}
		s = s[start:]
	}
	return marker + s
}
