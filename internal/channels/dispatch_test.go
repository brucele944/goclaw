package channels

import (
	"context"
	"errors"
	"testing"

	"github.com/nextlevelbuilder/goclaw/internal/bus"
)

// Reproduces the false-success bug: a cross-target forward (message tool,
// forward=true) whose destination chat ID is invalid must notify the ORIGIN
// chat with the real failure — not retry against the same broken
// destination, and not silently drop a text-only failure.
func TestHandleSendFailure_ForwardNotifiesOrigin(t *testing.T) {
	t.Parallel()

	mgr := NewManager(bus.New())
	origin := newMockChannel("bunny-zalo-personal", TypeZaloPersonal)
	mgr.channels["bunny-zalo-personal"] = origin

	badTarget := bus.OutboundMessage{
		Channel: "bunny-zalo-personal",
		ChatID:  "Ban Điều Hành", // display name passed as chat ID — invalid
		Content: "Anh Tài ơi, xem giúp comment khách hàng nhé.",
		Metadata: map[string]string{
			bus.MetaForwardOriginChannel: "bunny-zalo-personal",
			bus.MetaForwardOriginChatID:  "747300108647389888",
		},
	}

	mgr.handleSendFailure(context.Background(), origin, badTarget, errors.New("inner error code 114: Tham số không hợp lệ"))

	if origin.lastMsg.ChatID != "747300108647389888" {
		t.Fatalf("notice went to %q, want the origin chat, not the broken destination %q", origin.lastMsg.ChatID, badTarget.ChatID)
	}
	if origin.lastMsg.Content == "" {
		t.Fatal("expected a non-empty failure notice content")
	}
}

// Non-forward media failures keep the pre-existing behavior: retry-notify
// the SAME chat (no forward metadata present, so there's no separate origin).
func TestHandleSendFailure_NonForwardMediaNotifiesSameChat(t *testing.T) {
	t.Parallel()

	mgr := NewManager(bus.New())
	ch := newMockChannel("telegram-main", TypeTelegram)
	mgr.channels["telegram-main"] = ch

	msg := bus.OutboundMessage{
		Channel: "telegram-main",
		ChatID:  "chat-1",
		Media:   []bus.MediaAttachment{{URL: "/tmp/x.png"}},
	}

	mgr.handleSendFailure(context.Background(), ch, msg, errors.New("file is too big"))

	if ch.lastMsg.ChatID != "chat-1" {
		t.Fatalf("notice ChatID = %q, want chat-1 (same chat)", ch.lastMsg.ChatID)
	}
}

// Non-forward TEXT-ONLY failures are still dropped (pre-existing behavior,
// unrelated to the forward bug this file otherwise fixes) — no channel
// exists to receive a notice, so Send must not be called again.
func TestHandleSendFailure_NonForwardTextOnlyDropped(t *testing.T) {
	t.Parallel()

	mgr := NewManager(bus.New())
	ch := newMockChannel("telegram-main", TypeTelegram)
	mgr.channels["telegram-main"] = ch

	msg := bus.OutboundMessage{
		Channel: "telegram-main",
		ChatID:  "chat-1",
		Content: "hello",
	}

	mgr.handleSendFailure(context.Background(), ch, msg, errors.New("chat not found"))

	if ch.lastMsg.Content != "" {
		t.Fatalf("expected no notice sent for non-forward text-only failure, got: %+v", ch.lastMsg)
	}
}

// Issue #1475: the agent loop signals NO_REPLY / silent replies by publishing
// an outbound message with empty content plus the inbound routing metadata
// (placeholder_key / local_key). Slack, Telegram and Discord implement an
// empty-content branch in Send() that deletes the streamed "Thinking..."
// placeholder — the stray partial draft left behind when delivery is
// suppressed. deliverOutbound must not drop these cleanup signals before
// they reach the channel.
func TestDeliverOutbound_EmptyContentWithPlaceholderMetaReachesSend(t *testing.T) {
	t.Parallel()

	mgr := NewManager(bus.New())
	ch := newMockChannel("slack-main", TypeSlack)
	mgr.channels["slack-main"] = ch

	msg := bus.OutboundMessage{
		Channel: "slack-main",
		ChatID:  "C012345",
		Content: "",
		Metadata: map[string]string{
			"placeholder_key": "C012345:thread:1727500000.000100",
			"local_key":       "C012345:thread:1727500000.000100",
		},
	}

	mgr.deliverOutbound(context.Background(), msg)

	if ch.lastMsg.Content != "" || ch.lastMsg.ChatID != "C012345" {
		t.Fatal("empty-content cleanup signal with placeholder metadata was dropped before reaching channel.Send — stray partial draft is never deleted (issue #1475)")
	}
}

// The media-gone skip must keep working for empty messages that carry no
// placeholder routing metadata: those are NOT cleanup signals, and delivering
// them would make channels without an empty-content branch render empty bubbles.
func TestDeliverOutbound_EmptyContentWithoutMetaStillSkipped(t *testing.T) {
	t.Parallel()

	mgr := NewManager(bus.New())
	ch := newMockChannel("feishu-main", TypeFeishu)
	mgr.channels["feishu-main"] = ch

	mgr.deliverOutbound(context.Background(), bus.OutboundMessage{
		Channel: "feishu-main",
		ChatID:  "oc_1",
		Content: "",
	})

	if ch.lastMsg.ChatID != "" {
		t.Fatalf("empty content without routing metadata should be skipped, got: %+v", ch.lastMsg)
	}
}
