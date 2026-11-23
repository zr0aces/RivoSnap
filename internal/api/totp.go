package api

import (
	"crypto/hmac"
	"crypto/sha1" // RFC 6238 authenticator-app default.
	"crypto/sha256"
	"encoding/base32"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"log"
	"slices"
	"strings"
	"time"

	"rivosnap/internal/crypto"
)

// normalizeCode makes typed codes canonical: recovery codes are uppercase base32, and users may add spaces or hyphens.
func normalizeCode(code string) string {
	return strings.NewReplacer(" ", "", "-", "").Replace(strings.ToUpper(code))
}
func recoveryHash(code string) string {
	sum := sha256.Sum256([]byte(normalizeCode(code)))
	return hex.EncodeToString(sum[:])
}
func totpAt(key []byte, step int64) string {
	h := hmac.New(sha1.New, key)
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], uint64(step))
	h.Write(b[:])
	d := h.Sum(nil)
	offset := d[len(d)-1] & 15
	return fmt.Sprintf("%06d", (binary.BigEndian.Uint32(d[offset:])&0x7fffffff)%1000000)
}
func (s *Server) verifyTOTP(seed, code string, last int64) (int64, error) {
	// An unreadable seed fails closed as a rejected proof; the log names no seed or error detail.
	secret, e := crypto.Open(seed, s.Env.TokenEncryptionKey)
	if e != nil {
		log.Print("Owner authenticator seed is unreadable; check TOKEN_ENCRYPTION_KEY")
		return 0, rejected
	}
	key, e := base32.StdEncoding.DecodeString(secret)
	if e != nil {
		log.Print("Owner authenticator seed is unreadable; check TOKEN_ENCRYPTION_KEY")
		return 0, rejected
	}
	now := time.Now().Unix() / 30
	for _, step := range []int64{now, now - 1, now + 1} {
		if step > last && crypto.Equal(totpAt(key, step), normalizeCode(code)) {
			return step, nil
		}
	}
	return 0, rejected
}

// Caller holds the singleton owner row lock until the proof and its effect commit.
func (t *ownerTx) verifySecond(code string) error {
	if t.sealedSeed == "" {
		return rejected
	}
	if len(normalizeCode(code)) == 6 {
		step, e := t.s.verifyTOTP(t.sealedSeed, code, t.totpStep)
		if e != nil {
			return e
		}
		_, e = t.tx.Exec(t.r.Context(), `UPDATE "Owner" SET "totpStep"=$1 WHERE id=1`, step)
		t.totpStep = step
		return e
	}
	hash := recoveryHash(code)
	i := slices.Index(t.recoveryHashes, hash)
	if i < 0 {
		return rejected
	}
	t.recoveryHashes = slices.Delete(t.recoveryHashes, i, i+1)
	_, e := t.tx.Exec(t.r.Context(), `UPDATE "Owner" SET "recoveryHashes"=$1 WHERE id=1`, t.recoveryHashes)
	return e
}
