package alert

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSendWeCom_PostsTextMessage(t *testing.T) {
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&captured)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	err := sendWeCom(context.Background(), Channel{WebhookURL: srv.URL}, Message{Title: "t", Content: "c", Level: LevelInfo})
	if err != nil {
		t.Fatalf("sendWeCom: %v", err)
	}
	if captured["msgtype"] != "text" {
		t.Errorf("msgtype = %v", captured["msgtype"])
	}
	text, _ := captured["text"].(map[string]any)
	if text["content"] != "[info] t\nc" {
		t.Errorf("content = %v", text["content"])
	}
}
