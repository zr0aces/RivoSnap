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
	return rev, e == nil && v == sessionValue(rev), e
}

type flow struct {
	Purpose, SID, Action, Target, Seed, Confirmation string
	Revision                                         int64
	Hashes                                           []string
	Web                                              wa.SessionData
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
func (s *Server) loadFlow(r *http.Request, purpose string, consume bool) (flow, error) {
	var f flow
	k := flowKey(r)
	if k == "" {
		return f, expired
	}
	var b []byte
	var e error
	if consume {
		b, e = s.Redis.GetDel(r.Context(), k).Bytes()
	} else {
		b, e = s.Redis.Get(r.Context(), k).Bytes()
	}
	if errors.Is(e, redis.Nil) {
		return f, expired
	}
	if e != nil {
		return f, e
	}
	if e = json.Unmarshal(b, &f); e != nil {
		return f, e
	}
	if f.Purpose != purpose || (f.SID != "" && s.sid(r) != f.SID) {
		return f, expired
	}
	return f, nil
}

type securityInput struct {
	Action, Target, Password, Code, Method, Confirmation string
	Saved                                                bool
	Credential                                           json.RawMessage
}
type securityOwner struct {
	email, password, handle, seed string
	rev, step                     int64
	hashes                        []string
	keys                          []wa.Credential
	names                         map[string]string
}

func (o *securityOwner) WebAuthnID() []byte                   { return []byte(o.handle) }
func (o *securityOwner) WebAuthnName() string                 { return o.email }
func (o *securityOwner) WebAuthnDisplayName() string          { return o.email }
func (o *securityOwner) WebAuthnCredentials() []wa.Credential { return o.keys }

func loadOwner(r *http.Request, tx pgx.Tx) (*securityOwner, error) {
	ctx := r.Context()
	o := &securityOwner{names: map[string]string{}}
	err := tx.QueryRow(ctx, `SELECT email,"passwordHash","passkeyHandle"::text,"securityRevision","totpSecret","totpStep","recoveryHashes" FROM "Owner" WHERE id=1 FOR UPDATE`).Scan(&o.email, &o.password, &o.handle, &o.rev, &o.seed, &o.step, &o.hashes)
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
func (s *Server) assert(r *http.Request, tx pgx.Tx, o *securityOwner, f flow, b []byte) error {
	v, e := s.verifier()
	if e != nil {
		return e
	}
	_, c, e := v.FinishPasskeyLogin(func(id, handle []byte) (wa.User, error) {
		if !bytes.Equal(handle, o.WebAuthnID()) {
			return nil, rejected
		}
		if _, ok := o.names[keyID(id)]; !ok {
			return nil, rejected
		}
		return o, nil
	}, f.Web, credentialRequest(r, b))
	if e != nil || c.Authenticator.CloneWarning {
		return rejected
	}
	_, e = tx.Exec(r.Context(), `UPDATE "OwnerPasskey" SET credential=$1 WHERE id=$2`, c, c.ID)
	return e
}

const secBase = "/api/v1/"

// Only explicitly public operations may run before full owner authentication.
type securityOperation struct {
	method, summary string
	public          bool
}

var securityPaths = map[string]securityOperation{
	"profile":             {"GET", "Read owner email, enrolled methods and unused recovery-code count", false},
	"profile/start":       {"POST", "Bind fresh verification to a named security change and target", false},
	"profile/prove":       {"POST", "Verify the existing passkey assertion for a pending security change", false},
	"profile/finish":      {"POST", "Confirm a prepared change and revoke previous sessions", false},
	"auth/passkey/start":  {"POST", "Begin passwordless discoverable passkey sign-in", true},
	"auth/passkey/finish": {"POST", "Verify a user-verified assertion and issue the owner session", true},
	"auth/mfa":            {"POST", "Complete a pending password login with TOTP or a recovery code", true},
	"auth/cancel":         {"POST", "Cancel the browser's pending verification without changing the account", true},
}

func publicSecurityRoute(method, path string) bool {
	op := securityPaths[strings.TrimPrefix(path, secBase)]
	return op.public && op.method == method
}
func (s *Server) securityRoutes(m *http.ServeMux) {
	for path, op := range securityPaths {
		m.HandleFunc(op.method+" "+secBase+path, func(w http.ResponseWriter, r *http.Request) {
			if err := s.securityRequest(w, r, path); err != nil {
				writeErr(w, err)
			}
		})
	}
}
func (s *Server) securityRequest(w http.ResponseWriter, r *http.Request, path string) error {
	ctx := r.Context()
	var in securityInput
	if r.Method == "POST" {
		raw, e := readBody(r)
		if e != nil {
			return e
		}
		if len(raw) > 0 && json.Unmarshal(raw, &in) != nil {
			return errBadRequest("Invalid request.")
		}
	}
	if path == "auth/cancel" {
		if e := s.cancelFlow(r); e != nil {
			return e
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
		return nil
	}
	if path != "profile" && !s.checkRate(s.authRates, path+":"+clientHost(r), 8, 5*time.Minute) {
		return &httpError{429, "Too many attempts. Try again later."}
	}
	if path == "auth/passkey/start" {
		var rev int64
		err := s.DB.Pool.QueryRow(ctx, `SELECT COALESCE((SELECT "securityRevision" FROM "Owner" WHERE id=1),0)`).Scan(&rev)
		if err != nil {
			return err
		}
		out, e := s.beginAssertion(w, r, flow{Purpose: "login", Revision: rev})
		if e != nil {
			return e
		}
		writeJSON(w, 200, out)
		return nil
	}
	purpose := map[string]string{"auth/passkey/finish": "login", "auth/mfa": "mfa", "profile/prove": "prove", "profile/finish": "finish"}[path]
	retry := purpose == "mfa" || purpose == "finish"
	var pending flow
	if purpose != "" {
		var err error
		pending, err = s.loadFlow(r, purpose, !retry)
		if err != nil {
			return err
		}
		if retry && !s.checkRate(s.authRates, flowKey(r), 5, 5*time.Minute) {
			return expired
		}
	}
	tx, e := s.DB.Pool.Begin(ctx)
	if e != nil {
		return e
	}
	defer tx.Rollback(ctx)
	o, e := loadOwner(r, tx)
	if e != nil {
		return e
	}
	if rev, ok := ctx.Value(revisionContextKey{}).(int64); ok && rev != o.rev {
		return expired
	}
	var out any = map[string]bool{"ok": true}
	login := false
	var change *flow
	switch path {
	case "profile":
		out = map[string]any{"email": o.email, "totp": o.seed != "", "passkeys": o.names, "recoveryRemaining": len(o.hashes)}
	case "auth/passkey/finish", "auth/mfa", "profile/prove", "profile/finish":
		f := pending
		if f.Revision != o.rev {
			return expired
		}
		switch purpose {
		case "mfa":
			if e = s.verifySecond(r, tx, o, in.Code); e != nil {
				return e
			}
			login = true
		case "login", "prove":
			if e = s.assert(r, tx, o, f, in.Credential); e != nil {
				return e
			}
			if purpose == "login" {
				login = true
			} else {
				change = &f
			}
		case "finish":
			if e = s.finishChange(r, tx, o, f, in); e != nil {
				return e
			}
		}
		if retry {
			if _, e = s.loadFlow(r, purpose, true); e != nil {
				return e
			}
		}
	case "profile/start":
		if !validChange(o, in) {
			return errBadRequest("This change is unavailable. Refresh Profile.")
		}
		f := flow{Purpose: "prove", Revision: o.rev, SID: s.sid(r), Action: in.Action, Target: in.Target}
		if in.Method == "passkey" {
			out, e = s.beginAssertion(w, r, f)
		} else {
			if len(in.Password) > 256 || !crypto.VerifyPassword(o.password, in.Password) {
				return rejected
			}
			if o.seed != "" {
				if e = s.verifySecond(r, tx, o, in.Code); e != nil {
					return e
				}
			}
			change = &f
		}
		if e != nil {
			return e
		}
	}
	if e = tx.Commit(ctx); e != nil {
		return e
	}
	if change != nil {
		out, e = s.prepareChange(w, r, o, *change)
		if e != nil {
			return e
		}
	}
	if login {
		s.issueSession(w, r, o.rev)
	} else {
		writeJSON(w, 200, out)
	}
	return nil
}

func validChange(o *securityOwner, in securityInput) bool {
	switch in.Action {
	case "enable":
		return o.seed == ""
	case "disable", "regenerate", "replace":
		return o.seed != ""
	case "add":
		return strings.TrimSpace(in.Target) != "" && len(in.Target) <= 80 && len(o.keys) < 32
	case "remove":
		_, ok := o.names[in.Target]
		return ok
	}
	return false
}
func (s *Server) prepareChange(w http.ResponseWriter, r *http.Request, o *securityOwner, f flow) (any, error) {
	f.Purpose = "finish"
	f.Web = wa.SessionData{}
	f.Confirmation = rand.Text()
	result := map[string]any{"confirmation": f.Confirmation}
	if f.Action == "enable" || f.Action == "replace" {
		raw := make([]byte, 20)
		rand.Read(raw)
		secret := base32.StdEncoding.EncodeToString(raw)
		enc, e := crypto.Seal(secret, s.Env.TokenEncryptionKey)
		if e != nil {
			return nil, e
		}
		f.Seed = enc
		uri := "otpauth://totp/" + url.PathEscape("RivoSnap:"+o.email) + "?" + url.Values{"issuer": {"RivoSnap"}, "secret": {secret}}.Encode()
		png, e := qr.Encode(uri, qr.Medium, 240)
		if e != nil {
			return nil, e
		}
		result["secret"] = secret
		result["qr"] = "data:image/png;base64," + base64.StdEncoding.EncodeToString(png)
	}
	if f.Action == "enable" || f.Action == "replace" || f.Action == "regenerate" {
		codes := make([]string, 8)
		for i := range codes {
			codes[i] = rand.Text()
			f.Hashes = append(f.Hashes, recoveryHash(codes[i]))
		}
		result["codes"] = codes
	}
	if f.Action == "add" {
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
func (s *Server) finishChange(r *http.Request, tx pgx.Tx, o *securityOwner, f flow, in securityInput) error {
	ctx := r.Context()
	if !crypto.Equal(f.Confirmation, in.Confirmation) {
		return expired
	}
	switch f.Action {
	case "enable", "regenerate", "replace":
		if !in.Saved {
			return errBadRequest("Confirm you saved the recovery codes.")
		}
		if f.Action == "enable" || f.Action == "replace" {
			step, e := s.verifyTOTP(f.Seed, in.Code, -1)
			if e != nil {
				return e
			}
			o.step = step
			o.seed = f.Seed
		}
		o.hashes = f.Hashes
	case "disable":
		o.seed = ""
		o.step = -1
		o.hashes = []string{}
	case "add":
		v, e := s.verifier()
		if e != nil {
			return e
		}
		cred, e := v.FinishRegistration(o, f.Web, credentialRequest(r, in.Credential))
		if e != nil {
			return rejected
		}
		if _, e = tx.Exec(ctx, `INSERT INTO "OwnerPasskey"(id,"ownerId",name,credential) VALUES($1,1,$2,$3)`, cred.ID, f.Target, cred); e != nil {
			return e
		}
	case "remove":
		id, e := base64.RawURLEncoding.DecodeString(f.Target)
		if e != nil {
			return e
		}
		if _, e = tx.Exec(ctx, `DELETE FROM "OwnerPasskey" WHERE id=$1 AND "ownerId"=1`, id); e != nil {
			return e
		}
	default:
		return expired
	}
	_, e := tx.Exec(ctx, `UPDATE "Owner" SET "securityRevision"="securityRevision"+1,"totpSecret"=$1,"totpStep"=$2,"recoveryHashes"=$3 WHERE id=1`, o.seed, o.step, o.hashes)
	return e
}
