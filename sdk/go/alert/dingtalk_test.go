package alert

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
)

func TestSendDingTalk_NoSecret_NoQueryParams(t *testing.T) {
	var gotQuery string
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		json.NewDecoder(r.Body).Decode(&captured)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	err := sendDingTalk(context.Background(), Channel{WebhookURL: srv.URL}, Message{Title: "t", Content: "c", Level: LevelCritical})
	if err != nil {
		t.Fatalf("sendDingTalk: %v", err)
	}
	if gotQuery != "" {
		t.Errorf("expected no query params, got %q", gotQuery)
	}
	if captured["msgtype"] != "text" {
		t.Errorf("msgtype = %v", captured["msgtype"])
	}
	text, _ := captured["text"].(map[string]any)
	if text["content"] != "[critical] t\nc" {
		t.Errorf("content = %v", text["content"])
	}
}

func TestSendDingTalk_WithSecret_SignsCorrectly(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	err := sendDingTalk(context.Background(), Channel{WebhookURL: srv.URL, Secret: "topsecret"}, Message{Title: "t"})
	if err != nil {
		t.Fatalf("sendDingTalk: %v", err)
	}

	q, err := url.ParseQuery(gotQuery)
	if err != nil {
		t.Fatalf("parse query: %v", err)
	}
	tsStr := q.Get("timestamp")
	ts, _ := strconv.ParseInt(tsStr, 10, 64)
	if ts == 0 {
		t.Fatalf("timestamp not captured: %q", gotQuery)
	}

	stringToSign := strconv.FormatInt(ts, 10) + "\n" + "topsecret"
	h := hmac.New(sha256.New, []byte("topsecret"))
	h.Write([]byte(stringToSign))
	want := base64.StdEncoding.EncodeToString(h.Sum(nil))

	if q.Get("sign") != want {
		t.Errorf("sign = %v, want %v", q.Get("sign"), want)
	}
}

func TestSendDingTalk_AppendsToExistingQuery(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	err := sendDingTalk(context.Background(), Channel{WebhookURL: srv.URL + "?access_token=abc", Secret: "s"}, Message{Title: "t"})
	if err != nil {
		t.Fatalf("sendDingTalk: %v", err)
	}
	q, _ := url.ParseQuery(gotQuery)
	if q.Get("access_token") != "abc" {
		t.Errorf("expected access_token preserved, got query %q", gotQuery)
	}
	if q.Get("sign") == "" {
		t.Errorf("expected sign appended, got query %q", gotQuery)
	}
}
