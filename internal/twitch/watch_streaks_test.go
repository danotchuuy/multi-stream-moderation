package twitch

import (
	"context"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func TestParseIRCTags(t *testing.T) {
	line := `@badge-info=;badges=;color=#1E90FF;display-name=TwitchDev;msg-id=viewermilestone;msg-param-category=watch-streak;msg-param-value=3;room-id=197886470 :tmi.twitch.tv USERNOTICE #twitchrivals :TwitchDev watched 3 consecutive streams`

	tags, rest := parseIRCTags(line)
	if tags["msg-id"] != "viewermilestone" || tags["msg-param-category"] != "watch-streak" || tags["msg-param-value"] != "3" {
		t.Fatalf("tags = %+v", tags)
	}
	if tags["display-name"] != "TwitchDev" {
		t.Errorf("display-name = %q, want TwitchDev", tags["display-name"])
	}
	wantRest := `:tmi.twitch.tv USERNOTICE #twitchrivals :TwitchDev watched 3 consecutive streams`
	if rest != wantRest {
		t.Errorf("rest = %q, want %q", rest, wantRest)
	}
}

func TestParseIRCTagsNoTagBlock(t *testing.T) {
	tags, rest := parseIRCTags("PING :tmi.twitch.tv")
	if tags != nil {
		t.Errorf("tags = %+v, want nil for a line with no tag block", tags)
	}
	if rest != "PING :tmi.twitch.tv" {
		t.Errorf("rest = %q, want line unchanged", rest)
	}
}

func TestUnescapeIRCTagValue(t *testing.T) {
	got := unescapeIRCTagValue(`TwitchDev\swatched\s3\sconsecutive\sstreams\sthis\smonth\sand\ssparked\sa\swatch\sstreak!`)
	want := "TwitchDev watched 3 consecutive streams this month and sparked a watch streak!"
	if got != want {
		t.Errorf("unescapeIRCTagValue = %q, want %q", got, want)
	}
}

func TestConnectWatchStreaksReceivesEvent(t *testing.T) {
	server := wsTestServer(t, func(conn *websocket.Conn) {
		defer conn.Close()
		// Twitch's own documented example USERNOTICE for a watch-streak
		// milestone. https://dev.twitch.tv/docs/irc/tags/
		line := `@badge-info=;badges=;color=#1E90FF;display-name=TwitchDev;emotes=;flags=;id=97459ff9-8cd9-47d0-8a81-1fa5fe1d4367;login=twitchdev;mod=0;msg-id=viewermilestone;msg-param-category=watch-streak;msg-param-id=a872953b-606f-410a-9970-c95b744f116c;msg-param-value=3;room-id=197886470;subscriber=0;system-msg=TwitchDev\swatched\s3\sconsecutive\sstreams\sthis\smonth\sand\ssparked\sa\swatch\sstreak!;tmi-sent-ts=1681057151588;user-id=141981764;user-type= :tmi.twitch.tv USERNOTICE #twitchrivals`
		conn.WriteMessage(websocket.TextMessage, []byte(line))
		// Keep reading (rather than returning after the first message)
		// until the client closes — returning early would close this
		// end too, racing the client's own in-flight handshake writes
		// (CAP/PASS/NICK/JOIN) and occasionally resetting the
		// connection mid-write.
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	})

	sess, err := connectWatchStreaksTo(context.Background(), wsURL(server.URL), "twitchrivals")
	if err != nil {
		t.Fatalf("connectWatchStreaksTo returned error: %v", err)
	}
	defer sess.Close()

	select {
	case ev := <-sess.Events():
		if ev.Login != "twitchdev" || ev.DisplayName != "TwitchDev" || ev.Streak != 3 {
			t.Errorf("event = %+v, want Login twitchdev, DisplayName TwitchDev, Streak 3", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for watch-streak event")
	}
}

func TestConnectWatchStreaksIgnoresOtherNotices(t *testing.T) {
	server := wsTestServer(t, func(conn *websocket.Conn) {
		defer conn.Close()
		// An ordinary resub notice, then the watch-streak one — only the
		// latter should ever reach Events().
		resub := `@msg-id=resub;login=someuser;display-name=SomeUser :tmi.twitch.tv USERNOTICE #achannel :SomeUser subscribed for 6 months`
		streak := `@msg-id=viewermilestone;msg-param-category=watch-streak;msg-param-value=5;login=streaker;display-name=Streaker :tmi.twitch.tv USERNOTICE #achannel`
		conn.WriteMessage(websocket.TextMessage, []byte(resub))
		conn.WriteMessage(websocket.TextMessage, []byte(streak))
		// See the matching comment in TestConnectWatchStreaksReceivesEvent:
		// keep reading until the client closes, rather than racing its
		// in-flight handshake writes by returning (and closing) after
		// just the first message.
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	})

	sess, err := connectWatchStreaksTo(context.Background(), wsURL(server.URL), "achannel")
	if err != nil {
		t.Fatalf("connectWatchStreaksTo returned error: %v", err)
	}
	defer sess.Close()

	select {
	case ev := <-sess.Events():
		if ev.Login != "streaker" || ev.Streak != 5 {
			t.Errorf("event = %+v, want the watch-streak notice, not the resub", ev)
		}
		if ev.Message != "" {
			t.Errorf("Message = %q, want empty for a streak shared without a message", ev.Message)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for watch-streak event")
	}
}

func TestConnectWatchStreaksIncludesSharedMessage(t *testing.T) {
	server := wsTestServer(t, func(conn *websocket.Conn) {
		defer conn.Close()
		streak := `@msg-id=viewermilestone;msg-param-category=watch-streak;msg-param-value=7;login=streaker;display-name=Streaker :tmi.twitch.tv USERNOTICE #achannel :love these streams!`
		conn.WriteMessage(websocket.TextMessage, []byte(streak))
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	})

	sess, err := connectWatchStreaksTo(context.Background(), wsURL(server.URL), "achannel")
	if err != nil {
		t.Fatalf("connectWatchStreaksTo returned error: %v", err)
	}
	defer sess.Close()

	select {
	case ev := <-sess.Events():
		if ev.Streak != 7 || ev.Message != "love these streams!" {
			t.Errorf("event = %+v, want Streak 7, Message %q", ev, "love these streams!")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for watch-streak event")
	}
}

func TestConnectWatchStreaksRespondsToPing(t *testing.T) {
	pongReceived := make(chan struct{})
	server := wsTestServer(t, func(conn *websocket.Conn) {
		defer conn.Close()
		conn.WriteMessage(websocket.TextMessage, []byte("PING :tmi.twitch.tv"))
		// Drain the client's CAP/PASS/NICK/JOIN handshake lines first —
		// the PONG reply arrives after those, not instead of them.
		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if string(data) == "PONG :tmi.twitch.tv" {
				close(pongReceived)
				return
			}
		}
	})

	sess, err := connectWatchStreaksTo(context.Background(), wsURL(server.URL), "achannel")
	if err != nil {
		t.Fatalf("connectWatchStreaksTo returned error: %v", err)
	}
	defer sess.Close()

	select {
	case <-pongReceived:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for PONG reply")
	}
}
