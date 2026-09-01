package server

import (
	"encoding/json"
	"net/http"
	"strings"
)

// handleDeleteChatMessage deletes a single chat message. Twitch itself
// enforces that the caller is a moderator/broadcaster of the channel and
// rejects deleting the broadcaster's own messages or another
// moderator's.
func (s *Server) handleDeleteChatMessage(w http.ResponseWriter, r *http.Request) {
	sess := s.sessionFromRequest(r)
	if sess == nil {
		http.Error(w, "not authenticated", http.StatusUnauthorized)
		return
	}

	messageID := strings.TrimSpace(r.URL.Query().Get("messageId"))
	if messageID == "" {
		http.Error(w, "messageId query parameter is required", http.StatusBadRequest)
		return
	}
	channel, err := normalizeTwitchChannel(r.URL.Query().Get("channel"))
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	broadcasterID, err := s.resolveBroadcasterID(r.Context(), s.cfg.TwitchClientID, sess.AccessToken, channel)
	if err != nil {
		http.Error(w, "could not resolve channel", http.StatusBadGateway)
		return
	}

	if err := deleteChatMessage(r.Context(), s.cfg.TwitchClientID, sess.AccessToken, broadcasterID, sess.UserID, messageID); err != nil {
		writeTwitchAPIError(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

type sendMessageRequestDTO struct {
	Channel string `json:"channel"`
	Message string `json:"message"`
}

// handleSendChatMessage posts a new chat message to the channel, sent as
// the signed-in user themselves — Twitch has no "send as the channel"
// concept for a user access token.
func (s *Server) handleSendChatMessage(w http.ResponseWriter, r *http.Request) {
	sess := s.sessionFromRequest(r)
	if sess == nil {
		http.Error(w, "not authenticated", http.StatusUnauthorized)
		return
	}

	var req sendMessageRequestDTO
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	message := strings.TrimSpace(req.Message)
	if message == "" {
		http.Error(w, "message is required", http.StatusBadRequest)
		return
	}
	channel, err := normalizeTwitchChannel(req.Channel)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	broadcasterID, err := s.resolveBroadcasterID(r.Context(), s.cfg.TwitchClientID, sess.AccessToken, channel)
	if err != nil {
		http.Error(w, "could not resolve channel", http.StatusBadGateway)
		return
	}

	if err := sendChatMessage(r.Context(), s.cfg.TwitchClientID, sess.AccessToken, broadcasterID, sess.UserID, message); err != nil {
		writeTwitchAPIError(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

type banRequestDTO struct {
	Channel string `json:"channel"`
	UserID  string `json:"userId"`
	Reason  string `json:"reason,omitempty"`
}

type timeoutRequestDTO struct {
	Channel         string `json:"channel"`
	UserID          string `json:"userId"`
	Reason          string `json:"reason,omitempty"`
	DurationSeconds int    `json:"durationSeconds"`
}

// handleBanUser permanently bans a chatter from the channel.
func (s *Server) handleBanUser(w http.ResponseWriter, r *http.Request) {
	sess := s.sessionFromRequest(r)
	if sess == nil {
		http.Error(w, "not authenticated", http.StatusUnauthorized)
		return
	}

	var req banRequestDTO
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.UserID == "" {
		http.Error(w, "userId is required", http.StatusBadRequest)
		return
	}
	channel, err := normalizeTwitchChannel(req.Channel)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	broadcasterID, err := s.resolveBroadcasterID(r.Context(), s.cfg.TwitchClientID, sess.AccessToken, channel)
	if err != nil {
		http.Error(w, "could not resolve channel", http.StatusBadGateway)
		return
	}

	if err := banOrTimeoutUser(r.Context(), s.cfg.TwitchClientID, sess.AccessToken, broadcasterID, sess.UserID, req.UserID, 0, req.Reason); err != nil {
		writeTwitchAPIError(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// handleTimeoutUser temporarily bans (times out) a chatter.
func (s *Server) handleTimeoutUser(w http.ResponseWriter, r *http.Request) {
	sess := s.sessionFromRequest(r)
	if sess == nil {
		http.Error(w, "not authenticated", http.StatusUnauthorized)
		return
	}

	var req timeoutRequestDTO
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}
	if req.UserID == "" {
		http.Error(w, "userId is required", http.StatusBadRequest)
		return
	}
	if req.DurationSeconds <= 0 {
		http.Error(w, "durationSeconds must be greater than zero", http.StatusBadRequest)
		return
	}
	channel, err := normalizeTwitchChannel(req.Channel)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	broadcasterID, err := s.resolveBroadcasterID(r.Context(), s.cfg.TwitchClientID, sess.AccessToken, channel)
	if err != nil {
		http.Error(w, "could not resolve channel", http.StatusBadGateway)
		return
	}

	if err := banOrTimeoutUser(r.Context(), s.cfg.TwitchClientID, sess.AccessToken, broadcasterID, sess.UserID, req.UserID, req.DurationSeconds, req.Reason); err != nil {
		writeTwitchAPIError(w, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// writeTwitchAPIError relays a Helix error's status and message to the
// browser (e.g. "user is a moderator" from trying to ban another mod),
// falling back to a generic 502 for anything that didn't come from a
// Twitch API response.
func writeTwitchAPIError(w http.ResponseWriter, err error) {
	if apiErr, ok := err.(*twitchAPIError); ok {
		status := apiErr.Status
		if status < 400 || status > 599 {
			status = http.StatusBadGateway
		}
		http.Error(w, apiErr.Message, status)
		return
	}
	http.Error(w, "twitch moderation request failed", http.StatusBadGateway)
}
