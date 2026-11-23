package api

import (
	"bytes"
	"crypto/rand"
	"encoding/base32"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	wa "github.com/go-webauthn/webauthn/webauthn"
	"github.com/jackc/pgx/v5"
	"github.com/redis/go-redis/v9"
	qr "github.com/skip2/go-qrcode"

	"rivosnap/internal/crypto"
)

type revisionContextKey struct{}

func sessionValue(rev int64) string { return fmt.Sprintf("rev:%d", rev) }
func (s *Server) sessionRevision(r *http.Request, sid string) (int64, bool, error) {
	if sid == "" {
		return 0, false, nil
	}
	ctx := r.Context()
	v, e := s.Redis.Get(ctx, "session:"+sid).Result()
	if errors.Is(e, redis.Nil) {
		return 0, false, nil
	}
	if e != nil {
		return 0, false, e
	}
	var rev int64
	e = s.DB.Pool.QueryRow(ctx, `SELECT "securityRevision" FROM "Owner" WHERE id=1`).Scan(&rev)
	if errors.Is(e, pgx.ErrNoRows) {
		// No owner row means no owner is signed in; anonymous, not an error.
		return 0, false, nil
	}
	return rev, e == nil && v == sessionValue(rev), e
}

type action string

const (
	enable     action = "enable"
	disable    action = "disable"
	replace    action = "replace"
	regenerate action = "regenerate"
	add        action = "add"
	remove     action = "remove"
)

type purpose string

const (
	login  purpose = "login"
	mfa    purpose = "mfa"
	prove  purpose = "prove"
	finish purpose = "finish"
)

// rotates lists the actions that replace a factor and must show the owner new material.
var rotates = map[action]struct{ seed, codes bool }{
	enable:     {seed: true, codes: true},
	replace:    {seed: true, codes: true},
	regenerate: {codes: true},
}

type flow struct {
	Purpose                          purpose
	SID                              string
	Action                           action
	Target, SealedSeed, Confirmation string
	Revision                         int64
	RecoveryHashes                   []string
	Web                              wa.SessionData
}

const flowCookie = "owner_attempt"

var expired = &httpError{410, "Verification expired. Start again."}
var rejected = &httpError{400, "Verification failed. Check your details and try again."}

func flowKey(r *http.Request) string {
	c, e := r.Cookie(flowCookie)
	if e != nil || len(c.Value) != 64 {
		return ""
	}
	if _, e = hex.DecodeString(c.Value); e != nil {
		return ""
	}
	return "owner-flow:" + c.Value
}
func (s *Server) cancelFlow(r *http.Request) error {
	if k := flowKey(r); k != "" {
		return s.Redis.Del(r.Context(), k).Err()
	}
	return nil
}
func (s *Server) saveFlow(w http.ResponseWriter, r *http.Request, f flow) error {
	if err := s.cancelFlow(r); err != nil {
		return err
	}
	var id [32]byte
	rand.Read(id[:])
	token := hex.EncodeToString(id[:])
	b, e := json.Marshal(f)
	if e != nil {
		return e
	}
	if e = s.Redis.Set(r.Context(), "owner-flow:"+token, b, 5*time.Minute).Err(); e != nil {
		return e
	}
	http.SetCookie(w, &http.Cookie{Name: flowCookie, Value: token, Path: "/api/v1/", HttpOnly: true, Secure: s.Env.NodeEnv == "production", SameSite: http.SameSiteStrictMode, MaxAge: 300})
	return nil
}
func (s *Server) loadFlow(r *http.Request, want purpose, consume bool) (flow, error) {
	var f flow
	k := flowKey(r)
	if k == "" {
		return f, expired
	}
	b, e := s.Redis.Get(r.Context(), k).Bytes()
	if errors.Is(e, redis.Nil) {
		return f, expired
	}
	if e != nil {
		return f, e
	}
	if e = json.Unmarshal(b, &f); e != nil {
		return f, e
	}
	// A stray request for another purpose must not destroy the pending attempt.
	if f.Purpose != want || (f.SID != "" && s.sid(r) != f.SID) {
		return f, expired
	}
	if consume {
		// Only one concurrent request can delete the key and see a count of 1.
		n, e := s.Redis.Del(r.Context(), k).Result()
		if e != nil {
			return f, e
		}
		if n != 1 {
			return f, expired
		}
	}
	return f, nil
}

type securityInput struct {
	Action                                       action
	Target, Password, Code, Method, Confirmation string
	Saved                                        bool
	Credential                                   json.RawMessage
}
type securityOwner struct {
	email, passwordHash, handle, sealedSeed string
	rev, totpStep                           int64
	recoveryHashes                          []string
	keys                                    []wa.Credential
	names                                   map[string]string
}

func (o *securityOwner) WebAuthnID() []byte                   { return []byte(o.handle) }
func (o *securityOwner) WebAuthnName() string                 { return o.email }
func (o *securityOwner) WebAuthnDisplayName() string          { return o.email }
func (o *securityOwner) WebAuthnCredentials() []wa.Credential { return o.keys }

func loadOwner(r *http.Request, tx pgx.Tx, lock bool) (*securityOwner, error) {
	ctx := r.Context()
	o := &securityOwner{names: map[string]string{}}
	query := `SELECT email,"passwordHash","passkeyHandle"::text,"securityRevision","totpSecret","totpStep","recoveryHashes" FROM "Owner" WHERE id=1`
	if lock {
		query += " FOR UPDATE"
	}
	err := tx.QueryRow(ctx, query).Scan(&o.email, &o.passwordHash, &o.handle, &o.rev, &o.sealedSeed, &o.totpStep, &o.recoveryHashes)
	if err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT name,credential FROM "OwnerPasskey" WHERE "ownerId"=1 ORDER BY name,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		var cred wa.Credential
		if err = rows.Scan(&name, &cred); err != nil {
			return nil, err
		}
		o.keys = append(o.keys, cred)
		o.names[keyID(cred.ID)] = name
	}
	return o, rows.Err()
}
func keyID(id []byte) string { return base64.RawURLEncoding.EncodeToString(id) }
func (s *Server) verifier() (*wa.WebAuthn, error) {
	u, e := url.Parse(s.Env.AppURL)
	if e != nil {
		return nil, e
	}
	if u.Scheme != "https" && !(s.Env.NodeEnv != "production" && (u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1")) {
		return nil, errBadRequest("Passkeys require HTTPS.")
	}
	return wa.New(&wa.Config{RPID: u.Hostname(), RPDisplayName: "RivoSnap", RPOrigins: []string{s.Env.AppURL}, AuthenticatorSelection: protocol.AuthenticatorSelection{UserVerification: protocol.VerificationRequired}})
}
func (s *Server) beginAssertion(w http.ResponseWriter, r *http.Request, f flow) (any, error) {
	v, e := s.verifier()
	if e != nil {
		return nil, e
	}
	options, session, e := v.BeginDiscoverableLogin(wa.WithUserVerification(protocol.VerificationRequired))
	if e != nil {
		return nil, e
	}
	f.Web = *session
	return options, s.saveFlow(w, r, f)
}
func credentialRequest(r *http.Request, b []byte) *http.Request {
	c := r.Clone(r.Context())
	c.Body = io.NopCloser(bytes.NewReader(b))
	return c
}
func (t *ownerTx) assert(f flow, b []byte) error {
	v, e := t.s.verifier()
	if e != nil {
		return e
	}
	_, c, e := v.FinishPasskeyLogin(func(id, handle []byte) (wa.User, error) {
		if !bytes.Equal(handle, t.WebAuthnID()) {
			return nil, rejected
		}
		if _, ok := t.names[keyID(id)]; !ok {
			return nil, rejected
		}
		return t.securityOwner, nil
	}, f.Web, credentialRequest(t.r, b))
	if e != nil || c.Authenticator.CloneWarning {
		return rejected
	}
	_, e = t.tx.Exec(t.r.Context(), `UPDATE "OwnerPasskey" SET credential=$1 WHERE id=$2`, c, c.ID)
	return e
}

const secBase = "/api/v1/"

// Only explicitly public operations may run before full owner authentication.
// rated operations share the auth rate bucket; handle does one route's work.
type securityOperation struct {
	method, summary string
	public, rated   bool
	handle          func(*Server, http.ResponseWriter, *http.Request, securityInput) error
}

var securityPaths = map[string]securityOperation{
	"profile":             {method: "GET", summary: "Read owner email, enrolled methods and unused recovery-code count", handle: (*Server).handleProfile},
	"profile/start":       {method: "POST", summary: "Bind fresh verification to a named security change and target", rated: true, handle: (*Server).handleChangeStart},
	"profile/prove":       {method: "POST", summary: "Verify the existing passkey assertion for a pending security change", rated: true, handle: (*Server).handleChangeProve},
	"profile/finish":      {method: "POST", summary: "Confirm a prepared change and revoke previous sessions", rated: true, handle: (*Server).handleChangeFinish},
	"auth/passkey/start":  {method: "POST", summary: "Begin passwordless discoverable passkey sign-in", public: true, rated: true, handle: (*Server).handlePasskeyStart},
	"auth/passkey/finish": {method: "POST", summary: "Verify a user-verified assertion and issue the owner session", public: true, rated: true, handle: (*Server).handlePasskeyFinish},
	"auth/mfa":            {method: "POST", summary: "Complete a pending password login with TOTP or a recovery code", public: true, rated: true, handle: (*Server).handleMFA},
	"auth/cancel":         {method: "POST", summary: "Cancel the browser's pending verification without changing the account", public: true, handle: (*Server).handleCancel},
}

func publicSecurityRoute(method, path string) bool {
	op := securityPaths[strings.TrimPrefix(path, secBase)]
	return op.public && op.method == method
}

// securityRoutes is the only shared prelude: parse the body, apply the rate limit, then run the route.
func (s *Server) securityRoutes(m *http.ServeMux) {
	for path, op := range securityPaths {
		m.HandleFunc(op.method+" "+secBase+path, func(w http.ResponseWriter, r *http.Request) {
			var in securityInput
			if r.Method == "POST" {
				raw, err := readBody(r)
				if err != nil {
					writeErr(w, err)
					return
				}
				if len(raw) > 0 && json.Unmarshal(raw, &in) != nil {
					writeErr(w, errBadRequest("Invalid request."))
					return
				}
			}
			if op.rated && !s.checkRate(s.authRates, path+":"+clientHost(r), 8, 5*time.Minute) {
				writeErr(w, &httpError{429, "Too many attempts. Try again later."})
				return
			}
			if err := op.handle(s, w, r, in); err != nil {
				writeErr(w, err)
			}
		})
	}
}

// ownerTx is the owner transaction one security route works inside.
type ownerTx struct {
	s  *Server
	r  *http.Request
	tx pgx.Tx
	*securityOwner
}

// beginOwner starts a transaction, loads the owner and applies the session revision check.
// lock=true takes the singleton owner row lock for a proof or change; lock=false is a read-only snapshot.
// On error the transaction is already rolled back; on success the caller rolls back unless it commits.
func (s *Server) beginOwner(r *http.Request, lock bool) (*ownerTx, error) {
	ctx := r.Context()
	opts := pgx.TxOptions{AccessMode: pgx.ReadOnly, IsoLevel: pgx.RepeatableRead}
	if lock {
		opts = pgx.TxOptions{}
	}
	tx, err := s.DB.Pool.BeginTx(ctx, opts)
	if err != nil {
		return nil, err
	}
	o, err := loadOwner(r, tx, lock)
	if err != nil {
		_ = tx.Rollback(ctx)
		return nil, err
	}
	if rev, ok := ctx.Value(revisionContextKey{}).(int64); ok && rev != o.rev {
		_ = tx.Rollback(ctx)
		return nil, expired
	}
	return &ownerTx{s: s, r: r, tx: tx, securityOwner: o}, nil
}

// exhausted is an attempt that used its wrong-code budget; its flow is deleted, so the browser must restart.
var exhausted = &httpError{429, "Too many incorrect attempts. Start sign-in again."}

// pendingFlow loads the attempt for want. Retried purposes keep the flow until success and are limited per attempt.
func (s *Server) pendingFlow(r *http.Request, want purpose, retry bool) (flow, error) {
	f, err := s.loadFlow(r, want, !retry)
	if err != nil {
		return f, err
	}
	if retry && !s.checkRate(s.authRates, flowKey(r), 5, 5*time.Minute) {
		_ = s.cancelFlow(r)
		return f, exhausted
	}
	return f, nil
}

// completeLogin creates the session before commit, so a session-store failure rolls back the proof
// and the recovery code or TOTP step stays usable. The cookie is set only after the commit succeeds.
func (s *Server) completeLogin(w http.ResponseWriter, t *ownerTx) error {
	ctx := t.r.Context()
	sid, err := s.newSession(ctx, t.rev)
	if err != nil {
		return err
	}
	if err := t.tx.Commit(ctx); err != nil {
		// Best effort: the id is unreachable without a cookie and expires after 12 hours.
		_ = s.Redis.Del(ctx, "session:"+sid).Err()
		return err
	}
	s.setSessionCookie(w, sid)
	writeJSON(w, 200, map[string]bool{"ok": true})
	return nil
}

func (s *Server) handleCancel(w http.ResponseWriter, r *http.Request, _ securityInput) error {
	if err := s.cancelFlow(r); err != nil {
		return err
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
	return nil
}

func (s *Server) handleProfile(w http.ResponseWriter, r *http.Request, _ securityInput) error {
	t, err := s.beginOwner(r, false)
	if err != nil {
		return err
	}
	defer t.tx.Rollback(r.Context())
	out := map[string]any{"email": t.email, "totp": t.sealedSeed != "", "passkeys": t.names, "recoveryRemaining": len(t.recoveryHashes)}
	if err := t.tx.Commit(r.Context()); err != nil {
		return err
	}
	writeJSON(w, 200, out)
	return nil
}

func (s *Server) handlePasskeyStart(w http.ResponseWriter, r *http.Request, _ securityInput) error {
	var rev int64
	err := s.DB.Pool.QueryRow(r.Context(), `SELECT COALESCE((SELECT "securityRevision" FROM "Owner" WHERE id=1),0)`).Scan(&rev)
	if err != nil {
		return err
	}
	out, err := s.beginAssertion(w, r, flow{Purpose: login, Revision: rev})
	if err != nil {
		return err
	}
	writeJSON(w, 200, out)
	return nil
}

func (s *Server) handlePasskeyFinish(w http.ResponseWriter, r *http.Request, in securityInput) error {
	pending, err := s.pendingFlow(r, login, false)
	if err != nil {
		return err
	}
	t, err := s.beginOwner(r, true)
	if err != nil {
		return err
	}
	defer t.tx.Rollback(r.Context())
	if pending.Revision != t.rev {
		return expired
	}
	if err := t.assert(pending, in.Credential); err != nil {
		return err
	}
	return s.completeLogin(w, t)
}

func (s *Server) handleMFA(w http.ResponseWriter, r *http.Request, in securityInput) error {
	pending, err := s.pendingFlow(r, mfa, true)
	if err != nil {
		return err
	}
	t, err := s.beginOwner(r, true)
	if err != nil {
		return err
	}
	defer t.tx.Rollback(r.Context())
	if pending.Revision != t.rev {
		return expired
	}
	if err := t.verifySecond(in.Code); err != nil {
		return err
	}
	if _, err := s.loadFlow(r, mfa, true); err != nil {
		return err
	}
	return s.completeLogin(w, t)
}

func (s *Server) handleChangeStart(w http.ResponseWriter, r *http.Request, in securityInput) error {
	// The password check hashes under the gate. Take the slot before beginOwner locks the Owner row,
	// so a request waiting for a slot never holds that lock. Passkey changes do not hash.
	if in.Method != "passkey" {
		release, ok := s.acquireHash(r.Context())
		if !ok {
			return errHashBusy
		}
		defer release()
	}
	t, err := s.beginOwner(r, true)
	if err != nil {
		return err
	}
	defer t.tx.Rollback(r.Context())
	if !validChange(t.securityOwner, in) {
		return errBadRequest("This change is unavailable. Refresh Profile.")
	}
	f := flow{Purpose: prove, Revision: t.rev, SID: s.sid(r), Action: in.Action, Target: in.Target}
	if in.Method == "passkey" {
		out, err := s.beginAssertion(w, r, f)
		if err != nil {
			return err
		}
		if err := t.tx.Commit(r.Context()); err != nil {
			return err
		}
		writeJSON(w, 200, out)
		return nil
	}
	if len(in.Password) > 256 || !crypto.VerifyPassword(t.passwordHash, in.Password) {
		return rejected
	}
	if t.sealedSeed != "" {
		if err := t.verifySecond(in.Code); err != nil {
			return err
		}
	}
	if err := t.tx.Commit(r.Context()); err != nil {
		return err
	}
	out, err := s.prepareChange(w, r, t.securityOwner, f)
	if err != nil {
		return err
	}
	writeJSON(w, 200, out)
	return nil
}

func (s *Server) handleChangeProve(w http.ResponseWriter, r *http.Request, in securityInput) error {
	pending, err := s.pendingFlow(r, prove, false)
	if err != nil {
		return err
	}
	t, err := s.beginOwner(r, true)
	if err != nil {
		return err
	}
	defer t.tx.Rollback(r.Context())
	if pending.Revision != t.rev {
		return expired
	}
	if err := t.assert(pending, in.Credential); err != nil {
		return err
	}
	if err := t.tx.Commit(r.Context()); err != nil {
		return err
	}
	out, err := s.prepareChange(w, r, t.securityOwner, pending)
	if err != nil {
		return err
	}
	writeJSON(w, 200, out)
	return nil
}

func (s *Server) handleChangeFinish(w http.ResponseWriter, r *http.Request, in securityInput) error {
	pending, err := s.pendingFlow(r, finish, true)
	if err != nil {
		return err
	}
	t, err := s.beginOwner(r, true)
	if err != nil {
		return err
	}
	defer t.tx.Rollback(r.Context())
	if pending.Revision != t.rev {
		return expired
	}
	if err := t.finishChange(pending, in); err != nil {
		return err
	}
	if _, err := s.loadFlow(r, finish, true); err != nil {
		return err
	}
	if err := t.tx.Commit(r.Context()); err != nil {
		return err
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
	return nil
}

func validChange(o *securityOwner, in securityInput) bool {
	switch in.Action {
	case enable:
		return o.sealedSeed == ""
	case disable, regenerate, replace:
		return o.sealedSeed != ""
	case add:
		return strings.TrimSpace(in.Target) != "" && len(in.Target) <= 80 && len(o.keys) < 32
	case remove:
		_, ok := o.names[in.Target]
		return ok
	}
	return false
}
func (s *Server) prepareChange(w http.ResponseWriter, r *http.Request, o *securityOwner, f flow) (any, error) {
	f.Purpose = finish
	f.Web = wa.SessionData{}
	f.Confirmation = rand.Text()
	result := map[string]any{"confirmation": f.Confirmation}
	if rotates[f.Action].seed {
		raw := make([]byte, 20)
		rand.Read(raw)
		secret := base32.StdEncoding.EncodeToString(raw)
		enc, e := crypto.Seal(secret, s.Env.TokenEncryptionKey)
		if e != nil {
			return nil, e
		}
		f.SealedSeed = enc
		uri := "otpauth://totp/" + url.PathEscape("RivoSnap:"+o.email) + "?" + url.Values{"issuer": {"RivoSnap"}, "secret": {secret}}.Encode()
		png, e := qr.Encode(uri, qr.Medium, 240)
		if e != nil {
			return nil, e
		}
		result["secret"] = secret
		result["qr"] = "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)
	}
	if rotates[f.Action].codes {
		codes := make([]string, 8)
		for i := range codes {
			codes[i] = rand.Text()
			f.RecoveryHashes = append(f.RecoveryHashes, recoveryHash(codes[i]))
		}
		result["codes"] = codes
	}
	if f.Action == add {
		v, e := s.verifier()
		if e != nil {
			return nil, e
		}
		opts, data, e := v.BeginRegistration(o, wa.WithResidentKeyRequirement(protocol.ResidentKeyRequirementRequired), wa.WithExclusions(wa.Credentials(o.keys).CredentialDescriptors()))
		if e != nil {
			return nil, e
		}
		result["registration"] = opts
		f.Web = *data
	}
	return result, s.saveFlow(w, r, f)
}
func (t *ownerTx) finishChange(f flow, in securityInput) error {
	ctx := t.r.Context()
	if !crypto.Equal(f.Confirmation, in.Confirmation) {
		return expired
	}
	if rot, ok := rotates[f.Action]; ok {
		if !in.Saved {
			return errBadRequest("Confirm you saved the recovery codes.")
		}
		if rot.seed {
			step, e := t.s.verifyTOTP(f.SealedSeed, in.Code, -1)
			if e != nil {
				return e
			}
			t.totpStep = step
			t.sealedSeed = f.SealedSeed
		}
		t.recoveryHashes = f.RecoveryHashes
	} else {
		switch f.Action {
		case disable:
			t.sealedSeed = ""
			t.totpStep = -1
			t.recoveryHashes = []string{}
		case add:
			v, e := t.s.verifier()
			if e != nil {
				return e
			}
			cred, e := v.FinishRegistration(t.securityOwner, f.Web, credentialRequest(t.r, in.Credential))
			if e != nil {
				return rejected
			}
			if _, e = t.tx.Exec(ctx, `INSERT INTO "OwnerPasskey"(id,"ownerId",name,credential) VALUES($1,1,$2,$3)`, cred.ID, f.Target, cred); e != nil {
				return e
			}
		case remove:
			id, e := base64.RawURLEncoding.DecodeString(f.Target)
			if e != nil {
				return e
			}
			if _, e = t.tx.Exec(ctx, `DELETE FROM "OwnerPasskey" WHERE id=$1 AND "ownerId"=1`, id); e != nil {
				return e
			}
		default:
			return expired
		}
	}
	_, e := t.tx.Exec(ctx, `UPDATE "Owner" SET "securityRevision"="securityRevision"+1,"totpSecret"=$1,"totpStep"=$2,"recoveryHashes"=$3 WHERE id=1`, t.sealedSeed, t.totpStep, t.recoveryHashes)
	return e
}
