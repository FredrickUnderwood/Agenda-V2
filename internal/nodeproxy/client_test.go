package nodeproxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/FredrickUnderwood/agenda-v2/internal/contract"
)

func TestFetchLogs_BuildsRequestAndParsesResponse(t *testing.T) {
	var gotPath, gotQuery, gotToken string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.RawQuery
		gotToken = r.Header.Get(contract.HeaderNodeToken)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"app":"myapp","instance":"default","logs":[{"service":"api","file":"myapp__default__api.log","lines":["a","b"]}]}`))
	}))
	defer srv.Close()

	resp, err := FetchLogs(context.Background(), srv.URL, "secret-token", "myapp", "default", "/data/myapp/logs", "api", 50)
	if err != nil {
		t.Fatalf("FetchLogs: %v", err)
	}
	if gotPath != "/v1/logs/myapp/default" {
		t.Errorf("path = %q", gotPath)
	}
	if gotToken != "secret-token" {
		t.Errorf("token header = %q", gotToken)
	}
	if !containsAll(gotQuery, "dir=%2Fdata%2Fmyapp%2Flogs", "service=api", "tail=50") {
		t.Errorf("query = %q", gotQuery)
	}
	if resp.App != "myapp" || resp.Instance != "default" || len(resp.Logs) != 1 || resp.Logs[0].Service != "api" {
		t.Fatalf("resp = %+v", resp)
	}
}

func TestFetchLogs_OmitsOptionalParams(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.Write([]byte(`{"app":"myapp","instance":"default","logs":[]}`))
	}))
	defer srv.Close()

	if _, err := FetchLogs(context.Background(), srv.URL, "tok", "myapp", "default", "/data/logs", "", 0); err != nil {
		t.Fatalf("FetchLogs: %v", err)
	}
	if strings.Contains(gotQuery, "service=") || strings.Contains(gotQuery, "tail=") {
		t.Errorf("expected no service/tail params, got query = %q", gotQuery)
	}
}

func TestFetchLogs_ErrorStatusReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"error":"no logs found"}`))
	}))
	defer srv.Close()

	if _, err := FetchLogs(context.Background(), srv.URL, "tok", "myapp", "default", "/data/logs", "", 0); err == nil {
		t.Fatal("expected error for 404 response")
	}
}

func TestFetchLogs_EmptyBaseURL(t *testing.T) {
	if _, err := FetchLogs(context.Background(), "", "tok", "myapp", "default", "/data/logs", "", 0); err == nil {
		t.Fatal("expected error for empty agentBaseURL")
	}
}

func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}
