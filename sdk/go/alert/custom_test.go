package alert

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSendCustom_PostsStructuredFields(t *testing.T) {
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&captured)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	err := sendCustom(context.Background(), Channel{WebhookURL: srv.URL}, Message{Title: "t", Content: "c", Level: LevelCritical})
	if err != nil {
		t.Fatalf("sendCustom: %v", err)
	}
	if captured["title"] != "t" || captured["content"] != "c" || captured["level"] != "critical" {
		t.Errorf("captured = %+v", captured)
	}
}
