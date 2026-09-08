package outputtail

import (
	"io"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"
)

func TestBufferRetainsTailAcrossWrites(t *testing.T) {
	b := New(64)
	for i := 0; i < 1000; i++ {
		if n, err := io.WriteString(b, "old output\n"); n != 11 || err != nil {
			t.Fatalf("Write = %d, %v", n, err)
		}
	}
	io.WriteString(b, "final failure\n")
	got := b.String()
	if len(got) > 64 || !strings.HasPrefix(got, Marker) || !strings.HasSuffix(got, "final failure\n") {
		t.Fatalf("tail = %q", got)
	}
	if cap(b.data) > 64 {
		t.Fatalf("buffer capacity = %d, want <= 64", cap(b.data))
	}
	io.WriteString(b, strings.Repeat("x", 1<<20)+"last")
	if cap(b.data) > 64 || !strings.HasSuffix(b.String(), "last") {
		t.Fatal("one large write must stay bounded and retain its tail")
	}
}

func TestTailLimits(t *testing.T) {
	for _, tc := range []struct {
		name, input, want string
		bytes, lines      int
	}{
		{"short", "hello\n", "hello\n", 64, 2},
		{"lines with newline", "one\ntwo\nthree\n", Marker + "two\nthree\n", 64, 2},
		{"lines without newline", "one\ntwo\nthree", Marker + "two\nthree", 64, 2},
		{"blank lines", "one\n\n\n", Marker + "\n\n", 64, 2},
		{"tiny byte limit", "abcdef", "def", 3, 0},
		{"invalid UTF-8", "abc\xffdef", "abc?def", 64, 0},
		{"empty", "", "", 64, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := Tail(tc.input, tc.bytes, tc.lines)
			if got != tc.want || len(got) > tc.bytes || !utf8.ValidString(got) {
				t.Fatalf("Tail = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestTailUTF8AndMarkerStayWithinBudget(t *testing.T) {
	for limit := 1; limit < 100; limit++ {
		b := New(limit)
		input := strings.Repeat("构建日志\n", 100) + "最终错误\n"
		// Split writes within UTF-8 characters, as stdout pipes may do.
		for i := 0; i < len(input); i += 5 {
			b.Write([]byte(input[i:min(i+5, len(input))]))
		}
		got := Tail(b.String(), limit, 3)
		if len(got) > limit || !utf8.ValidString(got) {
			t.Fatalf("limit=%d output=%q", limit, got)
		}
		if strings.Count(got, Marker) > 1 {
			t.Fatalf("duplicate truncation markers: %q", got)
		}
	}
}

func TestBufferConcurrentSnapshots(t *testing.T) {
	b := New(1024)
	var wg sync.WaitGroup
	for worker := 0; worker < 4; worker++ {
		wg.Go(func() {
			for i := 0; i < 1000; i++ {
				io.WriteString(b, "构建中\n")
				if got := b.String(); len(got) > 1024 || !utf8.ValidString(got) {
					t.Errorf("invalid concurrent snapshot of %d bytes", len(got))
				}
			}
		})
	}
	wg.Wait()
}
