package alert

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSend_UnsupportedChannelType(t *testing.T) {
	err := Send(context.Background(), Channel{Type: "carrier-pigeon"}, Message{Title: "t"})
	if err == nil {
		t.Fatal("expected error for unsupported channel type")
	}
	var target *UnsupportedChannelTypeError
	if !errors.As(err, &target) {
		t.Fatalf("expected UnsupportedChannelTypeError, got %T: %v", err, err)
	}
}

func TestSendAll_PartialFailureDoesNotHideSuccess(t *testing.T) {
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer good.Close()
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer bad.Close()

	channels := []Channel{
		{Type: ChannelCustom, Name: "good-channel", WebhookURL: good.URL, Enabled: true},
		{Type: ChannelCustom, Name: "bad-channel", WebhookURL: bad.URL, Enabled: true},
	}
	results := SendAll(context.Background(), channels, Message{Title: "t"})
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	byName := map[string]Result{}
	for _, r := range results {
		byName[r.Channel] = r
	}
	if byName["good-channel"].Err != nil {
		t.Errorf("good-channel should have succeeded, got %v", byName["good-channel"].Err)
	}
	if byName["bad-channel"].Err == nil {
		t.Error("bad-channel should have failed")
	}
}

func TestFormatText_DefaultsToInfoLevel(t *testing.T) {
	got := formatText(Message{Title: "t", Content: "c"})
	if got != "[info] t\nc" {
		t.Errorf("formatText = %q", got)
	}
}
