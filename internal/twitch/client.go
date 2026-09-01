// Package twitch implements a minimal client for Twitch chat (IRC) so a
// single connection can follow and moderate several channels at once.
package twitch

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"math/rand"
	"net"
	"strings"
	"sync"
	"time"
)

const (
	defaultAddr        = "irc.chat.twitch.tv:6697"
	defaultRateLimit   = 20
	defaultRateWindow  = 30 * time.Second
	defaultDialTimeout = 10 * time.Second
)

// Config configures a Client.
type Config struct {
	// Nick is the Twitch username to connect as, lowercase. If empty, the
	// client connects anonymously (read-only) using a random justinfan
	// nick, and Token is ignored.
	Nick string
	// Token is an OAuth token authorizing Nick, e.g. "oauth:xxxxxxxx" (the
	// "oauth:" prefix is added automatically if missing). Required
	// whenever Nick is set; not used for anonymous connections.
	Token string
	// Addr is the IRC server address to dial. Defaults to
	// "irc.chat.twitch.tv:6697" (TLS).
	Addr string
	// MessageRateLimit and MessageRateWindow cap outgoing chat messages,
	// e.g. the default of 20 messages per 30s matches Twitch's limit for
	// non-moderator accounts. Both default when either is zero.
	MessageRateLimit  int
	MessageRateWindow time.Duration
}

// Client is a connection to Twitch chat capable of following multiple
// channels at once. The zero value is not usable; construct with New.
type Client struct {
	cfg     Config
	limiter *rateLimiter

	mu       sync.Mutex
	conn     net.Conn
	channels map[string]struct{}

	handlersMu sync.RWMutex
	onMessage  func(Message)
	onEvent    func(Event)
}

// New creates a Client. Call Run to connect and start processing.
func New(cfg Config) *Client {
	if cfg.Addr == "" {
		cfg.Addr = defaultAddr
	}
	if cfg.MessageRateLimit == 0 {
		cfg.MessageRateLimit = defaultRateLimit
	}
	if cfg.MessageRateWindow == 0 {
		cfg.MessageRateWindow = defaultRateWindow
	}
	return &Client{
		cfg:      cfg,
		limiter:  newRateLimiter(cfg.MessageRateLimit, cfg.MessageRateWindow),
		channels: make(map[string]struct{}),
	}
}

// OnMessage registers the handler invoked for every chat message
// (PRIVMSG). It replaces any previously registered handler and must be
// called before Run to avoid missing early messages.
func (c *Client) OnMessage(fn func(Message)) {
	c.handlersMu.Lock()
	defer c.handlersMu.Unlock()
	c.onMessage = fn
}

// OnEvent registers the handler invoked for every IRC line, including
// PRIVMSG. Moderation-relevant events such as timeouts/bans (CLEARCHAT)
// and single-message deletion (CLEARMSG) surface here with their raw
// tags; PING/PONG keepalive is handled internally and never reaches it.
func (c *Client) OnEvent(fn func(Event)) {
	c.handlersMu.Lock()
	defer c.handlersMu.Unlock()
	c.onEvent = fn
}

// Join starts following the given channels (with or without a leading
// "#"). Safe to call before or after Run; followed channels are
// (re)joined automatically on every connect.
func (c *Client) Join(channels ...string) error {
	c.mu.Lock()
	conn := c.conn
	for _, ch := range channels {
		c.channels[normalizeChannel(ch)] = struct{}{}
	}
	c.mu.Unlock()

	if conn == nil {
		return nil
	}
	return c.sendCommand("JOIN", channels...)
}

// Part stops following the given channels.
func (c *Client) Part(channels ...string) error {
	c.mu.Lock()
	conn := c.conn
	for _, ch := range channels {
		delete(c.channels, normalizeChannel(ch))
	}
	c.mu.Unlock()

	if conn == nil {
		return nil
	}
	return c.sendCommand("PART", channels...)
}

// Channels returns the channels currently being followed.
func (c *Client) Channels() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]string, 0, len(c.channels))
	for ch := range c.channels {
		out = append(out, ch)
	}
	return out
}

// Say sends a chat message to channel, waiting as needed to respect the
// configured rate limit. It requires Config.Nick/Token to have been set.
func (c *Client) Say(ctx context.Context, channel, text string) error {
	if err := c.limiter.Wait(ctx); err != nil {
		return err
	}
	return c.send(fmt.Sprintf("PRIVMSG %s :%s", normalizeChannel(channel), text))
}

// Run connects to Twitch chat and processes messages until ctx is
// cancelled or an unrecoverable error occurs. On connection loss it
// reconnects with exponential backoff and rejoins every channel passed
// to Join. Run blocks; call it in its own goroutine.
func (c *Client) Run(ctx context.Context) error {
	backoff := time.Second
	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		_ = c.connectAndServe(ctx)
		if err := ctx.Err(); err != nil {
			return err
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		backoff *= 2
		if backoff > 30*time.Second {
			backoff = 30 * time.Second
		}
	}
}

// Close closes the underlying connection, if any. A Run loop observing
// the closed connection will attempt to reconnect unless its context is
// also done.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.conn == nil {
		return nil
	}
	err := c.conn.Close()
	c.conn = nil
	return err
}

func (c *Client) connectAndServe(ctx context.Context) error {
	dialer := &tls.Dialer{NetDialer: &net.Dialer{Timeout: defaultDialTimeout}}
	conn, err := dialer.DialContext(ctx, "tcp", c.cfg.Addr)
	if err != nil {
		return fmt.Errorf("twitch: dial: %w", err)
	}
	defer conn.Close()

	c.mu.Lock()
	c.conn = conn
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		if c.conn == conn {
			c.conn = nil
		}
		c.mu.Unlock()
	}()

	stop := make(chan struct{})
	defer close(stop)
	go func() {
		select {
		case <-ctx.Done():
			conn.Close()
		case <-stop:
		}
	}()

	if err := c.authenticate(); err != nil {
		return err
	}

	c.mu.Lock()
	channels := make([]string, 0, len(c.channels))
	for ch := range c.channels {
		channels = append(channels, ch)
	}
	c.mu.Unlock()
	if len(channels) > 0 {
		if err := c.sendCommand("JOIN", channels...); err != nil {
			return err
		}
	}

	return c.readLoop(conn)
}

func (c *Client) authenticate() error {
	nick := c.cfg.Nick
	pass := c.cfg.Token
	if nick == "" {
		nick = anonymousNick()
		pass = "SCHMOOPIIE"
	} else if pass != "" && !strings.HasPrefix(pass, "oauth:") {
		pass = "oauth:" + pass
	}

	return c.send(
		"CAP REQ :twitch.tv/tags twitch.tv/commands twitch.tv/membership",
		"PASS "+pass,
		"NICK "+strings.ToLower(nick),
	)
}

// sendCommand sends cmd (JOIN, PART, ...) for each channel as a separate
// IRC line.
func (c *Client) sendCommand(cmd string, channels ...string) error {
	lines := make([]string, 0, len(channels))
	for _, ch := range channels {
		lines = append(lines, cmd+" "+normalizeChannel(ch))
	}
	return c.send(lines...)
}

// send writes one or more raw IRC lines to the connection.
func (c *Client) send(lines ...string) error {
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()
	if conn == nil {
		return fmt.Errorf("twitch: not connected")
	}
	for _, line := range lines {
		if _, err := fmt.Fprintf(conn, "%s\r\n", line); err != nil {
			return fmt.Errorf("twitch: write: %w", err)
		}
	}
	return nil
}

func (c *Client) readLoop(conn net.Conn) error {
	r := bufio.NewReader(conn)
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			continue
		}
		c.handleLine(line)
	}
}

func (c *Client) handleLine(line string) {
	raw, err := parseLine(line)
	if err != nil {
		return
	}

	if raw.Command == "PING" {
		_ = c.send("PONG :tmi.twitch.tv")
		return
	}

	if raw.Command == "PRIVMSG" {
		c.handlersMu.RLock()
		fn := c.onMessage
		c.handlersMu.RUnlock()
		if fn != nil {
			fn(newMessage(raw, line))
		}
	}

	c.handlersMu.RLock()
	fn := c.onEvent
	c.handlersMu.RUnlock()
	if fn != nil {
		fn(newEvent(raw, line))
	}
}

func normalizeChannel(channel string) string {
	channel = strings.ToLower(strings.TrimSpace(channel))
	if !strings.HasPrefix(channel, "#") {
		channel = "#" + channel
	}
	return channel
}

func anonymousNick() string {
	return fmt.Sprintf("justinfan%d", rand.Intn(80000)+1000)
}
