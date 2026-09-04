package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/danotchuuy/multi-stream-moderation/internal/twitch"
)

type chatMessageDTO struct {
	ID          string         `json:"id"`
	Channel     string         `json:"channel"`
	Text        string         `json:"text"`
	Username    string         `json:"username"`
	DisplayName string         `json:"displayName"`
	UserID      string         `json:"userId"`
	Color       string         `json:"color"`
	Mod         bool           `json:"mod"`
	Subscriber  bool           `json:"subscriber"`
	Broadcaster bool           `json:"broadcaster"`
	Badges      []chatBadgeDTO `json:"badges"`
	// FirstMessage is approximated from EventSub's message_type ==
	// "user_intro" — Twitch's "chat intro" feature — since EventSub
	// carries no direct equivalent of IRC's old first-ever-message flag.
	// It's a related but distinct signal: an intro is an opt-in message a
	// new/returning chatter chooses to write, not literally "the first
	// message this user has ever sent here".
	FirstMessage bool `json:"firstMessage,omitempty"`
	// Reply is the parent message this one is replying to, if any.
	Reply *chatReplyDTO `json:"reply,omitempty"`
	// Fragments breaks Text down for rendering, substituting an inline
	// emote image for each fragment that has one. Omitted (frontend
	// falls back to plain Text) for a message with no emotes.
	Fragments []chatFragmentDTO `json:"fragments,omitempty"`
	Timestamp int64             `json:"timestamp"`
}

// chatFragmentDTO is one piece of a chat message's text, shared by both
// Twitch and Kick messages: either plain text (ImageURL empty) or an
// inline emote (ImageURL set, Text the emote's name, used as alt/title
// text). Sending pre-resolved image URLs means the frontend needs no
// emote catalog of its own — it just renders each fragment as-is.
type chatFragmentDTO struct {
	Text     string `json:"text"`
	ImageURL string `json:"imageUrl,omitempty"`
}

// chatEmoteDTO is one emote available for a viewer to use in a given
// channel, shared by both Twitch and Kick's emote-picker endpoints (see
// twitch_emotes.go and kick_emotes.go).
type chatEmoteDTO struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	ImageURL string `json:"imageUrl"`
	// Type groups emotes for display — Twitch's emote_type
	// ("subscriptions", "follower", "globals", ...) or, for Kick,
	// "channel", "global", or "emoji".
	Type string `json:"type,omitempty"`
	// Locked marks an emote the signed-in account most likely can't
	// actually use. Kick's channel-emotes listing isn't scoped to the
	// viewer the way Twitch's is, so this flags its subscribers_only
	// emotes instead — always false for Twitch, which only ever returns
	// emotes the account can use in the first place.
	Locked bool `json:"locked,omitempty"`
}

// chatReplyDTO is the shared "replying to" shape sent to the browser for
// both Twitch and Kick messages.
type chatReplyDTO struct {
	Username    string `json:"username"`
	DisplayName string `json:"displayName,omitempty"`
	Text        string `json:"text"`
}

// chatBadgeDTO is the shared badge shape sent to the browser for both
// Twitch and Kick messages, so the frontend can use one badge-rendering
// component for both chat panels.
type chatBadgeDTO struct {
	Name string `json:"name"`
	// Version distinguishes tiers within a badge — for Twitch, a
	// subscriber badge's Version is their sub month count; for Kick, a
	// badge's optional count (also usually a subscriber tenure).
	Version string `json:"version,omitempty"`
	// ImageURL is the exact badge image Twitch itself shows for this
	// badge/version (see twitchBadgeCatalog in twitch_badges.go).
	// Always empty for Kick, which has no equivalent lookup in this
	// app — the frontend falls back to an approximate icon whenever
	// this is unset.
	ImageURL string `json:"imageUrl,omitempty"`
}

func twitchBadgeDTOs(badges []twitch.Badge, catalog twitchBadgeCatalog) []chatBadgeDTO {
	out := make([]chatBadgeDTO, len(badges))
	for i, b := range badges {
		out[i] = chatBadgeDTO{Name: b.SetID, Version: b.ID, ImageURL: catalog.imageURL(b.SetID, b.ID)}
	}
	return out
}

// twitchEmoteImageURL builds an emote's CDN image URL from its ID.
// "default" auto-selects between static and animated per whichever
// format the emote actually supports, so there's no need to inspect
// the fragment's own Format list first.
// https://dev.twitch.tv/docs/irc/emotes/
func twitchEmoteImageURL(emoteID string) string {
	return "https://static-cdn.jtvnw.net/emoticons/v2/" + emoteID + "/default/dark/2.0"
}

// twitchFragmentDTOs converts EventSub's message fragments, resolving
// each emote fragment's ID to a CDN image URL. Returns nil for a
// message with no fragments at all (Twitch always sends at least one
// for a real message, but this keeps the zero value harmless).
func twitchFragmentDTOs(fragments []twitch.Fragment) []chatFragmentDTO {
	if len(fragments) == 0 {
		return nil
	}
	out := make([]chatFragmentDTO, len(fragments))
	for i, f := range fragments {
		dto := chatFragmentDTO{Text: f.Text}
		if f.Type == "emote" && f.Emote != nil {
			dto.ImageURL = twitchEmoteImageURL(f.Emote.ID)
		}
		out[i] = dto
	}
	return out
}

// clearMsgDTO mirrors a channel.chat.message_delete EventSub
// notification: a single chat message was deleted.
type clearMsgDTO struct {
	Channel   string `json:"channel"`
	MessageID string `json:"messageId"`
}

// clearChatDTO mirrors a channel.chat.clear_user_messages notification
// (a user's messages were all cleared — a ban or timeout, UserID set) or
// a channel.chat.clear one (the whole chat was cleared, UserID empty).
// Unlike Twitch IRC's old CLEARCHAT, EventSub doesn't say whether a
// per-user clear was a permanent ban or a timeout, or for how long —
// DurationSeconds is kept for forward compatibility but is never
// actually populated here.
type clearChatDTO struct {
	Channel         string `json:"channel"`
	UserID          string `json:"userId,omitempty"`
	Username        string `json:"username,omitempty"`
	DurationSeconds int    `json:"durationSeconds,omitempty"`
}

// raidDTO mirrors a channel.chat.notification event with notice_type
// "raid": another channel's broadcaster raided this one, bringing their
// viewers along.
type raidDTO struct {
	Channel     string `json:"channel"`
	DisplayName string `json:"displayName"`
	Login       string `json:"login"`
	ViewerCount int    `json:"viewerCount"`
}

// watchStreakDTO mirrors a Twitch watch-streak milestone (a viewer
// having watched Streak consecutive streams) — see
// twitch.WatchStreakEvent for why this arrives over a separate IRC
// connection rather than the EventSub session everything else here
// uses.
type watchStreakDTO struct {
	Channel     string `json:"channel"`
	Login       string `json:"login"`
	DisplayName string `json:"displayName"`
	Streak      int    `json:"streak"`
}

// giftSubDTO mirrors a gift-sub event — a Twitch channel.chat.notification
// with notice_type "sub_gift" or "community_sub_gift", or a Kick
// channel.subscription.gifts webhook — deliberately reporting only the
// gifter and how many subs, never who each one went to.
type giftSubDTO struct {
	Channel     string `json:"channel"`
	DisplayName string `json:"displayName"`
	Count       int    `json:"count"`
}

// subDTO mirrors a channel.chat.notification event with notice_type
// "sub" (a brand-new subscription) or "resub" (an existing subscriber
// renewing) — both relayed as a single "sub" SSE event so the frontend
// shows one callout either way. Months is how long the subscriber has
// been subscribed: 1 for a first-time sub, or the resub's all-time
// cumulative total otherwise.
type subDTO struct {
	Channel      string `json:"channel"`
	DisplayName  string `json:"displayName"`
	Tier         string `json:"tier"`
	Months       int    `json:"months"`
	StreakMonths int    `json:"streakMonths,omitempty"`
	IsResub      bool   `json:"isResub"`
	// Message is whatever the resubscriber optionally typed alongside
	// their resub notice; only ever populated for a resub (IsResub),
	// never a brand-new "sub".
	Message string `json:"message,omitempty"`
}

// cheerDTO mirrors a channel.cheer notification: a viewer cheered
// Bits. DisplayName is a placeholder when the cheer was anonymous.
type cheerDTO struct {
	Channel     string `json:"channel"`
	DisplayName string `json:"displayName"`
	Bits        int    `json:"bits"`
	Message     string `json:"message,omitempty"`
}

// automodHoldDTO mirrors an automod.message.hold (v2) notification: a
// chat message AutoMod is holding for moderator review before it posts.
// Only ever sent to a viewer who's actually a moderator/broadcaster —
// Twitch itself only delivers these to the moderator who subscribed.
type automodHoldDTO struct {
	Channel     string            `json:"channel"`
	MessageID   string            `json:"messageId"`
	Username    string            `json:"username"`
	DisplayName string            `json:"displayName"`
	UserID      string            `json:"userId"`
	Text        string            `json:"text"`
	Fragments   []chatFragmentDTO `json:"fragments,omitempty"`
	// Reason is "automod" or "blocked_term"; Category/Level are only
	// meaningful for the former.
	Reason    string `json:"reason"`
	Category  string `json:"category,omitempty"`
	Level     int    `json:"level,omitempty"`
	Timestamp int64  `json:"timestamp"`
}

// automodUpdateDTO mirrors an automod.message.update (v2) notification:
// a previously held message's status changed, so the frontend should
// stop showing it in the queue.
type automodUpdateDTO struct {
	Channel   string `json:"channel"`
	MessageID string `json:"messageId"`
	// Status is "Approved", "Denied", or "Expired".
	Status string `json:"status"`
}

// handleChatStream opens a Twitch EventSub WebSocket session subscribed
// to the requested channel's chat and relays chat messages and
// moderation events (message deletion, bans/timeouts, raids) to the
// browser as server-sent events. One EventSub session is created per
// request; this is fine for a locally run, single-viewer tool.
//
// Unlike the old IRC-based version of this handler, reading chat now
// requires the viewer to be signed in — EventSub has no anonymous read
// the way IRC did — though in practice the frontend already required
// sign-in before ever reaching this endpoint.
// https://dev.twitch.tv/docs/chat/irc-migration/
func (s *Server) handleChatStream(w http.ResponseWriter, r *http.Request) {
	sess := s.sessionFromRequest(r)
	if sess == nil {
		http.Error(w, "not authenticated", http.StatusUnauthorized)
		return
	}

	channel, err := normalizeTwitchChannel(r.URL.Query().Get("channel"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// Resolving the channel's ID and opening the EventSub WebSocket are
	// independent of each other, so they run concurrently rather than
	// one after the other — each is its own network round trip, and
	// there's no reason to pay for both in sequence.
	var (
		wg                 sync.WaitGroup
		broadcasterID      string
		resolveErr         error
		eventSubSession    *twitch.Session
		connectErr         error
		watchStreakSession *twitch.WatchStreakSession
	)
	wg.Add(3)
	go func() {
		defer wg.Done()
		broadcasterID, resolveErr = s.resolveBroadcasterID(ctx, s.cfg.TwitchClientID, sess.AccessToken, channel)
	}()
	go func() {
		defer wg.Done()
		eventSubSession, connectErr = twitch.Connect(ctx)
	}()
	go func() {
		defer wg.Done()
		// Watch streaks have no EventSub equivalent — see
		// twitch.ConnectWatchStreaks — so they arrive over a second,
		// anonymous, read-only IRC connection instead. Best-effort like
		// the AutoMod subscription below: a failure here just means no
		// watch-streak callouts this session, never a reason to fail
		// chat itself.
		var watchStreakErr error
		watchStreakSession, watchStreakErr = twitch.ConnectWatchStreaks(ctx, channel)
		if watchStreakErr != nil {
			log.Printf("twitch: could not connect for watch-streak notices on #%s: %v", channel, watchStreakErr)
		}
	}()
	wg.Wait()

	if resolveErr != nil {
		if eventSubSession != nil {
			eventSubSession.Close()
		}
		if watchStreakSession != nil {
			watchStreakSession.Close()
		}
		http.Error(w, "could not resolve channel", http.StatusBadGateway)
		return
	}
	if connectErr != nil {
		if watchStreakSession != nil {
			watchStreakSession.Close()
		}
		http.Error(w, "could not connect to twitch chat", http.StatusBadGateway)
		return
	}
	defer eventSubSession.Close()
	if watchStreakSession != nil {
		defer watchStreakSession.Close()
	}

	// Creating the chat subscriptions, creating the AutoMod queue
	// subscriptions, and fetching the channel's badge catalog are all
	// independent of each other too, so — like the pair above — they
	// run concurrently rather than back-to-back.
	var (
		wg2           sync.WaitGroup
		subIDs        []string
		subErr        error
		badges        twitchBadgeCatalog
		automodSubIDs []string
		cheerSubIDs   []string
	)
	wg2.Add(4)
	go func() {
		defer wg2.Done()
		subIDs, subErr = createChatEventSubSubscriptions(ctx, s.cfg.TwitchClientID, sess.AccessToken, broadcasterID, sess.UserID, eventSubSession.ID())
	}()
	go func() {
		defer wg2.Done()
		// Badges are cosmetic: on failure, badges stays nil and every
		// badge just renders with the frontend's approximate icon
		// instead of blocking the chat stream over it.
		badges, _ = s.twitchBadgeCatalogFor(ctx, s.cfg.TwitchClientID, sess.AccessToken, broadcasterID)
	}()
	go func() {
		defer wg2.Done()
		// The AutoMod queue is only available to actual moderators —
		// Twitch rejects this subscription for anyone else — so a
		// failure here is expected for most viewers and never treated
		// as a reason to fail the whole chat connection. It's still
		// logged: a moderator whose session predates the
		// moderator:manage:automod scope (added after they last signed
		// in) will fail here every time until they reconnect their
		// Twitch account, and that's otherwise invisible — the AutoMod
		// panel just never appears, with no error shown anywhere.
		var automodErr error
		automodSubIDs, automodErr = createAutomodEventSubSubscriptions(ctx, s.cfg.TwitchClientID, sess.AccessToken, broadcasterID, sess.UserID, eventSubSession.ID())
		if automodErr != nil {
			log.Printf("twitch: could not subscribe to AutoMod queue events for broadcaster %s (viewer %s): %v", broadcasterID, sess.UserID, automodErr)
		}
	}()
	go func() {
		defer wg2.Done()
		// Same best-effort story as AutoMod above: Bits revenue is
		// channel-private, so this is expected to fail for anyone but
		// the broadcaster/moderators, and that should never take down
		// chat itself — it just means no cheer callouts this session.
		var cheerErr error
		cheerSubIDs, cheerErr = createCheerEventSubSubscriptions(ctx, s.cfg.TwitchClientID, sess.AccessToken, broadcasterID, eventSubSession.ID())
		if cheerErr != nil {
			log.Printf("twitch: could not subscribe to cheer events for broadcaster %s (viewer %s): %v", broadcasterID, sess.UserID, cheerErr)
		}
	}()
	wg2.Wait()
	allSubIDs := append(subIDs, automodSubIDs...)
	allSubIDs = append(allSubIDs, cheerSubIDs...)

	defer func() {
		// Best-effort: the request context is already done by the time
		// this runs, so cleanup gets its own short-lived one instead.
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cleanupCancel()
		for _, id := range allSubIDs {
			_ = deleteEventSubSubscription(cleanupCtx, s.cfg.TwitchClientID, sess.AccessToken, id)
		}
	}()
	if subErr != nil {
		http.Error(w, "could not subscribe to twitch chat", http.StatusBadGateway)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)

	// Twitch's EventSub is push-only — it has no way to fetch messages
	// posted before this subscription started — so the best available
	// "history" is whatever this server has already seen for this
	// channel since it started up.
	for _, dto := range s.twitchChatHistory.recent(broadcasterID) {
		writeSSE(w, "message", dto)
	}
	flusher.Flush()

	keepalive := time.NewTicker(30 * time.Second)
	defer keepalive.Stop()

	// nil when the watch-streak connection never came up (or has since
	// ended) — a nil channel's case in the select below simply never
	// fires, so regular chat is unaffected either way.
	var watchStreakEvents <-chan twitch.WatchStreakEvent
	if watchStreakSession != nil {
		watchStreakEvents = watchStreakSession.Events()
	}

	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-watchStreakEvents:
			if !ok {
				watchStreakEvents = nil
				continue
			}
			writeSSE(w, "watchstreak", watchStreakDTO{
				Channel:     channel,
				Login:       ev.Login,
				DisplayName: ev.DisplayName,
				Streak:      ev.Streak,
			})
			flusher.Flush()
		case n, ok := <-eventSubSession.Notifications():
			if !ok {
				// The EventSub session ended (Twitch closed it, or a
				// reconnect it requested failed) — nothing more to relay.
				return
			}
			if strings.HasPrefix(n.Type, "revocation:") {
				// A subscription was revoked (auth revoked, the viewer
				// banned from the channel, ...) — end the stream so the
				// browser's EventSource reconnects and this handler runs
				// the whole subscribe flow again, rather than silently
				// going stale.
				return
			}
			if dto := writeTwitchNotification(w, channel, n, badges); dto != nil {
				s.twitchChatHistory.add(broadcasterID, *dto)
			}
			flusher.Flush()
		case <-keepalive.C:
			fmt.Fprint(w, ": keepalive\n\n")
			flusher.Flush()
		}
	}
}

// writeTwitchNotification relays a single EventSub notification to the
// browser as an SSE event. badges resolves a chat message's badges to
// their Twitch-shown images (see twitchBadgeCatalog); pass nil to
// render every badge with the frontend's approximate icon instead.
// When n is a chat message, it also returns the DTO that was written,
// so the caller can record it into Server.twitchChatHistory; nil for
// every other notification type.
func writeTwitchNotification(w http.ResponseWriter, channel string, n twitch.Notification, badges twitchBadgeCatalog) *chatMessageDTO {
	switch n.Type {
	case "channel.chat.message":
		var m twitch.ChatMessage
		if err := json.Unmarshal(n.Event, &m); err != nil {
			return nil
		}
		dto := chatMessageDTO{
			ID:           m.MessageID,
			Channel:      channel,
			Text:         m.Message.Text,
			Username:     m.ChatterUserLogin,
			DisplayName:  m.ChatterUserName,
			UserID:       m.ChatterUserID,
			Color:        m.Color,
			Mod:          twitch.HasBadge(m.Badges, "moderator"),
			Subscriber:   twitch.HasBadge(m.Badges, "subscriber"),
			Broadcaster:  twitch.HasBadge(m.Badges, "broadcaster"),
			Badges:       twitchBadgeDTOs(m.Badges, badges),
			FirstMessage: m.MessageType == "user_intro",
			Fragments:    twitchFragmentDTOs(m.Message.Fragments),
			Timestamp:    time.Now().UnixMilli(),
		}
		if m.Reply != nil {
			dto.Reply = &chatReplyDTO{
				Username:    m.Reply.ParentUserLogin,
				DisplayName: m.Reply.ParentUserName,
				Text:        m.Reply.ParentMsgBody,
			}
		}
		writeSSE(w, "message", dto)
		return &dto

	case "channel.chat.message_delete":
		var d twitch.MessageDelete
		if err := json.Unmarshal(n.Event, &d); err != nil {
			return nil
		}
		writeSSE(w, "clearmsg", clearMsgDTO{Channel: channel, MessageID: d.MessageID})

	case "channel.chat.clear":
		writeSSE(w, "clearchat", clearChatDTO{Channel: channel})

	case "channel.chat.clear_user_messages":
		var c twitch.ClearUserMessages
		if err := json.Unmarshal(n.Event, &c); err != nil {
			return nil
		}
		writeSSE(w, "clearchat", clearChatDTO{
			Channel:  channel,
			UserID:   c.TargetUserID,
			Username: c.TargetUserLogin,
		})

	case "channel.chat.notification":
		var note twitch.ChatNotification
		if err := json.Unmarshal(n.Event, &note); err != nil {
			return nil
		}
		switch {
		case note.NoticeType == "raid" && note.Raid != nil:
			writeSSE(w, "raid", raidDTO{
				Channel:     channel,
				DisplayName: note.Raid.UserName,
				Login:       note.Raid.UserLogin,
				ViewerCount: note.Raid.ViewerCount,
			})

		case note.NoticeType == "community_sub_gift" && note.CommunitySubGift != nil:
			writeSSE(w, "giftsub", giftSubDTO{
				Channel:     channel,
				DisplayName: twitchNotificationGifterName(note),
				Count:       note.CommunitySubGift.Total,
			})

		case note.NoticeType == "sub_gift" && note.SubGift != nil && note.SubGift.CommunityGiftID == nil:
			// A standalone single gift. One of these also arrives for
			// each recipient inside a mass gift (CommunityGiftID set on
			// those), but the community_sub_gift case above already
			// reports that batch as a whole, so those are skipped here
			// to avoid double-counting.
			writeSSE(w, "giftsub", giftSubDTO{
				Channel:     channel,
				DisplayName: twitchNotificationGifterName(note),
				Count:       1,
			})

		case note.NoticeType == "sub" && note.Sub != nil:
			writeSSE(w, "sub", subDTO{
				Channel:     channel,
				DisplayName: note.ChatterUserName,
				Tier:        twitchSubTierLabel(note.Sub.SubTier, note.Sub.IsPrime),
				Months:      1,
			})

		case note.NoticeType == "resub" && note.Resub != nil:
			writeSSE(w, "sub", subDTO{
				Channel:      channel,
				DisplayName:  note.ChatterUserName,
				Tier:         twitchSubTierLabel(note.Resub.SubTier, note.Resub.IsPrime),
				Months:       note.Resub.CumulativeMonths,
				StreakMonths: note.Resub.StreakMonths,
				IsResub:      true,
				Message:      note.Message.Text,
			})
		}

	case "channel.cheer":
		var c twitch.Cheer
		if err := json.Unmarshal(n.Event, &c); err != nil {
			return nil
		}
		name := c.UserName
		if c.IsAnonymous || name == "" {
			name = "An anonymous cheerer"
		}
		writeSSE(w, "cheer", cheerDTO{
			Channel:     channel,
			DisplayName: name,
			Bits:        c.Bits,
			Message:     c.Message,
		})

	case "automod.message.hold":
		var hold twitch.AutomodMessageHold
		if err := json.Unmarshal(n.Event, &hold); err != nil {
			return nil
		}
		dto := automodHoldDTO{
			Channel:     channel,
			MessageID:   hold.MessageID,
			Username:    hold.UserLogin,
			DisplayName: hold.UserName,
			UserID:      hold.UserID,
			Text:        hold.Message.Text,
			Fragments:   twitchFragmentDTOs(hold.Message.Fragments),
			Reason:      hold.Reason,
			Timestamp:   time.Now().UnixMilli(),
		}
		if hold.Automod != nil {
			dto.Category = hold.Automod.Category
			dto.Level = hold.Automod.Level
		}
		writeSSE(w, "automodhold", dto)

	case "automod.message.update":
		var upd twitch.AutomodMessageUpdate
		if err := json.Unmarshal(n.Event, &upd); err != nil {
			return nil
		}
		writeSSE(w, "automodupdate", automodUpdateDTO{
			Channel:   channel,
			MessageID: upd.MessageID,
			Status:    upd.Status,
		})
	}
	return nil
}

// twitchNotificationGifterName returns who to credit a gift-sub
// notification to, honoring a gifter's choice to remain anonymous.
func twitchNotificationGifterName(note twitch.ChatNotification) string {
	if note.ChatterIsAnonymous {
		return "An anonymous gifter"
	}
	return note.ChatterUserName
}

// twitchSubTierLabel renders a sub/resub notification's raw sub_tier
// ("1000", "2000", "3000") as the label Twitch shows in its own UI. A
// Prime-linked sub always carries sub_tier "1000" underneath, so isPrime
// is checked first.
func twitchSubTierLabel(subTier string, isPrime bool) string {
	if isPrime {
		return "Prime"
	}
	switch subTier {
	case "2000":
		return "Tier 2"
	case "3000":
		return "Tier 3"
	default:
		return "Tier 1"
	}
}

func writeSSE(w http.ResponseWriter, event string, payload any) {
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, data)
}
