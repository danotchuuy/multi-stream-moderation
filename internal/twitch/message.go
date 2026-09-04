package twitch

// Badge is a single chat badge, as EventSub represents it.
// https://dev.twitch.tv/docs/eventsub/eventsub-reference/#channel-chat-message-event
type Badge struct {
	SetID string `json:"set_id"`
	ID    string `json:"id"`
	Info  string `json:"info"`
}

// HasBadge reports whether badges contains one with the given set ID
// (e.g. "moderator", "subscriber", "broadcaster").
func HasBadge(badges []Badge, setID string) bool {
	for _, b := range badges {
		if b.SetID == setID {
			return true
		}
	}
	return false
}

// ChatMessage is a channel.chat.message notification's event payload: a
// chat message.
// https://dev.twitch.tv/docs/eventsub/eventsub-reference/#channel-chat-message-event
type ChatMessage struct {
	MessageID        string `json:"message_id"`
	ChatterUserID    string `json:"chatter_user_id"`
	ChatterUserLogin string `json:"chatter_user_login"`
	ChatterUserName  string `json:"chatter_user_name"`
	Message          struct {
		Text string `json:"text"`
		// Fragments breaks Text down into plain-text and special
		// (emote/cheermote/mention) pieces, in order. Unlike IRC's old
		// "emotes" tag (positions into the raw text), EventSub
		// pre-segments the message, so no offset math is needed to line
		// emotes back up with the text.
		Fragments []Fragment `json:"fragments"`
	} `json:"message"`
	// MessageType distinguishes an ordinary text message from special
	// presentations like "channel_points_highlighted" or "user_intro" —
	// the latter is the closest available signal to IRC's old
	// first-ever-message-in-channel flag, though it's a distinct concept
	// (an opt-in "chat intro" rather than a literal first message).
	MessageType string  `json:"message_type"`
	Badges      []Badge `json:"badges"`
	Color       string  `json:"color"`
	Reply       *Reply  `json:"reply"`
}

// Fragment is one piece of a chat message's text. Type is "text",
// "cheermote", "emote", "mention", or "gif"; only Emote is populated
// for an "emote" fragment (the app doesn't currently render
// cheermotes, mentions, or gifs specially).
// https://dev.twitch.tv/docs/eventsub/eventsub-reference/#channel-chat-message-event
type Fragment struct {
	Type  string         `json:"type"`
	Text  string         `json:"text"`
	Emote *FragmentEmote `json:"emote"`
}

// FragmentEmote is an "emote" fragment's metadata — enough to build the
// emote's CDN image URL (see twitchEmoteImageURL in
// internal/server/handlers_chat.go).
type FragmentEmote struct {
	ID string `json:"id"`
}

// Reply is the parent message a chat reply is threaded to.
type Reply struct {
	ParentMessageID string `json:"parent_message_id"`
	ParentUserID    string `json:"parent_user_id"`
	ParentUserName  string `json:"parent_user_name"`
	ParentUserLogin string `json:"parent_user_login"`
	ParentMsgBody   string `json:"parent_message_body"`
}

// MessageDelete is a channel.chat.message_delete notification's event
// payload: a single chat message was deleted.
type MessageDelete struct {
	MessageID       string `json:"message_id"`
	TargetUserID    string `json:"target_user_id"`
	TargetUserLogin string `json:"target_user_login"`
	TargetUserName  string `json:"target_user_name"`
}

// ClearUserMessages is a channel.chat.clear_user_messages notification's
// event payload: a user's messages were all cleared (ban/timeout).
type ClearUserMessages struct {
	TargetUserID    string `json:"target_user_id"`
	TargetUserLogin string `json:"target_user_login"`
	TargetUserName  string `json:"target_user_name"`
}

// Cheer is a channel.cheer notification's event payload: a viewer
// cheered Bits. UserID/UserLogin/UserName are all empty when
// IsAnonymous is set — the cheerer chose not to be identified.
// https://dev.twitch.tv/docs/eventsub/eventsub-reference/#cheer-event
type Cheer struct {
	IsAnonymous bool   `json:"is_anonymous"`
	UserID      string `json:"user_id"`
	UserLogin   string `json:"user_login"`
	UserName    string `json:"user_name"`
	Message     string `json:"message"`
	Bits        int    `json:"bits"`
}

// ChatNotification is a channel.chat.notification notification's event
// payload — Twitch's catch-all for chat-room announcements (raids, subs,
// gifts, ...). Only the fields this app currently uses (raids and gift
// subs) are parsed; see NoticeType for the full set of possible values.
// https://dev.twitch.tv/docs/eventsub/eventsub-reference/#channel-chat-notification-event
type ChatNotification struct {
	NoticeType string `json:"notice_type"`
	// ChatterUserName identifies whoever triggered this notification —
	// e.g. the gifter for a sub_gift/community_sub_gift — unless
	// ChatterIsAnonymous is set, in which case they chose not to be
	// named and ChatterUserName is empty.
	ChatterUserName    string `json:"chatter_user_name"`
	ChatterIsAnonymous bool   `json:"chatter_is_anonymous"`
	// Message is whatever the chatter optionally typed alongside the
	// notification itself — e.g. a resub's "thanks for 12 months!" note.
	// Text is empty when nothing was attached.
	Message struct {
		Text      string     `json:"text"`
		Fragments []Fragment `json:"fragments"`
	} `json:"message"`
	Raid *struct {
		UserID      string `json:"user_id"`
		UserLogin   string `json:"user_login"`
		UserName    string `json:"user_name"`
		ViewerCount int    `json:"viewer_count"`
	} `json:"raid"`
	// SubGift is set for notice_type "sub_gift": a single gifted sub.
	// When it's one of several gifted together, CommunityGiftID is set
	// and CommunitySubGift (on the accompanying community_sub_gift
	// notification) already reports the whole batch — see
	// writeTwitchNotification, which skips these to avoid
	// double-counting.
	SubGift *struct {
		CommunityGiftID *string `json:"community_gift_id"`
	} `json:"sub_gift"`
	// CommunitySubGift is set for notice_type "community_sub_gift": a
	// gifter giving Total subs to the community at once.
	CommunitySubGift *struct {
		Total int `json:"total"`
	} `json:"community_sub_gift"`
	// Sub is set for notice_type "sub": a brand-new subscription (this
	// chatter's first month).
	Sub *struct {
		SubTier string `json:"sub_tier"`
		IsPrime bool   `json:"is_prime"`
	} `json:"sub"`
	// Resub is set for notice_type "resub": an existing subscriber
	// renewing. CumulativeMonths is the all-time total months
	// subscribed (independent of any lapses) — the "how long they've
	// been subscribed" figure; StreakMonths is the current run of
	// consecutive months, 0 if the subscriber chose not to share it.
	Resub *struct {
		CumulativeMonths int    `json:"cumulative_months"`
		StreakMonths     int    `json:"streak_months"`
		SubTier          string `json:"sub_tier"`
		IsPrime          bool   `json:"is_prime"`
	} `json:"resub"`
}

// AutomodMessageHold is an automod.message.hold (v2) notification's
// event payload: a chat message AutoMod is holding for moderator review
// before it posts.
// https://dev.twitch.tv/docs/eventsub/eventsub-reference/#automod-message-hold-v2-event
type AutomodMessageHold struct {
	MessageID string `json:"message_id"`
	UserID    string `json:"user_id"`
	UserLogin string `json:"user_login"`
	UserName  string `json:"user_name"`
	Message   struct {
		Text      string     `json:"text"`
		Fragments []Fragment `json:"fragments"`
	} `json:"message"`
	// Reason is "automod" (caught by AutoMod's own filters) or
	// "blocked_term" (a channel-configured blocked term); Automod is
	// only populated for the former.
	Reason  string `json:"reason"`
	Automod *struct {
		Category string `json:"category"`
		Level    int    `json:"level"`
	} `json:"automod"`
	HeldAt string `json:"held_at"`
}

// AutomodMessageUpdate is an automod.message.update (v2) notification's
// event payload: a previously held message's status changed — approved
// or denied by ModeratorUserName, or expired unactioned.
// https://dev.twitch.tv/docs/eventsub/eventsub-reference/#automod-message-update-v2-event
type AutomodMessageUpdate struct {
	MessageID string `json:"message_id"`
	// Status is "Approved", "Denied", or "Expired".
	Status            string `json:"status"`
	ModeratorUserName string `json:"moderator_user_name"`
}
