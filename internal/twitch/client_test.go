package twitch

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestHasBadge(t *testing.T) {
	badges := []Badge{{SetID: "moderator", ID: "1"}, {SetID: "subscriber", ID: "12"}}
	if !HasBadge(badges, "moderator") {
		t.Error("expected HasBadge(moderator) to be true")
	}
	if HasBadge(badges, "broadcaster") {
		t.Error("expected HasBadge(broadcaster) to be false")
	}
	if HasBadge(nil, "moderator") {
		t.Error("expected HasBadge on nil slice to be false")
	}
}

func TestChatMessageUnmarshal(t *testing.T) {
	raw := `{
		"message_id": "abc-123",
		"chatter_user_id": "456",
		"chatter_user_login": "someuser",
		"chatter_user_name": "SomeUser",
		"message": {"text": "Hello world"},
		"message_type": "text",
		"badges": [{"set_id": "broadcaster", "id": "1", "info": ""}],
		"color": "#FF0000",
		"reply": {
			"parent_message_id": "parent-1",
			"parent_user_id": "789",
			"parent_user_login": "other",
			"parent_user_name": "Other",
			"parent_message_body": "hi"
		}
	}`

	var m ChatMessage
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("Unmarshal returned error: %v", err)
	}
	if m.MessageID != "abc-123" || m.ChatterUserID != "456" || m.Message.Text != "Hello world" {
		t.Errorf("ChatMessage = %+v", m)
	}
	if !HasBadge(m.Badges, "broadcaster") {
		t.Error("expected broadcaster badge")
	}
	if m.Reply == nil || m.Reply.ParentMessageID != "parent-1" || m.Reply.ParentUserLogin != "other" {
		t.Errorf("Reply = %+v", m.Reply)
	}
}

func TestChatNotificationRaidUnmarshal(t *testing.T) {
	raw := `{
		"notice_type": "raid",
		"raid": {
			"user_id": "111",
			"user_login": "raider",
			"user_name": "Raider",
			"viewer_count": 42
		}
	}`

	var n ChatNotification
	if err := json.Unmarshal([]byte(raw), &n); err != nil {
		t.Fatalf("Unmarshal returned error: %v", err)
	}
	if n.NoticeType != "raid" {
		t.Errorf("NoticeType = %q, want raid", n.NoticeType)
	}
	if n.Raid == nil || n.Raid.ViewerCount != 42 || n.Raid.UserLogin != "raider" {
		t.Errorf("Raid = %+v", n.Raid)
	}
}

// wsTestServer upgrades every request to a WebSocket and hands the
// connection to onConn in its own goroutine.
func wsTestServer(t *testing.T, onConn func(*websocket.Conn)) *httptest.Server {
	t.Helper()
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		go onConn(conn)
	}))
	t.Cleanup(server.Close)
	return server
}

func wsURL(httpURL string) string {
	return "ws" + strings.TrimPrefix(httpURL, "http")
}

func sendJSON(t *testing.T, conn *websocket.Conn, v any) {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := conn.WriteMessage(websocket.TextMessage, data); err != nil {
		t.Fatalf("write: %v", err)
	}
}

func TestConnectAndReceiveNotification(t *testing.T) {
	server := wsTestServer(t, func(conn *websocket.Conn) {
		defer conn.Close()
		sendJSON(t, conn, map[string]any{
			"metadata": map[string]any{"message_type": "session_welcome"},
			"payload":  map[string]any{"session": map[string]any{"id": "sess-1"}},
		})
		sendJSON(t, conn, map[string]any{
			"metadata": map[string]any{
				"message_type":      "notification",
				"subscription_type": "channel.chat.message",
			},
			"payload": map[string]any{"event": map[string]any{"message_id": "m1"}},
		})
		// Keep the connection open until the test closes it.
		conn.ReadMessage()
	})

	sess, err := connectTo(context.Background(), wsURL(server.URL))
	if err != nil {
		t.Fatalf("connectTo returned error: %v", err)
	}
	defer sess.Close()

	if sess.ID() != "sess-1" {
		t.Errorf("ID() = %q, want sess-1", sess.ID())
	}

	select {
	case n := <-sess.Notifications():
		if n.Type != "channel.chat.message" {
			t.Errorf("notification Type = %q", n.Type)
		}
		var ev struct {
			MessageID string `json:"message_id"`
		}
		if err := json.Unmarshal(n.Event, &ev); err != nil || ev.MessageID != "m1" {
			t.Errorf("notification Event = %s (err %v)", n.Event, err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for notification")
	}
}

func TestSessionReconnect(t *testing.T) {
	var second *httptest.Server
	first := wsTestServer(t, func(conn *websocket.Conn) {
		sendJSON(t, conn, map[string]any{
			"metadata": map[string]any{"message_type": "session_welcome"},
			"payload":  map[string]any{"session": map[string]any{"id": "sess-1"}},
		})
		sendJSON(t, conn, map[string]any{
			"metadata": map[string]any{"message_type": "session_reconnect"},
			"payload": map[string]any{
				"session": map[string]any{"id": "sess-1", "reconnect_url": wsURL(second.URL)},
			},
		})
		conn.ReadMessage()
		conn.Close()
	})
	second = wsTestServer(t, func(conn *websocket.Conn) {
		defer conn.Close()
		sendJSON(t, conn, map[string]any{
			"metadata": map[string]any{"message_type": "session_welcome"},
			"payload":  map[string]any{"session": map[string]any{"id": "sess-2"}},
		})
		sendJSON(t, conn, map[string]any{
			"metadata": map[string]any{
				"message_type":      "notification",
				"subscription_type": "channel.chat.clear",
			},
			"payload": map[string]any{"event": map[string]any{}},
		})
		conn.ReadMessage()
	})

	sess, err := connectTo(context.Background(), wsURL(first.URL))
	if err != nil {
		t.Fatalf("connectTo returned error: %v", err)
	}
	defer sess.Close()

	select {
	case n := <-sess.Notifications():
		if n.Type != "channel.chat.clear" {
			t.Errorf("notification Type = %q, want channel.chat.clear (post-reconnect)", n.Type)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for post-reconnect notification")
	}
}
