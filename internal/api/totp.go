package api

import (
	"crypto/hmac"
	"crypto/sha1" // RFC 6238 authenticator-app default.
	"crypto/sha256"
	"encoding/base32"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"github.com/jackc/pgx/v5"
	"net/http"
	"rivosnap/internal/crypto"
	"slices"
	"strings"
	"time"
)

func recoveryHash(code string) string {
	sum := sha256.Sum256([]byte(strings.TrimSpace(code)))
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
	secret, e := crypto.Open(seed, s.Env.TokenEncryptionKey)
	if e != nil {
		return 0, e
	}
	key, e := base32.StdEncoding.DecodeString(secret)
	if e != nil {
		return 0, e
	}
	now := time.Now().Unix() / 30
	for _, step := range []int64{now, now - 1, now + 1} {
		if step > last && crypto.Equal(totpAt(key, step), strings.TrimSpace(code)) {
			return step, nil
		}
	}
	return 0, rejected
}

// Caller holds the singleton owner row lock until the proof and its effect commit.
func (s *Server) verifySecond(r *http.Request, tx pgx.Tx, o *securityOwner, code string) error {
	if o.seed == "" {
		return rejected
	}
	if len(strings.TrimSpace(code)) == 6 {
		step, e := s.verifyTOTP(o.seed, code, o.step)
		if e != nil {
			return e
		}
		_, e = tx.Exec(r.Context(), `UPDATE "Owner" SET "totpStep"=$1 WHERE id=1`, step)
		o.step = step
		return e
	}
	hash := recoveryHash(code)
	i := slices.Index(o.hashes, hash)
	if i < 0 {
		return rejected
	}
	o.hashes = slices.Delete(o.hashes, i, i+1)
	_, e := tx.Exec(r.Context(), `UPDATE "Owner" SET "recoveryHashes"=$1 WHERE id=1`, o.hashes)
	return e
}
