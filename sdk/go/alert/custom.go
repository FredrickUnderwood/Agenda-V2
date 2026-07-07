package alert

import "context"

// sendCustom POSTs a platform-agnostic JSON body, for any webhook receiver
// that doesn't match one of the built-in platforms.
func sendCustom(ctx context.Context, ch Channel, msg Message) error {
	body := map[string]any{
		"title":   msg.Title,
		"content": msg.Content,
		"level":   string(msg.Level),
	}
	return postJSON(ctx, ch.WebhookURL, body)
}
