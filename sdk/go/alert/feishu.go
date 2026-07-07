package alert

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"strconv"
	"time"
)

// sendFeishu POSTs a text message to a Feishu (Lark) custom-bot webhook. When
// ch.Secret is set, signs per Feishu's documented custom-bot scheme: the
// signing key is "{timestamp}\n{secret}", HMAC-SHA256 over an empty message,
// base64-encoded, with timestamp+sign included in the JSON body (not the URL).
func sendFeishu(ctx context.Context, ch Channel, msg Message) error {
	body := map[string]any{
		"msg_type": "text",
		"content":  map[string]string{"text": formatText(msg)},
	}
	if ch.Secret != "" {
		ts := time.Now().Unix()
		body["timestamp"] = strconv.FormatInt(ts, 10)
		body["sign"] = feishuSign(ts, ch.Secret)
	}
	return postJSON(ctx, ch.WebhookURL, body)
}

func feishuSign(timestamp int64, secret string) string {
	stringToSign := strconv.FormatInt(timestamp, 10) + "\n" + secret
	h := hmac.New(sha256.New, []byte(stringToSign))
	h.Write(nil)
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}
