package server

import (
	"bytes"
	"testing"
)

func testKey(t *testing.T, b byte) []byte {
	t.Helper()
	key := make([]byte, encryptionKeySize)
	for i := range key {
		key[i] = b
	}
	return key
}

func TestEncryptDecryptRoundTrip(t *testing.T) {
	key := testKey(t, 0x42)
	plaintext := []byte(`{"sessions":{"tok":{"accessToken":"secret"}}}`)

	ciphertext, err := encrypt(key, plaintext)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if bytes.Contains(ciphertext, []byte("secret")) {
		t.Fatal("ciphertext contains the plaintext access token")
	}

	got, err := decrypt(key, ciphertext)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Errorf("decrypt() = %q, want %q", got, plaintext)
	}
}

func TestDecryptWrongKeyFails(t *testing.T) {
	ciphertext, err := encrypt(testKey(t, 0x01), []byte("hello"))
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if _, err := decrypt(testKey(t, 0x02), ciphertext); err == nil {
		t.Error("decrypt with wrong key succeeded, want error")
	}
}

func TestDecryptTamperedCiphertextFails(t *testing.T) {
	key := testKey(t, 0x03)
	ciphertext, err := encrypt(key, []byte("hello"))
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	tampered := bytes.Clone(ciphertext)
	tampered[len(tampered)-1] ^= 0xFF

	if _, err := decrypt(key, tampered); err == nil {
		t.Error("decrypt of tampered ciphertext succeeded, want error")
	}
}

func TestEncryptRejectsWrongKeySize(t *testing.T) {
	if _, err := encrypt([]byte("too-short"), []byte("hello")); err == nil {
		t.Error("encrypt with a non-32-byte key succeeded, want error")
	}
}
