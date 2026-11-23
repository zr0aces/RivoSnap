package api

import (
	"encoding/base32"
	"rivosnap/internal/crypto"
	"strings"
	"testing"
	"time"
)

func TestAuthenticatorCodeAndReplay(t *testing.T) {
	key := []byte("12345678901234567890")
	if got := totpAt(key, 59/30); got != "287082" {
		t.Fatalf("RFC 6238 six digits: %s", got)
	}
	var s Server
	s.Env.TokenEncryptionKey = strings.Repeat("1", 64)
	enc, err := crypto.Seal(base32.StdEncoding.EncodeToString(key), s.Env.TokenEncryptionKey)
	if err != nil {
		t.Fatal(err)
	}
	step := time.Now().Unix() / 30
	code := totpAt(key, step)
	if got, err := s.verifyTOTP(enc, code, -1); got != step || err != nil {
		t.Fatal("fresh code rejected")
	}
	if _, err := s.verifyTOTP(enc, code, step); err == nil {
		t.Fatal("reused step accepted")
	}
}
