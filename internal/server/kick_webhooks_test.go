package server

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"testing"
)

func signKickWebhook(t *testing.T, priv *rsa.PrivateKey, messageID, timestamp string, body []byte) []byte {
	t.Helper()
	signed := []byte(messageID + "." + timestamp + "." + string(body))
	hashed := sha256.Sum256(signed)
	sig, err := rsa.SignPKCS1v15(rand.Reader, priv, crypto.SHA256, hashed[:])
	if err != nil {
		t.Fatalf("SignPKCS1v15: %v", err)
	}
	encoded := make([]byte, base64.StdEncoding.EncodedLen(len(sig)))
	base64.StdEncoding.Encode(encoded, sig)
	return encoded
}

func TestKickWebhookSignatureVerification(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	cache := &kickPublicKeyCache{key: &priv.PublicKey}

	messageID := "01HZ8X9K2M4N6P8Q0R2S4T6V8W0Y2Z4"
	timestamp := "2026-01-01T00:00:00Z"
	body := []byte(`{"message_id":"abc","content":"hello"}`)
	sig := signKickWebhook(t, priv, messageID, timestamp, body)

	if err := cache.verify(context.Background(), messageID, timestamp, body, sig); err != nil {
		t.Errorf("verify() with a valid signature returned an error: %v", err)
	}
}

func TestKickWebhookSignatureVerificationRejectsTamperedBody(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	cache := &kickPublicKeyCache{key: &priv.PublicKey}

	messageID := "01HZ8X9K2M4N6P8Q0R2S4T6V8W0Y2Z4"
	timestamp := "2026-01-01T00:00:00Z"
	body := []byte(`{"message_id":"abc","content":"hello"}`)
	sig := signKickWebhook(t, priv, messageID, timestamp, body)

	tampered := []byte(`{"message_id":"abc","content":"goodbye"}`)
	if err := cache.verify(context.Background(), messageID, timestamp, tampered, sig); err == nil {
		t.Error("verify() accepted a signature over a different body")
	}
}

func TestKickWebhookSignatureVerificationRejectsWrongKey(t *testing.T) {
	signer, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	other, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey: %v", err)
	}
	cache := &kickPublicKeyCache{key: &other.PublicKey}

	messageID := "id"
	timestamp := "2026-01-01T00:00:00Z"
	body := []byte(`{}`)
	sig := signKickWebhook(t, signer, messageID, timestamp, body)

	if err := cache.verify(context.Background(), messageID, timestamp, body, sig); err == nil {
		t.Error("verify() accepted a signature from an unrecognized key")
	}
}

func TestGeneratePKCE(t *testing.T) {
	verifier, challenge, err := generatePKCE()
	if err != nil {
		t.Fatalf("generatePKCE: %v", err)
	}
	if len(verifier) < 43 {
		t.Errorf("verifier length = %d, want >= 43 per RFC 7636", len(verifier))
	}

	sum := sha256.Sum256([]byte(verifier))
	want := base64.RawURLEncoding.EncodeToString(sum[:])
	if challenge != want {
		t.Errorf("challenge = %q, want S256(verifier) = %q", challenge, want)
	}

	verifier2, _, err := generatePKCE()
	if err != nil {
		t.Fatalf("generatePKCE (second call): %v", err)
	}
	if verifier == verifier2 {
		t.Error("generatePKCE returned the same verifier twice, want random")
	}
}

func TestKickBadgeHelpers(t *testing.T) {
	var nilIdentity *kickIdentity
	if nilIdentity.hasBadge("moderator") {
		t.Error("nil identity should never report a badge")
	}

	identity := &kickIdentity{Badges: []kickBadge{{Type: "moderator"}, {Type: "subscriber"}}}
	if !identity.hasBadge("moderator") {
		t.Error("expected hasBadge(moderator) to be true")
	}
	if identity.hasBadge("broadcaster") {
		t.Error("expected hasBadge(broadcaster) to be false")
	}
}

func TestKickBadgeDTOs(t *testing.T) {
	if got := kickBadgeDTOs(nil); got != nil {
		t.Errorf("kickBadgeDTOs(nil) = %#v, want nil", got)
	}

	identity := &kickIdentity{Badges: []kickBadge{
		{Type: "moderator"},
		{Type: "subscriber", Count: 6},
	}}
	got := kickBadgeDTOs(identity)
	want := []chatBadgeDTO{
		{Name: "moderator"},
		{Name: "subscriber", Version: "6"},
	}
	if len(got) != len(want) {
		t.Fatalf("kickBadgeDTOs() = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("kickBadgeDTOs()[%d] = %#v, want %#v", i, got[i], want[i])
		}
	}
}
