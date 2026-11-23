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

func TestUnreadableSeedRejected(t *testing.T) {
	var s Server
	s.Env.TokenEncryptionKey = strings.Repeat("1", 64)
	sealedOther, err := crypto.Seal(base32.StdEncoding.EncodeToString([]byte("12345678901234567890")), strings.Repeat("2", 64))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.verifyTOTP(sealedOther, "287082", -1); err != rejected {
		t.Fatal("seed sealed under another key not rejected:", err)
	}
	notBase32, err := crypto.Seal("not base32!", s.Env.TokenEncryptionKey)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.verifyTOTP(notBase32, "287082", -1); err != rejected {
		t.Fatal("undecodable seed not rejected:", err)
	}
}

func TestRecoveryCodeNormalization(t *testing.T) {
	want := recoveryHash("ABCDEFGHJKLM")
	for _, form := range []string{"abcdefghjklm", "ABCD-EFGH-JKLM", " abcd efgh jklm ", "  Abcd-Efgh JKLM"} {
		if got := recoveryHash(form); got != want {
			t.Fatalf("recovery code form %q does not match canonical hash", form)
		}
	}
	key := []byte("12345678901234567890")
	var s Server
	s.Env.TokenEncryptionKey = strings.Repeat("1", 64)
	enc, err := crypto.Seal(base32.StdEncoding.EncodeToString(key), s.Env.TokenEncryptionKey)
	if err != nil {
		t.Fatal(err)
	}
	code := totpAt(key, time.Now().Unix()/30)
	if _, err := s.verifyTOTP(enc, code[:3]+" "+code[3:], -1); err != nil {
		t.Fatal("spaced authenticator code rejected:", err)
	}
}
