package twitch

import (
	"context"
	"fmt"
	"math/rand/v2"
	"strconv"
	"strings"
	"sync"

	"github.com/gorilla/websocket"
)

// ircWebSocketURL is Twitch's legacy chat IRC endpoint, over WebSocket.
const ircWebSocketURL = "wss://irc-ws.chat.twitch.tv:443"

// WatchStreakEvent reports a viewer's watch streak milestone (Twitch's
// "N consecutive streams watched" celebration) — see
// ConnectWatchStreaks for why this needs its own IRC connection instead
// of arriving over the EventSub session everything else in this package
// uses.
type WatchStreakEvent struct {
	Login       string
	DisplayName string
	// Streak is how many consecutive streams the viewer has watched.
	Streak int
}

// WatchStreakSession is a live, anonymous, read-only Twitch IRC
// connection open for the sole purpose of catching watch-streak
// USERNOTICEs. The zero value is not usable; construct with
// ConnectWatchStreaks.
type WatchStreakSession struct {
	conn      *websocket.Conn
	events    chan WatchStreakEvent
	closed    chan struct{}
	closeOnce sync.Once
}

// ConnectWatchStreaks opens an anonymous, read-only Twitch IRC
// connection and joins channelLogin's chat, watching only for
// viewermilestone/watch-streak USERNOTICEs and discarding everything
// else Twitch sends over it (regular chat messages included — those
// still come from the EventSub session elsewhere in this package).
//
// This is a deliberate, narrow exception to the rest of this package
// being EventSub-only (see the package doc on client.go): as of 2026,
// Twitch's EventSub API has no equivalent event for watch streaks —
// they're only ever delivered as a legacy IRC USERNOTICE
// (msg-id=viewermilestone, msg-param-category=watch-streak). See
// https://discuss.dev.twitch.com/t/watch-streaks-via-eventsub/64429.
// No access token is needed: Twitch IRC still allows fully anonymous
// read access the old way, unlike EventSub's channel.chat.message
// subscriptions which need the signed-in viewer's token.
//
// The returned session does not reconnect on its own — like the
// AutoMod queue subscription elsewhere in this app, a dropped
// connection here should be treated as "no more watch-streak
// callouts for this chat session", not a reason to fail chat itself.
func ConnectWatchStreaks(ctx context.Context, channelLogin string) (*WatchStreakSession, error) {
	return connectWatchStreaksTo(ctx, ircWebSocketURL, channelLogin)
}

// connectWatchStreaksTo is ConnectWatchStreaks against an arbitrary URL,
// split out so tests can point it at a local fake IRC server instead of
// Twitch's own.
func connectWatchStreaksTo(ctx context.Context, url, channelLogin string) (*WatchStreakSession, error) {
	dialCtx, cancel := context.WithTimeout(ctx, connectTimeout)
	defer cancel()

	conn, _, err := websocket.DefaultDialer.DialContext(dialCtx, url, nil)
	if err != nil {
		return nil, fmt.Errorf("twitch: irc: dial: %w", err)
	}

	// Anonymous login: any password works for "justinfan" nicks, Twitch
	// IRC's long-standing read-only anonymous access.
	// https://dev.twitch.tv/docs/irc/authenticate-bot/#getting-an-access-token
	nick := fmt.Sprintf("justinfan%d", rand.IntN(100000))
	for _, line := range []string{
		"CAP REQ :twitch.tv/tags twitch.tv/commands",
		"PASS SCHMOOPIIE",
		"NICK " + nick,
		"JOIN #" + channelLogin,
	} {
		if err := conn.WriteMessage(websocket.TextMessage, []byte(line)); err != nil {
			conn.Close()
			return nil, fmt.Errorf("twitch: irc: sending %q: %w", line, err)
		}
	}

	s := &WatchStreakSession{
		conn:   conn,
		events: make(chan WatchStreakEvent),
		closed: make(chan struct{}),
	}
	go s.readLoop()
	return s, nil
}

// Events returns the channel watch-streak milestones are delivered on.
// It's closed when the session ends, whether from Close, a read error,
// or the connection being dropped from the Twitch end.
func (s *WatchStreakSession) Events() <-chan WatchStreakEvent {
	return s.events
}

// Close ends the session.
func (s *WatchStreakSession) Close() error {
	s.closeOnce.Do(func() { close(s.closed) })
	return s.conn.Close()
}

func (s *WatchStreakSession) readLoop() {
	defer close(s.events)

	for {
		_, data, err := s.conn.ReadMessage()
		if err != nil {
			return
		}
		// Twitch can batch multiple IRC lines into one WebSocket frame,
		// separated by CRLF.
		for _, line := range strings.Split(string(data), "\r\n") {
			if !s.handleLine(line) {
				return
			}
		}
	}
}

// handleLine processes a single raw IRC line, returning false if the
// session should stop reading (Close was called while handling it).
func (s *WatchStreakSession) handleLine(line string) bool {
	if line == "" {
		return true
	}
	if strings.HasPrefix(line, "PING") {
		_ = s.conn.WriteMessage(websocket.TextMessage, []byte("PONG :tmi.twitch.tv"))
		return true
	}

	tags, rest := parseIRCTags(line)
	if tags == nil {
		return true
	}
	// rest looks like ":tmi.twitch.tv USERNOTICE #channel :optional message".
	fields := strings.SplitN(rest, " ", 3)
	if len(fields) < 2 || fields[1] != "USERNOTICE" {
		return true
	}
	if tags["msg-id"] != "viewermilestone" || tags["msg-param-category"] != "watch-streak" {
		return true
	}
	streak, _ := strconv.Atoi(tags["msg-param-value"])
	event := WatchStreakEvent{
		Login:       tags["login"],
		DisplayName: tags["display-name"],
		Streak:      streak,
	}
	select {
	case s.events <- event:
		return true
	case <-s.closed:
		return false
	}
}

// parseIRCTags splits an IRCv3-tagged line ("@k1=v1;k2=v2 :rest...")
// into its decoded tags and everything after the tag block. Returns a
// nil map (and the line unchanged) for a line with no tag block at all
// — PING and a handful of other Twitch IRC lines never carry one.
func parseIRCTags(line string) (map[string]string, string) {
	if !strings.HasPrefix(line, "@") {
		return nil, line
	}
	sp := strings.IndexByte(line, ' ')
	if sp < 0 {
		return nil, ""
	}
	tags := make(map[string]string)
	for _, pair := range strings.Split(line[1:sp], ";") {
		k, v, _ := strings.Cut(pair, "=")
		tags[k] = unescapeIRCTagValue(v)
	}
	return tags, line[sp+1:]
}

// unescapeIRCTagValue undoes IRCv3 tag-value escaping (\s -> space, and
// so on) — see https://ircv3.net/specs/extensions/message-tags#escaping-values.
func unescapeIRCTagValue(v string) string {
	if !strings.ContainsRune(v, '\\') {
		return v
	}
	var b strings.Builder
	b.Grow(len(v))
	for i := 0; i < len(v); i++ {
		c := v[i]
		if c != '\\' || i == len(v)-1 {
			b.WriteByte(c)
			continue
		}
		i++
		switch v[i] {
		case 's':
			b.WriteByte(' ')
		case 'n':
			b.WriteByte('\n')
		case 'r':
			b.WriteByte('\r')
		case ':':
			b.WriteByte(';')
		case '\\':
			b.WriteByte('\\')
		default:
			b.WriteByte(v[i])
		}
	}
	return b.String()
}
