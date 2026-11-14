package server

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSaveLoadSessionsRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions.enc")
	key := testKey(t, 0x11)

	want := map[string]*twitchSession{
		"tok1": {
			AccessToken:  "access-secret",
			RefreshToken: "refresh-secret",
			UserID:       "1",
			Login:        "someuser",
			DisplayName:  "SomeUser",
			ExpiresAt:    time.Now().Add(time.Hour).UTC().Round(time.Second),
		},
	}

	if err := saveSessions(path, key, want); err != nil {
		t.Fatalf("saveSessions: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading persisted file: %v", err)
	}
	if bytes.Contains(raw, []byte("access-secret")) || bytes.Contains(raw, []byte("refresh-secret")) {
		t.Fatal("session file on disk contains a plaintext token")
	}

	got, err := loadSessions(path, key)
	if err != nil {
		t.Fatalf("loadSessions: %v", err)
	}
	gotSess, ok := got["tok1"]
	if !ok {
		t.Fatal("loaded sessions missing tok1")
	}
	if gotSess.AccessToken != want["tok1"].AccessToken || gotSess.RefreshToken != want["tok1"].RefreshToken {
		t.Errorf("loadSessions() = %+v, want %+v", gotSess, want["tok1"])
	}
}

func TestLoadSessionsWrongKeyFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions.enc")

	sessions := map[string]*twitchSession{"tok": {AccessToken: "secret"}}
	if err := saveSessions(path, testKey(t, 0xAA), sessions); err != nil {
		t.Fatalf("saveSessions: %v", err)
	}

	if _, err := loadSessions(path, testKey(t, 0xBB)); err == nil {
		t.Error("loadSessions with wrong key succeeded, want error")
	}
}

func TestLoadSessionsMissingFile(t *testing.T) {
	dir := t.TempDir()
	got, err := loadSessions(filepath.Join(dir, "does-not-exist.enc"), testKey(t, 0x01))
	if err != nil {
		t.Fatalf("loadSessions on missing file: %v", err)
	}
	if got != nil {
		t.Errorf("loadSessions on missing file = %v, want nil", got)
	}
}

func TestResolveEncryptionKeyGeneratesAndReusesKeyFile(t *testing.T) {
	dir := t.TempDir()
	sessionPath := filepath.Join(dir, "sessions.enc")

	key1, err := resolveEncryptionKey(sessionPath, "")
	if err != nil {
		t.Fatalf("resolveEncryptionKey (first call): %v", err)
	}
	if len(key1) != encryptionKeySize {
		t.Fatalf("generated key length = %d, want %d", len(key1), encryptionKeySize)
	}

	keyPath := filepath.Join(dir, sessionKeyFileName)
	info, err := os.Stat(keyPath)
	if err != nil {
		t.Fatalf("key file was not created: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("key file mode = %v, want 0600", perm)
	}

	key2, err := resolveEncryptionKey(sessionPath, "")
	if err != nil {
		t.Fatalf("resolveEncryptionKey (second call): %v", err)
	}
	if !bytes.Equal(key1, key2) {
		t.Error("resolveEncryptionKey generated a different key on the second call, want the cached one reused")
	}
}

func TestResolveEncryptionKeyFromConfig(t *testing.T) {
	dir := t.TempDir()
	key := testKey(t, 0x55)
	got, err := resolveEncryptionKey(filepath.Join(dir, "sessions.enc"), base64.StdEncoding.EncodeToString(key))
	if err != nil {
		t.Fatalf("resolveEncryptionKey: %v", err)
	}
	if !bytes.Equal(got, key) {
		t.Error("resolveEncryptionKey did not return the configured key")
	}
	if _, err := os.Stat(filepath.Join(dir, sessionKeyFileName)); !os.IsNotExist(err) {
		t.Error("resolveEncryptionKey wrote a key file even though a key was explicitly configured")
	}
}

func TestNewAuthStorePersistsAcrossRestarts(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions.enc")

	store1 := newAuthStore(path, "")
	token, err := store1.putSession(&twitchSession{AccessToken: "secret", Login: "someuser"})
	if err != nil {
		t.Fatalf("putSession: %v", err)
	}

	// Simulate a restart: a fresh store reading the same path should
	// pick the session back up.
	store2 := newAuthStore(path, "")
	sess := store2.session(token)
	if sess == nil {
		t.Fatal("session not restored after simulated restart")
	}
	if sess.AccessToken != "secret" {
		t.Errorf("restored AccessToken = %q, want %q", sess.AccessToken, "secret")
	}
}
