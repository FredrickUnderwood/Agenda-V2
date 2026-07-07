// Package alert sends ops alerts to chat-platform webhooks (Feishu, DingTalk,
// WeCom, Slack) or a generic custom endpoint. It has no dependency on the rest
// of the platform — like sdk/go/log, it works standalone for any Go project,
// and the control plane is just its first consumer (loading Channel configs
// from its Setting table and calling SendAll).
package alert

import (
	"context"
	"sync"
)

// ChannelType selects which wire format/signing scheme Send uses.
type ChannelType string

const (
	ChannelFeishu   ChannelType = "feishu"
	ChannelDingTalk ChannelType = "dingtalk"
	ChannelWeCom    ChannelType = "wecom"
	ChannelSlack    ChannelType = "slack"
	ChannelCustom   ChannelType = "custom"
)

// Channel is one configured destination. Secret is only meaningful for
// feishu/dingtalk (their optional signed-webhook schemes); other types ignore
// it. Name is caller-assigned, used only for identifying results in SendAll.
type Channel struct {
	Type       ChannelType
	Name       string
	WebhookURL string
	Secret     string
	Enabled    bool
}

// Level is the alert's severity, surfaced in the formatted message text.
type Level string

const (
	LevelInfo     Level = "info"
	LevelWarning  Level = "warning"
	LevelCritical Level = "critical"
)

// Message is the alert content, platform-agnostic.
type Message struct {
	Title   string
	Content string
	Level   Level
}

// Result is one channel's outcome from SendAll.
type Result struct {
	Channel string
	Type    ChannelType
	Err     error
}

// Send delivers msg to a single channel, dispatching by ch.Type.
func Send(ctx context.Context, ch Channel, msg Message) error {
	switch ch.Type {
	case ChannelFeishu:
		return sendFeishu(ctx, ch, msg)
	case ChannelDingTalk:
		return sendDingTalk(ctx, ch, msg)
	case ChannelWeCom:
		return sendWeCom(ctx, ch, msg)
	case ChannelSlack:
		return sendSlack(ctx, ch, msg)
	case ChannelCustom:
		return sendCustom(ctx, ch, msg)
	default:
		return &UnsupportedChannelTypeError{Type: ch.Type}
	}
}

// UnsupportedChannelTypeError is returned by Send/SendAll for an unrecognized
// ChannelType.
type UnsupportedChannelTypeError struct {
	Type ChannelType
}

func (e *UnsupportedChannelTypeError) Error() string {
	return "alert: unsupported channel type " + string(e.Type)
}

// SendAll delivers msg to every channel concurrently, collecting one Result
// per channel so one failure never hides another channel's outcome.
func SendAll(ctx context.Context, channels []Channel, msg Message) []Result {
	results := make([]Result, len(channels))
	var wg sync.WaitGroup
	for i, ch := range channels {
		wg.Add(1)
		go func(i int, ch Channel) {
			defer wg.Done()
			results[i] = Result{Channel: ch.Name, Type: ch.Type, Err: Send(ctx, ch, msg)}
		}(i, ch)
	}
	wg.Wait()
	return results
}

// formatText renders a Message as a plain-text body, the shape every
// provider except custom uses.
func formatText(msg Message) string {
	level := string(msg.Level)
	if level == "" {
		level = string(LevelInfo)
	}
	return "[" + level + "] " + msg.Title + "\n" + msg.Content
}
