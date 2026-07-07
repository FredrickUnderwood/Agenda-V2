package alert

import "context"

// sendSlack POSTs to a Slack incoming-webhook URL, whose only required field
// is "text".
func sendSlack(ctx context.Context, ch Channel, msg Message) error {
	body := map[string]any{"text": formatText(msg)}
	return postJSON(ctx, ch.WebhookURL, body)
}
