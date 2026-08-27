package runner

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/FredrickUnderwood/agenda-v2/internal/filestore"
)

func TestLocalRunnerPutAndStatFile(t *testing.T) {
	l := &localRunner{}
	target := filepath.Join(t.TempDir(), "sub", "cred.p8")

	put, err := l.PutFile(context.Background(), target, strings.NewReader("payload"), "0640", false)
	if err != nil {
		t.Fatalf("PutFile: %v", err)
	}
	if put.Mode != "0640" || put.Size != 7 {
		t.Fatalf("put = %+v", put)
	}

	stat, err := l.StatFile(context.Background(), target)
	if err != nil {
		t.Fatalf("StatFile: %v", err)
	}
	if stat.SHA256 != put.SHA256 {
		t.Errorf("stat sha256 = %s, put sha256 = %s", stat.SHA256, put.SHA256)
	}

	if _, err := l.PutFile(context.Background(), target, strings.NewReader("again"), "0640", false); err != filestore.ErrExists {
		t.Errorf("re-put err = %v, want ErrExists", err)
	}
	if got, _ := os.ReadFile(target); string(got) != "payload" {
		t.Errorf("content = %q; a refused overwrite must leave the original", got)
	}
}

func TestLocalRunnerRejectsRelativePath(t *testing.T) {
	l := &localRunner{}
	if _, err := l.PutFile(context.Background(), "relative/path", strings.NewReader("x"), "", false); err == nil {
		t.Fatal("relative path accepted")
	}
}

func TestParseStatOutput(t *testing.T) {
	tests := []struct {
		name    string
		out     string
		wantErr bool
	}{
		{name: "missing", out: "missing\n"},
		{name: "dir", out: "dir\n"},
		{name: "file", out: "file 12 600 1700000000 abc123\n"},
		{name: "empty", out: "\n", wantErr: true},
		{name: "garbage", out: "what is this\n", wantErr: true},
		{name: "bad size", out: "file x 600 1700000000 abc123\n", wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseStatOutput(tc.out)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("parseStatOutput(%q) = %+v, want error", tc.out, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseStatOutput(%q): %v", tc.out, err)
			}
			switch tc.name {
			case "missing":
				if got.Exists {
					t.Error("Exists = true for 'missing'")
				}
			case "dir":
				if !got.IsDir {
					t.Error("IsDir = false for 'dir'")
				}
			case "file":
				// `stat -c %a` prints 600; the rest of the system spells the
				// same mode 0600, and a mode that compares unequal across
				// backends would report every SSH-hosted file as changed.
				if got.Mode != "0600" {
					t.Errorf("Mode = %q, want 0600", got.Mode)
				}
				if got.Size != 12 || got.SHA256 != "abc123" || got.ModTimeUnix != 1700000000 {
					t.Errorf("got = %+v", got)
				}
			}
		})
	}
}

// The remote scripts are built for POSIX paths regardless of the control
// plane's own OS, so the parent of a target must not pick up a host separator.
func TestParentDir(t *testing.T) {
	for in, want := range map[string]string{
		"/a/b/c.txt": "/a/b",
		"/top.txt":   "/",
		"bare":       ".",
	} {
		if got := parentDir(in); got != want {
			t.Errorf("parentDir(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNormalizeOctal(t *testing.T) {
	for in, want := range map[string]string{"600": "0600", "0600": "0600", "": ""} {
		if got := normalizeOctal(in); got != want {
			t.Errorf("normalizeOctal(%q) = %q, want %q", in, got, want)
		}
	}
}
