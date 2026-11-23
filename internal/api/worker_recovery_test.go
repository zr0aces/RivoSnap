package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"rivosnap/internal/config"
	"rivosnap/internal/crypto"
	"rivosnap/internal/db"
	"rivosnap/internal/media"
	"rivosnap/internal/testguard"
	"rivosnap/internal/worker"
)

// fakeProviders stands in for LINE and Google over http.DefaultTransport and counts
// the calls that must never repeat (LINE downloads, album creation, batchCreate).
type fakeProviders struct {
	mu            sync.Mutex
	lineBody      []byte
	lineDownloads int
	lineFailures  int // the next N LINE downloads fail with a network error
	albumCreates  int
	uploads       int
	batchCreates  int
	searches      int
	created       []string // filenames accepted by batchCreate, returned by mediaItems:search
	// beforeSend runs before the request is sent; cancelling its context stops the request.
	beforeSend func(req *http.Request)
	// afterServe runs after the provider has acted; cancelling here loses the response.
	afterServe func(req *http.Request)
}

// RoundTrip behaves like a real transport: a request whose context ended is never sent,
// and one that ends in flight loses its response while the provider may already have acted.
func (f *fakeProviders) RoundTrip(req *http.Request) (*http.Response, error) {
	if f.beforeSend != nil {
		f.beforeSend(req)
	}
	if err := req.Context().Err(); err != nil {
		return nil, err
	}
	resp, err := f.serve(req)
	if f.afterServe != nil {
		f.afterServe(req)
	}
	if err := req.Context().Err(); err != nil {
		return nil, err
	}
	return resp, err
}

func (f *fakeProviders) serve(req *http.Request) (*http.Response, error) {
	switch req.URL.Host {
	case "api-data.line.me":
		f.mu.Lock()
		f.lineDownloads++
		fail := f.lineFailures > 0
		if fail {
			f.lineFailures--
		}
		f.mu.Unlock()
		if fail {
			return nil, errors.New("connection reset by peer")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(f.lineBody))}, nil
	case "oauth2.googleapis.com":
		return jsonResponse(200, map[string]any{"access_token": "token", "expires_in": 3600}), nil
	case "photoslibrary.googleapis.com":
		return f.photos(req)
	}
	return nil, fmt.Errorf("unexpected host %q", req.URL.Host)
}

func (f *fakeProviders) photos(req *http.Request) (*http.Response, error) {
	path := req.URL.Path
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case req.Method == http.MethodGet && strings.HasPrefix(path, "/v1/albums/"):
		return jsonResponse(200, map[string]any{"id": strings.TrimPrefix(path, "/v1/albums/"), "mediaItemsCount": "0"}), nil
	case req.Method == http.MethodPost && path == "/v1/albums":
		f.albumCreates++
		return jsonResponse(200, map[string]any{"id": "album-created"}), nil
	case req.Method == http.MethodPost && path == "/v1/uploads":
		f.uploads++
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("upload-token"))}, nil
	case req.Method == http.MethodPost && path == "/v1/mediaItems:batchCreate":
		var body struct {
			NewMediaItems []struct {
				SimpleMediaItem struct {
					FileName string `json:"fileName"`
				} `json:"simpleMediaItem"`
			} `json:"newMediaItems"`
		}
		if err := json.NewDecoder(req.Body).Decode(&body); err != nil || len(body.NewMediaItems) != 1 {
			return nil, fmt.Errorf("unexpected batchCreate body: %v", err)
		}
		f.batchCreates++
		name := body.NewMediaItems[0].SimpleMediaItem.FileName
		f.created = append(f.created, name)
		return jsonResponse(200, map[string]any{"newMediaItemResults": []any{map[string]any{
			"status":    map[string]any{"code": 0},
			"mediaItem": map[string]any{"id": fmt.Sprintf("item-%d", f.batchCreates), "productUrl": "https://photos.example/item"},
		}}}), nil
	case req.Method == http.MethodPost && path == "/v1/mediaItems:search":
		f.searches++
		items := []any{}
		for i, name := range f.created {
			items = append(items, map[string]any{"id": fmt.Sprintf("item-%d", i+1), "filename": name, "productUrl": "https://photos.example/item"})
		}
		return jsonResponse(200, map[string]any{"mediaItems": items}), nil
	}
	return nil, fmt.Errorf("unexpected Google request %s %s", req.Method, path)
}

func (f *fakeProviders) counts() (lineDownloads, albumCreates, uploads, batchCreates int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lineDownloads, f.albumCreates, f.uploads, f.batchCreates
}

// recovery is one seeded image, with LINE and Google stubbed and a stub ffmpeg
// that writes a valid WebP for every thumbnail request.
type recovery struct {
	ctx      context.Context
	db       *db.DB
	store    *media.Store
	proc     *worker.Processor
	fake     *fakeProviders
	groupID  string
	imageID  string
	outboxID string
}

// recoveryFixture uses its own disposable database and seeds one image in state.
func recoveryFixture(t *testing.T, state string) *recovery {
	t.Helper()
	testguard.Require(t)
	temp := t.TempDir()
	env := config.Env{
		DatabaseURL:            os.Getenv("DATABASE_URL"),
		TokenEncryptionKey:     strings.Repeat("1", 64),
		LineChannelAccessToken: "test",
		GoogleClientID:         "client",
		GoogleClientSecret:     "secret",
		StagingPath:            filepath.Join(temp, "stage"),
		ThumbnailPath:          filepath.Join(temp, "thumb"),
		MaxImageSizeMB:         50,
		MaxVideoSizeMB:         200,
		MinFreeMB:              1,
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
	if _, err := database.Pool.Exec(ctx, `TRUNCATE "UploadAttempt", "Image", "LineGroup", "WebhookEvent", "OutboxEvent", "AuditLog", "GoogleConnection", "Owner" CASCADE`); err != nil {
		t.Fatalf("truncate: %v", err)
	}
	if _, err := database.Pool.Exec(ctx, `INSERT INTO "Owner" ("email", "passwordHash", "updatedAt") VALUES ('owner@example.org', 'unused', NOW())`); err != nil {
		t.Fatalf("seed owner: %v", err)
	}
	store := media.NewStore(env)
	if err := store.InitDirs(); err != nil {
		t.Fatal(err)
	}
	return seedRecovery(t, database, store, env.TokenEncryptionKey, state, true)
}

// attachRecovery seeds an image on the integrationServer database, so the owner's
// HTTP endpoints (retry, resolve-album) act on the same rows the worker processes.
func attachRecovery(t *testing.T, srv integrationEnv, state string, queued bool) *recovery {
	t.Helper()
	testguard.Require(t)
	return seedRecovery(t, srv.db, srv.store, srv.env.TokenEncryptionKey, state, queued)
}

// seedRecovery inserts a Google connection, a group with an album and one image in state.
// queued adds the PROCESS outbox row a fresh pipeline would have.
func seedRecovery(t *testing.T, database *db.DB, store *media.Store, key, state string, queued bool) *recovery {
	t.Helper()
	ctx := context.Background()
	encrypted, err := crypto.Seal("fixture", key)
	if err != nil {
		t.Fatal(err)
	}
	r := &recovery{
		ctx:     ctx,
		db:      database,
		store:   store,
		groupID: uuid.NewString(),
		imageID: uuid.NewString(),
	}
	lineGroupID := "recovery-" + uuid.NewString()
	seed := []struct {
		sql  string
		args []any
	}{
		{`INSERT INTO "GoogleConnection" ("ownerId", "scopes", "status", "encryptedRefreshToken") VALUES (1, '', 'CONNECTED', $1)`, []any{encrypted}},
		{`INSERT INTO "LineGroup" ("id", "lineGroupId", "displayName", "googleAlbumId", "enabled", "membershipAt", "lastEventAt", "updatedAt") VALUES ($1, $2, 'Test Group', 'album-existing', true, NOW(), NOW(), NOW())`, []any{r.groupID, lineGroupID}},
		{`INSERT INTO "Image" ("id", "lineGroupId", "channelId", "lineMessageId", "lineEventTimestamp", "uploadStatus", "updatedAt") VALUES ($1, $2, 'channel', $3, NOW(), $4::"UploadState", NOW())`, []any{r.imageID, lineGroupID, uuid.NewString(), state}},
	}
	if queued {
		r.outboxID = uuid.NewString()
		seed = append(seed, struct {
			sql  string
			args []any
		}{`INSERT INTO "OutboxEvent" ("id", "eventType", "aggregateId", "payload", "createdAt", "availableAt") VALUES ($1, 'PROCESS', $2, '{}'::jsonb, NOW(), NOW())`, []any{r.outboxID, r.imageID}})
	}
	for _, st := range seed {
		if _, err := database.Pool.Exec(ctx, st.sql, st.args...); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	r.fake = &fakeProviders{lineBody: makeTestPNG(t)}
	previous := http.DefaultTransport
	http.DefaultTransport = r.fake
	t.Cleanup(func() { http.DefaultTransport = previous })
	binDir := filepath.Join(t.TempDir(), "bin")
	installFFmpeg(t, binDir, ffmpegWebPScript(t, binDir))

	r.proc = &worker.Processor{DB: database, Store: store}
	return r
}

// installFFmpeg puts a stub ffmpeg with the given script first in PATH for this test.
func installFFmpeg(t *testing.T, binDir, script string) {
	t.Helper()
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "ffmpeg"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// ffmpegWebPScript writes a WebP fixture into dir and returns a stub that copies it to the output path.
func ffmpegWebPScript(t *testing.T, dir string) string {
	t.Helper()
	var buf bytes.Buffer
	buf.WriteString("RIFF")
	vp8l := []byte{0x2f, 0x00, 0x00, 0x00, 0x00, 0x07, 0x10, 0xfd, 0x7f, 0x1d, 0x08, 0x00}
	buf.Write([]byte{byte(12 + len(vp8l)), 0, 0, 0})
	buf.WriteString("WEBPVP8L")
	buf.Write([]byte{byte(len(vp8l)), 0, 0, 0})
	buf.Write(vp8l)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	fixture := filepath.Join(dir, "fixture.webp")
	if err := os.WriteFile(fixture, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return "#!/bin/sh\nout=\"\"\nfor arg in \"$@\"; do out=\"$arg\"; done\ncp \"" + fixture + "\" \"$out\"\n"
}

type imageRow struct {
	status  string
	code    string
	sha     *string
	mime    *string
	staging *string
}

func (r *recovery) row(t *testing.T) imageRow {
	t.Helper()
	var out imageRow
	var code *string
	if err := r.db.Pool.QueryRow(
		r.ctx,
		`SELECT "uploadStatus"::text, "errorCode", "sha256", "mimeType", "stagingPath" FROM "Image" WHERE id = $1`,
		r.imageID,
	).Scan(&out.status, &code, &out.sha, &out.mime, &out.staging); err != nil {
		t.Fatal(err)
	}
	if code != nil {
		out.code = *code
	}
	return out
}

func (r *recovery) countOutbox(t *testing.T, eventType string) int {
	t.Helper()
	var n int
	if err := r.db.Pool.QueryRow(r.ctx, `SELECT COUNT(*) FROM "OutboxEvent" WHERE "aggregateId" = $1 AND "eventType" = $2`, r.imageID, eventType).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func (r *recovery) outboxIDOf(t *testing.T, eventType string) string {
	t.Helper()
	var id string
	if err := r.db.Pool.QueryRow(r.ctx, `SELECT id FROM "OutboxEvent" WHERE "aggregateId" = $1 AND "eventType" = $2`, r.imageID, eventType).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// runNext processes the single outbox row of eventType for the image.
func (r *recovery) runNext(t *testing.T, eventType string) {
	t.Helper()
	if err := r.proc.ProcessEvent(r.ctx, r.outboxIDOf(t, eventType)); err != nil {
		t.Fatalf("ProcessEvent %s: %v", eventType, err)
	}
}

// processAvailableAt returns when the pending PROCESS row becomes claimable.
func (r *recovery) processAvailableAt(t *testing.T) time.Time {
	t.Helper()
	var at time.Time
	if err := r.db.Pool.QueryRow(r.ctx, `SELECT "availableAt" FROM "OutboxEvent" WHERE "aggregateId" = $1 AND "eventType" = 'PROCESS'`, r.imageID).Scan(&at); err != nil {
		t.Fatal(err)
	}
	return at
}

// seedAttempts appends ledger rows numbered from 1 with the given statuses. Every row is closed.
func (r *recovery) seedAttempts(t *testing.T, statuses ...string) {
	t.Helper()
	for i, st := range statuses {
		if _, err := r.db.Pool.Exec(
			r.ctx,
			`INSERT INTO "UploadAttempt" ("id", "imageId", "attemptNumber", "status", "startedAt", "completedAt") VALUES ($1, $2, $3, $4, NOW(), NOW())`,
			uuid.NewString(), r.imageID, i+1, st,
		); err != nil {
			t.Fatalf("seed attempt: %v", err)
		}
	}
}

// setOriginal writes the original bytes at the staging path and records the path, as Stage does.
func (r *recovery) setOriginal(t *testing.T, data []byte) string {
	t.Helper()
	path, err := r.store.PathFor(r.imageID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := r.db.Pool.Exec(r.ctx, `UPDATE "Image" SET "stagingPath" = $1 WHERE id = $2`, path, r.imageID); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRetryReinspectsStagedOriginalWithoutChecksum(t *testing.T) {
	r := recoveryFixture(t, "RETRY_PENDING")
	path := r.setOriginal(t, makeTestPNG(t))

	r.runNext(t, "PROCESS")
	st := r.row(t)
	if st.status != "STAGED" || st.sha == nil || st.mime == nil || *st.mime != "image/png" {
		t.Fatalf("expected STAGED with recorded sha256 and mimeType, got %+v", st)
	}
	if st.code == "INTEGRITY" {
		t.Fatalf("retry hit INTEGRITY on a recovered original")
	}
	if lineDownloads, _, _, _ := r.fake.counts(); lineDownloads != 0 {
		t.Fatalf("expected no LINE download for a staged original, got %d", lineDownloads)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("original missing after re-inspection: %v", err)
	}
	if n := r.countOutbox(t, "UPLOAD"); n != 1 {
		t.Fatalf("expected one UPLOAD outbox row, got %d", n)
	}

	r.runNext(t, "UPLOAD")
	if st := r.row(t); st.status != "SYNCED" {
		t.Fatalf("expected SYNCED after upload, got %+v", st)
	}
	if _, _, _, batch := r.fake.counts(); batch != 1 {
		t.Fatalf("expected exactly one batchCreate, got %d", batch)
	}
}

func TestRetryOfQuarantinedGarbageStaysUnrecoverableAndKept(t *testing.T) {
	r := recoveryFixture(t, "RETRY_PENDING")
	path := r.setOriginal(t, []byte("not a photo"))

	r.runNext(t, "PROCESS")
	st := r.row(t)
	if st.status != "UNRECOVERABLE" || st.code != "UNSUPPORTED_IMAGE" {
		t.Fatalf("expected UNRECOVERABLE/UNSUPPORTED_IMAGE, got %+v", st)
	}
	if st.staging == nil || *st.staging != path {
		t.Fatalf("quarantined original must keep its staging path, got %v", st.staging)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("garbage original was removed: %v", err)
	}
	if lineDownloads, _, _, _ := r.fake.counts(); lineDownloads != 0 {
		t.Fatalf("expected no LINE download, got %d", lineDownloads)
	}
}

func TestRetryWithRecordedChecksumStillGoesToUpload(t *testing.T) {
	r := recoveryFixture(t, "RETRY_PENDING")
	path := r.setOriginal(t, makeTestPNG(t))
	info, err := media.Inspect(path)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if _, err := r.db.Pool.Exec(r.ctx, `UPDATE "Image" SET "sha256" = $1, "mimeType" = $2 WHERE id = $3`, info.Sha256, info.MimeType, r.imageID); err != nil {
		t.Fatal(err)
	}

	r.runNext(t, "PROCESS")
	if st := r.row(t); st.status != "SYNCED" {
		t.Fatalf("expected SYNCED in one run with recorded checksum, got %+v", st)
	}
	if lineDownloads, _, _, batch := r.fake.counts(); lineDownloads != 0 || batch != 1 {
		t.Fatalf("expected 0 LINE downloads and 1 batchCreate, got %d and %d", lineDownloads, batch)
	}
}

// Retry budget (AUD3-002): a transient download fault retries while the budget allows,
// and an owner Retry starts a fresh schedule.

func TestEightNetworkFailuresThenSuccess(t *testing.T) {
	r := recoveryFixture(t, "RECEIVED")
	r.fake.lineFailures = 8
	for i := 1; i <= 8; i++ {
		r.runNext(t, "PROCESS")
		st := r.row(t)
		if st.status != "RETRY_PENDING" || st.code != "NETWORK" {
			t.Fatalf("failure %d: expected RETRY_PENDING/NETWORK, got %+v", i, st)
		}
		if n := r.countOutbox(t, "PROCESS"); n != 1 {
			t.Fatalf("failure %d: expected one PROCESS retry row, got %d", i, n)
		}
	}
	r.runNext(t, "PROCESS")
	if st := r.row(t); st.status != "STAGED" {
		t.Fatalf("ninth run should stage the download, got %+v", st)
	}
	r.runNext(t, "UPLOAD")
	if st := r.row(t); st.status != "SYNCED" {
		t.Fatalf("expected SYNCED, got %+v", st)
	}
	if lineDownloads, _, _, batch := r.fake.counts(); lineDownloads != 9 || batch != 1 {
		t.Fatalf("expected 9 LINE attempts and 1 batchCreate, got %d and %d", lineDownloads, batch)
	}
}

func TestBudgetExhaustionThenManualRetryResets(t *testing.T) {
	srv := integrationServer(t)
	r := attachRecovery(t, srv, "RECEIVED", true)
	r.fake.lineFailures = 1000

	// Sixteen failing runs: fifteen automatic retries, then FAILED.
	for i := 1; i < 16; i++ {
		r.runNext(t, "PROCESS")
		if st := r.row(t); st.status != "RETRY_PENDING" {
			t.Fatalf("failure %d: expected RETRY_PENDING, got %+v", i, st)
		}
	}
	r.runNext(t, "PROCESS")
	if st := r.row(t); st.status != "FAILED" || st.code != "NETWORK" {
		t.Fatalf("expected FAILED/NETWORK after the budget, got %+v", st)
	}
	var lastStatus string
	if err := r.db.Pool.QueryRow(r.ctx, `SELECT "status" FROM "UploadAttempt" WHERE "imageId" = $1 ORDER BY "attemptNumber" DESC LIMIT 1`, r.imageID).Scan(&lastStatus); err != nil {
		t.Fatal(err)
	}
	if lastStatus != "FAILED" {
		t.Fatalf("last attempt must mark the cycle boundary as FAILED, got %q", lastStatus)
	}

	rec := apiRequest(srv.handler, "POST", "/api/v1/jobs/"+r.imageID+"/retry", "", srv.session)
	if rec.Code != 200 {
		t.Fatalf("owner retry: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	r.runNext(t, "PROCESS")
	if st := r.row(t); st.status != "RETRY_PENDING" {
		t.Fatalf("first failure after owner retry must schedule a retry, got %+v", st)
	}
	if wait := time.Until(r.processAvailableAt(t)); wait > 5*time.Second {
		t.Fatalf("retry after owner Retry must start a fresh schedule within 5 s, waits %s", wait)
	}
}

func TestLegacyExhaustedItemResetsOnRetry(t *testing.T) {
	srv := integrationServer(t)
	r := attachRecovery(t, srv, "FAILED", false)
	r.seedAttempts(t, "RETRY_PENDING", "RETRY_PENDING", "RETRY_PENDING", "RETRY_PENDING", "RETRY_PENDING", "RETRY_PENDING", "RETRY_PENDING", "FAILED")
	if _, err := r.db.Pool.Exec(r.ctx, `UPDATE "Image" SET "errorCode" = 'NETWORK' WHERE id = $1`, r.imageID); err != nil {
		t.Fatal(err)
	}
	r.fake.lineFailures = 1000

	rec := apiRequest(srv.handler, "POST", "/api/v1/jobs/"+r.imageID+"/retry", "", srv.session)
	if rec.Code != 200 {
		t.Fatalf("owner retry: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	r.runNext(t, "PROCESS")
	if st := r.row(t); st.status != "RETRY_PENDING" {
		t.Fatalf("legacy exhausted item must get a fresh budget, got %+v", st)
	}
}

func TestAlbumRepairRequeueResetsBudget(t *testing.T) {
	srv := integrationServer(t)
	r := attachRecovery(t, srv, "FAILED", false)
	if _, err := r.db.Pool.Exec(r.ctx, `UPDATE "Image" SET "errorCode" = 'ALBUM_REVIEW' WHERE id = $1`, r.imageID); err != nil {
		t.Fatal(err)
	}
	if _, err := r.db.Pool.Exec(r.ctx, `UPDATE "LineGroup" SET "albumCreating" = true, "googleAlbumId" = NULL WHERE id = $1`, r.groupID); err != nil {
		t.Fatal(err)
	}
	statuses := make([]string, 0, 20)
	for range 19 {
		statuses = append(statuses, "RETRY_PENDING")
	}
	r.seedAttempts(t, append(statuses, "FAILED")...)
	r.fake.lineFailures = 1000

	rec := apiRequest(srv.handler, "POST", "/api/v1/groups/"+r.groupID+"/resolve-album", `{"albumId":"","confirmation":"I checked Google Photos"}`, srv.session)
	if rec.Code != 200 {
		t.Fatalf("resolve-album: expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	r.runNext(t, "PROCESS")
	if st := r.row(t); st.status != "RETRY_PENDING" {
		t.Fatalf("requeued album-review item must get a fresh budget, got %+v", st)
	}
}

// readyForUpload puts a valid original on disk with its checksum and MIME type recorded,
// so a retry goes straight to upload.
func (r *recovery) readyForUpload(t *testing.T) {
	t.Helper()
	path := r.setOriginal(t, makeTestPNG(t))
	info, err := media.Inspect(path)
	if err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if _, err := r.db.Pool.Exec(r.ctx, `UPDATE "Image" SET "sha256" = $1, "mimeType" = $2 WHERE id = $3`, info.Sha256, info.MimeType, r.imageID); err != nil {
		t.Fatal(err)
	}
}

// clearAlbum makes the group's album unknown, so the worker must create it.
func (r *recovery) clearAlbum(t *testing.T) {
	t.Helper()
	if _, err := r.db.Pool.Exec(r.ctx, `UPDATE "LineGroup" SET "googleAlbumId" = NULL WHERE id = $1`, r.groupID); err != nil {
		t.Fatal(err)
	}
}

// groupAlbum returns the stored album ID (nil when unset) and the albumCreating claim.
func (r *recovery) groupAlbum(t *testing.T) (*string, bool) {
	t.Helper()
	var album *string
	var creating bool
	if err := r.db.Pool.QueryRow(r.ctx, `SELECT "googleAlbumId", "albumCreating" FROM "LineGroup" WHERE id = $1`, r.groupID).Scan(&album, &creating); err != nil {
		t.Fatal(err)
	}
	return album, creating
}

// attemptStatuses returns the status of every ledger row for the image, oldest first.
func (r *recovery) attemptStatuses(t *testing.T) []string {
	t.Helper()
	rows, err := r.db.Pool.Query(r.ctx, `SELECT "status" FROM "UploadAttempt" WHERE "imageId" = $1 ORDER BY "attemptNumber"`, r.imageID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// openAttempts counts ledger rows still open (no completedAt).
func (r *recovery) openAttempts(t *testing.T) int {
	t.Helper()
	var n int
	if err := r.db.Pool.QueryRow(r.ctx, `SELECT COUNT(*) FROM "UploadAttempt" WHERE "imageId" = $1 AND "completedAt" IS NULL`, r.imageID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// albumClaimTracer runs onClaim as soon as a claim UPDATE of "albumCreating" = true has
// committed, so a test can cancel the worker between the claim and CreateAlbum.
type albumClaimTracer struct{ onClaim func() }

type claimKey struct{}

func (a albumClaimTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	return context.WithValue(ctx, claimKey{}, strings.Contains(data.SQL, `"albumCreating" = true`))
}

func (a albumClaimTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	claim, _ := ctx.Value(claimKey{}).(bool)
	if claim && data.Err == nil && data.CommandTag.RowsAffected() == 1 {
		a.onClaim()
	}
}

// tracedProcessor reuses the recovery database with a pool whose claim queries call onClaim.
func tracedProcessor(t *testing.T, r *recovery, onClaim func()) *worker.Processor {
	t.Helper()
	cfg := r.db.Pool.Config()
	cfg.ConnConfig.Tracer = albumClaimTracer{onClaim: onClaim}
	pool, err := pgxpool.NewWithConfig(r.ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return &worker.Processor{DB: &db.DB{Pool: pool, Env: r.db.Env}, Store: r.store}
}

// Shutdown (AUD3-006): a SIGTERM records the attempt as interrupted and never burns the budget.

func TestShutdownKeepsItemRecoverable(t *testing.T) {
	r := recoveryFixture(t, "RETRY_PENDING")
	r.readyForUpload(t)
	ctx, cancel := context.WithCancel(r.ctx)
	defer cancel()
	r.fake.beforeSend = func(req *http.Request) {
		if req.URL.Path == "/v1/uploads" {
			cancel()
		}
	}

	if err := r.proc.ProcessEvent(ctx, r.outboxID); err != nil {
		t.Fatalf("shutdown must return nil, got %v", err)
	}
	st := r.row(t)
	if st.status != "UPLOADING" {
		t.Fatalf("shutdown must leave the step state unchanged (UPLOADING), got %+v", st)
	}
	if got := r.attemptStatuses(t); len(got) != 1 || got[0] != "INTERRUPTED" {
		t.Fatalf("expected one INTERRUPTED attempt, got %v", got)
	}
	if n := r.openAttempts(t); n != 0 {
		t.Fatalf("expected no open attempts after shutdown, got %d", n)
	}
	if n := r.countOutbox(t, "PROCESS"); n != 1 {
		t.Fatalf("shutdown must keep the outbox row, got %d", n)
	}
	if _, _, uploads, batch := r.fake.counts(); uploads != 0 || batch != 0 {
		t.Fatalf("no upload may start after cancel: uploads %d, batchCreate %d", uploads, batch)
	}

	r.fake.beforeSend = nil
	r.runNext(t, "PROCESS")
	if st := r.row(t); st.status != "SYNCED" {
		t.Fatalf("live run after shutdown must reach SYNCED, got %+v", st)
	}
	if _, _, _, batch := r.fake.counts(); batch != 1 {
		t.Fatalf("expected exactly one batchCreate across both runs, got %d", batch)
	}
}

func TestShutdownAfterCreateAlbumPersistsMapping(t *testing.T) {
	r := recoveryFixture(t, "RETRY_PENDING")
	r.readyForUpload(t)
	r.clearAlbum(t)
	ctx, cancel := context.WithCancel(r.ctx)
	defer cancel()
	r.fake.beforeSend = func(req *http.Request) {
		if req.Method == http.MethodPost && req.URL.Path == "/v1/albums" {
			cancel()
		}
	}

	if err := r.proc.ProcessEvent(ctx, r.outboxID); err != nil {
		t.Fatalf("shutdown must return nil, got %v", err)
	}
	album, creating := r.groupAlbum(t)
	if album == nil || *album != "album-created" || creating {
		t.Fatalf("created album must be recorded and the claim released, got album %v creating %v", album, creating)
	}
	if albumCreates := func() int { _, a, _, _ := r.fake.counts(); return a }(); albumCreates != 1 {
		t.Fatalf("expected exactly one album creation, got %d", albumCreates)
	}
	if got := r.attemptStatuses(t); len(got) != 1 || got[0] != "INTERRUPTED" {
		t.Fatalf("expected one INTERRUPTED attempt, got %v", got)
	}
}

func TestShutdownBeforeCreateAlbumReleasesClaim(t *testing.T) {
	r := recoveryFixture(t, "RETRY_PENDING")
	r.readyForUpload(t)
	r.clearAlbum(t)
	ctx, cancel := context.WithCancel(r.ctx)
	defer cancel()
	proc := tracedProcessor(t, r, cancel)

	if err := proc.ProcessEvent(ctx, r.outboxID); err != nil {
		t.Fatalf("shutdown must return nil, got %v", err)
	}
	if _, albums, _, _ := r.fake.counts(); albums != 0 {
		t.Fatalf("no album may be created after cancel, got %d", albums)
	}
	album, creating := r.groupAlbum(t)
	if album != nil || creating {
		t.Fatalf("claim must be released with no album recorded, got album %v creating %v", album, creating)
	}
	if got := r.attemptStatuses(t); len(got) != 1 || got[0] != "INTERRUPTED" {
		t.Fatalf("expected one INTERRUPTED attempt, got %v", got)
	}
}

func TestShutdownInCreatingMediaStaysReconcileOnly(t *testing.T) {
	r := recoveryFixture(t, "RETRY_PENDING")
	r.readyForUpload(t)
	ctx, cancel := context.WithCancel(r.ctx)
	defer cancel()
	// The provider accepts the batchCreate but the response is lost to the cancel.
	r.fake.afterServe = func(req *http.Request) {
		if req.Method == http.MethodPost && req.URL.Path == "/v1/mediaItems:batchCreate" {
			cancel()
		}
	}

	if err := r.proc.ProcessEvent(ctx, r.outboxID); err != nil {
		t.Fatalf("shutdown must return nil, got %v", err)
	}
	if st := r.row(t); st.status != "CREATING_MEDIA" {
		t.Fatalf("uncertain create after shutdown must stay CREATING_MEDIA, got %+v", st)
	}
	if got := r.attemptStatuses(t); len(got) != 1 || got[0] != "INTERRUPTED" {
		t.Fatalf("expected one INTERRUPTED attempt, got %v", got)
	}

	r.fake.afterServe = nil
	r.runNext(t, "PROCESS")
	if st := r.row(t); st.status != "SYNCED" {
		t.Fatalf("live run must reconcile to SYNCED, got %+v", st)
	}
	if _, _, _, batch := r.fake.counts(); batch != 1 {
		t.Fatalf("reconcile must never repeat batchCreate, got %d", batch)
	}
}

func TestBudgetIgnoresInterruptedAttempts(t *testing.T) {
	r := recoveryFixture(t, "RECEIVED")
	r.seedAttempts(t, "INTERRUPTED", "INTERRUPTED", "INTERRUPTED", "INTERRUPTED", "INTERRUPTED",
		"RETRY_PENDING", "RETRY_PENDING", "RETRY_PENDING", "RETRY_PENDING", "RETRY_PENDING", "RETRY_PENDING", "RETRY_PENDING", "RETRY_PENDING", "RETRY_PENDING", "RETRY_PENDING", "RETRY_PENDING", "RETRY_PENDING", "RETRY_PENDING")
	r.fake.lineFailures = 1000

	// Fourteen counted attempts give budget 15, so this failure still retries.
	r.runNext(t, "PROCESS")
	if st := r.row(t); st.status != "RETRY_PENDING" {
		t.Fatalf("interrupted attempts must not shorten the budget, got %+v", st)
	}
}

func TestRunLoopExitsCleanlyOnCancel(t *testing.T) {
	r := recoveryFixture(t, "RETRY_PENDING")
	r.readyForUpload(t)
	ctx, cancel := context.WithCancel(r.ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- r.proc.RunLoop(ctx) }()

	deadline := time.Now().Add(20 * time.Second)
	for r.row(t).status != "SYNCED" {
		if time.Now().After(deadline) {
			t.Fatalf("worker did not sync before the deadline: %+v", r.row(t))
		}
		time.Sleep(50 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunLoop after SIGTERM must return nil (exit 0), got %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("RunLoop did not return after cancel")
	}
}

// Preview generation is best effort (AUD3-008): a failed thumbnail never blocks the upload.
func TestPreviewFailureDoesNotBlockUpload(t *testing.T) {
	r := recoveryFixture(t, "RETRY_PENDING")
	r.readyForUpload(t)
	installFFmpeg(t, filepath.Join(t.TempDir(), "failing-bin"), "#!/bin/sh\nexit 1\n")

	r.runNext(t, "PROCESS")
	if st := r.row(t); st.status != "SYNCED" {
		t.Fatalf("a failed preview must not block the upload, got %+v", st)
	}
	var thumbnail *string
	if err := r.db.Pool.QueryRow(r.ctx, `SELECT "thumbnailPath" FROM "Image" WHERE id = $1`, r.imageID).Scan(&thumbnail); err != nil {
		t.Fatal(err)
	}
	if thumbnail != nil {
		t.Fatalf("failed preview must leave thumbnailPath NULL, got %s", *thumbnail)
	}
}

// A saturated password-hash gate answers 503 before any password check or Owner row lock (AUD3-001).
func TestHashGateBusyAnswers503(t *testing.T) {
	srv := integrationServer(t)
	srv.server.SetHashWait(time.Second)
	release := srv.server.HoldHashSlots()

	rec := apiRequest(srv.handler, "POST", "/api/v1/auth/login", `{"email":"owner@example.org","password":"wrong-password"}`, "")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("login while saturated: expected 503, got %d: %s", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Retry-After"); got != "2" {
		t.Fatalf("login busy: Retry-After = %q, want 2", got)
	}
	if !strings.Contains(rec.Body.String(), "Sign-in is busy. Try again shortly.") {
		t.Fatalf("login busy: unexpected body %s", rec.Body.String())
	}

	// A password change waits for a slot before it opens its transaction, so Owner stays unlocked.
	done := make(chan *httptest.ResponseRecorder, 1)
	go func() {
		done <- apiRequest(srv.handler, "POST", "/api/v1/profile/start", `{"action":"change","target":"password","method":"password","password":"long-fixture-password"}`, srv.session)
	}()
	time.Sleep(100 * time.Millisecond)
	tx, err := srv.db.Pool.Begin(srv.ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(srv.ctx, `SELECT 1 FROM "Owner" WHERE id = 1 FOR UPDATE NOWAIT`); err != nil {
		t.Fatalf("Owner row locked while a password change waits for a hash slot: %v", err)
	}
	_ = tx.Rollback(srv.ctx)
	change := <-done
	if change.Code != http.StatusServiceUnavailable || change.Header().Get("Retry-After") != "2" {
		t.Fatalf("profile/start while saturated: expected 503 with Retry-After, got %d: %s", change.Code, change.Body.String())
	}

	release()
	rec = apiRequest(srv.handler, "POST", "/api/v1/auth/login", `{"email":"owner@example.org","password":"wrong-password"}`, "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("after release a wrong password must be 401, got %d: %s", rec.Code, rec.Body.String())
	}
}

// sqlTracer runs onMatch when a statement whose text matches re starts, so a test can
// cancel the worker at an exact point inside one step.
type sqlTracer struct {
	re      *regexp.Regexp
	onMatch func()
}

func (s sqlTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryStartData) context.Context {
	if s.re.MatchString(data.SQL) {
		s.onMatch()
	}
	return ctx
}

func (s sqlTracer) TraceQueryEnd(context.Context, *pgx.Conn, pgx.TraceQueryEndData) {}

// tracedProcessorWith reuses the recovery database with a pool that uses tracer.
func tracedProcessorWith(t *testing.T, r *recovery, tracer pgx.QueryTracer) *worker.Processor {
	t.Helper()
	cfg := r.db.Pool.Config()
	cfg.ConnConfig.Tracer = tracer
	pool, err := pgxpool.NewWithConfig(r.ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return &worker.Processor{DB: &db.DB{Pool: pool, Env: r.db.Env}, Store: r.store}
}

// Shutdown during a failure must not lose the retry row (AUD3-006 follow-up, R-1).
func TestRetryOutboxRowSurvivesShutdown(t *testing.T) {
	r := recoveryFixture(t, "RECEIVED")
	r.fake.lineFailures = 1000
	ctx, cancel := context.WithCancel(r.ctx)
	defer cancel()
	proc := tracedProcessorWith(t, r, sqlTracer{
		re:      regexp.MustCompile(`UPDATE "UploadAttempt"\s+SET "status" = \$1`),
		onMatch: cancel,
	})

	if err := proc.ProcessEvent(ctx, r.outboxID); err != nil {
		t.Fatalf("failure bookkeeping must commit after shutdown, got %v", err)
	}
	if n := r.openAttempts(t); n != 0 {
		t.Fatalf("expected no open attempt after the failure commit, got %d", n)
	}
	if n := r.countOutbox(t, "PROCESS"); n != 1 {
		t.Fatalf("expected exactly one PROCESS retry row, got %d", n)
	}
	if st := r.row(t); st.status != "RETRY_PENDING" {
		t.Fatalf("expected RETRY_PENDING, got %+v", st)
	}
}
