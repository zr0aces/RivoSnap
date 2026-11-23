package api_test

import (
	"bytes"
	"encoding/json"
	"github.com/redis/go-redis/v9"
	"net/http"
	"net/http/httptest"
	"rivosnap/internal/api"
	"strings"
	"testing"
)

func TestOwnerSecurityBoundaries(t *testing.T) {
	env := integrationServer(t)
	call := func(method, path string, data any, cookies ...*http.Cookie) *httptest.ResponseRecorder {
		b, _ := json.Marshal(data)
		r := httptest.NewRequest(method, "/api/v1/"+path, bytes.NewReader(b))
		r.Header.Set("Origin", "http://localhost")
		r.Header.Set("X-Gallery-Request", "1")
		for _, c := range cookies {
			r.AddCookie(c)
		}
		w := httptest.NewRecorder()
		env.handler.ServeHTTP(w, r)
		return w
	}
	owner := &http.Cookie{Name: "session", Value: env.session}
	type payload = map[string]any
	start := call("POST", "profile/start", payload{"action": "enable", "password": "long-fixture-password"}, owner)
	var preview struct {
		Confirmation, Secret string
		Codes                []string
	}
	if start.Code != 200 || json.Unmarshal(start.Body.Bytes(), &preview) != nil || len(preview.Codes) != 8 || preview.Secret == "" {
		t.Fatal("missing enrollment preview", start.Code)
	}
	flow := start.Result().Cookies()[0]
	if got := call("POST", "profile/finish", payload{"confirmation": preview.Confirmation, "saved": false}, owner, flow).Code; got != 400 {
		t.Fatal("unconfirmed enrollment", got)
	}
	if got := call("GET", "profile", nil, owner); got.Code != 200 || !strings.Contains(got.Body.String(), `"totp":false`) {
		t.Fatal("failed enrollment changed owner")
	}
	// Seed the second step directly for concurrency tests; the browser test covers enrollment.
	if _, e := env.db.Pool.Exec(env.ctx, `UPDATE "Owner" SET "securityRevision"="securityRevision"+1,"totpSecret"='fixture',"recoveryHashes"=ARRAY[encode(sha256('once'::bytea),'hex')]`); e != nil {
		t.Fatal(e)
	}
	if got := call("GET", "profile", nil, owner).Code; got != 401 {
		t.Fatal("stale session accepted", got)
	}
	login := func() *http.Cookie {
		w := call("POST", "auth/login", payload{"email": "owner@example.org", "password": "long-fixture-password"})
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
		if got := call("GET", p, nil, a).Code; got != 401 {
			t.Fatal("pending attempt authorized", p)
		}
	}
	if got := call("GET", "auth/status", nil, a); !strings.Contains(got.Body.String(), `"authenticated":false`) {
		t.Fatal("pending attempt changed status")
	}
	if got := call("POST", "auth/mfa", payload{"code": "once"}).Code; got != 410 {
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
	if err = admin.Do(env.ctx, "ACL", "SETUSER", restrictedUser, "on", ">test-only", "~*", "+@all", "-getdel").Err(); err != nil {
		t.Fatal(err)
	}
	defer admin.Do(env.ctx, "ACL", "DELUSER", restrictedUser)
	opts.Username, opts.Password = restrictedUser, "test-only"
	restricted := redis.NewClient(opts)
	defer restricted.Close()
	denied := api.NewServer(env.db.Env, env.db, restricted, env.store).Handler()
	req := httptest.NewRequest("POST", "/api/v1/auth/mfa", strings.NewReader(`{"code":"once"}`))
	req.Header.Set("Origin", "http://localhost")
	req.Header.Set("X-Gallery-Request", "1")
	req.AddCookie(a)
	rec := httptest.NewRecorder()
	denied.ServeHTTP(rec, req)
	if rec.Code != 500 || len(rec.Result().Cookies()) != 0 {
		t.Fatal("failed attempt consumption issued session", rec.Code)
	}
	results := make(chan int, 2)
	for _, c := range []*http.Cookie{a, b} {
		go func() { results <- call("POST", "auth/mfa", payload{"code": "once"}, c).Code }()
	}
	x, y := <-results, <-results
	if x != 200 && y != 200 || x != 400 && y != 400 {
		t.Fatalf("concurrent recovery use: %d/%d", x, y)
	}
	w := call("POST", "auth/passkey/start", nil)
	if w.Code != 200 || strings.Contains(w.Body.String(), "owner@example.org") || strings.Contains(w.Body.String(), "allowCredentials") {
		t.Fatal("passkey start disclosed account information", w.Code)
	}
	attempt := w.Result().Cookies()[0]
	if got := call("POST", "auth/cancel", nil, attempt).Code; got != 200 {
		t.Fatal(got)
	}
	if got := call("POST", "auth/passkey/finish", payload{}, attempt).Code; got != 410 {
		t.Fatal("cancelled attempt accepted", got)
	}
	stale := login()
	if _, e := env.db.Pool.Exec(env.ctx, `UPDATE "Owner" SET "securityRevision"="securityRevision"+1 WHERE id=1`); e != nil {
		t.Fatal(e)
	}
	if got := call("POST", "auth/mfa", payload{"code": "once"}, stale).Code; got != 410 {
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
