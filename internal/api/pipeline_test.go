package api_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"

	"rivosnap/internal/api"
	"rivosnap/internal/config"
	"rivosnap/internal/crypto"
	"rivosnap/internal/db"
	"rivosnap/internal/google"
	"rivosnap/internal/media"
	"rivosnap/internal/testguard"
	"rivosnap/internal/worker"
)

type roundTripFunc func(req *http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func makeTestPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 20, 15))
	for y := 0; y < 15; y++ {
		for x := 0; x < 20; x++ {
			img.Set(x, y, color.RGBA{G: 128, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func jsonResponse(status int, v any) *http.Response {
	b, _ := json.Marshal(v)
	return &http.Response{
		StatusCode: status,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(bytes.NewReader(b)),
	}
}

func TestFullPipelineIntegration(t *testing.T) {
	testguard.Require(t)

	temp := t.TempDir()
	// Provide stub ffmpeg if host does not have ffmpeg installed
	if _, err := os.Stat("/usr/bin/ffmpeg"); err != nil {
		binDir := filepath.Join(temp, "bin")
		_ = os.MkdirAll(binDir, 0o755)
		var buf bytes.Buffer
		buf.WriteString("RIFF")
		vp8l := []byte{0x2f, 0x00, 0x00, 0x00, 0x00, 0x07, 0x10, 0xfd, 0x7f, 0x1d, 0x08, 0x00}
		buf.Write([]byte{byte(12 + len(vp8l)), 0, 0, 0})
		buf.WriteString("WEBPVP8L")
		buf.Write([]byte{byte(len(vp8l)), 0, 0, 0})
		buf.Write(vp8l)
		webpFixture := filepath.Join(binDir, "fixture.webp")
		_ = os.WriteFile(webpFixture, buf.Bytes(), 0o644)
		webpStub := "#!/bin/sh\nout=\"\"\nfor arg in \"$@\"; do out=\"$arg\"; done\ncp \"" + webpFixture + "\" \"$out\"\n"
		_ = os.WriteFile(filepath.Join(binDir, "ffmpeg"), []byte(webpStub), 0o755)
		t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	}
	// A missing ffprobe is a transient tool fault (AUD3-007); this stub fails on garbage like the real tool.
	if _, err := exec.LookPath("ffprobe"); err != nil {
		binDir := filepath.Join(temp, "probe-bin")
		_ = os.MkdirAll(binDir, 0o755)
		_ = os.WriteFile(filepath.Join(binDir, "ffprobe"), []byte("#!/bin/sh\nexit 1\n"), 0o755)
		t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	}
	env := config.Env{
		AppURL:                 "http://localhost",
		NodeEnv:                "test",
		DatabaseURL:            os.Getenv("DATABASE_URL"),
		RedisURL:               os.Getenv("REDIS_URL"),
		SessionSecret:          strings.Repeat("x", 32),
		BootstrapSecret:        strings.Repeat("b", 24),
		TokenEncryptionKey:     strings.Repeat("1", 64),
		LineChannelSecret:      "fixture",
		LineChannelAccessToken: "test",
		GoogleClientID:         "client",
		GoogleClientSecret:     "secret",
		GoogleRedirectURI:      "http://localhost/api/v1/google/callback",
		StagingPath:            filepath.Join(temp, "stage"),
		ThumbnailPath:          filepath.Join(temp, "thumb"),
		MaxImageSizeMB:         50,
		MaxVideoSizeMB:         200,
		MinFreeMB:              1,
		StagingRetentionHours:  24,
		FailedRetentionDays:    7,
		ThumbnailRetentionDays: 30,
	}

	ctx := context.Background()
	database, err := db.Connect(ctx, env)
	if err != nil {
		t.Fatalf("db.Connect: %v", err)
	}
	defer database.Close()
	if err := database.Migrate(ctx); err != nil {
		t.Fatalf("db.Migrate: %v", err)
	}
	// Clean tables for repeatable test run on same container
	_, _ = database.Pool.Exec(ctx, `TRUNCATE "UploadAttempt", "Image", "LineGroup", "WebhookEvent", "OutboxEvent", "AuditLog", "GoogleConnection", "Owner" CASCADE`)

	rOpts, err := redis.ParseURL(env.RedisURL)
	if err != nil {
		t.Fatalf("redis.ParseURL: %v", err)
	}
	rdb := redis.NewClient(rOpts)
	defer rdb.Close()
	_ = rdb.FlushDB(ctx).Err()

	store := media.NewStore(env)
	if err := store.InitDirs(); err != nil {
		t.Fatalf("store.InitDirs: %v", err)
	}

	srv := api.NewServer(env, database, rdb, store)
	handler := srv.Handler()
	proc := &worker.Processor{DB: database, Store: store, Redis: rdb}

	doReq := func(method, path string, headers map[string]string, body any) *httptest.ResponseRecorder {
		var rdr io.Reader
		switch v := body.(type) {
		case nil:
		case string:
			rdr = strings.NewReader(v)
		case []byte:
			rdr = bytes.NewReader(v)
		default:
			b, _ := json.Marshal(v)
			rdr = bytes.NewReader(b)
		}
		req := httptest.NewRequest(method, path, rdr)
		for k, val := range headers {
			req.Header.Set(k, val)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}

	origin := map[string]string{
		"Origin":            "http://localhost",
		"X-Gallery-Request": "1",
	}

	if res := doReq("GET", "/api/v1/images", nil, nil); res.Code != 401 {
		t.Fatalf("expected 401 unauthenticated, got %d", res.Code)
	}

	setupRes := doReq("POST", "/api/v1/auth/setup", origin, map[string]string{
		"email":     "owner@example.org",
		"password":  "long-fixture-password",
		"bootstrap": strings.Repeat("b", 24),
	})
	if setupRes.Code != 200 {
		t.Fatalf("setup failed (%d): %s", setupRes.Code, setupRes.Body.String())
	}
	var sessionCookie string
	for _, c := range setupRes.Result().Cookies() {
		if c.Name == "session" {
			sessionCookie = c.Value
		}
	}
	if sessionCookie == "" {
		t.Fatalf("missing session cookie on setup")
	}
	headers := map[string]string{
		"Origin":            "http://localhost",
		"X-Gallery-Request": "1",
		"Cookie":            "session=" + sessionCookie,
	}

	if res := doReq("POST", "/api/v1/auth/login", origin, map[string]string{
		"email":    "owner@example.org",
		"password": "wrong",
	}); res.Code != 401 {
		t.Fatalf("expected 401 on wrong password, got %d", res.Code)
	}

	mkEvent := func(evType, id, kind, source string) map[string]any {
		if kind == "" {
			kind = "image"
		}
		if source == "" {
			source = "group"
		}
		return map[string]any{
			"type":           evType,
			"webhookEventId": id,
			"timestamp":      time.Now().UnixMilli(),
			"source":         map[string]any{"type": source, "groupId": "group-a"},
			"message":        map[string]any{"type": kind, "id": "message-a"},
		}
	}

	sendWebhook := func(events []map[string]any, valid bool) *httptest.ResponseRecorder {
		raw, _ := json.Marshal(map[string]any{
			"destination": "channel",
			"events":      events,
		})
		sig := "wrong"
		if valid {
			mac := hmac.New(sha256.New, []byte("fixture"))
			mac.Write(raw)
			sig = base64.StdEncoding.EncodeToString(mac.Sum(nil))
		}
		return doReq("POST", "/api/webhooks/line", map[string]string{
			"Content-Type":     "application/json",
			"X-Line-Signature": sig,
		}, raw)
	}

	if res := sendWebhook([]map[string]any{}, false); res.Code != 403 {
		t.Fatalf("expected 403 on bad signature, got %d", res.Code)
	}
	if res := sendWebhook([]map[string]any{
		mkEvent("join", "j", "image", "group"),
		mkEvent("message", "disabled", "image", "group"),
	}, true); res.Code != 200 {
		t.Fatalf("expected 200 on join webhook, got %d: %s", res.Code, res.Body.String())
	}

	var imgCount int
	_ = database.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM "Image"`).Scan(&imgCount)
	if imgCount != 0 {
		t.Fatalf("expected 0 images while group disabled, got %d", imgCount)
	}

	var groupUUID, lineGroupID string
	if err := database.Pool.QueryRow(ctx, `SELECT "id", "lineGroupId" FROM "LineGroup" LIMIT 1`).Scan(&groupUUID, &lineGroupID); err != nil {
		t.Fatalf("expected LineGroup row: %v", err)
	}

	if res := doReq("PATCH", "/api/v1/groups/"+groupUUID, headers, map[string]bool{"enabled": true}); res.Code != 200 {
		t.Fatalf("enable group failed (%d): %s", res.Code, res.Body.String())
	}

	sendWebhook([]map[string]any{
		mkEvent("message", "video", "video", "group"),
		mkEvent("message", "direct", "image", "user"),
	}, true)
	_ = database.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM "Image"`).Scan(&imgCount)
	if imgCount != 0 {
		t.Fatalf("expected 0 images for video (disabled) and direct user message, got %d", imgCount)
	}

	// Simulate a delayed webhook without clock-dependent sleeps.
	if _, err := database.Pool.Exec(ctx, `UPDATE "LineGroup" SET "membershipAt"=NOW()-interval '3 hours', "enabledSince"=NOW()-interval '2 hours' WHERE id=$1`, groupUUID); err != nil {
		t.Fatal(err)
	}
	delayed := mkEvent("message", "img", "image", "group")
	delayed["timestamp"] = time.Now().Add(-time.Hour).UnixMilli()
	if res := doReq("PATCH", "/api/v1/groups/"+groupUUID, headers, map[string]bool{"enabled": true}); res.Code != 200 {
		t.Fatal(res.Body.String())
	}
	duplicate := mkEvent("message", "other-event", "image", "group")
	duplicate["timestamp"] = delayed["timestamp"]
	sendWebhook([]map[string]any{delayed, delayed, duplicate}, true)

	_ = database.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM "Image"`).Scan(&imgCount)
	if imgCount != 1 {
		t.Fatalf("expected 1 deduplicated image, got %d", imgCount)
	}

	for _, enabled := range []bool{false, true} {
		if res := doReq("PATCH", "/api/v1/groups/"+groupUUID, headers, map[string]bool{"enabled": enabled}); res.Code != 200 {
			t.Fatal(res.Body.String())
		}
	}
	delayed["webhookEventId"] = "prior-window"
	delayed["message"] = map[string]any{"type": "image", "id": "prior-window"}
	if res := sendWebhook([]map[string]any{delayed}, true); res.Code != 200 {
		t.Fatal(res.Body.String())
	}
	if err := database.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM "Image"`).Scan(&imgCount); err != nil || imgCount != 1 {
		t.Fatalf("old window accepted: count=%d err=%v", imgCount, err)
	}
	pngBytes := makeTestPNG(t)
	var (
		mu                sync.Mutex
		albums            int
		creates           int
		mode              = "success"
		matched           string
		groupName         = "Family"
		patchedAlbumTitle string
	)

	origTransport := http.DefaultTransport
	defer func() { http.DefaultTransport = origTransport }()
	http.DefaultTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		u := req.URL.String()
		var bodyBytes []byte
		if req.Body != nil {
			bodyBytes, _ = io.ReadAll(req.Body)
		}
		bodyStr := string(bodyBytes)

		mu.Lock()
		curMode := mode
		curGroupName := groupName
		mu.Unlock()

		if strings.Contains(u, "oauth2.googleapis.com") && curMode == "invalid-grant" {
			return jsonResponse(400, map[string]string{"error": "invalid_grant"}), nil
		}
		if strings.Contains(u, "oauth2.googleapis.com") && strings.Contains(bodyStr, "authorization_code") {
			return jsonResponse(200, map[string]string{
				"refresh_token": "refresh",
				"scope":         strings.Join(google.Scopes, " "),
				"access_token":  "access",
			}), nil
		}
		if strings.HasSuffix(u, "albums?pageSize=1") {
			return jsonResponse(200, map[string]any{}), nil
		}
		if strings.Contains(u, "oauth2.googleapis.com") {
			return jsonResponse(200, map[string]string{"access_token": "access"}), nil
		}
		if strings.Contains(u, "photoslibrary.googleapis.com/v1/mediaItems/media-1") {
			return jsonResponse(200, map[string]string{
				"id":      "media-1",
				"baseUrl": "https://lh3.googleusercontent.com/fixture-media-1",
			}), nil
		}
		if strings.HasPrefix(u, "https://lh3.googleusercontent.com/fixture-media-1=w") {
			return &http.Response{
				StatusCode: 200,
				Header:     http.Header{"Content-Type": []string{"image/png"}},
				Body:       io.NopCloser(bytes.NewReader(pngBytes)),
			}, nil
		}
		if strings.HasPrefix(u, "https://lh3.googleusercontent.com/fixture-media-1=dv") {
			rng := req.Header.Get("Range")
			if rng != "" {
				return &http.Response{
					StatusCode: 206,
					Header: http.Header{
						"Content-Type":   []string{"video/mp4"},
						"Content-Range":  []string{"bytes 0-6/100"},
						"Accept-Ranges":  []string{"bytes"},
						"Content-Length": []string{"7"},
					},
					Body: io.NopCloser(strings.NewReader("partial")),
				}, nil
			}
			return &http.Response{
				StatusCode: 200,
				Header:     http.Header{"Content-Type": []string{"video/mp4"}},
				Body:       io.NopCloser(strings.NewReader("full-video")),
			}, nil
		}
		if strings.Contains(u, "api-data.line.me") && curMode == "preparing" {
			return jsonResponse(202, map[string]any{}), nil
		}
		if strings.Contains(u, "api-data.line.me") && curMode == "throttle" {
			return &http.Response{
				StatusCode: 429,
				Header:     http.Header{"Retry-After": []string{"60"}},
				Body:       io.NopCloser(strings.NewReader("slow")),
			}, nil
		}
		if strings.Contains(u, "api-data.line.me") && curMode == "invalid" {
			return &http.Response{
				StatusCode: 200,
				Body:       io.NopCloser(strings.NewReader("not a photo")),
			}, nil
		}
		if strings.Contains(u, "api-data.line.me") {
			return &http.Response{
				StatusCode: 200,
				Body:       io.NopCloser(bytes.NewReader(pngBytes)),
			}, nil
		}
		if strings.HasSuffix(u, "/summary") {
			return jsonResponse(200, map[string]string{"groupName": curGroupName}), nil
		}
		if strings.HasSuffix(u, "/albums/album?updateMask=title") && req.Method == http.MethodPatch {
			var p struct {
				Title string `json:"title"`
			}
			_ = json.Unmarshal(bodyBytes, &p)
			mu.Lock()
			patchedAlbumTitle = p.Title
			mu.Unlock()
			return jsonResponse(200, map[string]string{"id": "album", "title": p.Title}), nil
		}
		if strings.HasSuffix(u, "/albums") {
			mu.Lock()
			albums++
			mu.Unlock()
			return jsonResponse(200, map[string]string{"id": "album"}), nil
		}
		if strings.HasSuffix(u, "/albums/album") {
			return jsonResponse(200, map[string]string{"id": "album", "mediaItemsCount": "1"}), nil
		}
		if strings.HasSuffix(u, "/uploads") {
			return &http.Response{
				StatusCode: 200,
				Body:       io.NopCloser(strings.NewReader("upload")),
			}, nil
		}
		if strings.HasSuffix(u, ":batchCreate") {
			var b struct {
				NewMediaItems []struct {
					SimpleMediaItem struct {
						FileName string `json:"fileName"`
					} `json:"simpleMediaItem"`
				} `json:"newMediaItems"`
			}
			_ = json.Unmarshal(bodyBytes, &b)
			mu.Lock()
			creates++
			if len(b.NewMediaItems) > 0 {
				matched = b.NewMediaItems[0].SimpleMediaItem.FileName
			}
			mu.Unlock()
			if curMode == "ambiguous" {
				return nil, io.ErrUnexpectedEOF
			}
			if curMode == "partial" {
				return jsonResponse(200, map[string]any{
					"newMediaItemResults": []map[string]any{
						{"status": map[string]any{"code": 7}},
					},
				}), nil
			}
			return jsonResponse(200, map[string]any{
				"newMediaItemResults": []map[string]any{
					{"status": map[string]any{}, "mediaItem": map[string]string{"id": "media-1"}},
				},
			}), nil
		}
		if strings.HasSuffix(u, ":search") {
			mu.Lock()
			mFile := matched
			mu.Unlock()
			items := []map[string]string{}
			if curMode == "found" {
				items = append(items, map[string]string{"id": "reconciled", "filename": mFile})
			}
			return jsonResponse(200, map[string]any{"mediaItems": items}), nil
		}
		t.Fatalf("unexpected URL in mock transport: %s", u)
		return nil, nil
	})

	consentRes := doReq("GET", "/api/v1/google/connect", headers, nil)
	loc, _ := url.Parse(consentRes.Header().Get("Location"))
	oauthState := loc.Query().Get("state")
	cbURL := "/api/v1/google/callback?code=code&state=" + oauthState
	if res := doReq("GET", cbURL, headers, nil); res.Code != 302 {
		t.Fatalf("expected 302 on OAuth callback, got %d: %s", res.Code, res.Body.String())
	}
	if res := doReq("GET", cbURL, headers, nil); res.Code != 302 || res.Header().Get("Location") != "/settings?google_error=state" {
		t.Fatalf("expected state-error redirect on replayed OAuth state, got %d %q", res.Code, res.Header().Get("Location"))
	}

	runNextOutbox := func() {
		var outboxID string
		if err := database.Pool.QueryRow(ctx, `SELECT id FROM "OutboxEvent" ORDER BY "createdAt" ASC LIMIT 1`).Scan(&outboxID); err != nil {
			t.Fatalf("expected outbox event: %v", err)
		}
		if err := proc.ProcessEvent(ctx, outboxID); err != nil {
			t.Fatalf("ProcessEvent(%s) failed: %v", outboxID, err)
		}
	}

	runNextOutbox() // GROUP_SUMMARY
	mode = "preparing"
	runNextOutbox()
	var pendingID string
	if err := database.Pool.QueryRow(ctx, `SELECT id FROM "Image" WHERE "uploadStatus"='RETRY_PENDING' AND "stagingPath" IS NULL AND "errorCode"='LINE_PREPARING'`).Scan(&pendingID); err != nil {
		t.Fatal(err)
	}
	pendingPath, err := store.PathFor(pendingID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(pendingPath); !os.IsNotExist(err) {
		t.Fatalf("preparation response staged: %v", err)
	}
	mode = "success"
	runNextOutbox() // PROCESS (download & stage)

	var stagedID, stagedMime string
	var stagedWidth int
	var stagedPath *string
	if err := database.Pool.QueryRow(
		ctx,
		`SELECT "id", "mimeType", "width", "stagingPath" FROM "Image" LIMIT 1`,
	).Scan(&stagedID, &stagedMime, &stagedWidth, &stagedPath); err != nil {
		t.Fatalf("load staged image: %v", err)
	}
	if stagedMime != "image/png" || stagedWidth != 20 {
		t.Fatalf("expected image/png 20px, got %s %d", stagedMime, stagedWidth)
	}

	gRow := worker.LineGroupRow{ID: groupUUID, LineGroupID: lineGroupID}
	var wg sync.WaitGroup
	wg.Add(2)
	for range 2 {
		go func() {
			defer wg.Done()
			_, _ = proc.ResolveAlbum(ctx, gRow, "access")
		}()
	}
	wg.Wait()
	if albums != 1 {
		t.Fatalf("expected exactly 1 album creation under concurrency, got %d", albums)
	}

	runNextOutbox() // UPLOAD

	groupName = "Family 2026"
	refreshRes := doReq("POST", "/api/v1/groups/"+groupUUID+"/refresh", headers, nil)
	if refreshRes.Code != 200 {
		t.Fatalf("refresh group failed (%d): %s", refreshRes.Code, refreshRes.Body.String())
	}
	if patchedAlbumTitle != "LINE - Family 2026" {
		t.Fatalf("expected patched album title 'LINE - Family 2026', got %q", patchedAlbumTitle)
	}

	// Legacy scope fallback test
	_, _ = database.Pool.Exec(
		ctx,
		`UPDATE "GoogleConnection" SET "scopes" = 'https://www.googleapis.com/auth/photoslibrary.appendonly https://www.googleapis.com/auth/photoslibrary.readonly.appcreateddata' WHERE id = 1`,
	)
	groupName = "Family Legacy Scope"
	legacyRes := doReq("POST", "/api/v1/groups/"+groupUUID+"/refresh", headers, nil)
	if legacyRes.Code != 200 || !strings.Contains(legacyRes.Body.String(), "Reconnect Google Photos") {
		t.Fatalf("unexpected legacy scope refresh response: %s", legacyRes.Body.String())
	}
	_, _ = database.Pool.Exec(ctx, `UPDATE "GoogleConnection" SET "scopes" = $1 WHERE id = 1`, strings.Join(google.Scopes, " "))

	syncedList := doReq("GET", "/api/v1/images?status=SYNCED&group=group-a", headers, nil)
	if syncedList.Code != 200 || !strings.Contains(syncedList.Body.String(), `"total":1`) {
		t.Fatalf("expected 1 synced image, got %s", syncedList.Body.String())
	}

	thumbRes := doReq("GET", "/api/v1/images/"+stagedID+"/thumbnail", headers, nil)
	if thumbRes.Code != 200 {
		t.Fatalf("expected 200 thumbnail, got %d", thumbRes.Code)
	}

	for _, st := range []string{"partial", "ambiguous"} {
		mode = st
		e := mkEvent("message", st, "image", "group")
		e["message"] = map[string]any{"type": "image", "id": st}
		sendWebhook([]map[string]any{e}, true)
		runNextOutbox()
		runNextOutbox()

		var rowID, rowStatus string
		if err := database.Pool.QueryRow(ctx, `SELECT "id", "uploadStatus"::text FROM "Image" WHERE "lineMessageId" = $1`, st).Scan(&rowID, &rowStatus); err != nil {
			t.Fatalf("query image %s: %v", st, err)
		}
		want := "NEEDS_REVIEW"
		if st == "partial" {
			want = "FAILED"
		}
		if rowStatus != want {
			t.Fatalf("expected %s for %s, got %s", want, st, rowStatus)
		}
		if st == "ambiguous" {
			mode = "invalid-grant"
			database.ClearAccessCache()
			doReq("POST", "/api/v1/jobs/"+rowID+"/retry", headers, nil)
			runNextOutbox()
			_ = database.Pool.QueryRow(ctx, `SELECT "uploadStatus"::text FROM "Image" WHERE id = $1`, rowID).Scan(&rowStatus)
			if rowStatus != "NEEDS_REVIEW" {
				t.Fatalf("expected NEEDS_REVIEW to persist on invalid-grant, got %s", rowStatus)
			}
			mode = "found"
			doReq("POST", "/api/v1/jobs/"+rowID+"/retry", headers, nil)
			runNextOutbox()
			var mediaID *string
			_ = database.Pool.QueryRow(ctx, `SELECT "googleMediaItemId" FROM "Image" WHERE id = $1`, rowID).Scan(&mediaID)
			if mediaID == nil || *mediaID != "reconciled" {
				t.Fatalf("expected reconciled media ID, got %v", mediaID)
			}
		}
	}
	if creates != 3 {
		t.Fatalf("expected 3 batchCreate calls (no duplicate upload on reconcile), got %d", creates)
	}

	if err := proc.Cleanup(ctx, time.Now().Add(31*24*time.Hour)); err != nil {
		t.Fatalf("Cleanup failed: %v", err)
	}
	if _, err := os.Stat(*stagedPath); !os.IsNotExist(err) {
		t.Fatalf("expected synced staged file to be cleaned up")
	}

	remoteThumb := doReq("GET", "/api/v1/images/"+stagedID+"/thumbnail?size=320", headers, nil)
	if remoteThumb.Code != 200 {
		t.Fatalf("expected 200 on remote thumbnail fetch, got %d", remoteThumb.Code)
	}

	// Video setting + unsupported video + Range streaming
	if res := doReq("PATCH", "/api/v1/settings", headers, map[string]any{"collectVideos": true}); res.Code != 200 {
		t.Fatalf("enable collectVideos failed: %d", res.Code)
	}
	mode = "invalid"
	badVid := mkEvent("message", "bad-video", "video", "group")
	badVid["message"] = map[string]any{"type": "video", "id": "bad-video"}
	sendWebhook([]map[string]any{badVid}, true)
	runNextOutbox()
	var vidMediaType, vidStatus, vidErrCode string
	if err := database.Pool.QueryRow(
		ctx,
		`SELECT "mediaType", "uploadStatus"::text, "errorCode" FROM "Image" WHERE "lineMessageId" = 'bad-video'`,
	).Scan(&vidMediaType, &vidStatus, &vidErrCode); err != nil {
		t.Fatalf("query bad-video: %v", err)
	}
	if vidMediaType != "VIDEO" || vidStatus != "UNRECOVERABLE" || vidErrCode != "UNSUPPORTED_VIDEO" {
		t.Fatalf("unexpected bad-video state: %s %s %s", vidMediaType, vidStatus, vidErrCode)
	}
	badImg := mkEvent("message", "bad-image", "image", "group")
	badImg["message"] = map[string]any{"type": "image", "id": "bad-image"}
	sendWebhook([]map[string]any{badImg}, true)
	runNextOutbox()
	// Undecodable originals are quarantined for owner review, never silently deleted.
	for _, msgID := range []string{"bad-video", "bad-image"} {
		var status string
		var keptPath *string
		if err := database.Pool.QueryRow(
			ctx,
			`SELECT "uploadStatus"::text, "stagingPath" FROM "Image" WHERE "lineMessageId" = $1`, msgID,
		).Scan(&status, &keptPath); err != nil {
			t.Fatalf("query %s: %v", msgID, err)
		}
		if status != "UNRECOVERABLE" || keptPath == nil {
			t.Fatalf("%s: expected UNRECOVERABLE with stagingPath, got %s %v", msgID, status, keptPath)
		}
		if _, err := os.Stat(*keptPath); err != nil {
			t.Fatalf("%s: quarantined original missing: %v", msgID, err)
		}
	}

	_, _ = database.Pool.Exec(ctx, `UPDATE "Image" SET "mediaType" = 'VIDEO', "mimeType" = 'video/mp4' WHERE id = $1`, stagedID)
	rangeRes := doReq("GET", "/api/v1/images/"+stagedID+"/content", map[string]string{
		"Cookie": "session=" + sessionCookie,
		"Range":  "bytes=0-6",
	}, nil)
	if rangeRes.Code != 206 || rangeRes.Header().Get("Content-Range") != "bytes 0-6/100" || rangeRes.Body.String() != "partial" {
		t.Fatalf("unexpected Range response: code=%d cr=%s body=%q", rangeRes.Code, rangeRes.Header().Get("Content-Range"), rangeRes.Body.String())
	}
	t.Run("local media ranges", func(t *testing.T) {
		path, err := store.PathFor(stagedID, 0)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		data := []byte("0123456789abcdefghijklmnopqrstuvwxyz")
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := database.Pool.Exec(ctx, `UPDATE "Image" SET "stagingPath" = $2 WHERE id = $1`, stagedID, path); err != nil {
			t.Fatal(err)
		}
		defer func() {
			_ = os.Remove(path)
			_, _ = database.Pool.Exec(ctx, `UPDATE "Image" SET "stagingPath" = NULL WHERE id = $1`, stagedID)
		}()
		n := len(data)
		get := func(rng string) *httptest.ResponseRecorder {
			h := map[string]string{"Cookie": "session=" + sessionCookie}
			if rng != "" {
				h["Range"] = rng
			}
			return doReq("GET", "/api/v1/images/"+stagedID+"/content", h, nil)
		}
		if res := get("bytes=-10"); res.Code != 206 ||
			res.Header().Get("Content-Range") != fmt.Sprintf("bytes %d-%d/%d", n-10, n-1, n) ||
			res.Body.String() != string(data[n-10:]) {
			t.Fatalf("suffix range: code=%d cr=%q body=%q", res.Code, res.Header().Get("Content-Range"), res.Body.String())
		}
		if res := get(fmt.Sprintf("bytes=%d-", n+100)); res.Code != 416 {
			t.Fatalf("unsatisfiable range: expected 416, got %d", res.Code)
		}
		if res := get("bytes=0-9"); res.Code != 206 || res.Body.String() != string(data[:10]) {
			t.Fatalf("first range: code=%d body=%q", res.Code, res.Body.String())
		}
		if res := get(""); res.Code != 200 || res.Body.Len() != n || res.Header().Get("Content-Type") != "video/mp4" {
			t.Fatalf("full body: code=%d len=%d ct=%q", res.Code, res.Body.Len(), res.Header().Get("Content-Type"))
		}
	})
	t.Run("discard retries failed unlink", func(t *testing.T) {
		path, err := store.PathFor(stagedID, 0)
		if err != nil {
			t.Fatal(err)
		}
		// A nonempty directory reliably produces an unlink error, even as root.
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
		child := filepath.Join(path, "fixture")
		if err := os.WriteFile(child, nil, 0600); err != nil {
			t.Fatal(err)
		}
		const event = "discard-fixture"
		if _, err := database.Pool.Exec(ctx, `INSERT INTO "OutboxEvent" (id,"eventType","aggregateId") VALUES ($1,'DISCARD',$2)`, event, stagedID); err != nil {
			t.Fatal(err)
		}
		if err := proc.ProcessEvent(ctx, event); err == nil {
			t.Fatal("acknowledged failed unlink")
		}
		var exists bool
		if err := database.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT FROM "OutboxEvent" WHERE id=$1)`, event).Scan(&exists); err != nil || !exists {
			t.Fatalf("lost pending discard: %v", err)
		}
		if err := os.Remove(child); err != nil {
			t.Fatal(err)
		}
		if err := proc.ProcessEvent(ctx, event); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("discard retry did not remove path: %v", err)
		}
	})
	t.Run("heartbeat continues during blocked claim", func(t *testing.T) {
		tx, err := database.Pool.Begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback(ctx)
		if _, err := tx.Exec(ctx, `LOCK TABLE "OutboxEvent" IN ACCESS EXCLUSIVE MODE`); err != nil {
			t.Fatal(err)
		}
		if err := rdb.Del(ctx, "worker:heartbeat").Err(); err != nil {
			t.Fatal(err)
		}
		child, cancel := context.WithCancel(ctx)
		done := make(chan error, 1)
		go func() { done <- proc.RunLoop(child) }()
		defer func() {
			cancel()
			select {
			case <-done:
			case <-time.After(4 * time.Second):
				t.Error("worker did not stop")
			}
		}()
		first := ""
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			v, err := rdb.Get(ctx, "worker:heartbeat").Result()
			if err == nil && v != "" {
				if first != "" && first != v {
					var free bool
					if err := tx.QueryRow(ctx, "SELECT pg_try_advisory_xact_lock(72707370)").Scan(&free); err != nil || free {
						t.Fatalf("active worker did not reserve the global slot: %v", err)
					}
					return
				}
				first = v
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatal("no heartbeat renewal while outbox claim blocked")
	})

}

func crashLoopFixture(t *testing.T, state string, attempts int) (*db.DB, *worker.Processor, string, string) {
	t.Helper()
	testguard.Require(t)
	temp := t.TempDir()
	env := config.Env{
		DatabaseURL:    os.Getenv("DATABASE_URL"),
		StagingPath:    filepath.Join(temp, "stage"),
		ThumbnailPath:  filepath.Join(temp, "thumb"),
		MaxImageSizeMB: 50,
		MaxVideoSizeMB: 200,
		MinFreeMB:      1,
	}
	ctx := context.Background()
	database, err := db.Connect(ctx, env)
	if err != nil {
		t.Fatalf("db.Connect: %v", err)
	}
	t.Cleanup(database.Close)
	if err := database.Migrate(ctx); err != nil {
		t.Fatalf("db.Migrate: %v", err)
	}
	store := media.NewStore(env)
	if err := store.InitDirs(); err != nil {
		t.Fatal(err)
	}
	lineGroupID := "crash-" + uuid.NewString()
	imageID := uuid.NewString()
	outboxID := uuid.NewString()
	stmts := []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO "LineGroup" ("id", "lineGroupId", "enabled", "membershipAt", "lastEventAt", "updatedAt") VALUES ($1, $2, true, NOW(), NOW(), NOW())`, []any{uuid.NewString(), lineGroupID}},
		{`INSERT INTO "Image" ("id", "lineGroupId", "channelId", "lineMessageId", "lineEventTimestamp", "uploadStatus", "updatedAt") VALUES ($1, $2, 'channel', $3, NOW(), $4::"UploadState", NOW())`, []any{imageID, lineGroupID, uuid.NewString(), state}},
		{`INSERT INTO "OutboxEvent" ("id", "eventType", "aggregateId", "payload", "createdAt", "availableAt") VALUES ($1, 'PROCESS', $2, '{}'::jsonb, NOW(), NOW())`, []any{outboxID, imageID}},
	}
	for n := 1; n <= attempts; n++ {
		stmts = append(stmts, struct {
			sql  string
			args []any
		}{`INSERT INTO "UploadAttempt" ("id", "imageId", "attemptNumber", "status", "startedAt") VALUES ($1, $2, $3, 'ACTIVE', NOW())`, []any{uuid.NewString(), imageID, n}})
	}
	for _, st := range stmts {
		if _, err := database.Pool.Exec(ctx, st.sql, st.args...); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	return database, &worker.Processor{DB: database, Store: store}, imageID, outboxID
}

func assertCrashLoopStopped(t *testing.T, state, wantStatus string) {
	ctx := context.Background()
	database, proc, imageID, outboxID := crashLoopFixture(t, state, 3)
	if err := proc.ProcessEvent(ctx, outboxID); err != nil {
		t.Fatalf("ProcessEvent: %v", err)
	}
	var status, code string
	if err := database.Pool.QueryRow(ctx, `SELECT "uploadStatus"::text, "errorCode" FROM "Image" WHERE id = $1`, imageID).Scan(&status, &code); err != nil {
		t.Fatal(err)
	}
	if status != wantStatus || code != "WORKER_INTERRUPTED" {
		t.Fatalf("expected %s/WORKER_INTERRUPTED, got %s/%s", wantStatus, status, code)
	}
	var abandoned, open, outbox int
	if err := database.Pool.QueryRow(ctx, `SELECT
		COUNT(*) FILTER (WHERE "status" = 'ABANDONED' AND "completedAt" IS NOT NULL),
		COUNT(*) FILTER (WHERE "completedAt" IS NULL)
		FROM "UploadAttempt" WHERE "imageId" = $1`, imageID).Scan(&abandoned, &open); err != nil {
		t.Fatal(err)
	}
	if abandoned != 3 || open != 0 {
		t.Fatalf("expected 3 abandoned and 0 open attempts, got %d and %d", abandoned, open)
	}
	if err := database.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM "OutboxEvent" WHERE "aggregateId" = $1`, imageID).Scan(&outbox); err != nil {
		t.Fatal(err)
	}
	if outbox != 0 {
		t.Fatalf("crash loop left %d outbox events", outbox)
	}
}

func TestCrashLoopEndsInFailed(t *testing.T) {
	assertCrashLoopStopped(t, "DOWNLOAD_PENDING", "FAILED")
}

func TestCrashLoopInCreatingMediaGoesToReview(t *testing.T) {
	assertCrashLoopStopped(t, "CREATING_MEDIA", "NEEDS_REVIEW")
}

// A crash after Stage kept undecodable bytes but before the failure commit leaves
// the file at the staging path with "stagingPath" NULL; it must stay discardable.
func assertOrphanQuarantined(t *testing.T, attempts int, wantStatus, wantCode string) {
	ctx := context.Background()
	database, proc, imageID, outboxID := crashLoopFixture(t, "DOWNLOADING", attempts)
	path, err := proc.Store.PathFor(imageID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("not a photo"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := proc.ProcessEvent(ctx, outboxID); err != nil {
		t.Fatalf("ProcessEvent: %v", err)
	}
	var status, code string
	var kept *string
	if err := database.Pool.QueryRow(ctx, `SELECT "uploadStatus"::text, "errorCode", "stagingPath" FROM "Image" WHERE id = $1`, imageID).Scan(&status, &code, &kept); err != nil {
		t.Fatal(err)
	}
	if status != wantStatus || code != wantCode || kept == nil || *kept != path {
		t.Fatalf("expected %s/%s with stagingPath %s, got %s/%s %v", wantStatus, wantCode, path, status, code, kept)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("original missing: %v", err)
	}
}

func TestRestageInspectFailureKeepsStagingPath(t *testing.T) {
	assertOrphanQuarantined(t, 0, "UNRECOVERABLE", "UNSUPPORTED_IMAGE")
}

func TestCrashLoopKeepsStagedFileDiscardable(t *testing.T) {
	assertOrphanQuarantined(t, 3, "FAILED", "WORKER_INTERRUPTED")
}

// thumbnailFixture serves the real handler with a stub ffmpeg that logs "S" on start and
// "E" on end (atomic O_APPEND lines), so overlap is read from log order, not timing.
type integrationEnv struct {
	ctx     context.Context
	db      *db.DB
	store   *media.Store
	handler http.Handler
	session string
	dir     string
	env     config.Env
	server  *api.Server
}

// integrationServer builds an API server on the disposable Postgres/Redis, resets state and signs the owner in.
func integrationServer(t *testing.T) integrationEnv {
	t.Helper()
	testguard.Require(t)
	temp := t.TempDir()
	env := config.Env{
		AppURL:                 "http://localhost",
		NodeEnv:                "test",
		DatabaseURL:            os.Getenv("DATABASE_URL"),
		RedisURL:               os.Getenv("REDIS_URL"),
		SessionSecret:          strings.Repeat("x", 32),
		BootstrapSecret:        strings.Repeat("b", 24),
		TokenEncryptionKey:     strings.Repeat("1", 64),
		LineChannelAccessToken: "test",
		GoogleClientID:         "client",
		GoogleClientSecret:     "secret",
		GoogleRedirectURI:      "http://localhost/api/v1/google/callback",
		StagingPath:            filepath.Join(temp, "stage"),
		ThumbnailPath:          filepath.Join(temp, "thumb"),
		MaxImageSizeMB:         50,
		MaxVideoSizeMB:         200,
		MinFreeMB:              1,
		ThumbnailRetentionDays: 30,
	}
	ctx := context.Background()
	database, err := db.Connect(ctx, env)
	if err != nil {
		t.Fatalf("db.Connect: %v", err)
	}
	t.Cleanup(database.Close)
	if err := database.Migrate(ctx); err != nil {
		t.Fatalf("db.Migrate: %v", err)
	}
	_, _ = database.Pool.Exec(ctx, `TRUNCATE "UploadAttempt", "Image", "LineGroup", "WebhookEvent", "OutboxEvent", "AuditLog", "GoogleConnection", "Owner" CASCADE`)
	rOpts, err := redis.ParseURL(env.RedisURL)
	if err != nil {
		t.Fatalf("redis.ParseURL: %v", err)
	}
	rdb := redis.NewClient(rOpts)
	t.Cleanup(func() { _ = rdb.Close() })
	_ = rdb.FlushDB(ctx).Err()
	store := media.NewStore(env)
	if err := store.InitDirs(); err != nil {
		t.Fatal(err)
	}
	server := api.NewServer(env, database, rdb, store)
	handler := server.Handler()

	setup := httptest.NewRequest("POST", "/api/v1/auth/setup", strings.NewReader(
		`{"email":"owner@example.org","password":"long-fixture-password","bootstrap":"`+strings.Repeat("b", 24)+`"}`))
	setup.Header.Set("Origin", "http://localhost")
	setup.Header.Set("X-Gallery-Request", "1")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, setup)
	if rec.Code != 200 {
		t.Fatalf("setup failed (%d): %s", rec.Code, rec.Body.String())
	}
	var session string
	for _, c := range rec.Result().Cookies() {
		if c.Name == "session" {
			session = c.Value
		}
	}
	if session == "" {
		t.Fatal("missing session cookie on setup")
	}
	return integrationEnv{ctx, database, store, handler, session, temp, env, server}
}

func thumbnailFixture(t *testing.T, remote bool) (get func(ctx context.Context, id string) *httptest.ResponseRecorder, seed func() string, ffmpegLog string) {
	t.Helper()
	srv := integrationServer(t)
	ctx, database, store, handler, session, temp := srv.ctx, srv.db, srv.store, srv.handler, srv.session, srv.dir
	binDir := filepath.Join(temp, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	buf.WriteString("RIFF")
	vp8l := []byte{0x2f, 0x00, 0x00, 0x00, 0x00, 0x07, 0x10, 0xfd, 0x7f, 0x1d, 0x08, 0x00}
	buf.Write([]byte{byte(12 + len(vp8l)), 0, 0, 0})
	buf.WriteString("WEBPVP8L")
	buf.Write([]byte{byte(len(vp8l)), 0, 0, 0})
	buf.Write(vp8l)
	fixture := filepath.Join(binDir, "fixture.webp")
	if err := os.WriteFile(fixture, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	ffmpegLog = filepath.Join(temp, "ffmpeg.log")
	script := "#!/bin/sh\nout=\"\"\nfor arg in \"$@\"; do out=\"$arg\"; done\n" +
		"echo S >> \"" + ffmpegLog + "\"\nsleep 0.5\ncp \"" + fixture + "\" \"$out\"\necho E >> \"" + ffmpegLog + "\"\n"
	if err := os.WriteFile(filepath.Join(binDir, "ffmpeg"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	lineGroupID := "thumb-" + uuid.NewString()
	if _, err := database.Pool.Exec(ctx, `INSERT INTO "LineGroup" ("id", "lineGroupId", "enabled", "membershipAt", "lastEventAt", "updatedAt") VALUES ($1, $2, true, NOW(), NOW(), NOW())`, uuid.NewString(), lineGroupID); err != nil {
		t.Fatalf("seed group: %v", err)
	}
	if remote {
		encrypted, err := crypto.Seal("fixture", database.Env.TokenEncryptionKey)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := database.Pool.Exec(ctx, `INSERT INTO "GoogleConnection" ("ownerId","scopes","status","encryptedRefreshToken") VALUES(1,'','CONNECTED',$1)`, encrypted); err != nil {
			t.Fatal(err)
		}
		var seen sync.Map
		previous := http.DefaultTransport
		t.Cleanup(func() { http.DefaultTransport = previous })
		http.DefaultTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
			switch req.URL.Host {
			case "oauth2.googleapis.com":
				return jsonResponse(200, map[string]string{"access_token": "token"}), nil
			case "photoslibrary.googleapis.com":
				if _, duplicate := seen.LoadOrStore(req.URL.Path, true); duplicate {
					t.Errorf("duplicate remote fetch: %s", req.URL.Path)
				}
				return jsonResponse(200, map[string]string{"id": "item", "baseUrl": "https://lh3.googleusercontent.com/item"}), nil
			case "lh3.googleusercontent.com":
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"image/webp"}}, Body: io.NopCloser(bytes.NewReader(buf.Bytes()))}, nil
			}
			return nil, fmt.Errorf("unexpected host %q", req.URL.Host)
		})
	}
	seed = func() string {
		id := uuid.NewString()
		if remote {
			if _, err := database.Pool.Exec(ctx, `INSERT INTO "Image" ("id","lineGroupId","channelId","lineMessageId","lineEventTimestamp","uploadStatus","googleMediaItemId","updatedAt") VALUES($1,$2,'channel',$1,NOW(),'SYNCED',$1,NOW())`, id, lineGroupID); err != nil {
				t.Fatal(err)
			}
			return id
		}

		path, err := store.PathFor(id, 0)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("original"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := database.Pool.Exec(ctx, `INSERT INTO "Image" ("id", "lineGroupId", "channelId", "lineMessageId", "lineEventTimestamp", "uploadStatus", "stagingPath", "mimeType", "updatedAt") VALUES ($1, $2, 'channel', $3, NOW(), 'STAGED'::"UploadState", $4, 'image/png', NOW())`, id, lineGroupID, uuid.NewString(), path); err != nil {
			t.Fatalf("seed image: %v", err)
		}
		return id
	}
	get = func(ctx context.Context, id string) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", "/api/v1/images/"+id+"/thumbnail", nil).WithContext(ctx)
		req.Header.Set("Cookie", "session="+session)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		return rec
	}
	return get, seed, ffmpegLog
}

func TestThumbnailConcurrencyBounded(t *testing.T) {
	for _, remote := range []bool{false, true} {
		t.Run(fmt.Sprintf("remote=%t", remote), func(t *testing.T) { checkThumbnailConcurrency(t, remote) })
	}
}

func checkThumbnailConcurrency(t *testing.T, remote bool) {
	get, seed, ffmpegLog := thumbnailFixture(t, remote)

	fanOut := func(ids []string) {
		t.Helper()
		var wg sync.WaitGroup
		codes := make([]int, len(ids))
		for i, id := range ids {
			wg.Add(1)
			go func() {
				defer wg.Done()
				codes[i] = get(context.Background(), id).Code
			}()
		}
		wg.Wait()
		for i, c := range codes {
			if c != 200 {
				t.Fatalf("request %d: expected 200, got %d", i, c)
			}
		}
	}

	distinct := make([]string, 10)
	for i := range distinct {
		distinct[i] = seed()
	}
	fanOut(distinct)
	raw, err := os.ReadFile(ffmpegLog)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Fields(string(raw))
	running, peak, starts := 0, 0, 0
	for _, l := range lines {
		if l == "S" {
			running++
			starts++
			peak = max(peak, running)
		} else {
			running--
		}
	}
	if starts != 10 {
		t.Fatalf("expected 10 ffmpeg runs for 10 ids, got %d", starts)
	}
	if peak > 2 {
		t.Fatalf("expected at most 2 concurrent ffmpeg runs, saw %d", peak)
	}

	if err := os.Remove(ffmpegLog); err != nil {
		t.Fatal(err)
	}
	one := seed()
	fanOut([]string{one, one, one, one, one, one, one, one, one, one})
	raw, err = os.ReadFile(ffmpegLog)
	if err != nil {
		t.Fatal(err)
	}
	if n := strings.Count(string(raw), "S"); n != 1 {
		t.Fatalf("expected exactly 1 ffmpeg run for one id, got %d", n)
	}
}

// A departing leader must neither fail the callers sharing its flight nor block on it.
func TestThumbnailLeaderCancelDoesNotFailFollower(t *testing.T) {
	get, seed, ffmpegLog := thumbnailFixture(t, false)
	id := seed()

	leaderCtx, cancelLeader := context.WithCancel(context.Background())
	defer cancelLeader()
	order := make(chan string, 2)
	var leaderRec, followerRec *httptest.ResponseRecorder
	go func() { leaderRec = get(leaderCtx, id); order <- "leader" }()
	// The stub logs "S" once the flight is running; the leader owns it.
	for deadline := time.Now().Add(5 * time.Second); ; time.Sleep(10 * time.Millisecond) {
		if raw, _ := os.ReadFile(ffmpegLog); strings.Contains(string(raw), "S") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("ffmpeg stub never started")
		}
	}
	go func() { followerRec = get(context.Background(), id); order <- "follower" }()
	time.Sleep(100 * time.Millisecond) // follower joins the 500 ms flight
	cancelLeader()

	if first := <-order; first != "leader" {
		t.Fatalf("expected cancelled leader to return before the flight ended, got %s first", first)
	}
	if leaderRec.Body.Len() != 0 {
		t.Fatalf("cancelled request wrote a body: %q", leaderRec.Body.String())
	}
	<-order
	if followerRec.Code != 200 {
		t.Fatalf("follower expected 200, got %d: %s", followerRec.Code, followerRec.Body.String())
	}
}

func TestResolveAlbumRequeuesGroup(t *testing.T) {
	testguard.Require(t)
	srv := integrationServer(t)
	ctx, database, handler, session := srv.ctx, srv.db, srv.handler, srv.session
	var rec *httptest.ResponseRecorder
	seedGroup := func(creating bool) (gid, lineGroupID string) {
		gid, lineGroupID = uuid.NewString(), "resolve-"+uuid.NewString()
		if _, err := database.Pool.Exec(ctx, `INSERT INTO "LineGroup" ("id", "lineGroupId", "enabled", "albumCreating", "membershipAt", "lastEventAt", "updatedAt") VALUES ($1, $2, true, $3, NOW(), NOW(), NOW())`, gid, lineGroupID, creating); err != nil {
			t.Fatalf("seed group: %v", err)
		}
		return gid, lineGroupID
	}
	seedFailed := func(lineGroupID, code string) string {
		id := uuid.NewString()
		if _, err := database.Pool.Exec(ctx, `INSERT INTO "Image" ("id", "lineGroupId", "channelId", "lineMessageId", "lineEventTimestamp", "uploadStatus", "errorCode", "updatedAt") VALUES ($1, $2, 'channel', $3, NOW(), 'FAILED'::"UploadState", $4, NOW())`, id, lineGroupID, uuid.NewString(), code); err != nil {
			t.Fatalf("seed image: %v", err)
		}
		return id
	}
	gidA, lineA := seedGroup(true)
	_, lineB := seedGroup(true)
	review := []string{seedFailed(lineA, "ALBUM_REVIEW"), seedFailed(lineA, "ALBUM_REVIEW"), seedFailed(lineA, "ALBUM_REVIEW")}
	storage := seedFailed(lineA, "GOOGLE_STORAGE")
	other := seedFailed(lineB, "ALBUM_REVIEW")

	req := httptest.NewRequest("POST", "/api/v1/groups/"+gidA+"/resolve-album", strings.NewReader(`{"albumId":"","confirmation":"I checked Google Photos"}`))
	req.Header.Set("Cookie", "session="+session)
	req.Header.Set("Origin", "http://localhost")
	req.Header.Set("X-Gallery-Request", "1")
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != 200 {
		t.Fatalf("resolve failed (%d): %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		OK       bool `json:"ok"`
		Requeued int  `json:"requeued"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil || !resp.OK || resp.Requeued != 3 {
		t.Fatalf("response = %s, want ok with requeued=3", rec.Body.String())
	}

	status := func(id string) string {
		var s string
		if err := database.Pool.QueryRow(ctx, `SELECT "uploadStatus"::text FROM "Image" WHERE id = $1`, id).Scan(&s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	outbox := func(id string) int {
		var n int
		if err := database.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM "OutboxEvent" WHERE "aggregateId" = $1 AND "eventType" = 'PROCESS'`, id).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	for _, id := range review {
		if s, n := status(id), outbox(id); s != "RETRY_PENDING" || n != 1 {
			t.Errorf("review image %s: status=%s outbox=%d, want RETRY_PENDING/1", id, s, n)
		}
	}
	for _, id := range []string{storage, other} {
		if s, n := status(id), outbox(id); s != "FAILED" || n != 0 {
			t.Errorf("untouched image %s: status=%s outbox=%d, want FAILED/0", id, s, n)
		}
	}
	var requeued string
	if err := database.Pool.QueryRow(ctx, `SELECT metadata->>'requeued' FROM "AuditLog" WHERE action = 'Owner resolved uncertain album'`).Scan(&requeued); err != nil || requeued != "3" {
		t.Errorf("audit requeued = %q (err %v), want 3", requeued, err)
	}
}

// Guards the /groups latest-image fields: newest image wins per group, none yields images: [].
func TestGroupsLatestImage(t *testing.T) {
	srv := integrationServer(t)
	ctx, database := srv.ctx, srv.db
	base := time.Date(2026, 3, 1, 10, 0, 0, 0, time.UTC)
	seedGroup := func(name string) (gid, lineGroupID string) {
		gid, lineGroupID = uuid.NewString(), name+"-"+uuid.NewString()
		if _, err := database.Pool.Exec(ctx, `INSERT INTO "LineGroup" ("id", "lineGroupId", "enabled", "membershipAt", "lastEventAt", "updatedAt") VALUES ($1, $2, true, NOW(), $3, NOW())`, gid, lineGroupID, base); err != nil {
			t.Fatalf("seed group: %v", err)
		}
		return gid, lineGroupID
	}
	seedImage := func(lineGroupID string, ts time.Time, status string) string {
		id := uuid.NewString()
		if _, err := database.Pool.Exec(ctx, `INSERT INTO "Image" ("id", "lineGroupId", "channelId", "lineMessageId", "lineEventTimestamp", "uploadStatus", "updatedAt") VALUES ($1, $2, 'channel', $3, $4, $5::"UploadState", NOW())`, id, lineGroupID, uuid.NewString(), ts, status); err != nil {
			t.Fatalf("seed image: %v", err)
		}
		return id
	}
	gidA, lgA := seedGroup("a")
	gidB, lgB := seedGroup("b")
	gidC, _ := seedGroup("c")
	seedImage(lgA, base.Add(-time.Hour), "FAILED")
	newestA := seedImage(lgA, base.Add(time.Hour), "UPLOAD_PENDING")
	newestB := seedImage(lgB, base.Add(30*time.Minute), "STAGED")
	seedImage(lgB, base.Add(-2*time.Hour), "UPLOAD_PENDING")

	type group struct {
		ID     string               `json:"id"`
		Count  struct{ Images int } `json:"_count"`
		Images []struct {
			ID                 string `json:"id"`
			LineEventTimestamp string `json:"lineEventTimestamp"`
			UploadStatus       string `json:"uploadStatus"`
		} `json:"images"`
	}
	get := func(path string, out any) {
		t.Helper()
		req := httptest.NewRequest("GET", path, nil)
		req.Header.Set("Cookie", "session="+srv.session)
		rec := httptest.NewRecorder()
		srv.handler.ServeHTTP(rec, req)
		if rec.Code != 200 {
			t.Fatalf("GET %s: %d %s", path, rec.Code, rec.Body.String())
		}
		if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
	}
	want := map[string]struct {
		count    int
		id       string
		ts, stat string
	}{
		gidA: {2, newestA, "2026-03-01T11:00:00.000Z", "UPLOAD_PENDING"},
		gidB: {2, newestB, "2026-03-01T10:30:00.000Z", "STAGED"},
		gidC: {0, "", "", ""},
	}
	check := func(g group) {
		t.Helper()
		w, ok := want[g.ID]
		if !ok {
			t.Fatalf("unexpected group %s", g.ID)
		}
		if g.Count.Images != w.count {
			t.Errorf("%s: count %d, want %d", g.ID, g.Count.Images, w.count)
		}
		if w.id == "" {
			if g.Images == nil || len(g.Images) != 0 {
				t.Errorf("%s: images = %v, want []", g.ID, g.Images)
			}
			return
		}
		if len(g.Images) != 1 || g.Images[0].ID != w.id || g.Images[0].LineEventTimestamp != w.ts || g.Images[0].UploadStatus != w.stat {
			t.Errorf("%s: images = %+v, want id=%s ts=%s status=%s", g.ID, g.Images, w.id, w.ts, w.stat)
		}
	}
	var list []group
	get("/api/v1/groups", &list)
	if len(list) != 3 {
		t.Fatalf("got %d groups, want 3", len(list))
	}
	for _, g := range list {
		check(g)
	}
	for gid := range want {
		var one group
		get("/api/v1/groups/"+gid, &one)
		check(one)
	}
}

// closedPoolHandler serves the API on a second, already-closed pool so every DB call fails,
// while the shared pool and Redis session stay usable.
func closedPoolHandler(t *testing.T, srv integrationEnv) http.Handler {
	t.Helper()
	second, err := db.Connect(srv.ctx, srv.env)
	if err != nil {
		t.Fatalf("db.Connect: %v", err)
	}
	second.Close()
	rOpts, err := redis.ParseURL(srv.env.RedisURL)
	if err != nil {
		t.Fatalf("redis.ParseURL: %v", err)
	}
	rdb := redis.NewClient(rOpts)
	t.Cleanup(func() { _ = rdb.Close() })
	return api.NewServer(srv.env, second, rdb, srv.store).Handler()
}

func apiRequest(handler http.Handler, method, path, body, session string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Origin", "http://localhost")
	req.Header.Set("X-Gallery-Request", "1")
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	if session != "" {
		req.AddCookie(&http.Cookie{Name: "session", Value: session})
	}
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func TestLoginDatabaseErrorIs500Not401(t *testing.T) {
	srv := integrationServer(t)
	h := closedPoolHandler(t, srv)
	rec := apiRequest(h, "POST", "/api/v1/auth/login", `{"email":"owner@example.org","password":"long-fixture-password"}`, "")
	if rec.Code != 500 {
		t.Fatalf("expected 500 on DB error, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestLoginWrongPasswordIs401(t *testing.T) {
	srv := integrationServer(t)
	rec := apiRequest(srv.handler, "POST", "/api/v1/auth/login", `{"email":"owner@example.org","password":"wrong-password"}`, "")
	if rec.Code != 401 {
		t.Fatalf("expected 401 for wrong password, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestDashboardStatsDatabaseErrorIs500(t *testing.T) {
	srv := integrationServer(t)
	h := closedPoolHandler(t, srv)
	rec := apiRequest(h, "GET", "/api/v1/dashboard/stats", "", srv.session)
	if rec.Code != 500 {
		t.Fatalf("expected 500 on DB error, got %d: %s", rec.Code, rec.Body.String())
	}
}

func TestDashboardStatsCounts(t *testing.T) {
	srv := integrationServer(t)
	rec := apiRequest(srv.handler, "GET", "/api/v1/dashboard/stats", "", srv.session)
	if rec.Code != 200 {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out["total"] != float64(0) || out["failed"] != float64(0) || out["pending"] != float64(0) {
		t.Fatalf("unexpected stats on empty db: %v", out)
	}
}

func TestGoogleCallbackBadStateRedirectsToSettings(t *testing.T) {
	srv := integrationServer(t)
	rec := apiRequest(srv.handler, "GET", "/api/v1/google/callback?code=c&state=short", "", srv.session)
	if rec.Code != 302 || rec.Header().Get("Location") != "/settings?google_error=state" {
		t.Fatalf("expected 302 to /settings?google_error=state, got %d %q", rec.Code, rec.Header().Get("Location"))
	}
}

func TestHealthFlagsUndecryptableLineCredentials(t *testing.T) {
	srv := integrationServer(t)
	if _, err := srv.db.Pool.Exec(srv.ctx, `UPDATE "Owner" SET settings = '{"lineToken":"not-ciphertext"}'::jsonb WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	rec := apiRequest(srv.handler, "GET", "/api/v1/health", "", srv.session)
	if rec.Code != 200 {
		t.Fatalf("health %d: %s", rec.Code, rec.Body.String())
	}
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	if out["lineCredentials"] != "Undecryptable" {
		t.Fatalf("expected lineCredentials Undecryptable, got %v", out)
	}
}

func TestGoogleCallbackAlbumCheckFailureRedirects(t *testing.T) {
	srv := integrationServer(t)
	if _, err := srv.db.Pool.Exec(srv.ctx, `INSERT INTO "LineGroup" ("id", "lineGroupId", "enabled", "googleAlbumId", "membershipAt", "lastEventAt", "updatedAt") VALUES ($1, $2, true, 'album', NOW(), NOW(), NOW())`, uuid.NewString(), "cb-"+uuid.NewString()); err != nil {
		t.Fatal(err)
	}
	origTransport := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = origTransport })
	http.DefaultTransport = roundTripFunc(func(req *http.Request) (*http.Response, error) {
		u := req.URL.String()
		switch {
		case strings.Contains(u, "oauth2.googleapis.com"):
			return jsonResponse(200, map[string]string{
				"refresh_token": "refresh",
				"scope":         strings.Join(google.Scopes, " "),
				"access_token":  "access",
			}), nil
		case strings.HasSuffix(u, "/albums/album"):
			return jsonResponse(403, map[string]string{"error": "denied"}), nil
		default:
			return jsonResponse(200, map[string]any{}), nil
		}
	})
	check := func() {
		t.Helper()
		connect := apiRequest(srv.handler, "GET", "/api/v1/google/connect", "", srv.session)
		loc, err := url.Parse(connect.Header().Get("Location"))
		if err != nil || loc.Query().Get("state") == "" {
			t.Fatalf("connect did not return a state: %d %q", connect.Code, connect.Header().Get("Location"))
		}
		rec := apiRequest(srv.handler, "GET", "/api/v1/google/callback?code=c&state="+loc.Query().Get("state"), "", srv.session)
		if rec.Code != 302 || rec.Header().Get("Location") != "/settings?google_error=exchange" {
			t.Fatalf("expected 302 to /settings?google_error=exchange, got %d %q: %s", rec.Code, rec.Header().Get("Location"), rec.Body.String())
		}
	}
	check()
	// A lookup error is not an empty mapping. Keep the previous connection intact.
	if _, err := srv.db.Pool.Exec(srv.ctx, `INSERT INTO "GoogleConnection" ("ownerId","scopes","status","encryptedRefreshToken") VALUES(1,'','CONNECTED','original')`); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.db.Pool.Exec(srv.ctx, `ALTER TABLE "LineGroup" RENAME COLUMN "googleAlbumId" TO "unavailableAlbumId"`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := srv.db.Pool.Exec(srv.ctx, `ALTER TABLE "LineGroup" RENAME COLUMN "unavailableAlbumId" TO "googleAlbumId"`); err != nil {
			t.Error(err)
		}
	})
	check()
	var saved string
	if err := srv.db.Pool.QueryRow(srv.ctx, `SELECT "encryptedRefreshToken" FROM "GoogleConnection" WHERE id=1`).Scan(&saved); err != nil || saved != "original" {
		t.Fatalf("lookup failure replaced connection: err=%v", err)
	}

}

func TestCleanupPrunesOldAttemptsAndAuditLogs(t *testing.T) {
	srv := integrationServer(t)
	ctx := srv.ctx
	groupID := "ret-" + uuid.NewString()
	if _, err := srv.db.Pool.Exec(ctx, `INSERT INTO "LineGroup" ("id", "lineGroupId", "enabled", "membershipAt", "lastEventAt", "updatedAt") VALUES ($1, $2, true, NOW(), NOW(), NOW())`, uuid.NewString(), groupID); err != nil {
		t.Fatal(err)
	}
	seedImage := func(status string) string {
		id := uuid.NewString()
		if _, err := srv.db.Pool.Exec(ctx, `INSERT INTO "Image" ("id", "lineGroupId", "channelId", "lineMessageId", "lineEventTimestamp", "uploadStatus", "googleMediaItemId", "updatedAt") VALUES ($1, $2, 'channel', $3, NOW(), $4::"UploadState", CASE WHEN $4 = 'SYNCED' THEN 'media' END, NOW())`, id, groupID, uuid.NewString(), status); err != nil {
			t.Fatal(err)
		}
		return id
	}
	attemptNo := 0
	seedAttempt := func(imageID string, age time.Duration) string {
		id := uuid.NewString()
		attemptNo++
		if _, err := srv.db.Pool.Exec(ctx, `INSERT INTO "UploadAttempt" ("id", "imageId", "attemptNumber", "status", "startedAt") VALUES ($1, $2, $3, 'DONE', $4)`, id, imageID, attemptNo, time.Now().UTC().Add(-age)); err != nil {
			t.Fatal(err)
		}
		return id
	}
	seedAudit := func(age time.Duration) string {
		id := uuid.NewString()
		if _, err := srv.db.Pool.Exec(ctx, `INSERT INTO "AuditLog" ("id", "action", "targetType", "targetId", "createdAt") VALUES ($1, 'x', 'image', 'y', $2)`, id, time.Now().UTC().Add(-age)); err != nil {
			t.Fatal(err)
		}
		return id
	}
	const day = 24 * time.Hour
	synced, failed := seedImage("SYNCED"), seedImage("FAILED")
	oldSynced, newSynced := seedAttempt(synced, 100*day), seedAttempt(synced, 10*day)
	oldFailed := seedAttempt(failed, 100*day) // unsynced history is kept for diagnosis
	oldAudit, newAudit := seedAudit(400*day), seedAudit(10*day)

	proc := &worker.Processor{DB: srv.db, Store: srv.store}
	if err := proc.Cleanup(ctx, time.Now()); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	exists := func(table, id string) bool {
		var n int
		if err := srv.db.Pool.QueryRow(ctx, `SELECT COUNT(*) FROM "`+table+`" WHERE id = $1`, id).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n == 1
	}
	for _, c := range []struct {
		table, id string
		keep      bool
	}{
		{"UploadAttempt", oldSynced, false}, {"UploadAttempt", newSynced, true}, {"UploadAttempt", oldFailed, true},
		{"AuditLog", oldAudit, false}, {"AuditLog", newAudit, true},
	} {
		if got := exists(c.table, c.id); got != c.keep {
			t.Errorf("%s %s: exists=%v, want %v", c.table, c.id, got, c.keep)
		}
	}
}

func TestHealthReportsWorkerLastProgress(t *testing.T) {
	srv := integrationServer(t)
	health := func() map[string]any {
		rec := apiRequest(srv.handler, "GET", "/api/v1/health", "", srv.session)
		if rec.Code != 200 {
			t.Fatalf("health %d: %s", rec.Code, rec.Body.String())
		}
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return out
	}
	if _, ok := health()["workerLastProgressAt"]; ok {
		t.Fatal("workerLastProgressAt must be omitted when the key is missing")
	}
	rOpts, _ := redis.ParseURL(srv.env.RedisURL)
	rdb := redis.NewClient(rOpts)
	defer rdb.Close()
	const ts = "2026-11-21T01:02:03.123456789Z"
	if err := rdb.Set(srv.ctx, "worker:lastProgress", ts, 0).Err(); err != nil {
		t.Fatal(err)
	}
	if got := health()["workerLastProgressAt"]; got != ts {
		t.Fatalf("workerLastProgressAt = %v, want %s", got, ts)
	}
}

func TestLogoutRevocationFailure(t *testing.T) {
	srv := integrationServer(t)
	opts, err := redis.ParseURL(srv.db.Env.RedisURL)
	if err != nil {
		t.Fatal(err)
	}
	admin := redis.NewClient(opts)
	defer admin.Close()
	username := "logoutfixture"
	if err := admin.Do(srv.ctx, "ACL", "SETUSER", username, "on", ">test-only", "~*", "+@all", "-del").Err(); err != nil {
		t.Fatal(err)
	}
	defer admin.Do(srv.ctx, "ACL", "DELUSER", username)
	opts.Username, opts.Password = username, "test-only"
	restricted := redis.NewClient(opts)
	defer restricted.Close()
	handler := api.NewServer(srv.db.Env, srv.db, restricted, srv.store).Handler()
	rec := apiRequest(handler, "POST", "/api/v1/auth/logout", "", srv.session)
	if rec.Code != 500 || rec.Header().Get("Set-Cookie") != "" {
		t.Fatalf("failed revocation cleared session: %d %v", rec.Code, rec.Result().Cookies())
	}
	rec = apiRequest(srv.handler, "POST", "/api/v1/auth/logout", "", srv.session)
	if rec.Code != 200 {
		t.Fatalf("retry logout: %d", rec.Code)
	}
	if rec = apiRequest(srv.handler, "GET", "/api/v1/health", "", srv.session); rec.Code != 401 {
		t.Fatalf("revoked cookie was accepted: %d", rec.Code)
	}
}

// An image that never passed inspection gets no on-demand ffmpeg run (AUD3-009).
func TestQuarantinedRowGetsNoThumbnailRun(t *testing.T) {
	srv := integrationServer(t)
	ctx := context.Background()
	binDir := filepath.Join(t.TempDir(), "bin")
	logPath := filepath.Join(t.TempDir(), "ffmpeg.log")
	ffmpegWebPScript(t, binDir) // writes fixture.webp into binDir
	fixture := filepath.Join(binDir, "fixture.webp")
	installFFmpeg(t, binDir, "#!/bin/sh\necho S >> \""+logPath+"\"\nout=\"\"\nfor arg in \"$@\"; do out=\"$arg\"; done\ncp \""+fixture+"\" \"$out\"\n")

	lineGroupID := "quarantine-" + uuid.NewString()
	if _, err := srv.db.Pool.Exec(ctx, `INSERT INTO "LineGroup" ("id", "lineGroupId", "enabled", "membershipAt", "lastEventAt", "updatedAt") VALUES ($1, $2, true, NOW(), NOW(), NOW())`, uuid.NewString(), lineGroupID); err != nil {
		t.Fatalf("seed group: %v", err)
	}
	seed := func(state string, mime *string, data []byte) string {
		id := uuid.NewString()
		path, err := srv.store.PathFor(id, 0)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := srv.db.Pool.Exec(ctx, `INSERT INTO "Image" ("id", "lineGroupId", "channelId", "lineMessageId", "lineEventTimestamp", "uploadStatus", "stagingPath", "mimeType", "updatedAt") VALUES ($1, $2, 'channel', $3, NOW(), $4::"UploadState", $5, $6, NOW())`, id, lineGroupID, uuid.NewString(), state, path, mime); err != nil {
			t.Fatalf("seed image: %v", err)
		}
		return id
	}
	png := "image/png"
	validID := seed("STAGED", &png, makeTestPNG(t))
	quarantinedID := seed("UNRECOVERABLE", nil, []byte("not a photo"))

	rec := apiRequest(srv.handler, "GET", "/api/v1/images/"+quarantinedID+"/thumbnail", "", srv.session)
	if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), "Image not available yet.") {
		t.Fatalf("quarantined row: expected 404 Image not available yet., got %d: %s", rec.Code, rec.Body.String())
	}
	if log, _ := os.ReadFile(logPath); len(log) != 0 {
		t.Fatalf("ffmpeg ran on uninspected bytes: %q", log)
	}

	rec = apiRequest(srv.handler, "GET", "/api/v1/images/"+validID+"/thumbnail", "", srv.session)
	if rec.Code != http.StatusOK {
		t.Fatalf("validated row must still render, got %d: %s", rec.Code, rec.Body.String())
	}
	if log, _ := os.ReadFile(logPath); len(log) == 0 {
		t.Fatal("validated row did not run ffmpeg; the control is not exercising the thumbnail path")
	}
}

// A staging volume that cannot be read is a 500, not zeroed stats (AUD3-013).
func TestDashboardStatsStagingErrorIs500(t *testing.T) {
	srv := integrationServer(t)
	if err := os.RemoveAll(srv.env.StagingPath); err != nil {
		t.Fatal(err)
	}
	rec := apiRequest(srv.handler, "GET", "/api/v1/dashboard/stats", "", srv.session)
	if rec.Code != 500 {
		t.Fatalf("missing staging volume: expected 500, got %d: %s", rec.Code, rec.Body.String())
	}
	if err := os.MkdirAll(srv.env.StagingPath, 0o700); err != nil {
		t.Fatal(err)
	}
	rec = apiRequest(srv.handler, "GET", "/api/v1/dashboard/stats", "", srv.session)
	if rec.Code != 200 {
		t.Fatalf("restored staging volume: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
}
