package api

import (
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"rivosnap/internal/config"
)

// TestOpenAPISecurityMatchesMiddleware is the route-table check (AUD3-027): every documented
// operation not marked public must be rejected by the session gate, and the public set must be
// exactly the operations the middleware lets through without a session.
func TestOpenAPISecurityMatchesMiddleware(t *testing.T) {
	env := config.Env{AppURL: "http://localhost"}
	handler := NewServer(env, nil, nil, nil).Handler()
	spec := openAPISpec()

	wantTop := []any{map[string]any{"session": []string{}}}
	if got := spec["security"]; !reflect.DeepEqual(got, wantTop) {
		t.Fatalf("top-level security = %v, want %v", got, wantTop)
	}
	if info := spec["info"].(map[string]any); info["version"] != "1.0.0" {
		t.Fatalf("spec version = %v, want 1.0.0 (the API contract version, not a release)", info["version"])
	}

	wantPublic := map[string]bool{
		"post /api/webhooks/line":          true,
		"get /api/v1/auth/status":          true,
		"post /api/v1/auth/login":          true,
		"post /api/v1/auth/setup":          true,
		"get /ready":                       true,
		"post /api/v1/auth/passkey/start":  true,
		"post /api/v1/auth/passkey/finish": true,
		"post /api/v1/auth/mfa":            true,
		"post /api/v1/auth/cancel":         true,
	}
	gotPublic := map[string]bool{}
	for path, item := range spec["paths"].(map[string]any) {
		for method, raw := range item.(map[string]any) {
			op := raw.(map[string]any)
			key := method + " " + path
			if sec, declared := op["security"]; declared && len(sec.([]any)) == 0 {
				gotPublic[key] = true
				continue
			}
			concrete := strings.ReplaceAll(path, "{id}", "00000000-0000-4000-8000-000000000001")
			req := httptest.NewRequest(strings.ToUpper(method), concrete, nil)
			req.Header.Set("Origin", env.AppURL)
			req.Header.Set("X-Gallery-Request", "1")
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != 401 {
				t.Errorf("%s %s without a session: got %d, want 401", strings.ToUpper(method), path, rec.Code)
			}
		}
	}
	if !reflect.DeepEqual(gotPublic, wantPublic) {
		t.Fatalf("public operations differ:\n got  %v\n want %v", gotPublic, wantPublic)
	}

	// Each public operation must get past the session gate. A nil database panics inside the
	// handler, which only happens after the gate, so a panic counts as reaching the handler.
	for key := range wantPublic {
		method, path, _ := strings.Cut(key, " ")
		func() {
			defer func() { _ = recover() }()
			req := httptest.NewRequest(strings.ToUpper(method), path, nil)
			req.Header.Set("Origin", env.AppURL)
			req.Header.Set("X-Gallery-Request", "1")
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code == 401 {
				t.Errorf("public %s %s answered 401", strings.ToUpper(method), path)
			}
		}()
	}
}
