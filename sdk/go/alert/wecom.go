package alert

import "context"

// sendWeCom POSTs a text message to a WeCom (WeChat Work) group-bot webhook. The
// bot's key is already embedded in ch.WebhookURL; WeCom's basic text messages
// have no additional signing scheme (unlike Feishu/DingTalk).
func sendWeCom(ctx context.Context, ch Channel, msg Message) error {
	body := map[string]any{
		"msgtype": "text",
		"text":    map[string]string{"content": formatText(msg)},
	}
	return postJSON(ctx, ch.WebhookURL, body)
}
