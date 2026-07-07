package alert

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// sendDingTalk POSTs a text message to a DingTalk custom-bot webhook. When
// ch.Secret is set, signs per DingTalk's documented scheme — distinct from
// Feishu's: HMAC-SHA256 keyed by the secret itself, over "{timestamp_ms}\n
// {secret}", base64-encoded and URL-escaped, appended as timestamp+sign query
// params (not in the body).
func sendDingTalk(ctx context.Context, ch Channel, msg Message) error {
	body := map[string]any{
		"msgtype": "text",
		"text":    map[string]string{"content": formatText(msg)},
	}
	target := ch.WebhookURL
	if ch.Secret != "" {
		ts := time.Now().UnixMilli()
		sign := dingTalkSign(ts, ch.Secret)
		sep := "?"
		if strings.Contains(target, "?") {
			sep = "&"
		}
		target += sep + "timestamp=" + strconv.FormatInt(ts, 10) + "&sign=" + url.QueryEscape(sign)
	}
	return postJSON(ctx, target, body)
}

func dingTalkSign(timestampMS int64, secret string) string {
	stringToSign := strconv.FormatInt(timestampMS, 10) + "\n" + secret
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(stringToSign))
	return base64.StdEncoding.EncodeToString(h.Sum(nil))
}
