package server

import "sync"

// chatHistoryLimit caps how many messages chatHistoryCache keeps per
// channel. It only needs to be enough to give a newly connected viewer
// some context, not a full backlog.
const chatHistoryLimit = 50

// chatHistoryCache remembers the last few chat messages this server has
// relayed for each channel, keyed by K (a broadcaster ID), so a viewer
// connecting to a channel someone else — or an earlier tab — has
// already been watching sees some context immediately instead of a
// blank chat log. Neither Twitch's EventSub nor Kick's webhooks offer a
// way to fetch chat history directly; both are push-only for whatever
// happens after you subscribe. So this is the only source of "previous
// messages" available: whatever this server process has already seen
// since it started.
type chatHistoryCache[K comparable, M any] struct {
	max  int
	idOf func(M) string

	mu      sync.Mutex
	entries map[K][]M
}

// newChatHistoryCache builds a cache that keeps at most max messages
// per key, using idOf to recognize (and skip) a message it has already
// recorded — needed because Twitch delivers a chat message once per
// EventSub subscription, so two viewers watching the same channel each
// report the same message to the shared cache.
func newChatHistoryCache[K comparable, M any](max int, idOf func(M) string) *chatHistoryCache[K, M] {
	return &chatHistoryCache[K, M]{max: max, idOf: idOf, entries: make(map[K][]M)}
}

func (c *chatHistoryCache[K, M]) add(key K, msg M) {
	c.mu.Lock()
	defer c.mu.Unlock()

	id := c.idOf(msg)
	for _, existing := range c.entries[key] {
		if c.idOf(existing) == id {
			return
		}
	}

	list := append(c.entries[key], msg)
	if len(list) > c.max {
		list = list[len(list)-c.max:]
	}
	c.entries[key] = list
}

// update applies fn to each of key's buffered messages in place — e.g.
// to mark a banned user's messages deleted, so a viewer connecting
// afterwards doesn't see them as live.
func (c *chatHistoryCache[K, M]) update(key K, fn func(*M)) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range c.entries[key] {
		fn(&c.entries[key][i])
	}
}

// recent returns a copy of key's buffered history, oldest first.
func (c *chatHistoryCache[K, M]) recent(key K) []M {
	c.mu.Lock()
	defer c.mu.Unlock()

	list := c.entries[key]
	out := make([]M, len(list))
	copy(out, list)
	return out
}
