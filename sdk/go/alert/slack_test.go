package alert

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSendSlack_PostsTextField(t *testing.T) {
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&captured)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	err := sendSlack(context.Background(), Channel{WebhookURL: srv.URL}, Message{Title: "t", Content: "c", Level: LevelWarning})
	if err != nil {
		t.Fatalf("sendSlack: %v", err)
	}
	if captured["text"] != "[warning] t\nc" {
		t.Errorf("text = %v", captured["text"])
	}
}
