package node

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/FredrickUnderwood/agenda-v2/internal/contract"
)

func newFileTestServer(t *testing.T, roots []string, maxUpload int64) (*httptest.Server, string) {
	t.Helper()
	const token = "node-token"
	s := NewServer(token, NewJobStore(1024, time.Hour), NewProxyRegistry(), "127.0.0.1")
	s.SetFileConfig(roots, maxUpload)
	srv := httptest.NewServer(s.Handler())
	t.Cleanup(srv.Close)
	return srv, token
}

func putFile(t *testing.T, srv *httptest.Server, token, path, body string, overwrite bool) (int, contract.FileStat, string) {
	t.Helper()
	q := url.Values{contract.NodeFileQueryPath: {path}}
	if overwrite {
		q.Set(contract.NodeFileQueryOverwrite, "true")
	}
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/v1/files?"+q.Encode(), strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set(contract.HeaderNodeToken, token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var stat contract.FileStat
	_ = json.Unmarshal(raw, &stat)
	return resp.StatusCode, stat, string(raw)
}

func statViaHTTP(t *testing.T, srv *httptest.Server, token, path string) (int, contract.FileStat) {
	t.Helper()
	q := url.Values{contract.NodeFileQueryPath: {path}}
	req, err := http.NewRequest(http.MethodGet, srv.URL+"/v1/files/stat?"+q.Encode(), nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set(contract.HeaderNodeToken, token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var stat contract.FileStat
	_ = json.Unmarshal(raw, &stat)
	return resp.StatusCode, stat
}

func TestPutAndStatFile_RoundTrip(t *testing.T) {
	root := t.TempDir()
	srv, token := newFileTestServer(t, []string{root}, 0)
	target := filepath.Join(root, "app", "prod", ".files", "key.p8")

	code, stat, body := putFile(t, srv, token, target, "secret-key", false)
	if code != http.StatusOK {
		t.Fatalf("put status = %d, body = %s", code, body)
	}
	if stat.Size != int64(len("secret-key")) || stat.SHA256 == "" {
		t.Fatalf("put stat = %+v", stat)
	}
	if got, err := os.ReadFile(target); err != nil || string(got) != "secret-key" {
		t.Fatalf("on-disk content = %q, err = %v", got, err)
	}

	// Stat must report the same checksum for the same bytes; that equality is
	// what every later verification depends on.
	code, got := statViaHTTP(t, srv, token, target)
	if code != http.StatusOK || !got.Exists {
		t.Fatalf("stat status = %d, stat = %+v", code, got)
	}
	if got.SHA256 != stat.SHA256 {
		t.Errorf("stat sha256 = %s, put sha256 = %s", got.SHA256, stat.SHA256)
	}
}

func TestPutFile_ExistingFileIsAConflictUnlessOverwriteRequested(t *testing.T) {
	root := t.TempDir()
	srv, token := newFileTestServer(t, []string{root}, 0)
	target := filepath.Join(root, "f")

	if code, _, body := putFile(t, srv, token, target, "one", false); code != http.StatusOK {
		t.Fatalf("first put status = %d, body = %s", code, body)
	}
	if code, _, _ := putFile(t, srv, token, target, "two", false); code != http.StatusConflict {
		t.Fatalf("second put status = %d, want 409", code)
	}
	if code, _, body := putFile(t, srv, token, target, "two", true); code != http.StatusOK {
		t.Fatalf("overwrite status = %d, body = %s", code, body)
	}
	if got, _ := os.ReadFile(target); string(got) != "two" {
		t.Errorf("content = %q, want %q", got, "two")
	}
}

func TestPutFile_OutsideConfiguredRootsIsForbidden(t *testing.T) {
	root := t.TempDir()
	other := t.TempDir()
	srv, token := newFileTestServer(t, []string{root}, 0)

	if code, _, _ := putFile(t, srv, token, filepath.Join(other, "x"), "data", false); code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", code)
	}
	if code, _ := statViaHTTP(t, srv, token, filepath.Join(other, "x")); code != http.StatusForbidden {
		t.Fatalf("stat status = %d, want 403", code)
	}
}

func TestPutFile_OversizeIsRejectedAndNothingIsLeftBehind(t *testing.T) {
	root := t.TempDir()
	srv, token := newFileTestServer(t, []string{root}, 4)

	code, _, _ := putFile(t, srv, token, filepath.Join(root, "big"), "0123456789", false)
	if code != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", code)
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("root not clean after rejected upload: %v", entries)
	}
}

func TestStatFile_MissingReportsExistsFalseNotAnError(t *testing.T) {
	root := t.TempDir()
	srv, token := newFileTestServer(t, []string{root}, 0)

	// A 404 here would be indistinguishable from the node being unreachable,
	// which is the one distinction verification depends on.
	code, stat := statViaHTTP(t, srv, token, filepath.Join(root, "absent"))
	if code != http.StatusOK {
		t.Fatalf("status = %d, want 200", code)
	}
	if stat.Exists {
		t.Error("Exists = true for an absent file")
	}
}

func TestFileEndpointsRequireToken(t *testing.T) {
	root := t.TempDir()
	srv, _ := newFileTestServer(t, []string{root}, 0)

	resp, err := http.Post(srv.URL+"/v1/files?path="+url.QueryEscape(filepath.Join(root, "x")), "application/octet-stream", strings.NewReader("x"))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}
