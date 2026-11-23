package api_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base32"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"

	"rivosnap/internal/api"
	"rivosnap/internal/crypto"
)

// ownerRequest sends one JSON request through h with the same-origin headers the browser sends.
func ownerRequest(h http.Handler, method, path string, body any, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	b, _ := json.Marshal(body)
	r := httptest.NewRequest(method, "/api/v1/"+path, bytes.NewReader(b))
	r.Header.Set("Origin", "http://localhost")
	r.Header.Set("X-Gallery-Request", "1")
	for _, c := range cookies {
		r.AddCookie(c)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func ownerCall(env integrationEnv, method, path string, body any, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	return ownerRequest(env.handler, method, path, body, cookies...)
}

// seedOwnerSecurity enrolls a real encrypted TOTP seed and the given recovery codes (stored
// in canonical uppercase form), and bumps the revision so existing sessions go stale.
func seedOwnerSecurity(t *testing.T, env integrationEnv, codes ...string) {
	t.Helper()
	raw := make([]byte, 20)
	if _, err := rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	seed, err := crypto.Seal(base32.StdEncoding.EncodeToString(raw), env.db.Env.TokenEncryptionKey)
	if err != nil {
		t.Fatal(err)
	}
	hashes := make([]string, 0, len(codes))
	for _, c := range codes {
		sum := sha256.Sum256([]byte(strings.ToUpper(c)))
		hashes = append(hashes, hex.EncodeToString(sum[:]))
	}
	if _, err := env.db.Pool.Exec(env.ctx, `UPDATE "Owner" SET "securityRevision"="securityRevision"+1,"totpSecret"=$1,"recoveryHashes"=$2 WHERE id=1`, seed, hashes); err != nil {
		t.Fatal(err)
	}
}

func TestOwnerSecurityBoundaries(t *testing.T) {
	env := integrationServer(t)
	owner := &http.Cookie{Name: "session", Value: env.session}
	type payload = map[string]any
	start := ownerCall(env, "POST", "profile/start", payload{"action": "enable", "password": "long-fixture-password"}, owner)
	var preview struct {
		Confirmation, Secret string
		Codes                []string
	}
	if start.Code != 200 || json.Unmarshal(start.Body.Bytes(), &preview) != nil || len(preview.Codes) != 8 || preview.Secret == "" {
		t.Fatal("missing enrollment preview", start.Code)
	}
	flow := start.Result().Cookies()[0]
	if got := ownerCall(env, "POST", "profile/finish", payload{"confirmation": preview.Confirmation, "saved": false}, owner, flow).Code; got != 400 {
		t.Fatal("unconfirmed enrollment", got)
	}
	if got := ownerCall(env, "GET", "profile", nil, owner); got.Code != 200 || !strings.Contains(got.Body.String(), `"totp":false`) {
		t.Fatal("failed enrollment changed owner")
	}
	// Seed the second step directly for concurrency tests; the browser test covers enrollment.
	seedOwnerSecurity(t, env, "ONCE")
	if got := ownerCall(env, "GET", "profile", nil, owner).Code; got != 401 {
		t.Fatal("stale session accepted", got)
	}
	login := func() *http.Cookie {
		w := ownerCall(env, "POST", "auth/login", payload{"email": "owner@example.org", "password": "long-fixture-password"})
		if w.Code != 200 || !strings.Contains(w.Body.String(), "mfaRequired") {
			t.Fatal("missing challenge")
		}
		cookies := w.Result().Cookies()
		if len(cookies) != 1 || cookies[0].Name == "session" {
			t.Fatal("password issued a full session")
		}
		return cookies[0]
	}
	a, b := login(), login()
	for _, p := range []string{"profile", "images", "google/status"} {
		if got := ownerCall(env, "GET", p, nil, a).Code; got != 401 {
			t.Fatal("pending attempt authorized", p)
		}
	}
	if got := ownerCall(env, "GET", "auth/status", nil, a); !strings.Contains(got.Body.String(), `"authenticated":false`) {
		t.Fatal("pending attempt changed status")
	}
	if got := ownerCall(env, "POST", "auth/mfa", payload{"code": "ONCE"}).Code; got != 410 {
		t.Fatal("missing binding", got)
	}
	// A Redis failure while consuming the attempt must roll back recovery-code use.
	opts, err := redis.ParseURL(env.db.Env.RedisURL)
	if err != nil {
		t.Fatal(err)
	}
	admin := redis.NewClient(opts)
	defer admin.Close()
	const restrictedUser = "profilefixture"
	if err = admin.Do(env.ctx, "ACL", "SETUSER", restrictedUser, "on", ">test-only", "~*", "+@all", "-del").Err(); err != nil {
		t.Fatal(err)
	}
	defer admin.Do(env.ctx, "ACL", "DELUSER", restrictedUser)
	opts.Username, opts.Password = restrictedUser, "test-only"
	restricted := redis.NewClient(opts)
	defer restricted.Close()
	denied := api.NewServer(env.db.Env, env.db, restricted, env.store).Handler()
	rec := ownerRequest(denied, "POST", "auth/mfa", payload{"code": "ONCE"}, a)
	if rec.Code != 500 || len(rec.Result().Cookies()) != 0 {
		t.Fatal("failed attempt consumption issued session", rec.Code)
	}
	results := make(chan int, 2)
	for _, c := range []*http.Cookie{a, b} {
		go func() { results <- ownerCall(env, "POST", "auth/mfa", payload{"code": "ONCE"}, c).Code }()
	}
	x, y := <-results, <-results
	if x != 200 && y != 200 || x != 400 && y != 400 {
		t.Fatalf("concurrent recovery use: %d/%d", x, y)
	}
	w := ownerCall(env, "POST", "auth/passkey/start", nil)
	if w.Code != 200 || strings.Contains(w.Body.String(), "owner@example.org") || strings.Contains(w.Body.String(), "allowCredentials") {
		t.Fatal("passkey start disclosed account information", w.Code)
	}
	attempt := w.Result().Cookies()[0]
	if got := ownerCall(env, "POST", "auth/cancel", nil, attempt).Code; got != 200 {
		t.Fatal(got)
	}
	if got := ownerCall(env, "POST", "auth/passkey/finish", payload{}, attempt).Code; got != 410 {
		t.Fatal("cancelled attempt accepted", got)
	}
	stale := login()
	if _, e := env.db.Pool.Exec(env.ctx, `UPDATE "Owner" SET "securityRevision"="securityRevision"+1 WHERE id=1`); e != nil {
		t.Fatal(e)
	}
	if got := ownerCall(env, "POST", "auth/mfa", payload{"code": "ONCE"}, stale).Code; got != 410 {
		t.Fatal("superseded challenge", got)
	}
	for _, p := range []string{"auth/passkey/start", "auth/passkey/finish", "auth/mfa", "auth/cancel", "profile/start", "profile/finish"} {
		req := httptest.NewRequest("POST", "/api/v1/"+p, nil)
		req.Header.Set("Origin", "https://other.invalid")
		req.Header.Set("X-Gallery-Request", "1")
		rec := httptest.NewRecorder()
		env.handler.ServeHTTP(rec, req)
		if rec.Code != 403 {
			t.Fatal("origin guard missing", p)
		}
	}
}

// ownerRow is the security state a failed change or login must leave untouched.
type ownerRow struct {
	SealedSeed     string
	TOTPStep       int64
	RecoveryHashes []string
	Rev            int64
}

func readOwnerRow(t *testing.T, env integrationEnv) ownerRow {
	t.Helper()
	var row ownerRow
	if err := env.db.Pool.QueryRow(env.ctx, `SELECT "totpSecret","totpStep","recoveryHashes","securityRevision" FROM "Owner" WHERE id=1`).Scan(&row.SealedSeed, &row.TOTPStep, &row.RecoveryHashes, &row.Rev); err != nil {
		t.Fatal(err)
	}
	return row
}

func sessionKeys(t *testing.T, env integrationEnv) []string {
	t.Helper()
	opts, err := redis.ParseURL(env.db.Env.RedisURL)
	if err != nil {
		t.Fatal(err)
	}
	client := redis.NewClient(opts)
	defer client.Close()
	keys, err := client.Keys(env.ctx, "session:*").Result()
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(keys)
	return keys
}

// injectOwnerCommitFailure makes any transaction that updates "Owner" fail at COMMIT.
// The returned function removes the trigger; it is also registered with t.Cleanup.
func injectOwnerCommitFailure(t *testing.T, env integrationEnv) func() {
	t.Helper()
	if _, err := env.db.Pool.Exec(env.ctx, `CREATE OR REPLACE FUNCTION owner_test_commit_failure() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'injected owner commit failure'; END $$`); err != nil {
		t.Fatal(err)
	}
	if _, err := env.db.Pool.Exec(env.ctx, `CREATE CONSTRAINT TRIGGER owner_test_commit_failure AFTER UPDATE ON "Owner" DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION owner_test_commit_failure()`); err != nil {
		t.Fatal(err)
	}
	remove := func() {
		if _, err := env.db.Pool.Exec(context.Background(), `DROP TRIGGER IF EXISTS owner_test_commit_failure ON "Owner"; DROP FUNCTION IF EXISTS owner_test_commit_failure()`); err != nil {
			t.Error(err)
		}
	}
	t.Cleanup(remove)
	return remove
}

func passwordLogin(t *testing.T, env integrationEnv) *http.Cookie {
	t.Helper()
	w := ownerCall(env, "POST", "auth/login", map[string]any{"email": "owner@example.org", "password": "long-fixture-password"})
	if w.Code != 200 || !strings.Contains(w.Body.String(), "mfaRequired") {
		t.Fatal("missing challenge", w.Code)
	}
	return w.Result().Cookies()[0]
}

func sessionCookie(w *httptest.ResponseRecorder) *http.Cookie {
	for _, c := range w.Result().Cookies() {
		if c.Name == "session" {
			return c
		}
	}
	return nil
}

func TestOwnerSecurityPostgresFailure(t *testing.T) {
	env := integrationServer(t)
	type payload = map[string]any

	t.Run("login commit failure keeps recovery code", func(t *testing.T) {
		seedOwnerSecurity(t, env, "FIRSTCODE")
		attempt := passwordLogin(t, env)
		before, keysBefore := readOwnerRow(t, env), sessionKeys(t, env)
		remove := injectOwnerCommitFailure(t, env)
		w := ownerCall(env, "POST", "auth/mfa", payload{"code": "FIRSTCODE"}, attempt)
		if w.Code != 500 || sessionCookie(w) != nil {
			t.Fatal("commit failure issued session", w.Code)
		}
		if after := readOwnerRow(t, env); !reflect.DeepEqual(before, after) {
			t.Fatal("commit failure changed owner", before, after)
		}
		if !reflect.DeepEqual(keysBefore, sessionKeys(t, env)) {
			t.Fatal("commit failure left a session key")
		}
		remove()
		retry := passwordLogin(t, env)
		if w := ownerCall(env, "POST", "auth/mfa", payload{"code": "FIRSTCODE"}, retry); w.Code != 200 || sessionCookie(w) == nil {
			t.Fatal("recovery code not usable after rollback", w.Code)
		}
	})

	t.Run("profile finish commit failure keeps owner row", func(t *testing.T) {
		seedOwnerSecurity(t, env, "FIRSTCODE", "SECONDCODE")
		login := passwordLogin(t, env)
		w := ownerCall(env, "POST", "auth/mfa", payload{"code": "FIRSTCODE"}, login)
		session := sessionCookie(w)
		if w.Code != 200 || session == nil {
			t.Fatal("login failed", w.Code)
		}
		start := ownerCall(env, "POST", "profile/start", payload{"action": "regenerate", "password": "long-fixture-password", "code": "SECONDCODE"}, session)
		var preview struct{ Confirmation string }
		if start.Code != 200 || json.Unmarshal(start.Body.Bytes(), &preview) != nil || preview.Confirmation == "" {
			t.Fatal("prepare regenerate failed", start.Code)
		}
		flow := start.Result().Cookies()[0]
		before, keysBefore := readOwnerRow(t, env), sessionKeys(t, env)
		remove := injectOwnerCommitFailure(t, env)
		defer remove()
		finish := ownerCall(env, "POST", "profile/finish", payload{"confirmation": preview.Confirmation, "saved": true}, session, flow)
		if finish.Code != 500 {
			t.Fatal("commit failure answered", finish.Code)
		}
		if after := readOwnerRow(t, env); !reflect.DeepEqual(before, after) {
			t.Fatal("failed finish changed owner", before, after)
		}
		if !reflect.DeepEqual(keysBefore, sessionKeys(t, env)) {
			t.Fatal("failed finish changed sessions")
		}
	})
}

func TestOwnerSecurityFactorChangeRace(t *testing.T) {
	env := integrationServer(t)
	type payload = map[string]any
	seedOwnerSecurity(t, env, "FIRSTCODE", "SECONDCODE", "THIRDCODE")
	w := ownerCall(env, "POST", "auth/mfa", payload{"code": "FIRSTCODE"}, passwordLogin(t, env))
	old := sessionCookie(w)
	if w.Code != 200 || old == nil {
		t.Fatal("sign in failed", w.Code)
	}
	start := ownerCall(env, "POST", "profile/start", payload{"action": "regenerate", "password": "long-fixture-password", "code": "SECONDCODE"}, old)
	var preview struct{ Confirmation string }
	if start.Code != 200 || json.Unmarshal(start.Body.Bytes(), &preview) != nil || preview.Confirmation == "" {
		t.Fatal("prepare regenerate failed", start.Code)
	}
	flow := start.Result().Cookies()[0]
	attempt := passwordLogin(t, env)

	var wg sync.WaitGroup
	gate := make(chan struct{})
	var finish, mfa *httptest.ResponseRecorder
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-gate
		finish = ownerCall(env, "POST", "profile/finish", payload{"confirmation": preview.Confirmation, "saved": true}, old, flow)
	}()
	go func() {
		defer wg.Done()
		<-gate
		mfa = ownerCall(env, "POST", "auth/mfa", payload{"code": "THIRDCODE"}, attempt)
	}()
	close(gate)
	wg.Wait()

	if finish.Code != 200 {
		t.Fatal("factor change failed", finish.Code, finish.Body.String())
	}
	if got := ownerCall(env, "GET", "profile", nil, old).Code; got != 401 {
		t.Fatal("old session usable after factor change", got)
	}
	switch mfa.Code {
	case 200:
		issued := sessionCookie(mfa)
		if issued == nil {
			t.Fatal("login reported success without session")
		}
		if got := ownerCall(env, "GET", "profile", nil, issued).Code; got != 401 {
			t.Fatal("login raced past factor change", got)
		}
	case 410:
		if sessionCookie(mfa) != nil {
			t.Fatal("expired challenge set session cookie")
		}
	default:
		t.Fatal("unexpected mfa outcome", mfa.Code)
	}
}

func TestOwnerSecurityAggregateGuessLimit(t *testing.T) {
	env := integrationServer(t)
	type payload = map[string]any
	seedOwnerSecurity(t, env, "FIRSTCODE")
	// Two attempts of four wrong codes each stay under the per-attempt limit of 5 but use 8 aggregate slots.
	for attempt := 0; attempt < 2; attempt++ {
		cookie := passwordLogin(t, env)
		for _, wrong := range []string{"000000", "111111", "222222", "333333"} {
			if got := ownerCall(env, "POST", "auth/mfa", payload{"code": wrong}, cookie).Code; got != 400 {
				t.Fatal("wrong code not rejected inside limit", attempt, wrong, got)
			}
		}
	}
	third := passwordLogin(t, env)
	w := ownerCall(env, "POST", "auth/mfa", payload{"code": "FIRSTCODE"}, third)
	if w.Code != 429 || sessionCookie(w) != nil {
		t.Fatal("aggregate limit did not hold across restarted attempts", w.Code)
	}
	if got := ownerCall(env, "POST", "auth/login", payload{"email": "owner@example.org", "password": "long-fixture-password"}); got.Code == 429 {
		t.Fatal("login limiter tripped before the aggregate guess limit")
	}
}

func TestOwnerSecurityProfileReadDoesNotLock(t *testing.T) {
	env := integrationServer(t)
	// Another transaction holds the owner row lock; a profile read is a snapshot and must not wait.
	holder, err := env.db.Pool.Begin(env.ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Rollback(env.ctx)
	var one int
	if err := holder.QueryRow(env.ctx, `SELECT 1 FROM "Owner" WHERE id=1 FOR UPDATE`).Scan(&one); err != nil {
		t.Fatal(err)
	}
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- ownerCall(env, "GET", "profile", nil, &http.Cookie{Name: "session", Value: env.session})
	}()
	select {
	case w := <-done:
		if w.Code != 200 {
			t.Fatal("profile read failed under owner lock", w.Code, w.Body.String())
		}
	case <-time.After(2 * time.Second):
		_ = holder.Rollback(env.ctx)
		<-done
		t.Fatal("profile read blocked by owner row lock")
	}
}

func TestOwnerSecurityExhaustedAttempt(t *testing.T) {
	env := integrationServer(t)
	type payload = map[string]any
	seedOwnerSecurity(t, env, "FIRSTCODE")
	attempt := passwordLogin(t, env)
	// Seven calls on one attempt stay under the aggregate limit of 8; the sixth exhausts the attempt.
	for i := 1; i <= 5; i++ {
		if got := ownerCall(env, "POST", "auth/mfa", payload{"code": "000000"}, attempt).Code; got != 400 {
			t.Fatal("wrong code inside attempt limit", i, got)
		}
	}
	w := ownerCall(env, "POST", "auth/mfa", payload{"code": "000000"}, attempt)
	if w.Code != 429 || !strings.Contains(w.Body.String(), "Too many incorrect attempts. Start sign-in again.") {
		t.Fatal("exhausted attempt not distinguishable", w.Code, w.Body.String())
	}
	if got := ownerCall(env, "POST", "auth/mfa", payload{"code": "FIRSTCODE"}, attempt).Code; got != 410 {
		t.Fatal("exhausted attempt still usable", got)
	}
}

func TestOwnerSecurityStrayFinishKeepsPending(t *testing.T) {
	type payload = map[string]any
	t.Run("wrong purpose leaves a pending mfa attempt", func(t *testing.T) {
		env := integrationServer(t)
		seedOwnerSecurity(t, env, "FIRSTCODE")
		attempt := passwordLogin(t, env)
		if got := ownerCall(env, "POST", "auth/passkey/finish", payload{}, attempt).Code; got != 410 {
			t.Fatal("stray passkey finish accepted", got)
		}
		if w := ownerCall(env, "POST", "auth/mfa", payload{"code": "FIRSTCODE"}, attempt); w.Code != 200 || sessionCookie(w) == nil {
			t.Fatal("stray request consumed the pending mfa attempt", w.Code)
		}
	})
	t.Run("wrong purpose leaves a prepared profile change", func(t *testing.T) {
		env := integrationServer(t)
		seedOwnerSecurity(t, env, "FIRSTCODE", "SECONDCODE")
		w := ownerCall(env, "POST", "auth/mfa", payload{"code": "FIRSTCODE"}, passwordLogin(t, env))
		session := sessionCookie(w)
		if w.Code != 200 || session == nil {
			t.Fatal("sign in failed", w.Code)
		}
		start := ownerCall(env, "POST", "profile/start", payload{"action": "regenerate", "password": "long-fixture-password", "code": "SECONDCODE"}, session)
		var preview struct{ Confirmation string }
		if start.Code != 200 || json.Unmarshal(start.Body.Bytes(), &preview) != nil || preview.Confirmation == "" {
			t.Fatal("prepare regenerate failed", start.Code)
		}
		flow := start.Result().Cookies()[0]
		if got := ownerCall(env, "POST", "profile/prove", payload{}, session, flow).Code; got != 410 {
			t.Fatal("stray prove accepted", got)
		}
		if got := ownerCall(env, "POST", "profile/finish", payload{"confirmation": preview.Confirmation, "saved": true}, session, flow).Code; got != 200 {
			t.Fatal("stray prove consumed the prepared change", got)
		}
	})
}

func TestOwnerSecuritySessionStoreFailureKeepsRecoveryCode(t *testing.T) {
	env := integrationServer(t)
	type payload = map[string]any
	seedOwnerSecurity(t, env, "FIRSTCODE")
	attempt := passwordLogin(t, env)
	opts, err := redis.ParseURL(env.db.Env.RedisURL)
	if err != nil {
		t.Fatal(err)
	}
	admin := redis.NewClient(opts)
	defer admin.Close()
	const restrictedUser = "sessionfixture"
	if err = admin.Do(env.ctx, "ACL", "SETUSER", restrictedUser, "on", ">test-only", "~*", "+@all", "-set").Err(); err != nil {
		t.Fatal(err)
	}
	defer admin.Do(env.ctx, "ACL", "DELUSER", restrictedUser)
	opts.Username, opts.Password = restrictedUser, "test-only"
	restricted := redis.NewClient(opts)
	defer restricted.Close()
	denied := api.NewServer(env.db.Env, env.db, restricted, env.store).Handler()
	w := ownerRequest(denied, "POST", "auth/mfa", payload{"code": "FIRSTCODE"}, attempt)
	if w.Code != 500 || sessionCookie(w) != nil {
		t.Fatal("session store failure granted access", w.Code)
	}
	// The recovery code was not consumed, so a restarted login with the same code succeeds.
	retry := passwordLogin(t, env)
	if w := ownerCall(env, "POST", "auth/mfa", payload{"code": "FIRSTCODE"}, retry); w.Code != 200 || sessionCookie(w) == nil {
		t.Fatal("recovery code burned by session store failure", w.Code)
	}
}

func TestOwnerSecurityNoOwnerAnonymous(t *testing.T) {
	env := integrationServer(t)
	// GoogleConnection.ownerId is ON DELETE RESTRICT, so it is cleared first; OwnerPasskey cascades.
	if _, err := env.db.Pool.Exec(env.ctx, `DELETE FROM "GoogleConnection"; DELETE FROM "Owner"`); err != nil {
		t.Fatal(err)
	}
	// Send the session cookie: without one, the middleware answers 401 before reading the owner row.
	session := &http.Cookie{Name: "session", Value: env.session}
	status := ownerCall(env, "GET", "auth/status", nil, session)
	if status.Code != 200 || !strings.Contains(status.Body.String(), `"authenticated":false`) || !strings.Contains(status.Body.String(), `"setup":true`) {
		t.Fatal("missing owner not anonymous", status.Code, status.Body.String())
	}
	if got := ownerCall(env, "GET", "profile", nil, session).Code; got != 401 {
		t.Fatal("missing owner reached protected route", got)
	}
}

func TestOwnerSecurityStorageErrorFailsClosed(t *testing.T) {
	env := integrationServer(t)
	opts, err := redis.ParseURL(env.db.Env.RedisURL)
	if err != nil {
		t.Fatal(err)
	}
	admin := redis.NewClient(opts)
	defer admin.Close()
	const restrictedUser = "statusfixture"
	if err = admin.Do(env.ctx, "ACL", "SETUSER", restrictedUser, "on", ">test-only", "~*", "+@all", "-get").Err(); err != nil {
		t.Fatal(err)
	}
	defer admin.Do(env.ctx, "ACL", "DELUSER", restrictedUser)
	opts.Username, opts.Password = restrictedUser, "test-only"
	restricted := redis.NewClient(opts)
	defer restricted.Close()
	denied := api.NewServer(env.db.Env, env.db, restricted, env.store).Handler()
	w := ownerRequest(denied, "GET", "auth/status", nil, &http.Cookie{Name: "session", Value: env.session})
	if w.Code != 500 {
		t.Fatal("session storage error did not fail closed", w.Code, w.Body.String())
	}
}
