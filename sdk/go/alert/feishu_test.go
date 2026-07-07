package alert

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

func TestSendFeishu_NoSecret_NoSignFields(t *testing.T) {
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&captured)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	err := sendFeishu(context.Background(), Channel{WebhookURL: srv.URL}, Message{Title: "t", Content: "c", Level: LevelWarning})
	if err != nil {
		t.Fatalf("sendFeishu: %v", err)
	}
	if captured["msg_type"] != "text" {
		t.Errorf("msg_type = %v", captured["msg_type"])
	}
	content, _ := captured["content"].(map[string]any)
	if content["text"] != "[warning] t\nc" {
		t.Errorf("text = %v", content["text"])
	}
	if _, ok := captured["sign"]; ok {
		t.Error("expected no sign field when secret is empty")
	}
}

func TestSendFeishu_WithSecret_SignsCorrectly(t *testing.T) {
	var captured map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&captured)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	err := sendFeishu(context.Background(), Channel{WebhookURL: srv.URL, Secret: "shh"}, Message{Title: "t", Content: "c"})
	if err != nil {
		t.Fatalf("sendFeishu: %v", err)
	}
	tsStr, _ := captured["timestamp"].(string)
	ts, _ := strconv.ParseInt(tsStr, 10, 64)
	if ts == 0 {
		t.Fatalf("timestamp not captured: %v", captured["timestamp"])
	}

	stringToSign := strconv.FormatInt(ts, 10) + "\n" + "shh"
	h := hmac.New(sha256.New, []byte(stringToSign))
	h.Write(nil)
	want := base64.StdEncoding.EncodeToString(h.Sum(nil))

	if captured["sign"] != want {
		t.Errorf("sign = %v, want %v", captured["sign"], want)
	}
}

func TestSendFeishu_ErrorStatusReturnsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	if err := sendFeishu(context.Background(), Channel{WebhookURL: srv.URL}, Message{Title: "t"}); err == nil {
		t.Fatal("expected error for 500 response")
	}
}
