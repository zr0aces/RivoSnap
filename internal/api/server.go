package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/redis/go-redis/v9"
	"golang.org/x/sync/singleflight"

	"rivosnap/internal/config"
	"rivosnap/internal/crypto"
	"rivosnap/internal/db"
	"rivosnap/internal/google"
	"rivosnap/internal/line"
	"rivosnap/internal/media"
	"rivosnap/internal/model"
	"rivosnap/internal/web"
)

var (
	uuidParamRe = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
	rangeRe     = regexp.MustCompile(`^bytes=(\d*)-(\d*)$`)
	digitsOnly  = regexp.MustCompile(`^\d+$`)
	isoDateRe   = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)
)

type httpError struct {
	status  int
	message string
}

func (e *httpError) Error() string { return e.message }

func errBadRequest(msg string) error {
	return &httpError{status: 400, message: msg}
}

type rateEntry struct {
	count   int
	resetAt time.Time
}

type Server struct {
	Env   config.Env
	DB    *db.DB
	Redis *redis.Client
	Store *media.Store

	nextRateSweep time.Time
	rateMu        sync.Mutex
	rateMap       map[string]rateEntry
	authRates     map[string]rateEntry

	// thumbSem caps concurrent on-demand ffmpeg runs; thumbFlight shares one run per id+size.
	thumbSem    chan struct{}
	thumbFlight singleflight.Group
}

const (
	maxThumbJobs     = 2
	thumbWaitTimeout = 20 * time.Second
)

var errThumbBusy = errors.New("thumbnail generation busy")

type thumbnailResult struct {
	path, mime string
	data       []byte
}

// thumbnail generates a missing thumbnail, waiting up to thumbWaitTimeout for one of
// maxThumbJobs slots. Waiters hold no ffmpeg process. The flight runs on a context
// detached from the caller, so one departing client cannot fail the callers sharing it;
// an abandoned flight still finishes and fills the cache.
func (s *Server) thumbnail(ctx context.Context, target string, generate func(context.Context) (thumbnailResult, error)) (thumbnailResult, error) {
	ch := s.thumbFlight.DoChan(target, func() (any, error) {
		fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), thumbWaitTimeout+time.Minute)
		defer cancel()
		select {
		case s.thumbSem <- struct{}{}:
			defer func() { <-s.thumbSem }()
		case <-fctx.Done():
			return nil, fctx.Err()
		case <-time.After(thumbWaitTimeout):
			return nil, errThumbBusy
		}
		if _, err := os.Stat(target); err == nil {
			_ = media.Touch(target)
			return thumbnailResult{path: target}, nil
		}
		return generate(fctx)
	})
	select {
	case <-ctx.Done():
		return thumbnailResult{}, ctx.Err()
	case r := <-ch:
		if r.Err != nil {
			return thumbnailResult{}, r.Err
		}
		return r.Val.(thumbnailResult), nil
	}
}

func NewServer(env config.Env, database *db.DB, rdb *redis.Client, store *media.Store) *Server {
	return &Server{
		Env:       env,
		DB:        database,
		Redis:     rdb,
		Store:     store,
		rateMap:   make(map[string]rateEntry),
		authRates: make(map[string]rateEntry),
		thumbSem:  make(chan struct{}, maxThumbJobs),
	}
}

// peerHost returns the socket peer address without the port.
func peerHost(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return host
}

// clientHost returns the real client address. X-Real-IP is honoured only from a
// loopback or private peer (the Nginx proxy), which overwrites it on every request.
func clientHost(r *http.Request) string {
	peer := peerHost(r.RemoteAddr)
	if pa, err := netip.ParseAddr(peer); err == nil && (pa.IsLoopback() || pa.IsPrivate()) {
		if ip, err := netip.ParseAddr(r.Header.Get("X-Real-IP")); err == nil {
			return ip.String()
		}
	}
	return peer
}

// rateKey picks the general rate-limit bucket for a request.
func rateKey(r *http.Request, sid string) string {
	if r.URL.Path == "/api/webhooks/line" {
		host := clientHost(r)
		// LINE never connects from a private address: a private/loopback host means the
		// external proxy did not forward the client, so fall back to per-signature buckets.
		if ip, err := netip.ParseAddr(host); err == nil && (ip.IsLoopback() || ip.IsPrivate()) {
			if sig := r.Header.Get("X-Line-Signature"); sig != "" {
				return "webhook:sig:" + sig
			}
		}
		return "webhook:" + host
	}
	if sid != "" {
		return "session:" + sid
	}
	return "ip:" + clientHost(r)
}

func (s *Server) checkRate(bucket map[string]rateEntry, key string, max int, window time.Duration) bool {
	now := time.Now()
	s.rateMu.Lock()
	defer s.rateMu.Unlock()
	if !now.Before(s.nextRateSweep) {
		for _, entries := range []map[string]rateEntry{s.rateMap, s.authRates} {
			for k, entry := range entries {
				if !now.Before(entry.resetAt) {
					delete(entries, k)
				}
			}
		}
		s.nextRateSweep = now.Add(time.Minute)
	}
	e, ok := bucket[key]
	if !ok || now.After(e.resetAt) {
		bucket[key] = rateEntry{count: 1, resetAt: now.Add(window)}
		return true
	}
	if e.count >= max {
		return false
	}
	e.count++
	bucket[key] = e
	return true
}

func (s *Server) sid(r *http.Request) string {
	c, err := r.Cookie("session")
	if err != nil || c.Value == "" {
		return ""
	}
	val, ok := crypto.UnsignCookie(c.Value, s.Env.SessionSecret)
	if !ok {
		return ""
	}
	return val
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeErr(w http.ResponseWriter, err error) {
	var he *httpError
	if errors.As(err, &he) {
		msg := he.message
		if msg == "" {
			msg = "Invalid request. Check the submitted values."
		}
		writeJSON(w, he.status, map[string]string{"error": msg})
		return
	}
	var f *model.Fault
	if errors.As(err, &f) {
		status := 409
		if f.Transient {
			status = 503
		}
		writeJSON(w, status, map[string]string{"error": f.Message})
		return
	}
	if errors.Is(err, pgx.ErrNoRows) {
		writeJSON(w, 404, map[string]string{"error": "Not found."})
		return
	}
	writeJSON(w, 500, map[string]string{"error": "Request failed."})
}

func parseUUID(raw string) (string, error) {
	if !uuidParamRe.MatchString(raw) {
		return "", errBadRequest("Invalid request. Check the submitted values.")
	}
	return raw, nil
}

func readBody(r *http.Request) ([]byte, error) {
	if r.Body == nil {
		return nil, nil
	}
	defer r.Body.Close()
	b, err := io.ReadAll(io.LimitReader(r.Body, 1024*1024+1))
	if err != nil || len(b) > 1024*1024 {
		return nil, errBadRequest("Invalid request. Check the submitted values.")
	}
	return b, nil
}

func fmtTime(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05.000Z")
}

func fmtTimePtr(t *time.Time) any {
	if t == nil {
		return nil
	}
	return fmtTime(*t)
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /ready", func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		var one int
		if err := s.DB.Pool.QueryRow(ctx, `SELECT 1`).Scan(&one); err != nil {
			writeErr(w, err)
			return
		}
		if err := s.Redis.Ping(ctx).Err(); err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, map[string]bool{"ok": true})
	})

	mux.HandleFunc("GET /api/openapi.json", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, openAPISpec())
	})

	mux.HandleFunc("POST /api/webhooks/line", func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		creds, err := s.DB.LineCredentials(ctx)
		if err != nil {
			writeErr(w, err)
			return
		}
		body, err := readBody(r)
		if err != nil {
			writeErr(w, err)
			return
		}
		if !line.Signed(body, r.Header.Get("X-Line-Signature"), creds.Secret) {
			writeJSON(w, 403, map[string]string{"error": "Invalid signature."})
			return
		}
		var destProbe struct {
			Destination string `json:"destination"`
		}
		if err := json.Unmarshal(body, &destProbe); err != nil {
			writeErr(w, errBadRequest("Invalid request. Check the submitted values."))
			return
		}
		if creds.Destination != "" && creds.Destination != destProbe.Destination {
			writeJSON(w, 403, map[string]string{"error": "Unexpected channel."})
			return
		}
		if _, err := s.Ingest(ctx, body); err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, map[string]any{})
	})

	mux.HandleFunc("GET /api/v1/auth/status", func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		var count int
		if err := s.DB.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM "Owner"`).Scan(&count); err != nil {
			writeErr(w, err)
			return
		}
		_, authed, err := s.sessionRevision(r, s.sid(r))
		if err != nil {
			writeErr(w, err)
			return
		}
		writeJSON(w, 200, map[string]bool{
			"setup":         count == 0,
			"authenticated": authed,
		})
	})

	mux.HandleFunc("POST /api/v1/auth/setup", func(w http.ResponseWriter, r *http.Request) {
		s.handleAuth(w, r, "setup")
	})
	s.securityRoutes(mux)
	mux.HandleFunc("POST /api/v1/auth/login", func(w http.ResponseWriter, r *http.Request) {
		s.handleAuth(w, r, "login")
	})
	mux.HandleFunc("POST /api/v1/auth/logout", func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		sid := s.sid(r)
		if sid != "" {
			if err := s.Redis.Del(ctx, "session:"+sid).Err(); err != nil {
				writeErr(w, err)
				return
			}
		}
		http.SetCookie(w, &http.Cookie{
			Name:   "session",
			Value:  "",
			Path:   "/",
			MaxAge: -1,
		})
		writeJSON(w, 200, map[string]bool{"ok": true})
	})

	mux.HandleFunc("GET /api/v1/health", s.handleHealth)
	mux.HandleFunc("GET /api/v1/settings", s.handleGetSettings)
	mux.HandleFunc("PATCH /api/v1/settings", s.handlePatchSettings)
	mux.HandleFunc("POST /api/v1/settings/line", s.handlePostSettingsLine)
	mux.HandleFunc("GET /api/v1/google/status", s.handleGoogleStatus)
	mux.HandleFunc("GET /api/v1/google/connect", s.handleGoogleConnect)
	mux.HandleFunc("GET /api/v1/google/callback", s.handleGoogleCallback)
	mux.HandleFunc("POST /api/v1/google/validate", s.handleGoogleValidate)
	mux.HandleFunc("POST /api/v1/google/disconnect", s.handleGoogleDisconnect)
	mux.HandleFunc("GET /api/v1/groups", s.handleGetGroups)
	mux.HandleFunc("GET /api/v1/groups/{id}", s.handleGetGroup)
	mux.HandleFunc("PATCH /api/v1/groups/{id}", s.handlePatchGroup)
	mux.HandleFunc("POST /api/v1/groups/{id}/refresh", s.handleRefreshGroup)
	mux.HandleFunc("POST /api/v1/groups/{id}/resolve-album", s.handleResolveAlbum)
	mux.HandleFunc("GET /api/v1/images", func(w http.ResponseWriter, r *http.Request) {
		s.handleListImages(w, r, false)
	})
	mux.HandleFunc("GET /api/v1/jobs", func(w http.ResponseWriter, r *http.Request) {
		s.handleListImages(w, r, true)
	})
	mux.HandleFunc("GET /api/v1/images/{id}", s.handleGetImage)
	mux.HandleFunc("GET /api/v1/images/{id}/thumbnail", func(w http.ResponseWriter, r *http.Request) {
		s.handleMediaStream(w, r, true)
	})
	mux.HandleFunc("GET /api/v1/images/{id}/content", func(w http.ResponseWriter, r *http.Request) {
		s.handleMediaStream(w, r, false)
	})
	mux.HandleFunc("POST /api/v1/jobs/{id}/discard", s.handleDiscardJob)
	mux.HandleFunc("POST /api/v1/jobs/{id}/retry", s.handleRetryJob)
	mux.HandleFunc("GET /api/v1/dashboard/stats", s.handleDashboardStats)
	mux.Handle("/api/", http.NotFoundHandler())
	mux.Handle("/", newSPAHandler(web.Dist()))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "SAMEORIGIN")

		urlPath := r.URL.Path
		sid := s.sid(r)
		if !s.checkRate(s.rateMap, rateKey(r, sid), 600, time.Minute) {
			writeJSON(w, 429, map[string]string{"error": "Too many requests. Try again later."})
			return
		}

		if urlPath != "/api/webhooks/line" && urlPath != "/ready" {
			if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodOptions {
				if r.Header.Get("Origin") != s.Env.AppURL || r.Header.Get("X-Gallery-Request") != "1" {
					writeJSON(w, 403, map[string]string{"error": "Invalid request origin."})
					return
				}
			}
			if strings.HasPrefix(urlPath, "/api/") &&
				urlPath != "/api/v1/auth/status" &&
				urlPath != "/api/v1/auth/login" &&
				urlPath != "/api/v1/auth/setup" && !publicSecurityRoute(r.Method, urlPath) {
				if sid == "" {
					writeJSON(w, 401, map[string]string{"error": "Sign in to continue."})
					return
				}
				rev, valid, err := s.sessionRevision(r, sid)
				if err != nil {
					writeErr(w, err)
					return
				}
				if !valid {
					writeJSON(w, 401, map[string]string{"error": "Sign in to continue."})
					return
				}
				r = r.WithContext(context.WithValue(r.Context(), revisionContextKey{}, rev))
			}
		}

		mux.ServeHTTP(w, r)
	})
}

func newSPAHandler(dist fs.FS) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		clean := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if clean != "" && clean != "." {
			if info, err := fs.Stat(dist, clean); err == nil && !info.IsDir() {
				if strings.HasPrefix(clean, "assets/") {
					w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
				}
				http.ServeFileFS(w, r, dist, clean)
				return
			}
		}
		http.ServeFileFS(w, r, dist, "index.html")
	})
}

func (s *Server) handleAuth(w http.ResponseWriter, r *http.Request, mode string) {
	ctx := r.Context()
	if !s.checkRate(s.authRates, mode+":"+clientHost(r), 8, 5*time.Minute) {
		writeJSON(w, 429, map[string]string{"error": "Too many requests. Try again later."})
		return
	}
	raw, err := readBody(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	var b struct {
		Email     string `json:"email"`
		Password  string `json:"password"`
		Bootstrap string `json:"bootstrap"`
	}
	if err := json.Unmarshal(raw, &b); err != nil {
		writeErr(w, errBadRequest("Invalid request. Check the submitted values."))
		return
	}
	b.Email = strings.ToLower(strings.TrimSpace(b.Email))
	minPass := 1
	if mode == "setup" {
		minPass = 12
	}
	if !strings.Contains(b.Email, "@") || len(b.Email) > 254 || len(b.Password) < minPass || len(b.Password) > 256 {
		writeErr(w, errBadRequest("Invalid request. Check the submitted values."))
		return
	}
	rev := int64(1)
	if mode == "setup" {
		var count int
		if err := s.DB.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM "Owner"`).Scan(&count); err != nil {
			writeErr(w, err)
			return
		}
		if !crypto.Equal(b.Bootstrap, s.Env.BootstrapSecret) || count > 0 {
			writeJSON(w, 403, map[string]string{"error": "Setup is locked or the setup key is incorrect."})
			return
		}
		hash, err := crypto.HashPassword(b.Password)
		if err != nil {
			writeErr(w, err)
			return
		}
		if _, err := s.DB.Pool.Exec(
			ctx,
			`INSERT INTO "Owner" ("id", "email", "passwordHash", "settings", "createdAt", "updatedAt") VALUES (1, $1, $2, '{}'::jsonb, NOW(), NOW())`,
			b.Email, hash,
		); err != nil {
			writeErr(w, err)
			return
		}
	} else {
		var email, passwordHash string
		var mfa bool
		err := s.DB.Pool.QueryRow(ctx, `SELECT "email", "passwordHash","securityRevision","totpSecret" != '' FROM "Owner" WHERE id = 1`).Scan(&email, &passwordHash, &rev, &mfa)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			writeErr(w, err)
			return
		}
		if err != nil || !crypto.VerifyPassword(passwordHash, b.Password) || email != b.Email {
			writeJSON(w, 401, map[string]string{"error": "Email or password is incorrect."})
			return
		}
		if mfa {
			err := s.saveFlow(w, r, flow{Purpose: "mfa", Revision: rev})
			if err != nil {
				writeErr(w, err)
				return
			}
			writeJSON(w, 200, map[string]bool{"mfaRequired": true})
			return
		}
	}
	s.issueSession(w, r, rev)
}
func (s *Server) issueSession(w http.ResponseWriter, r *http.Request, rev int64) {
	ctx := r.Context()
	sidBytes := make([]byte, 32)
	_, _ = rand.Read(sidBytes)
	sid := hex.EncodeToString(sidBytes)
	if err := s.Redis.Set(ctx, "session:"+sid, sessionValue(rev), 43200*time.Second).Err(); err != nil {
		writeErr(w, err)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     "session",
		Value:    crypto.SignCookie(sid, s.Env.SessionSecret),
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   s.Env.NodeEnv == "production",
		MaxAge:   43200,
	})
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var one int
	if err := s.DB.Pool.QueryRow(ctx, `SELECT 1`).Scan(&one); err != nil {
		writeErr(w, err)
		return
	}
	policy, err := s.DB.OwnerPolicy(ctx)
	if err != nil {
		writeErr(w, err)
		return
	}
	pong, err := s.Redis.Ping(ctx).Result()
	if err != nil {
		writeErr(w, err)
		return
	}
	hb, _ := s.Redis.Get(ctx, "worker:heartbeat").Result()
	workerState := "Heartbeat missing"
	if hb != "" {
		workerState = "Online"
	}
	pressured, err := s.Store.Pressured(policy.CollectVideos)
	if err != nil {
		writeErr(w, err)
		return
	}
	if _, err := s.DB.LineCredentials(ctx); err != nil {
		writeErr(w, err)
		return
	}
	out := map[string]any{
		"database":     "Online",
		"redis":        pong,
		"worker":       workerState,
		"diskPressure": pressured,
	}
	if s.DB.LineCredentialsUndecryptable() {
		out["lineCredentials"] = "Undecryptable"
	}
	if lp, _ := s.Redis.Get(ctx, "worker:lastProgress").Result(); lp != "" {
		out["workerLastProgressAt"] = lp
	}
	writeJSON(w, 200, out)
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	creds, err := s.DB.LineCredentials(ctx)
	if err != nil {
		writeErr(w, err)
		return
	}
	policy, err := s.DB.OwnerPolicy(ctx)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{
		"stagingHours":   policy.StagingHours,
		"failedDays":     policy.FailedDays,
		"thumbnailDays":  policy.ThumbnailDays,
		"collectVideos":  policy.CollectVideos,
		"lineConfigured": creds.Token != "" && creds.Secret != "",
		"webhook":        s.Env.AppURL + "/api/webhooks/line",
		"maxImageMB":     s.Env.MaxImageSizeMB,
		"maxVideoMB":     s.Env.MaxVideoSizeMB,
	})
}

func (s *Server) mergeSettings(ctx context.Context, patch map[string]any) error {
	b, err := json.Marshal(patch)
	if err != nil {
		return err
	}
	_, err = s.DB.Pool.Exec(ctx, `UPDATE "Owner" SET settings = settings || $1::jsonb, "updatedAt" = NOW() WHERE id = 1`, string(b))
	return err
}

func (s *Server) handlePatchSettings(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	raw, err := readBody(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		writeErr(w, errBadRequest("Invalid request. Check the submitted values."))
		return
	}
	clean := make(map[string]any)
	for k, v := range body {
		switch k {
		case "stagingHours":
			n, ok := asStrictInt(v, 1, 720)
			if !ok {
				writeErr(w, errBadRequest("Invalid request. Check the submitted values."))
				return
			}
			clean[k] = n
		case "failedDays":
			n, ok := asStrictInt(v, 1, 90)
			if !ok {
				writeErr(w, errBadRequest("Invalid request. Check the submitted values."))
				return
			}
			clean[k] = n
		case "thumbnailDays":
			n, ok := asStrictInt(v, 1, 365)
			if !ok {
				writeErr(w, errBadRequest("Invalid request. Check the submitted values."))
				return
			}
			clean[k] = n
		case "collectVideos":
			b, ok := v.(bool)
			if !ok {
				writeErr(w, errBadRequest("Invalid request. Check the submitted values."))
				return
			}
			clean[k] = b
		}
	}
	if err := s.mergeSettings(ctx, clean); err != nil {
		writeErr(w, err)
		return
	}
	_ = s.DB.Audit(ctx, uuid.NewString(), "Settings updated", "owner", "1")
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func asStrictInt(v any, min, max int) (int, bool) {
	f, ok := v.(float64)
	if !ok || float64(int(f)) != f {
		return 0, false
	}
	n := int(f)
	if n < min || n > max {
		return 0, false
	}
	return n, true
}

func (s *Server) handlePostSettingsLine(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	raw, err := readBody(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	var b struct {
		Token  string `json:"token"`
		Secret string `json:"secret"`
	}
	if err := json.Unmarshal(raw, &b); err != nil || len(b.Token) < 5 || len(b.Token) > 4096 || len(b.Secret) < 10 || len(b.Secret) > 256 {
		writeErr(w, errBadRequest("Invalid request. Check the submitted values."))
		return
	}
	resp, err := line.Call(ctx, "info", b.Token, false, 90*time.Second)
	if err != nil {
		writeErr(w, err)
		return
	}
	defer resp.Body.Close()
	var info struct {
		UserID string `json:"userId"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil || info.UserID == "" {
		writeErr(w, errBadRequest("Invalid request. Check the submitted values."))
		return
	}
	existing, err := s.DB.LineCredentials(ctx)
	if err != nil {
		writeErr(w, err)
		return
	}
	var mismatchCount int
	if err := s.DB.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM "Image" WHERE "channelId" <> $1`, info.UserID).Scan(&mismatchCount); err != nil {
		writeErr(w, err)
		return
	}
	if (existing.Destination != "" && existing.Destination != info.UserID) || mismatchCount > 0 {
		writeErr(w, model.NewFault("CHANNEL", "Use the original LINE channel.", false, 0))
		return
	}
	encTok, err := crypto.Seal(b.Token, s.Env.TokenEncryptionKey)
	if err != nil {
		writeErr(w, err)
		return
	}
	encSec, err := crypto.Seal(b.Secret, s.Env.TokenEncryptionKey)
	if err != nil {
		writeErr(w, err)
		return
	}
	if err := s.mergeSettings(ctx, map[string]any{
		"lineToken":   encTok,
		"lineSecret":  encSec,
		"destination": info.UserID,
	}); err != nil {
		writeErr(w, err)
		return
	}
	_ = s.DB.Audit(ctx, uuid.NewString(), "LINE credentials updated", "owner", "1")
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) queryGoogleStatus(ctx context.Context) (map[string]any, error) {
	var status, scopes string
	var lastVal *time.Time
	var lastErr *string
	err := s.DB.Pool.QueryRow(
		ctx,
		`SELECT "status", "lastValidatedAt", "lastError", "scopes" FROM "GoogleConnection" WHERE id = 1`,
	).Scan(&status, &lastVal, &lastErr, &scopes)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"status":          status,
		"lastValidatedAt": fmtTimePtr(lastVal),
		"lastError":       lastErr,
		"scopes":          scopes,
	}, nil
}

func (s *Server) handleGoogleStatus(w http.ResponseWriter, r *http.Request) {
	st, err := s.queryGoogleStatus(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	if st == nil {
		writeJSON(w, 200, map[string]string{"status": "DISCONNECTED"})
		return
	}
	writeJSON(w, 200, st)
}

func (s *Server) handleGoogleConnect(w http.ResponseWriter, r *http.Request) {
	stateBytes := make([]byte, 32)
	_, _ = rand.Read(stateBytes)
	state := hex.EncodeToString(stateBytes)
	if err := s.Redis.Set(r.Context(), "oauth:"+state, s.sid(r), 600*time.Second).Err(); err != nil {
		writeErr(w, err)
		return
	}
	target, err := google.Consent(s.Env, state)
	if err != nil {
		writeErr(w, err)
		return
	}
	http.Redirect(w, r, target, http.StatusFound)
}

func (s *Server) handleGoogleCallback(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	fail := func(code string) {
		http.Redirect(w, r, "/settings?google_error="+code, http.StatusFound)
	}
	state := r.URL.Query().Get("state")
	code := r.URL.Query().Get("code")
	if len(state) != 64 {
		fail("state")
		return
	}
	storedSid, err := s.Redis.GetDel(ctx, "oauth:"+state).Result()
	if err != nil || storedSid != s.sid(r) {
		fail("state")
		return
	}
	if code == "" {
		fail("denied")
		return
	}
	t, err := google.Token(ctx, s.Env, map[string]string{
		"code":         code,
		"redirect_uri": s.Env.GoogleRedirectURI,
		"grant_type":   "authorization_code",
	})
	if err != nil {
		fail("exchange")
		return
	}
	granted := strings.Split(t.Scope, " ")
	allScopes := true
	for _, reqScope := range google.Scopes {
		if !slices.Contains(granted, reqScope) {
			allScopes = false
			break
		}
	}
	if t.RefreshToken == "" || !allScopes {
		fail("scopes")
		return
	}
	if err := google.Validate(ctx, t.AccessToken); err != nil {
		fail("exchange")
		return
	}
	var mappedAlbumID *string
	if err := s.DB.Pool.QueryRow(ctx, `SELECT "googleAlbumId" FROM "LineGroup" WHERE "googleAlbumId" IS NOT NULL LIMIT 1`).Scan(&mappedAlbumID); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		fail("exchange")
		return
	}
	if mappedAlbumID != nil && *mappedAlbumID != "" {
		if _, err := google.Album(ctx, *mappedAlbumID, t.AccessToken); err != nil {
			fail("exchange")
			return
		}
	}
	encRef, err := crypto.Seal(t.RefreshToken, s.Env.TokenEncryptionKey)
	if err != nil {
		fail("exchange")
		return
	}
	if _, err := s.DB.Pool.Exec(
		ctx,
		`INSERT INTO "GoogleConnection" ("id", "ownerId", "encryptedRefreshToken", "status", "scopes", "connectedAt", "lastValidatedAt", "lastError")
		 VALUES (1, 1, $1, 'CONNECTED', $2, NOW(), NOW(), NULL)
		 ON CONFLICT ("id") DO UPDATE
		 SET "encryptedRefreshToken" = EXCLUDED."encryptedRefreshToken",
		     "status" = 'CONNECTED',
		     "scopes" = EXCLUDED."scopes",
		     "lastValidatedAt" = NOW(),
		     "lastError" = NULL`,
		encRef, t.Scope,
	); err != nil {
		fail("exchange")
		return
	}
	s.DB.ClearAccessCache()
	_ = s.DB.Audit(ctx, uuid.NewString(), "Google connected", "owner", "1")
	http.Redirect(w, r, "/settings", http.StatusFound)
}

func (s *Server) handleGoogleValidate(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	tok, err := s.DB.Access(ctx, true)
	if err != nil {
		writeErr(w, err)
		return
	}
	if err := google.Validate(ctx, tok); err != nil {
		writeErr(w, err)
		return
	}
	if _, err := s.DB.Pool.Exec(
		ctx,
		`UPDATE "GoogleConnection" SET "status" = 'CONNECTED', "lastError" = NULL, "lastValidatedAt" = NOW() WHERE id = 1`,
	); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) handleGoogleDisconnect(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if _, err := s.DB.Pool.Exec(
		ctx,
		`UPDATE "GoogleConnection" SET "status" = 'DISCONNECTED', "encryptedRefreshToken" = NULL, "lastError" = NULL`,
	); err != nil {
		writeErr(w, err)
		return
	}
	s.DB.ClearAccessCache()
	_ = s.DB.Audit(ctx, uuid.NewString(), "Google disconnected", "owner", "1")
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) fetchGroupMap(row pgx.Row) (map[string]any, error) {
	var id, lineGroupID, membershipStatus string
	var displayName, googleAlbumID *string
	var enabled, albumCreating bool
	var enabledSince *time.Time
	var membershipAt, lastEventAt, createdAt, updatedAt time.Time
	var imageCount int
	var imgID, imgStatus *string
	var imgTS *time.Time

	err := row.Scan(
		&id, &lineGroupID, &displayName, &enabled, &membershipStatus,
		&enabledSince, &membershipAt, &googleAlbumID, &albumCreating,
		&lastEventAt, &createdAt, &updatedAt, &imageCount,
		&imgID, &imgTS, &imgStatus,
	)
	if err != nil {
		return nil, err
	}

	images := make([]map[string]any, 0, 1)
	if imgID != nil && imgTS != nil && imgStatus != nil {
		images = append(images, map[string]any{
			"id":                 *imgID,
			"lineEventTimestamp": fmtTime(*imgTS),
			"uploadStatus":       *imgStatus,
		})
	}

	return map[string]any{
		"id":               id,
		"lineGroupId":      lineGroupID,
		"displayName":      displayName,
		"enabled":          enabled,
		"membershipStatus": membershipStatus,
		"enabledSince":     fmtTimePtr(enabledSince),
		"membershipAt":     fmtTime(membershipAt),
		"googleAlbumId":    googleAlbumID,
		"albumCreating":    albumCreating,
		"lastEventAt":      fmtTime(lastEventAt),
		"createdAt":        fmtTime(createdAt),
		"updatedAt":        fmtTime(updatedAt),
		"_count":           map[string]int{"images": imageCount},
		"images":           images,
	}, nil
}

func (s *Server) handleGetGroups(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	rows, err := s.DB.Pool.Query(
		ctx,
		`SELECT g."id", g."lineGroupId", g."displayName", g."enabled", g."membershipStatus",
		        g."enabledSince", g."membershipAt", g."googleAlbumId", g."albumCreating",
		        g."lastEventAt", g."createdAt", g."updatedAt",
		        (SELECT COUNT(*) FROM "Image" i WHERE i."lineGroupId" = g."lineGroupId"),
		        li."id", li."lineEventTimestamp", li."uploadStatus"
		 FROM "LineGroup" g
		 LEFT JOIN LATERAL (
		        SELECT "id", "lineEventTimestamp", "uploadStatus"::text AS "uploadStatus" FROM "Image"
		        WHERE "lineGroupId" = g."lineGroupId" ORDER BY "lineEventTimestamp" DESC LIMIT 1
		 ) li ON true
		 ORDER BY g."lastEventAt" DESC`,
	)
	if err != nil {
		writeErr(w, err)
		return
	}
	defer rows.Close()
	result := make([]map[string]any, 0)
	for rows.Next() {
		m, err := s.fetchGroupMap(rows)
		if err != nil {
			writeErr(w, err)
			return
		}
		result = append(result, m)
	}
	writeJSON(w, 200, result)
}

func (s *Server) handleGetGroup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	gid, err := parseUUID(r.PathValue("id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	row := s.DB.Pool.QueryRow(
		ctx,
		`SELECT g."id", g."lineGroupId", g."displayName", g."enabled", g."membershipStatus",
		        g."enabledSince", g."membershipAt", g."googleAlbumId", g."albumCreating",
		        g."lastEventAt", g."createdAt", g."updatedAt",
		        (SELECT COUNT(*) FROM "Image" i WHERE i."lineGroupId" = g."lineGroupId"),
		        li."id", li."lineEventTimestamp", li."uploadStatus"
		 FROM "LineGroup" g
		 LEFT JOIN LATERAL (
		        SELECT "id", "lineEventTimestamp", "uploadStatus"::text AS "uploadStatus" FROM "Image"
		        WHERE "lineGroupId" = g."lineGroupId" ORDER BY "lineEventTimestamp" DESC LIMIT 1
		 ) li ON true
		 WHERE g."id" = $1`,
		gid,
	)
	m, err := s.fetchGroupMap(row)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, m)
}

func (s *Server) handlePatchGroup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	gid, err := parseUUID(r.PathValue("id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	raw, err := readBody(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	var b struct {
		Enabled *bool `json:"enabled"`
	}
	if err := json.Unmarshal(raw, &b); err != nil || b.Enabled == nil {
		writeErr(w, errBadRequest("Invalid request. Check the submitted values."))
		return
	}
	tx, err := s.DB.Pool.Begin(ctx)
	if err != nil {
		writeErr(w, err)
		return
	}
	defer tx.Rollback(ctx)

	var cmd pgconn.CommandTag
	if *b.Enabled {
		cmd, err = tx.Exec(
			ctx,
			`UPDATE "LineGroup" SET "enabled" = true, "enabledSince" = CASE WHEN "enabled" THEN COALESCE("enabledSince", NOW()) ELSE NOW() END, "updatedAt" = NOW() WHERE id = $1 AND "membershipStatus" = 'JOINED'`,
			gid,
		)
	} else {
		cmd, err = tx.Exec(
			ctx,
			`UPDATE "LineGroup" SET "enabled" = false, "enabledSince" = NULL, "updatedAt" = NOW() WHERE id = $1`,
			gid,
		)
	}
	if err != nil {
		writeErr(w, err)
		return
	}
	if cmd.RowsAffected() == 0 {
		writeErr(w, pgx.ErrNoRows)
		return
	}
	action := "Collection paused"
	if *b.Enabled {
		action = "Collection enabled"
	}
	if _, err := tx.Exec(
		ctx,
		`INSERT INTO "AuditLog" ("id", "action", "targetType", "targetId", "metadata", "createdAt") VALUES ($1, $2, 'group', $3, '{}'::jsonb, NOW())`,
		uuid.NewString(), action, gid,
	); err != nil {
		writeErr(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) handleRefreshGroup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	gid, err := parseUUID(r.PathValue("id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	var lineGroupID, membershipStatus string
	var googleAlbumID *string
	err = s.DB.Pool.QueryRow(
		ctx,
		`SELECT "lineGroupId", "membershipStatus", "googleAlbumId" FROM "LineGroup" WHERE id = $1`,
		gid,
	).Scan(&lineGroupID, &membershipStatus, &googleAlbumID)
	if err != nil {
		writeErr(w, err)
		return
	}
	if membershipStatus != "JOINED" {
		writeErr(w, model.NewFault("GROUP_LEFT", "Bot is not a member of this LINE group.", false, 0))
		return
	}
	creds, err := s.DB.LineCredentials(ctx)
	if err != nil {
		writeErr(w, err)
		return
	}
	if creds.Token == "" {
		writeErr(w, model.NewFault("LINE_CONFIG", "Configure LINE credentials first.", false, 0))
		return
	}
	displayName, err := line.GroupName(ctx, creds.Token, lineGroupID)
	if errors.Is(err, line.ErrNoGroupName) {
		writeErr(w, errBadRequest("Invalid request. Check the submitted values."))
		return
	}
	if err != nil {
		writeErr(w, err)
		return
	}
	if displayName == "" {
		writeErr(w, model.NewFault("LINE_SUMMARY", "LINE did not return a group name.", false, 0))
		return
	}
	if _, err := s.DB.Pool.Exec(
		ctx,
		`UPDATE "LineGroup" SET "displayName" = $1, "updatedAt" = NOW() WHERE id = $2`,
		displayName, gid,
	); err != nil {
		writeErr(w, err)
		return
	}

	albumUpdated := false
	var albumError string
	if googleAlbumID != nil && *googleAlbumID != "" {
		var status, scopes string
		connErr := s.DB.Pool.QueryRow(ctx, `SELECT "status", "scopes" FROM "GoogleConnection" WHERE id = 1`).Scan(&status, &scopes)
		if connErr == nil && status == "CONNECTED" && slices.Contains(strings.Split(scopes, " "), google.EditScope) {
			tok, err := s.DB.Access(ctx, false)
			if err != nil {
				var f *model.Fault
				if errors.As(err, &f) {
					albumError = f.Message
				} else {
					albumError = "Could not update Google Photos album title."
				}
			} else if _, err := google.RenameAlbum(ctx, *googleAlbumID, google.Title(&displayName, lineGroupID), tok); err != nil {
				var f *model.Fault
				if errors.As(err, &f) {
					albumError = f.Message
				} else {
					albumError = "Could not update Google Photos album title."
				}
			} else {
				albumUpdated = true
			}
		} else if connErr == nil && status == "CONNECTED" {
			albumError = "Reconnect Google Photos in Settings to sync the album title."
		} else {
			albumError = "Connect Google Photos in Settings to sync the album title."
		}
	}

	auditAction := "Group name refreshed"
	if albumUpdated {
		auditAction = "Group and Google album name refreshed"
	}
	_ = s.DB.Audit(ctx, uuid.NewString(), auditAction, "group", gid)

	out := map[string]any{
		"ok":           true,
		"displayName":  displayName,
		"albumUpdated": albumUpdated,
	}
	if albumError != "" {
		out["albumError"] = albumError
	}
	writeJSON(w, 200, out)
}

func (s *Server) handleResolveAlbum(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	gid, err := parseUUID(r.PathValue("id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	raw, err := readBody(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	var b struct {
		AlbumID      string `json:"albumId"`
		Confirmation string `json:"confirmation"`
	}
	if err := json.Unmarshal(raw, &b); err != nil || b.Confirmation != "I checked Google Photos" || len(b.AlbumID) > 512 {
		writeErr(w, errBadRequest("Invalid request. Check the submitted values."))
		return
	}
	var lineGroupID string
	if err := s.DB.Pool.QueryRow(
		ctx,
		`SELECT "lineGroupId" FROM "LineGroup" WHERE id = $1 AND "albumCreating" = true`,
		gid,
	).Scan(&lineGroupID); err != nil {
		writeErr(w, err)
		return
	}
	var reviewCount int
	if err := s.DB.Pool.QueryRow(
		ctx,
		`SELECT COUNT(*) FROM "Image" WHERE "lineGroupId" = $1 AND "errorCode" = 'ALBUM_REVIEW'`,
		lineGroupID,
	).Scan(&reviewCount); err != nil {
		writeErr(w, err)
		return
	}
	if reviewCount == 0 {
		writeErr(w, model.NewFault("ALBUM_BUSY", "Wait until the album creation attempt finishes.", false, 0))
		return
	}
	if b.AlbumID != "" {
		tok, err := s.DB.Access(ctx, false)
		if err != nil {
			writeErr(w, err)
			return
		}
		if _, err := google.Album(ctx, b.AlbumID, tok); err != nil {
			writeErr(w, err)
			return
		}
	}
	var albumPtr *string
	if b.AlbumID != "" {
		albumPtr = &b.AlbumID
	}
	tx, err := s.DB.Pool.Begin(ctx)
	if err != nil {
		writeErr(w, err)
		return
	}
	defer tx.Rollback(ctx)
	cmd, err := tx.Exec(
		ctx,
		`UPDATE "LineGroup" SET "albumCreating" = false, "googleAlbumId" = $1, "updatedAt" = NOW() WHERE id = $2 AND "albumCreating" = true`,
		albumPtr, gid,
	)
	if err != nil {
		writeErr(w, err)
		return
	}
	if cmd.RowsAffected() == 0 {
		writeErr(w, pgx.ErrNoRows)
		return
	}
	// Mirrors handleRetryJob for every image this group parked as ALBUM_REVIEW.
	requeue, err := tx.Exec(
		ctx,
		`WITH r AS (
		   UPDATE "Image" SET "uploadStatus" = 'RETRY_PENDING'::"UploadState", "updatedAt" = NOW()
		   WHERE "lineGroupId" = (SELECT "lineGroupId" FROM "LineGroup" WHERE id = $1)
		     AND "uploadStatus" = 'FAILED'::"UploadState" AND "errorCode" = 'ALBUM_REVIEW'
		   RETURNING id)
		 INSERT INTO "OutboxEvent" ("id", "eventType", "aggregateId", "payload", "createdAt", "availableAt")
		 SELECT gen_random_uuid()::text, 'PROCESS', id, '{}'::jsonb, NOW(), NOW() FROM r`,
		gid,
	)
	if err != nil {
		writeErr(w, err)
		return
	}
	requeued := int(requeue.RowsAffected())
	if _, err := tx.Exec(
		ctx,
		`INSERT INTO "AuditLog" ("id", "action", "targetType", "targetId", "metadata", "createdAt") VALUES ($1, 'Owner resolved uncertain album', 'group', $2, jsonb_build_object('requeued', $3::int), NOW())`,
		uuid.NewString(), gid, requeued,
	); err != nil {
		writeErr(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "requeued": requeued})
}

var bangkokZone = time.FixedZone("ICT", 7*3600)

func parseBangkokStart(isoDate string) (time.Time, error) {
	if !isoDateRe.MatchString(isoDate) {
		return time.Time{}, errBadRequest("Invalid request. Check the submitted values.")
	}
	t, err := time.ParseInLocation("2006-01-02", isoDate, bangkokZone)
	if err != nil {
		return time.Time{}, errBadRequest("Invalid request. Check the submitted values.")
	}
	return t.UTC(), nil
}

func (s *Server) handleListImages(w http.ResponseWriter, r *http.Request, isJobs bool) {
	ctx := r.Context()
	q := r.URL.Query()
	page := 1
	if pStr := q.Get("page"); pStr != "" {
		p, err := strconv.Atoi(pStr)
		if err != nil || p < 1 || p > 100000 {
			writeErr(w, errBadRequest("Invalid request. Check the submitted values."))
			return
		}
		page = p
	}
	group := q.Get("group")
	if len(group) > 100 {
		writeErr(w, errBadRequest("Invalid request. Check the submitted values."))
		return
	}
	status := q.Get("status")
	if status != "" && !model.IsValidState(status) {
		writeErr(w, errBadRequest("Invalid request. Check the submitted values."))
		return
	}
	var fromTime, toTime *time.Time
	if fromStr := q.Get("from"); fromStr != "" {
		t, err := parseBangkokStart(fromStr)
		if err != nil {
			writeErr(w, err)
			return
		}
		fromTime = &t
	}
	if toStr := q.Get("to"); toStr != "" {
		t, err := parseBangkokStart(toStr)
		if err != nil {
			writeErr(w, err)
			return
		}
		end := t.Add(24 * time.Hour)
		toTime = &end
	}

	var conds []string
	var args []any
	argIdx := 1

	if group != "" {
		conds = append(conds, fmt.Sprintf(`i."lineGroupId" = $%d`, argIdx))
		args = append(args, group)
		argIdx++
	}
	if status != "" {
		conds = append(conds, fmt.Sprintf(`i."uploadStatus" = $%d::"UploadState"`, argIdx))
		args = append(args, status)
		argIdx++
	} else if isJobs {
		conds = append(conds, `i."uploadStatus" <> 'SYNCED'::"UploadState"`)
	}
	if fromTime != nil {
		conds = append(conds, fmt.Sprintf(`i."lineEventTimestamp" >= $%d`, argIdx))
		args = append(args, *fromTime)
		argIdx++
	}
	if toTime != nil {
		conds = append(conds, fmt.Sprintf(`i."lineEventTimestamp" < $%d`, argIdx))
		args = append(args, *toTime)
		argIdx++
	}

	whereSQL := ""
	if len(conds) > 0 {
		whereSQL = "WHERE " + strings.Join(conds, " AND ")
	}

	var total int
	if err := s.DB.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM "Image" i `+whereSQL, args...).Scan(&total); err != nil {
		writeErr(w, err)
		return
	}

	queryArgs := append([]any{}, args...)
	queryArgs = append(queryArgs, (page-1)*36, 36)
	// Count only the card's Bangkok day, independently of filters and pagination.
	// At equal timestamps, larger IDs are displayed later and have lower numbers.
	frameColumn := "NULL::bigint"
	if !isJobs {
		frameColumn = `(SELECT count(*) FROM "Image" f
		  WHERE f."lineEventTimestamp" >= date_trunc('day', i."lineEventTimestamp", 'Asia/Bangkok')
		    AND f."lineEventTimestamp" <= i."lineEventTimestamp"
		    AND (f."lineEventTimestamp" < i."lineEventTimestamp" OR f.id >= i.id))`
	}
	querySQL := fmt.Sprintf(
		`SELECT i."id", i."lineGroupId", i."channelId", i."lineMessageId", i."lineEventTimestamp",
		        i."mediaType", i."durationMs", i."mimeType", i."fileSize", i."width", i."height",
		        i."sha256", i."googleMediaItemId", i."productUrl", i."uploadStatus"::text,
		        i."stagingPath", i."uploadedAt", i."errorCode", i."lastError", i."createdAt", i."updatedAt",
		        g."displayName", %s
		 FROM "Image" i
		 JOIN "LineGroup" g ON g."lineGroupId" = i."lineGroupId"
		 %s
		 ORDER BY i."lineEventTimestamp" DESC, i."id" ASC
		 OFFSET $%d LIMIT $%d`,
		frameColumn, whereSQL, argIdx, argIdx+1,
	)

	rows, err := s.DB.Pool.Query(ctx, querySQL, queryArgs...)
	if err != nil {
		writeErr(w, err)
		return
	}
	defer rows.Close()

	items := make([]map[string]any, 0)
	for rows.Next() {
		var id, lineGroupID, channelID, lineMessageID, mediaType, uploadStatus string
		var lineEventTS, createdAt, updatedAt time.Time
		var durationMs, fileSize, width, height *int
		var mimeType, sha256, googleMediaItemID, productURL, stagingPath, errorCode, lastError, groupDisplayName *string
		var dayNumber *int64
		var uploadedAt *time.Time

		if err := rows.Scan(
			&id, &lineGroupID, &channelID, &lineMessageID, &lineEventTS,
			&mediaType, &durationMs, &mimeType, &fileSize, &width, &height,
			&sha256, &googleMediaItemID, &productURL, &uploadStatus,
			&stagingPath, &uploadedAt, &errorCode, &lastError, &createdAt, &updatedAt,
			&groupDisplayName, &dayNumber,
		); err != nil {
			writeErr(w, err)
			return
		}
		item := map[string]any{
			"id":                 id,
			"lineGroupId":        lineGroupID,
			"channelId":          channelID,
			"lineMessageId":      lineMessageID,
			"lineEventTimestamp": fmtTime(lineEventTS),
			"mediaType":          mediaType,
			"durationMs":         durationMs,
			"mimeType":           mimeType,
			"fileSize":           fileSize,
			"width":              width,
			"height":             height,
			"sha256":             sha256,
			"googleMediaItemId":  googleMediaItemID,
			"productUrl":         productURL,
			"uploadStatus":       uploadStatus,
			"uploadedAt":         fmtTimePtr(uploadedAt),
			"errorCode":          errorCode,
			"lastError":          lastError,
			"createdAt":          fmtTime(createdAt),
			"updatedAt":          fmtTime(updatedAt),
			"group":              map[string]any{"displayName": groupDisplayName},
			"hasStaging":         stagingPath != nil && *stagingPath != "",
		}
		if dayNumber != nil {
			item["dayNumber"] = *dayNumber
		}
		items = append(items, item)
	}
	if err := rows.Err(); err != nil {
		writeErr(w, err)
		return
	}

	writeJSON(w, 200, map[string]any{
		"items": items,
		"total": total,
		"page":  page,
	})
}

func (s *Server) handleGetImage(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	imgID, err := parseUUID(r.PathValue("id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	var id, lineGroupID, channelID, lineMessageID, mediaType, uploadStatus string
	var lineEventTS, createdAt, updatedAt time.Time
	var durationMs, fileSize, width, height *int
	var mimeType, sha256, googleMediaItemID, productURL, errorCode, lastError *string
	var uploadedAt *time.Time

	err = s.DB.Pool.QueryRow(
		ctx,
		`SELECT "id", "lineGroupId", "channelId", "lineMessageId", "lineEventTimestamp",
		        "mediaType", "durationMs", "mimeType", "fileSize", "width", "height",
		        "sha256", "googleMediaItemId", "productUrl", "uploadStatus"::text,
		        "uploadedAt", "errorCode", "lastError", "createdAt", "updatedAt"
		 FROM "Image" WHERE id = $1`,
		imgID,
	).Scan(
		&id, &lineGroupID, &channelID, &lineMessageID, &lineEventTS,
		&mediaType, &durationMs, &mimeType, &fileSize, &width, &height,
		&sha256, &googleMediaItemID, &productURL, &uploadStatus,
		&uploadedAt, &errorCode, &lastError, &createdAt, &updatedAt,
	)
	if err != nil {
		writeErr(w, err)
		return
	}

	attRows, err := s.DB.Pool.Query(
		ctx,
		`SELECT "id", "imageId", "attemptNumber", "status", "startedAt", "completedAt", "errorCode", "errorMessage"
		 FROM "UploadAttempt" WHERE "imageId" = $1 ORDER BY "attemptNumber" DESC LIMIT 20`,
		imgID,
	)
	if err != nil {
		writeErr(w, err)
		return
	}
	defer attRows.Close()
	attempts := make([]map[string]any, 0)
	for attRows.Next() {
		var aid, aImgID, aStatus string
		var aNum int
		var aStarted time.Time
		var aCompleted *time.Time
		var aCode, aMsg *string
		if err := attRows.Scan(&aid, &aImgID, &aNum, &aStatus, &aStarted, &aCompleted, &aCode, &aMsg); err == nil {
			attempts = append(attempts, map[string]any{
				"id":            aid,
				"imageId":       aImgID,
				"attemptNumber": aNum,
				"status":        aStatus,
				"startedAt":     fmtTime(aStarted),
				"completedAt":   fmtTimePtr(aCompleted),
				"errorCode":     aCode,
				"errorMessage":  aMsg,
			})
		}
	}

	writeJSON(w, 200, map[string]any{
		"id":                 id,
		"lineGroupId":        lineGroupID,
		"channelId":          channelID,
		"lineMessageId":      lineMessageID,
		"lineEventTimestamp": fmtTime(lineEventTS),
		"mediaType":          mediaType,
		"durationMs":         durationMs,
		"mimeType":           mimeType,
		"fileSize":           fileSize,
		"width":              width,
		"height":             height,
		"sha256":             sha256,
		"googleMediaItemId":  googleMediaItemID,
		"productUrl":         productURL,
		"uploadStatus":       uploadStatus,
		"uploadedAt":         fmtTimePtr(uploadedAt),
		"errorCode":          errorCode,
		"lastError":          lastError,
		"createdAt":          fmtTime(createdAt),
		"updatedAt":          fmtTime(updatedAt),
		"attempts":           attempts,
	})
}

func (s *Server) handleMediaStream(w http.ResponseWriter, r *http.Request, thumb bool) {
	ctx := r.Context()
	imgID, err := parseUUID(r.PathValue("id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	sizeParam := r.URL.Query().Get("size")
	if sizeParam == "" {
		sizeParam = "320"
	}
	if sizeParam != "320" && sizeParam != "640" {
		writeErr(w, errBadRequest("Invalid request. Check the submitted values."))
		return
	}
	size := 320
	if sizeParam == "640" {
		size = 640
	}

	var mediaType string
	var mimeType, stagingPath, googleMediaItemID *string
	if err := s.DB.Pool.QueryRow(
		ctx,
		`SELECT "mediaType", "mimeType", "stagingPath", "googleMediaItemId" FROM "Image" WHERE id = $1`,
		imgID,
	).Scan(&mediaType, &mimeType, &stagingPath, &googleMediaItemID); err != nil {
		writeErr(w, err)
		return
	}

	pathSize := 0
	if thumb {
		pathSize = size
	}
	localPath, err := s.Store.PathFor(imgID, pathSize)
	if err != nil {
		writeErr(w, err)
		return
	}

	var localInfo os.FileInfo
	if thumb || (stagingPath != nil && *stagingPath != "") {
		localInfo, _ = os.Stat(localPath)
	}
	if localInfo != nil {
		if thumb {
			_ = media.Touch(localPath)
		}
		contentType := "application/octet-stream"
		if thumb {
			contentType = "image/webp"
		} else if mimeType != nil && *mimeType != "" {
			contentType = *mimeType
		}
		f, err := os.Open(localPath)
		if err != nil {
			writeErr(w, err)
			return
		}
		defer f.Close()

		w.Header().Set("Content-Type", contentType)
		// ServeContent handles Range (incl. suffix), 416, and conditional requests.
		http.ServeContent(w, r, "", localInfo.ModTime(), f)
		return
	}

	hasRemote := googleMediaItemID != nil && *googleMediaItemID != ""
	if !hasRemote && (!thumb || stagingPath == nil || *stagingPath == "") {
		writeJSON(w, 404, map[string]string{"error": "Image not available yet."})
		return
	}
	if thumb {
		result, err := s.thumbnail(ctx, localPath, func(ctx context.Context) (thumbnailResult, error) {
			if !hasRemote {
				path, err := s.Store.Thumbnail(ctx, imgID, size, mediaType)
				return thumbnailResult{path: path}, err
			}
			remote, contentType, err := s.remoteMedia(ctx, *googleMediaItemID, mediaType, mimeType, size, "")
			if err != nil {
				return thumbnailResult{}, err
			}
			defer remote.Body.Close()
			const limit = 10 << 20
			buf, err := io.ReadAll(io.LimitReader(remote.Body, limit+1))
			if err != nil || len(buf) > limit {
				return thumbnailResult{}, model.NewFault("GOOGLE_MEDIA", "Google thumbnail is unavailable.", true, 0)
			}
			path, err := s.Store.CacheThumbnail(ctx, imgID, size, buf)
			if err != nil {
				return thumbnailResult{data: buf, mime: contentType}, nil
			}
			return thumbnailResult{path: path}, nil
		})
		if ctx.Err() != nil {
			return // client gone; nothing to write
		}
		if errors.Is(err, errThumbBusy) {
			w.Header().Set("Retry-After", "5")
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": "Thumbnail generation is busy. Try again shortly."})
			return
		}
		if err != nil {
			writeErr(w, err)
			return
		}
		if result.path == "" {
			w.Header().Set("Content-Type", result.mime)
			_, _ = w.Write(result.data)
			return
		}
		f, err := os.Open(result.path)
		if err != nil {
			writeErr(w, err)
			return
		}
		defer f.Close()
		w.Header().Set("Content-Type", "image/webp")
		w.WriteHeader(http.StatusOK)
		_, _ = io.Copy(w, f)
		return
	}

	remote, outMime, err := s.remoteMedia(ctx, *googleMediaItemID, mediaType, mimeType, 0, r.Header.Get("Range"))
	if err != nil {
		writeErr(w, err)
		return
	}
	defer remote.Body.Close()
	if cr := remote.Header.Get("Content-Range"); cr != "" {
		w.Header().Set("Content-Range", cr)
	}
	ar := remote.Header.Get("Accept-Ranges")
	if ar != "" {
		w.Header().Set("Accept-Ranges", ar)
	} else if mediaType == "VIDEO" {
		w.Header().Set("Accept-Ranges", "bytes")
	}
	if cl := remote.Header.Get("Content-Length"); digitsOnly.MatchString(cl) {
		w.Header().Set("Content-Length", cl)
	}
	w.Header().Set("Content-Type", outMime)
	if remote.StatusCode == http.StatusPartialContent {
		w.WriteHeader(http.StatusPartialContent)
	} else {
		w.WriteHeader(http.StatusOK)
	}
	_, _ = io.Copy(w, remote.Body)
}

func (s *Server) remoteMedia(ctx context.Context, mediaID, mediaType string, mimeType *string, size int, byteRange string) (*http.Response, string, error) {
	tok, err := s.DB.Access(ctx, false)
	if err != nil {
		return nil, "", err
	}
	m, err := google.Item(ctx, mediaID, tok)
	if err != nil {
		return nil, "", err
	}
	parsedBase, err := url.Parse(m.BaseURL)
	if err != nil || parsedBase.Scheme != "https" || !strings.HasSuffix(parsedBase.Hostname(), ".googleusercontent.com") {
		return nil, "", model.NewFault("GOOGLE_MEDIA", "Invalid remote media URL.", false, 0)
	}

	thumb := size != 0
	suffix := "=d"
	if thumb {
		suffix = fmt.Sprintf("=w%d-h%d", size, size)
	} else if mediaType == "VIDEO" {
		suffix = "=dv"
	}

	timeout := 60 * time.Second
	if mediaType == "VIDEO" && !thumb {
		timeout = 300 * time.Second
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, m.BaseURL+suffix, nil)
	if err != nil {
		return nil, "", model.NewFault("GOOGLE_MEDIA", "Google media is unavailable.", true, 0)
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	if !thumb && mediaType == "VIDEO" && rangeRe.MatchString(byteRange) {
		req.Header.Set("Range", byteRange)
	}
	client := &http.Client{
		Timeout: timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	remote, err := client.Do(req)
	if err != nil || remote.StatusCode < 200 || remote.StatusCode >= 300 {
		if remote != nil {
			remote.Body.Close()
		}
		return nil, "", model.NewFault("GOOGLE_MEDIA", "Google media is unavailable.", true, 0)
	}

	remoteMime := strings.ToLower(strings.TrimSpace(strings.Split(remote.Header.Get("Content-Type"), ";")[0]))
	allowedMimes := []string{
		"image/jpeg", "image/webp", "image/png", "image/gif", "image/avif",
		"video/mp4", "video/quicktime", "video/webm", "application/octet-stream",
	}
	if !slices.Contains(allowedMimes, remoteMime) {
		remote.Body.Close()
		return nil, "", model.NewFault("GOOGLE_MEDIA", "Unexpected content type from Google.", false, 0)
	}

	outMime := remoteMime
	if outMime == "application/octet-stream" {
		if mimeType != nil && *mimeType != "" {
			outMime = *mimeType
		} else {
			outMime = "image/jpeg"
		}
	}

	return remote, outMime, nil
}

func (s *Server) handleDiscardJob(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	imageID, err := parseUUID(r.PathValue("id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	raw, err := readBody(r)
	if err != nil {
		writeErr(w, err)
		return
	}
	var b struct {
		Confirmation string `json:"confirmation"`
	}
	if err := json.Unmarshal(raw, &b); err != nil || b.Confirmation != "Discard staged original" {
		writeErr(w, errBadRequest("Invalid request. Check the submitted values."))
		return
	}

	var statusStr string
	var stagingPath *string
	var createdAt time.Time
	if err := s.DB.Pool.QueryRow(
		ctx,
		`SELECT "uploadStatus"::text, "stagingPath", "createdAt" FROM "Image" WHERE id = $1`,
		imageID,
	).Scan(&statusStr, &stagingPath, &createdAt); err != nil {
		writeErr(w, err)
		return
	}
	policy, err := s.DB.OwnerPolicy(ctx)
	if err != nil {
		writeErr(w, err)
		return
	}
	curState := model.State(statusStr)
	allowedDiscard := slices.Contains([]model.State{model.StateFailed, model.StateNeedsReview, model.StateUnrecoverable}, curState)
	deadline := createdAt.Add(time.Duration(policy.FailedDays) * 24 * time.Hour)
	if stagingPath == nil || *stagingPath == "" || !allowedDiscard || deadline.After(time.Now()) {
		writeErr(w, model.NewFault("RETENTION", "Only expired, stopped uploads can be discarded.", false, 0))
		return
	}
	next := model.StateUnrecoverable
	if curState == model.StateNeedsReview {
		next = model.StateNeedsReview
	}
	if curState != next {
		if err := model.Transition(curState, next, ""); err != nil {
			writeErr(w, err)
			return
		}
	}

	tx, err := s.DB.Pool.Begin(ctx)
	if err != nil {
		writeErr(w, err)
		return
	}
	defer tx.Rollback(ctx)
	cmd, err := tx.Exec(
		ctx,
		`UPDATE "Image"
		 SET "uploadStatus" = $1::"UploadState", "stagingPath" = NULL, "lastError" = 'Owner discarded the expired staged original.', "updatedAt" = NOW()
		 WHERE id = $2 AND "uploadStatus" = $3::"UploadState" AND "stagingPath" = $4`,
		string(next), imageID, string(curState), *stagingPath,
	)
	if err != nil {
		writeErr(w, err)
		return
	}
	if cmd.RowsAffected() == 0 {
		writeErr(w, pgx.ErrNoRows)
		return
	}
	if _, err := tx.Exec(
		ctx,
		`INSERT INTO "OutboxEvent" ("id", "eventType", "aggregateId", "payload", "createdAt", "availableAt") VALUES ($1, 'DISCARD', $2, '{}'::jsonb, NOW(), NOW())`,
		uuid.NewString(), imageID,
	); err != nil {
		writeErr(w, err)
		return
	}
	if _, err := tx.Exec(
		ctx,
		`INSERT INTO "AuditLog" ("id", "action", "targetType", "targetId", "metadata", "createdAt") VALUES ($1, 'Discard of expired staged original authorized', 'image', $2, '{}'::jsonb, NOW())`,
		uuid.NewString(), imageID,
	); err != nil {
		writeErr(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) handleRetryJob(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	imageID, err := parseUUID(r.PathValue("id"))
	if err != nil {
		writeErr(w, err)
		return
	}
	var statusStr string
	if err := s.DB.Pool.QueryRow(ctx, `SELECT "uploadStatus"::text FROM "Image" WHERE id = $1`, imageID).Scan(&statusStr); err != nil {
		writeErr(w, err)
		return
	}
	curState := model.State(statusStr)
	if curState != model.StateFailed && curState != model.StateNeedsReview {
		writeErr(w, model.NewFault("NOT_RETRYABLE", "Only failed or uncertain jobs can be retried.", false, 0))
		return
	}
	next := model.StateNeedsReview
	eventType := "RECONCILE"
	if curState == model.StateFailed {
		if err := model.Transition(model.StateFailed, model.StateRetryPending, ""); err != nil {
			writeErr(w, err)
			return
		}
		next = model.StateRetryPending
		eventType = "PROCESS"
	}

	tx, err := s.DB.Pool.Begin(ctx)
	if err != nil {
		writeErr(w, err)
		return
	}
	defer tx.Rollback(ctx)
	cmd, err := tx.Exec(
		ctx,
		`UPDATE "Image" SET "uploadStatus" = $1::"UploadState", "updatedAt" = NOW() WHERE id = $2 AND "uploadStatus" = $3::"UploadState"`,
		string(next), imageID, string(curState),
	)
	if err != nil {
		writeErr(w, err)
		return
	}
	if cmd.RowsAffected() == 0 {
		writeErr(w, pgx.ErrNoRows)
		return
	}
	if _, err := tx.Exec(
		ctx,
		`INSERT INTO "OutboxEvent" ("id", "eventType", "aggregateId", "payload", "createdAt", "availableAt") VALUES ($1, $2, $3, '{}'::jsonb, NOW(), NOW())`,
		uuid.NewString(), eventType, imageID,
	); err != nil {
		writeErr(w, err)
		return
	}
	if _, err := tx.Exec(
		ctx,
		`INSERT INTO "AuditLog" ("id", "action", "targetType", "targetId", "metadata", "createdAt") VALUES ($1, 'Retry requested', 'image', $2, '{}'::jsonb, NOW())`,
		uuid.NewString(), imageID,
	); err != nil {
		writeErr(w, err)
		return
	}
	if err := tx.Commit(ctx); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (s *Server) handleDashboardStats(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	policy, err := s.DB.OwnerPolicy(ctx)
	if err != nil {
		writeErr(w, err)
		return
	}
	var total, synced, failed, pending, groups, warnings int
	warnCutoff := time.Now().UTC().Add(-time.Duration(policy.FailedDays-1) * 24 * time.Hour)
	if err := s.DB.Pool.QueryRow(
		ctx,
		`SELECT
		   COUNT(*),
		   COUNT(*) FILTER (WHERE "uploadStatus" = 'SYNCED'::"UploadState"),
		   COUNT(*) FILTER (WHERE "uploadStatus" IN ('FAILED'::"UploadState", 'NEEDS_REVIEW'::"UploadState", 'UNRECOVERABLE'::"UploadState")),
		   COUNT(*) FILTER (WHERE "uploadStatus" NOT IN ('SYNCED'::"UploadState", 'NEEDS_REVIEW'::"UploadState", 'FAILED'::"UploadState", 'UNRECOVERABLE'::"UploadState")),
		   COUNT(*) FILTER (WHERE "uploadStatus" <> 'SYNCED'::"UploadState" AND "stagingPath" IS NOT NULL AND "createdAt" <= $1),
		   (SELECT COUNT(*) FROM "LineGroup" WHERE "enabled" = true)
		 FROM "Image"`,
		warnCutoff,
	).Scan(&total, &synced, &failed, &pending, &warnings, &groups); err != nil {
		writeErr(w, err)
		return
	}

	var oldest *time.Time
	_ = s.DB.Pool.QueryRow(
		ctx,
		`SELECT "createdAt" FROM "Image" WHERE "uploadStatus" <> 'SYNCED'::"UploadState" ORDER BY "createdAt" ASC LIMIT 1`,
	).Scan(&oldest)

	dirs := s.Store.Dirs()
	stagingBytes, _ := s.Store.Usage(dirs[0])
	thumbnailBytes, _ := s.Store.Usage(dirs[1])
	freeBytes, _ := s.Store.Free()
	diskPressure, _ := s.Store.Pressured(policy.CollectVideos)

	recent := make([]map[string]any, 0)
	rows, err := s.DB.Pool.Query(
		ctx,
		`SELECT "id", "action", "targetType", "targetId", "metadata", "createdAt" FROM "AuditLog" ORDER BY "createdAt" DESC LIMIT 10`,
	)
	if err == nil {
		defer rows.Close()
		for rows.Next() {
			var id, action, targetType, targetID string
			var meta []byte
			var createdAt time.Time
			if rows.Scan(&id, &action, &targetType, &targetID, &meta, &createdAt) == nil {
				var metaObj any = map[string]any{}
				_ = json.Unmarshal(meta, &metaObj)
				recent = append(recent, map[string]any{
					"id":         id,
					"action":     action,
					"targetType": targetType,
					"targetId":   targetID,
					"metadata":   metaObj,
					"createdAt":  fmtTime(createdAt),
				})
			}
		}
	}

	gStatus := "DISCONNECTED"
	if st, err := s.queryGoogleStatus(ctx); err == nil && st != nil {
		if sv, ok := st["status"].(string); ok && sv != "" {
			gStatus = sv
		}
	}

	out := map[string]any{
		"total":          total,
		"synced":         synced,
		"failed":         failed,
		"pending":        pending,
		"groups":         groups,
		"stagingBytes":   stagingBytes,
		"thumbnailBytes": thumbnailBytes,
		"freeBytes":      freeBytes,
		"diskPressure":   diskPressure,
		"warnings":       warnings,
		"recent":         recent,
		"google":         gStatus,
	}
	if oldest != nil {
		out["oldest"] = fmtTime(*oldest)
	}
	writeJSON(w, 200, out)
}

func openAPISpec() map[string]any {
	spec := map[string]any{
		"openapi": "3.0.3",
		"info": map[string]any{
			"title":   "RivoSnap",
			"version": "1.0.0",
		},
		"components": map[string]any{
			"securitySchemes": map[string]any{
				"session": map[string]any{
					"type": "apiKey",
					"in":   "cookie",
					"name": "session",
				},
			},
		},
		"paths": map[string]any{
			"/api/webhooks/line": map[string]any{
				"post": map[string]any{"tags": []string{"webhook"}, "summary": "POST /api/webhooks/line"},
			},
			"/api/v1/auth/status": map[string]any{
				"get": map[string]any{"tags": []string{"auth"}, "summary": "GET /auth/status"},
			},
			"/api/v1/auth/setup": map[string]any{
				"post": map[string]any{"tags": []string{"auth"}, "summary": "POST /auth/setup"},
			},
			"/api/v1/auth/login": map[string]any{
				"post": map[string]any{"tags": []string{"auth"}, "summary": "Verify password; issue a session or return mfaRequired with a pending attempt"},
			},
			"/api/v1/auth/logout": map[string]any{
				"post": map[string]any{"tags": []string{"auth"}, "summary": "POST /auth/logout"},
			},
			"/api/v1/health": map[string]any{
				"get": map[string]any{"tags": []string{"health"}, "summary": "GET /health"},
			},
			"/api/v1/settings": map[string]any{
				"get":   map[string]any{"tags": []string{"settings"}, "summary": "GET /settings"},
				"patch": map[string]any{"tags": []string{"settings"}, "summary": "PATCH /settings"},
			},
			"/api/v1/settings/line": map[string]any{
				"post": map[string]any{"tags": []string{"settings"}, "summary": "POST /settings/line"},
			},
			"/api/v1/google/status": map[string]any{
				"get": map[string]any{"tags": []string{"google"}, "summary": "GET /google/status"},
			},
			"/api/v1/google/connect": map[string]any{
				"get": map[string]any{"tags": []string{"google"}, "summary": "GET /google/connect"},
			},
			"/api/v1/google/callback": map[string]any{
				"get": map[string]any{"tags": []string{"google"}, "summary": "GET /google/callback"},
			},
			"/api/v1/google/validate": map[string]any{
				"post": map[string]any{"tags": []string{"google"}, "summary": "POST /google/validate"},
			},
			"/api/v1/google/disconnect": map[string]any{
				"post": map[string]any{"tags": []string{"google"}, "summary": "POST /google/disconnect"},
			},
			"/api/v1/groups": map[string]any{
				"get": map[string]any{"tags": []string{"groups"}, "summary": "GET /groups"},
			},
			"/api/v1/groups/{id}": map[string]any{
				"get":   map[string]any{"tags": []string{"groups"}, "summary": "GET /groups/:id"},
				"patch": map[string]any{"tags": []string{"groups"}, "summary": "PATCH /groups/:id"},
			},
			"/api/v1/groups/{id}/refresh": map[string]any{
				"post": map[string]any{"tags": []string{"groups"}, "summary": "POST /groups/:id/refresh"},
			},
			"/api/v1/groups/{id}/resolve-album": map[string]any{
				"post": map[string]any{"tags": []string{"groups"}, "summary": "POST /groups/:id/resolve-album (responds {ok, requeued})"},
			},
			"/api/v1/images": map[string]any{
				"get": map[string]any{"tags": []string{"images"}, "summary": "GET /images"},
			},
			"/api/v1/jobs": map[string]any{
				"get": map[string]any{"tags": []string{"jobs"}, "summary": "GET /jobs"},
			},
			"/api/v1/images/{id}": map[string]any{
				"get": map[string]any{"tags": []string{"images"}, "summary": "GET /images/:id"},
			},
			"/api/v1/images/{id}/thumbnail": map[string]any{
				"get": map[string]any{"tags": []string{"images"}, "summary": "GET /images/:id/thumbnail"},
			},
			"/api/v1/images/{id}/content": map[string]any{
				"get": map[string]any{"tags": []string{"images"}, "summary": "GET /images/:id/content"},
			},
			"/api/v1/jobs/{id}/discard": map[string]any{
				"post": map[string]any{"tags": []string{"jobs"}, "summary": "POST /jobs/:id/discard"},
			},
			"/api/v1/jobs/{id}/retry": map[string]any{
				"post": map[string]any{"tags": []string{"jobs"}, "summary": "POST /jobs/:id/retry"},
			},
			"/api/v1/dashboard/stats": map[string]any{
				"get": map[string]any{"tags": []string{"dashboard"}, "summary": "GET /dashboard/stats"},
			},
		},
	}
	paths := spec["paths"].(map[string]any)
	for path, route := range securityPaths {
		op := map[string]any{"tags": []string{"auth"}, "summary": route.summary}
		if !route.public {
			op["security"] = []any{map[string]any{"session": []string{}}}
		}
		paths[secBase+path] = map[string]any{strings.ToLower(route.method): op}
	}
	return spec
}
