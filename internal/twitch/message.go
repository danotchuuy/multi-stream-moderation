package twitch

import (
	"errors"
	"strconv"
	"strings"
	"time"
)

var errMalformed = errors.New("twitch: malformed IRC line")

// Message is a chat message received on a channel (PRIVMSG).
type Message struct {
	// ID is the unique message ID (tags "id"), used to target a specific
	// message for deletion.
	ID          string
	Channel     string
	Text        string
	Username    string
	DisplayName string
	UserID      string
	Mod         bool
	Subscriber  bool
	Broadcaster bool
	Color       string
	Tags        map[string]string
	Timestamp   time.Time
	Raw         string
}

// Event is any IRC line received from Twitch (PRIVMSG, JOIN, PART,
// NOTICE, CLEARCHAT, CLEARMSG, USERNOTICE, ROOMSTATE, ...). Type is the
// IRC command name; Channel is set when the first param names one.
type Event struct {
	Type    string
	Channel string
	Params  []string
	Tags    map[string]string
	Raw     string
}

// parsedLine is a raw, tag-decorated IRC line as sent by Twitch.
type parsedLine struct {
	Tags    map[string]string
	Prefix  string
	Command string
	Params  []string
}

// parseLine parses a single IRC line (without the trailing CRLF) per the
// IRCv3 message-tags spec: https://ircv3.net/specs/extensions/message-tags
func parseLine(line string) (parsedLine, error) {
	var p parsedLine

	if strings.HasPrefix(line, "@") {
		sp := strings.IndexByte(line, ' ')
		if sp < 0 {
			return p, errMalformed
		}
		p.Tags = parseTags(line[1:sp])
		line = line[sp+1:]
	}

	if strings.HasPrefix(line, ":") {
		sp := strings.IndexByte(line, ' ')
		if sp < 0 {
			return p, errMalformed
		}
		p.Prefix = line[1:sp]
		line = line[sp+1:]
	}

	// A param starting with ':' runs to the end of the line (may contain
	// spaces); everything before it is space-separated.
	if idx := strings.Index(line, " :"); idx >= 0 {
		fields := strings.Fields(line[:idx])
		if len(fields) == 0 {
			return p, errMalformed
		}
		p.Command = fields[0]
		p.Params = append(fields[1:], line[idx+2:])
	} else {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			return p, errMalformed
		}
		p.Command = fields[0]
		p.Params = fields[1:]
	}

	return p, nil
}

func parseTags(raw string) map[string]string {
	tags := make(map[string]string)
	for _, pair := range strings.Split(raw, ";") {
		if pair == "" {
			continue
		}
		k, v, _ := strings.Cut(pair, "=")
		tags[k] = unescapeTagValue(v)
	}
	return tags
}

// unescapeTagValue undoes IRCv3 tag value escaping (\s -> space, \: ->
// ';', \\ -> \, \r, \n).
func unescapeTagValue(v string) string {
	if !strings.ContainsRune(v, '\\') {
		return v
	}
	var b strings.Builder
	for i := 0; i < len(v); i++ {
		if v[i] == '\\' && i+1 < len(v) {
			i++
			switch v[i] {
			case ':':
				b.WriteByte(';')
			case 's':
				b.WriteByte(' ')
			case '\\':
				b.WriteByte('\\')
			case 'r':
				b.WriteByte('\r')
			case 'n':
				b.WriteByte('\n')
			default:
				b.WriteByte(v[i])
			}
			continue
		}
		b.WriteByte(v[i])
	}
	return b.String()
}

func newMessage(p parsedLine, raw string) Message {
	var channel, text string
	if len(p.Params) > 0 {
		channel = p.Params[0]
	}
	if len(p.Params) > 1 {
		text = p.Params[1]
	}

	username := p.Prefix
	if i := strings.IndexByte(username, '!'); i >= 0 {
		username = username[:i]
	}

	tags := p.Tags
	ts := time.Now()
	if v, ok := tags["tmi-sent-ts"]; ok {
		if ms, err := strconv.ParseInt(v, 10, 64); err == nil {
			ts = time.UnixMilli(ms)
		}
	}

	return Message{
		ID:          tags["id"],
		Channel:     channel,
		Text:        text,
		Username:    username,
		DisplayName: firstNonEmpty(tags["display-name"], username),
		UserID:      tags["user-id"],
		Mod:         tags["mod"] == "1",
		Subscriber:  tags["subscriber"] == "1",
		Broadcaster: strings.Contains(tags["badges"], "broadcaster/"),
		Color:       tags["color"],
		Tags:        tags,
		Timestamp:   ts,
		Raw:         raw,
	}
}

func newEvent(p parsedLine, raw string) Event {
	var channel string
	if len(p.Params) > 0 && strings.HasPrefix(p.Params[0], "#") {
		channel = p.Params[0]
	}
	return Event{
		Type:    p.Command,
		Channel: channel,
		Params:  p.Params,
		Tags:    p.Tags,
		Raw:     raw,
	}
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
