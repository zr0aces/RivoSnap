# Audit 3 Remediation (AUD3-001 to AUD3-031)

**Status:** 2026-11-23 — Implemented, all 35 tasks checked, uncommitted; about to be committed on a branch with a PR. Two fix rounds: re-audit round 1 found 5 LOW and 6 INFO (Tasks 28–35) and the round-2 delta re-audit was clean. Run on the working tree: `go test ./...` (68 passed, 14 packages), `go vet`, `gofmt`, `staticcheck`, `go test -race` on worker, media and api, `scripts/test-integration.sh` (127 passed, 0 failed, 1 pre-existing `ffmpeg required` skip), `pnpm typecheck`, `pnpm build`, Chromium e2e (267 passed, 2 skipped), `docker compose --profile edge config --quiet`, `sync-version.mjs --check` (`Checked 2026.11.7`), local image builds. Not run: WebKit (host libraries), the first CI run of the new image and Compose jobs and the SHA-pinned actions, `actionlint`, `govulncheck`, a real SIGTERM under the 110 s grace period, the real-Chromium profile fixture, the plan validator (script absent), and LINE content retention (unverified). Task 34's `routes.spec.ts` is a characterization test: the AUD3-018 leaked-subscription impact was not reproduced, and the split stays for rules-of-hooks correctness. Stays under **Active** until the PR merges and CI passes, then move to `completed/` per the filing rules.

## Goal and scope

Fix every finding in [`docs/audit/2026-11-23-audit-report.md`](../audit/2026-11-23-audit-report.md) at the layer that owns the behavior, with a regression test for each behavior change. Order is by risk: original-photo recovery first (AUD3-003, AUD3-002, AUD3-006), then availability and error handling, then the frontend, then CI, agent tooling and docs.

Binding user decisions:

- **AUD3-010:** documentation only. No release commit, tag, `VERSION` change or code guard. The migration runner already refuses to start a pre-MFA binary against an MFA database (`internal/db/migrate.go:145`, "database has migrations unknown to this binary"; `cmd/rivosnap/main.go` exits 1), so a rollback fails closed.
- **AUD3-005:** ~~track the agent-parity managed files and ignore only runtime, cache, log and secret paths.~~ Decision reversed by owner decision on 2026-11-23 after PR #20 opened: `.agent-parity/` is ignored again and the 21 files were untracked with `git rm --cached` (local copies kept); a fresh clone needs a local agent-parity install.

Out of scope: a release or version bump, `GOMEMLIMIT` (see the values table), a bulk "retry all FAILED" action, new dependencies, and the OpenAPI document version (it identifies the API contract, not a calendar release).

Invariants kept (AGENTS.md): channel and message dedup, the transactional outbox, reconcile never repeats an uncertain Google write, no silent deletion of an unuploaded original, one Nginx `web` service, existing host bindings and the `WEB_PORT` contract, secrets stay server-side.

## Verified state and decisions

Line numbers are from `a9ac792`; find each site by the function named next to it.

**Values chosen (override here if needed):**

| Decision | Value | Why |
|---|---|---|
| Automatic retry budget (AUD3-002) | Retry while the attempt number within the current budget cycle is below `MaxAutoAttempts = 16` (15 retries). `Backoff` is unchanged: `2000·2^n` ms with jitter 0.5–1 and the existing 1-hour cap. | Retries 1–10 span 34 min–68 min; the cap is reached at n=11, so retries 11–15 wait 30–60 min each. Total horizon is 3.1 h (minimum jitter) to 6.1 h (maximum), about 4.6 h on average. The old budget was 4–8.5 min. LINE does not publish a content retention period (the audit could not verify it), but the project's own failed-item review deadline is 7 days, so 6 h is far inside any plausible expiry. It is not longer because a persistent transient fault (revoked Google access, quota) should surface as an actionable FAILED within hours, not hide in RETRY_PENDING for days. |
| Retry budget reset (AUD3-002) | No schema change. A budget cycle ends at the item's latest attempt whose status is `FAILED`. Budget = number of attempts with `attemptNumber` greater than `COALESCE(MAX(attemptNumber) FILTER (WHERE status = 'FAILED'), 0)` and status other than `INTERRUPTED`, plus one. The ledger numbering (`attemptNumber = count + 1`) does not change. | The only code that sets an image to FAILED is the worker failure transaction (`ProcessEvent`, `next = model.StateFailed`), and that same transaction writes the current attempt row with `status = next`, so every FAILED item, including crash-loop and budget-exhaustion endings, has a `FAILED` attempt row marking the cycle boundary. `handleRetryJob` and the album-repair requeue (`handleResolveAlbum`) only restart FAILED items, so both reset the budget with no edit. Items that were exhausted before this change reset the same way. NEEDS_REVIEW items never enter RETRY_PENDING, so the budget does not apply to them. |
| Crash-loop protection (AUD3-002) | Unchanged: three open `ACTIVE` attempts end the item as `WORKER_INTERRUPTED`. It does not read the retry budget. | A poison item that kills the worker is still stopped after three crashes. |
| Password-hash gate (AUD3-001) | `maxHashJobs = 2` slots, `hashWaitTimeout = 5 s`, answer `503` with `Retry-After: 2`. Applies to `HashPassword` (setup) and `VerifyPassword` (login and profile change start). | One hash holds 64 MiB (`m=65536`); two hold 128 MiB, at most about 256 MiB with Go GC headroom. That leaves at least 512 MiB of the 768 MiB `mem_limit` for the runtime and the two `ffmpeg` thumbnail jobs (`maxThumbJobs = 2`). Two slots also cap CPU at 8 Argon2 threads. 5 s absorbs a burst of about 10 queued hashes at 0.2–0.4 s each. 503, not 429: the frontend treats 429 on `/auth/mfa` as "restart sign-in", and this is server saturation, not a per-client limit. The per-address rate check still runs first. |
| `GOMEMLIMIT` | Not set. | With 2 slots the live Argon2 memory is bounded; `ffmpeg` children are outside `GOMEMLIMIT` anyway, and a too-low limit risks GC thrash. |
| Worker `stop_grace_period` (AUD3-006) | `110s` on `worker` in both Compose files. | Album creation keeps the normal 90 s request timeout (`google.CreateAlbum` and `httpclient.Request`) but is no longer cancelled by SIGTERM, so a stop can wait up to 90 s for that call plus up to 10 s of bookkeeping. A fixed shorter detached timeout was rejected because it would turn a slow but normal creation into an uncertain one and block the group on `ALBUM_REVIEW`. The `AfterFunc` cap alternative was rejected as more machinery for a window that is normally about a second long. Longer uploads are still cancelled and recorded as interrupted. |

**No-change decisions (INFO):**

- **AUD3-023 (worker healthcheck, no `statement_timeout`):** no change. A global `statement_timeout` would also apply to migrations, which both `api` and `worker` run at start (a large index build could be killed), and it does not cure the audit's scenario, a hung socket. The API already shows a stalled worker through `worker:lastProgress` on `/health`, and Docker does not restart unhealthy containers, so a health tweak buys no recovery. Revisit if a restart policy is added. Recorded in `docs/plans/roadmap.md` (Task 27).
- **AUD3-025 (per-request revision lookup, `no-store` on thumbnails):** no change. `no-store` is the safer default for a private photo library on shared devices: `private, max-age` would leave thumbnails in the browser cache after logout. The lookup is one Redis read plus one primary-key Postgres read; 36 per gallery page is negligible at single-owner scale. Benchmark candidate in `roadmap.md`.
- **AUD3-026 (cleanup queries scale with history):** no change. The audit itself rates it negligible at personal scale. Two extra indexes would add write cost to every `Image` and `WebhookEvent` insert for no measured benefit. Benchmark candidate in `roadmap.md`, to be promoted when the library passes roughly 100k images.

**Scope calls inside fixes:**

- AUD3-013: `handleDashboardStats` returns 5xx when staging usage, free space or disk pressure cannot be read, instead of reporting zero. The frontend `Stats` type stays numeric. `rows.Err()` fixes in `handleGetGroups` and `handleGetImage` have no practical mid-stream fault injector in this suite, so they are covered by the dashboard regression tests plus review of the identical `handleListImages` pattern.
- AUD3-009: the on-demand thumbnail path needs a non-NULL `mimeType` (set only after inspection) and a status other than `UNRECOVERABLE`. The UI already tolerates the existing 404 "Image not available yet." response, so no frontend change.
- AUD3-004: keep rendering when `/auth/status` has cached data and a refetch fails; no new banner (the sidebar health state already reports an unreachable API).
- AUD3-030: no `prefers-color-scheme` default; that is a feature, not a fix.

## Traceability

| ID | Severity | Task | Outcome |
|---|---|---|---|
| AUD3-001 | MEDIUM | 8 | Process-wide Argon2 gate, 503 on timeout |
| AUD3-002 | MEDIUM | 3 | Budget cycle ends at the last FAILED attempt, so owner retry resets it; 15 retries over 3–6 h; no migration |
| AUD3-003 | MEDIUM | 2 | Retry routes to download unless the original has recorded checksum and MIME |
| AUD3-004 | MEDIUM | 12 | Blocking error only when there is no cached auth data |
| AUD3-005 | MEDIUM | 21 | ~~Track agent-parity managed files; narrow `.gitignore`~~ Reversed: keep `.agent-parity/` local and ignored |
| AUD3-006 | LOW | 4 | Detached persistence, album creation not cancelled by SIGTERM, interrupted attempts not counted, 110 s grace period |
| AUD3-007 | LOW | 5 | SIGKILL, missing binary, cancelled parent context and filesystem errors are transient; bad exits, other signals and the inner timeout stay unsupported |
| AUD3-008 | LOW | 6 | Preview failure no longer blocks upload |
| AUD3-009 | LOW | 7 | No on-demand `ffmpeg` on uninspected or unrecoverable rows |
| AUD3-010 | LOW | 23 | Docs only: MFA ships in the next release; do not pin 2026.11.7 after enrolling |
| AUD3-011 | LOW | 24 | Backup runbook gains key and staging steps |
| AUD3-012 | LOW | 24 | UTC repair command works inside Compose |
| AUD3-013 | LOW | 9 | `rows.Err()` and dashboard error propagation |
| AUD3-014 | LOW | 20 | CI image build, Compose checks, SHA-pinned actions |
| AUD3-015 | LOW | 25 | Document `WEB_PORT=127.0.0.1:8080` and the firewall caveat |
| AUD3-016 | LOW | 25 | Reword the guess limit as per client address |
| AUD3-017 | LOW | 13 | Viewer tracks item id, not index |
| AUD3-018 | LOW | 14 | Albums route no longer shares `Groups` hooks |
| AUD3-019 | LOW | 15 | Focus moves into and back out of change forms |
| AUD3-020 | LOW | 11, 19 | Honest mock API and the missing auth-path specs |
| AUD3-021 | LOW | 26 | Plan index and Status lines corrected |
| AUD3-022 | LOW | 22 | Portable Codex graphify hook |
| AUD3-023 | INFO | 27 | No change (reason above) |
| AUD3-024 | INFO | 1 | Integration tests refuse non-loopback databases |
| AUD3-025 | INFO | 27 | No change (reason above) |
| AUD3-026 | INFO | 27 | No change (reason above) |
| AUD3-027 | INFO | 10 | OpenAPI security metadata and missing paths, plus a route-table test |
| AUD3-028 | INFO | 25 | Docs say both `api` and `worker` migrate |
| AUD3-029 | INFO | 16 | Health labels, Bangkok time, onboarding copy |
| AUD3-030 | INFO | 17 | Guarded storage and theme before first paint |
| AUD3-031 | INFO | 18 | Empty state for a page past the end |

## Touchpoints

- Backend: `internal/worker/worker.go`, `internal/model/model.go`, `internal/media/media.go`, `internal/api/server.go`, `internal/api/security.go`, `internal/db/migrate_test.go`, new `internal/testguard/testguard.go`. No migration.
- Tests: `internal/api/pipeline_test.go`, new `internal/api/worker_recovery_test.go`, new `internal/api/export_test.go`, new `internal/api/hashgate_test.go`, new `internal/api/openapi_test.go`, `internal/media/media_test.go`, `internal/model/model_test.go`.
- Frontend: `apps/web/src/main.tsx`, `photos.tsx`, `workspace.tsx`, `profile.tsx`, `onboarding.tsx`; `e2e/fixtures.ts` and specs.
- Deployment and CI: `docker-compose.yml`, `docs/example-deploy/compose.yml`, `.github/workflows/ci.yml`, `.github/workflows/release-images.yml`.
- Tooling: `.gitignore`, `.codex/hooks.json`, the 21 files under `.agent-parity/`.
- Docs: `README.md`, `docs/{ARCHITECTURE,SECURITY,DOCKER_DEPLOYMENT,TROUBLESHOOTING,OWNER_GUIDE}.md`, `docs/example-deploy/{README.md,env.example}`, `.env.example`, `docs/plans/*`, `docs/audit/2026-11-23-audit-report.md`.

## Public contracts

- No schema change. The retry budget is derived from the existing `UploadAttempt` ledger, so there is no migration and rollback compatibility is unchanged.
- `POST /api/v1/auth/login`, `/auth/setup` and `/profile/start` can now answer `503` with `Retry-After: 2` and `{"error":"Sign-in is busy. Try again shortly."}`.
- `GET /api/v1/dashboard/stats` answers 5xx rather than zeroed values when the staging volume cannot be read.
- `GET /api/v1/images/{id}/thumbnail` answers 404 for rows that never passed inspection and have no remote copy.
- `/api/openapi.json` gains a top-level `security` default, `security: []` on public operations and the `/api/openapi.json` and `/ready` paths. Spec `version` stays `1.0.0`.
- Compose: `worker.stop_grace_period: 110s`. `WEB_PORT` contract unchanged.
- `UploadAttempt.status` gains the value `INTERRUPTED` (text column, no constraint).

## Blast radius

Worker (every item passes through Tasks 2–6), login and profile change flow (Task 8), the whole SPA shell (Tasks 12–19), CI and release workflow (Task 20), and every agent client's configuration (Tasks 21–22). The riskiest edits are Task 3 (retry arithmetic over the attempt ledger) and Task 4 (context handling around uncertain Google writes); both have fixture tests against disposable Postgres.

## Task list

Run tasks in order. Where a task says "test first", write the failing test, run it, then make the change. Integration tests run only through `rtk proxy bash scripts/test-integration.sh` (disposable containers); never against a real `DATABASE_URL`. Prefix every shell command with `rtk`.

- [x] **Task 1: Refuse destructive integration tests on non-disposable databases (AUD3-024)**
  - Files: new `internal/testguard/testguard.go`; replace the seven `GALLERY_INTEGRATION` skip blocks in `internal/api/pipeline_test.go` (lines 68, 793, 933, 1192) and `internal/db/migrate_test.go` (lines 36, 112, 131).
  - Change: add `testguard.Require(t)`. It skips when `GALLERY_INTEGRATION` is unset, and calls `t.Fatal` unless the host of `DATABASE_URL` (and `REDIS_URL`) is `127.0.0.1`, `::1` or `localhost`. `scripts/test-integration.sh` binds both containers to `127.0.0.1`, so no override variable is needed. Production URLs use the Compose host `postgres`, so a shell with a production environment loaded is refused. The package is imported only by tests.
  - Test: a table test in `internal/testguard/testguard_test.go` for loopback hosts accepted (`127.0.0.1`, `::1`, `localhost`) and the Compose host `postgres` refused. The package stays shared because its callers span the `api` and `db` test packages.
  - Verify: `rtk go test ./internal/testguard/...`; `rtk proxy bash scripts/test-integration.sh` still passes; with `GALLERY_INTEGRATION=1` and `DATABASE_URL=postgresql://u:p@postgres:5432/gallery` set in the shell, `rtk go test ./internal/db/...` fails at the guard before any connection is attempted.

- [x] **Task 2: Route retries by what was recorded, not by file presence (AUD3-003)**
  - Files: `internal/worker/worker.go` (`ProcessEvent`, the `RECEIVED`/`RETRY_PENDING` routing block).
  - Test first: new `internal/api/worker_recovery_test.go` with a fixture `recoveryFixture(t)` (copy `crashLoopFixture`, but set `LineChannelAccessToken: "test"`, `TokenEncryptionKey`, Google client fields, and seed a `GoogleConnection` with `crypto.Seal` as in `thumbnailFixture`) and a stub transport helper (LINE download counter, Google token, album, upload, `batchCreate` counter). Tests: `TestRetryReinspectsStagedOriginalWithoutChecksum` (FAILED/`WORKER_INTERRUPTED` item with a valid PNG at `PathFor(id, 0)`, `stagingPath` set, `sha256` and `mimeType` NULL, moved to RETRY_PENDING as `handleRetryJob` does; after `ProcessEvent` the image is STAGED with `sha256` and `mimeType` recorded, an `UPLOAD` outbox row exists, the file is intact, zero LINE downloads, never `INTEGRITY`; a second `ProcessEvent` on the `UPLOAD` row reaches SYNCED with one `batchCreate`); `TestRetryOfQuarantinedGarbageStaysUnrecoverableAndKept` (garbage bytes at the path end UNRECOVERABLE/`UNSUPPORTED_IMAGE` with the file still present); `TestRetryWithRecordedChecksumStillGoesToUpload` (RETRY_PENDING with `sha256`, `mimeType` and file set uploads without a LINE download).
  - Change: send RETRY_PENDING (and RECEIVED) to `UPLOAD_PENDING` only when `stagingPath` is non-empty and both `sha256` and `mimeType` are non-NULL; otherwise to `DOWNLOAD_PENDING`. The existing `DOWNLOADING` branch already inspects a file found at the staging path and persists STAGED via `moveStaged` without downloading. `RETRY_PENDING → DOWNLOAD_PENDING` is already an allowed edge in `internal/model/model.go`.
  - Verify: `rtk go test ./internal/model/...`; `rtk proxy bash scripts/test-integration.sh` (new tests plus `TestCrashLoopKeepsStagedFileDiscardable` and `TestRestageInspectFailureKeepsStagingPath` unchanged).

- [x] **Task 3: Retry budget that resets on owner action and spans hours (AUD3-002)**
  - Files: `internal/model/model.go` (`MaxAutoAttempts = 16`, comment tying it to the Backoff cap); `internal/worker/worker.go` (the attempt-count query in `ProcessEvent`, the `f.Transient && n < 8` decision, the `model.Backoff(n, ...)` call). No migration, no `ImageRow` or `loadImage` change, and no edit to `handleRetryJob` or `handleResolveAlbum`.
  - Change: keep `n = count + 1` as the ledger `attemptNumber`. Compute `budget` in the same place, before the new attempt row is inserted: the number of the item's attempts whose `attemptNumber` is greater than `COALESCE(MAX(attemptNumber) FILTER (WHERE status = 'FAILED'), 0)` and whose status is not `INTERRUPTED` (the exclusion is wired in Task 4; add it here if Task 4 has not landed), plus one. Retry while `f.Transient && budget < model.MaxAutoAttempts`; call `Backoff(budget, f.Delay)`. Why this resets on owner action: `handleRetryJob` and the album requeue only restart FAILED items, and every FAILED transition writes a `FAILED` attempt row in the same transaction as the image update (the failure transaction in `ProcessEvent`, including the crash-loop path). Crash-loop code is untouched.
  - Test first: `internal/model/model_test.go` `TestRetryHorizon` sums `Backoff(1..MaxAutoAttempts-1, 0)` over many samples and asserts every sum is at least 3 h and at most 6.2 h; `TestBackoffBounds` still holds. In `worker_recovery_test.go`: `TestEightNetworkFailuresThenSuccess` (LINE stub fails with a network error eight times; after each `ProcessEvent` the image is RETRY_PENDING with a new `PROCESS` outbox row; the ninth call succeeds and the item reaches SYNCED); `TestBudgetExhaustionThenManualRetryResets` (after 15 failures the item is FAILED and its last attempt row is `FAILED`; `POST /api/v1/jobs/{id}/retry` through `integrationServer` is unchanged, and the next transient failure is RETRY_PENDING with `availableAt` within 5 s, proving `Backoff(1)`); `TestLegacyExhaustedItemResetsOnRetry` (seed eight attempts ending in a `FAILED` row, as the old budget produced, retry, and expect RETRY_PENDING after one transient failure); `TestAlbumRepairRequeueResetsBudget` (a FAILED/`ALBUM_REVIEW` item with many attempts, requeued by resolve-album, gets a fresh budget); `TestBudgetIgnoresInterruptedAttempts` (added with Task 4). Add `LineChannelAccessToken: "test"` to `integrationServer`'s env.
  - Verify: `rtk go test ./internal/model/...`; `rtk proxy bash scripts/test-integration.sh`.

- [x] **Task 4: Shutdown records its outcome and does not burn the budget (AUD3-006)**
  - Files: `internal/worker/worker.go` (`ProcessEvent`, `ResolveAlbum`, `dispatch`, `RunLoop`); `docker-compose.yml` and `docs/example-deploy/compose.yml` (`stop_grace_period: 110s` under `worker`); the budget query from Task 3.
  - Change:
    1. Add a detached persistence context (`context.WithoutCancel(ctx)` with a 10 s timeout) for the writes that close a step: the STAGED checkpoint transaction, the final SYNCED transaction and the failure bookkeeping transaction. `moveState`, `moveStaged` and `syncMedia` keep using `ctx`. That is safe: if a cancel makes one of them fail, the image stays in its previous state and resumes from there on restart. In particular, if `batchCreate` succeeded but `syncMedia` fails on cancel, the item is still `CREATING_MEDIA` and the next run reconciles by filename and adopts the single match; it never creates again.
    2. In `ResolveAlbum`: (a) after the `albumCreating` claim succeeds (the first UPDATE) and before `CreateAlbum`, check `ctx.Err()`; if cancelled, release the claim (`albumCreating = false`) on a detached 10 s context and return `ctx.Err()`, so no new Google write starts after SIGTERM. (b) Run `CreateAlbum` on `context.WithoutCancel(ctx)`; the call keeps its normal 90 s timeout from `httpclient.Request`, so a slow creation is not turned into an uncertain one. (c) Run the success UPDATE (`googleAlbumId`, `albumCreating = false`) and the error-path `albumCreating = false` UPDATE on detached 10 s contexts. The 110 s `stop_grace_period` covers 90 s plus bookkeeping.
    3. Define interruption as `ctx.Err() != nil` after `run()` returns with a non-nil error, whatever that error is. Place the check after the existing `runErr == nil` early return (`worker.go:569-571`), so a run that committed SYNCED and then saw a cancel keeps its SYNCED attempt status. A cancel during `CreateMedia` makes the `Reconcile` call fail on the dead context, which returns the `MEDIA_UNCERTAIN` fault; that must not move the item to NEEDS_REVIEW. When interrupted and the run did not end in the crash-loop path, mark the attempt `INTERRUPTED` with `completedAt` on the detached context, leave the image state and the outbox row unchanged, and return nil. A `CREATING_MEDIA` image therefore resumes as a lookup-only reconcile and never repeats `batchCreate`; other states resume from their current step after the one-minute lease.
    4. Exclude `INTERRUPTED` attempts from the retry budget count (Task 3 query). They do not count toward the crash-loop cap either, because that counts only open `ACTIVE` attempts.
    5. In `dispatch`, return nil at the top of the loop when the context is cancelled and when `ProcessEvent` fails because of cancellation, so `RunLoop` exits 0 on SIGTERM instead of exit status 1.
    - A non-cancellation `ProcessEvent` error still exits the process by design; the open attempt it leaves still counts toward the three-crash cap, which is the poison-item protection.
  - Test first: `TestShutdownKeepsItemRecoverable` (stub cancels the worker context on the `/uploads` request; after `ProcessEvent` the attempt is `INTERRUPTED` with `completedAt`, no open attempts, the image is not FAILED, the outbox row remains; a second `ProcessEvent` with a live context reaches SYNCED with exactly one `batchCreate`); `TestShutdownAfterCreateAlbumPersistsMapping` (context cancelled while `POST /albums` is in flight; the call completes, `LineGroup.googleAlbumId` is set and `albumCreating` is false); `TestShutdownBeforeCreateAlbumReleasesClaim` (context cancelled between the claim and `CreateAlbum`; no `POST /albums` is sent and `albumCreating` is false again); `TestShutdownInCreatingMediaStaysReconcileOnly` (context cancelled during `batchCreate`; the state stays `CREATING_MEDIA`, not NEEDS_REVIEW, no second `batchCreate` on the following live run, which reconciles to SYNCED); `TestBudgetIgnoresInterruptedAttempts` (interrupted attempts do not shorten the retry budget). Existing `TestCrashLoop*` stay green.
  - Verify: `rtk proxy bash scripts/test-integration.sh`; `rtk docker compose --profile edge config --quiet`; `rtk docker compose -f docs/example-deploy/compose.yml config --quiet` with dummy values for the required variables exported inline (never print the expanded config).

- [x] **Task 5: Classify transient tool and filesystem faults, not hostile files (AUD3-007)**
  - Files: `internal/media/media.go` (`Inspect`, `InspectVideo`).
  - Change: in `InspectVideo`, when `ffprobe` fails, classify in this order. (1) The parent context ended (`ctx.Err() != nil`, shutdown or caller deadline): transient fault `MEDIA_TOOL` ("Media tools were unavailable. Retry later."). (2) The inner 30 s timeout fired (`cctx.Err() != nil` while the parent is live): stays `UNSUPPORTED_VIDEO`, because a file that makes `ffprobe` run for 30 s is treated as hostile and must not get 15 retries over about 6 h. (3) `exec.ErrNotFound`: transient `MEDIA_TOOL`. (4) Terminated by SIGKILL (the OOM-killer signal; checked after step 2 because `CommandContext` also kills with SIGKILL on timeout): transient `MEDIA_TOOL`. (5) Everything else, including a non-zero exit, SIGSEGV and SIGABRT: `UNSUPPORTED_VIDEO`. Make the inner timeout a package variable (`probeTimeout`, 30 s) so a test can shorten it. In `Inspect`, a failure to open the file (other than not-exist) and a `Digest` I/O error return the raw error, which the worker already maps to transient `PROCESSING`. Not-exist keeps today's `UNSUPPORTED_*`: the file is gone and retrying cannot help.
  - Test first (`internal/media/media_test.go`, stub `ffprobe` placed first in `PATH` through `t.Setenv`): `TestInspectVideoToolFaults` with one case per class. Transient (non-unsupported) cases: stub killed with SIGKILL, binary absent from `PATH`, parent context cancelled while the stub sleeps. `UNSUPPORTED_VIDEO` cases: stub exits 1 on garbage, stub raises SIGSEGV, stub raises SIGABRT, stub sleeps past a shortened `probeTimeout` with a live parent context. `TestInspectOpenErrorIsNotUnsupported` (a directory or unreadable file yields a non-fault error).
  - Verify: `rtk go test ./internal/media/...`.

- [x] **Task 6: Preview generation is best effort (AUD3-008)**
  - Files: `internal/worker/worker.go` (preview block before `moveState(StateUploadPending)`).
  - Change: if `Store.Thumbnail` fails for either size, log one line with the image ID and error type, leave `thumbnailPath` NULL and continue to the upload. If the work context is cancelled, still return the error (shutdown handling from Task 4 applies). The API regenerates previews on demand for inspected rows (Task 7 keeps that path), so only the gallery view degrades.
  - Test first: `TestPreviewFailureDoesNotBlockUpload` (stub `ffmpeg` that exits 1 first in `PATH`, valid staged PNG with recorded checksum; the item reaches SYNCED, `thumbnailPath` is NULL) in `worker_recovery_test.go`.
  - Verify: `rtk proxy bash scripts/test-integration.sh`.

- [x] **Task 7: No on-demand `ffmpeg` on uninspected bytes (AUD3-009)**
  - Files: `internal/api/server.go` (`handleMediaStream`, the SELECT and the `!hasRemote` gate); `internal/api/pipeline_test.go` (`thumbnailFixture` local `seed`).
  - Change: also select `"uploadStatus"`. When there is no remote copy, generate a thumbnail only if `mimeType` is non-empty and the status is not `UNRECOVERABLE`; otherwise answer 404 "Image not available yet." as today. Update the fixture's local seed to set `mimeType = 'image/png'` (a staged, inspected row) so `TestThumbnailConcurrencyBounded` and `TestThumbnailLeaderCancelDoesNotFailFollower` remain valid controls.
  - Test first: `TestQuarantinedRowGetsNoThumbnailRun` (UNRECOVERABLE row, `stagingPath` set, `mimeType` NULL, garbage file; GET `/thumbnail` returns 404 and the stub-`ffmpeg` log is empty; a validated row beside it still renders).
  - Verify: `rtk proxy bash scripts/test-integration.sh`.

- [x] **Task 8: Bound concurrent password hashing (AUD3-001)**
  - Files: `internal/api/server.go` (`Server` fields `hashSem chan struct{}` and `hashWait time.Duration`, constants `maxHashJobs = 2` and `hashWaitTimeout = 5 * time.Second` beside `maxThumbJobs`, `NewServer`, `writeErr`, `handleAuth` setup and login branches); `internal/api/security.go` (`handleChangeStart`, the `VerifyPassword` call); new `internal/api/export_test.go`.
  - Change: add `acquireHash(ctx) (release func(), ok bool)` that waits for a slot, the wait timeout or request cancellation. Wrap only the `HashPassword` and `VerifyPassword` calls. On timeout return a sentinel `errHashBusy` (`httpError` status 503, message "Sign-in is busy. Try again shortly."); `writeErr` adds `Retry-After: 2` for that sentinel. The rate-limit check still runs before the gate. In `handleChangeStart`, when `in.Method != "passkey"`, take the slot at the top of the handler, before `beginOwner(r, true)`, and release it with a defer. Otherwise the wait (up to 5 s) and the hash would run while the transaction holds the `Owner` row lock. The existing `len(in.Password) > 256` and `VerifyPassword` logic stays where it is, and the passkey method never takes a slot. No semaphore in `internal/crypto`.
  - Test first: `internal/api/hashgate_test.go` (package `api`): acquire both slots, a third `acquireHash` with a 50 ms wait returns false, releasing a slot lets it succeed, and a cancelled context returns false. An integration test in `worker_recovery_test.go` builds a server with `api.NewServer(env, db, nil, store)` and uses the `export_test.go` helpers (`HoldHashSlots`, `SetHashWait`): with both slots held, `POST /auth/login` returns 503 with `Retry-After`, not 401 or 429; after release a wrong password returns 401. In the `handleChangeStart` case, the integration test also asserts that a 503 is returned without the Owner row lock being taken (a concurrent `SELECT ... FOR UPDATE NOWAIT` on `Owner` succeeds while the request waits). Peak overlap proof: a unit test with 20 goroutines through `acquireHash` records a maximum concurrent count of 2.
  - Verify: `rtk go test ./internal/api/...`; `rtk proxy bash scripts/test-integration.sh` (existing `TestLoginWrongPasswordIs401` and the owner-security tests unchanged).

- [x] **Task 9: Do not turn read errors into healthy-looking 200s (AUD3-013)**
  - Files: `internal/api/server.go` (`handleGetGroups`, `handleGetImage` attempts loop, `handleDashboardStats`).
  - Change: add `rows.Err()` checks after the `/groups` loop and the attempts loop; return on a `Scan` error in the attempts loop. In `handleDashboardStats`: treat `pgx.ErrNoRows` from the `oldest` query as "none" and any other error as `writeErr`; return `writeErr` when `Store.Usage` (both directories), `Store.Free` or `Store.Pressured` fail (same rule as `handleHealth`); check `rows.Err()` and scan errors for the `recent` audit query.
  - Test first: `TestDashboardStatsStagingErrorIs500` (remove the staging directory from `integrationServer`'s store dir, then GET `/dashboard/stats` returns 500; restore and expect 200 and the existing `TestDashboardStatsCounts`); `TestDashboardStatsDatabaseErrorIs500` stays. No fault injector exists for mid-stream row errors, so `handleGetGroups` and `handleGetImage` are reviewed against `handleListImages` and covered by `TestGroupsLatestImage` staying green.
  - Verify: `rtk proxy bash scripts/test-integration.sh`.

- [x] **Task 10: OpenAPI describes the real auth surface (AUD3-027)**
  - Files: `internal/api/server.go` (`openAPISpec`); new `internal/api/openapi_test.go` (package `api`).
  - Change: add a top-level `security: [{session: []}]`, `security: []` on `/api/webhooks/line`, `/api/v1/auth/status`, `/api/v1/auth/login`, `/api/v1/auth/setup` and the public `securityPaths` entries, and add the `/api/openapi.json` (session) and `/ready` (public) paths. Keep `version` at `1.0.0`.
  - Test first: `TestOpenAPISecurityMatchesMiddleware` builds `NewServer(env, nil, nil, nil).Handler()` and, for every path and method in `openAPISpec()`, sends a request with the exact `Origin`, `X-Gallery-Request` and a placeholder UUID for `{id}` but no session. Operations not marked `security: []` must answer 401 (the middleware rejects before any database access); operations marked public must be exactly the hard-coded set above. This doubles as the audit's route-table test.
  - Verify: `rtk go test ./internal/api/...`.

- [x] **Task 11: Make `mockApi` honest and add shared auth mocks (AUD3-020, part 1)**
  - Files: `e2e/fixtures.ts`.
  - Change: `/images` and `/jobs` honor `page` (36 per page, echo the page number) and `from`/`to` (compare the Bangkok date of `lineEventTimestamp`); add a default 7-day `/dashboard/activity` response; export a `signedInThenFailing(page)` helper and a `statusSequence` helper for tests that need `/auth/status` to change over time; unmatched non-GET requests still return 403.
  - Verify: `rtk proxy corepack pnpm typecheck`; `rtk proxy corepack pnpm e2e --project=chromium` (the activity default changes `responsive.spec.ts` from the error state to the chart; fix only real layout regressions, and note any in the task's commit message).

- [x] **Task 12: Keep the workspace when a refetch fails (AUD3-004)**
  - Files: `apps/web/src/main.tsx` (`App`).
  - Change: show the blocking "Cannot reach the gallery" screen only when `auth.error && !auth.data`. With cached data the shell, routes, open forms, the lightbox and the login MFA step keep rendering.
  - Test first: `e2e/auth-resilience.spec.ts`: `page.clock.install()`, open Profile and start "Replace recovery codes" until the codes are shown (mocked `/profile/start`), then make `/auth/status` return 503 and `page.clock.fastForward(20_000)`; assert the codes and the form are still visible and no "Cannot reach" alert appears. Second test: the login MFA step survives the same failure. Third test: first load with `/auth/status` failing still shows the blocking screen and "Try again".
  - Verify: `rtk proxy corepack pnpm typecheck`; `rtk proxy corepack pnpm e2e --project=chromium -g "auth resilience"`.

- [x] **Task 13: Viewer follows the selected item (AUD3-017)**
  - Files: `apps/web/src/photos.tsx` (`Library` state and the `Lightbox` call).
  - Change: store the selected item's `id`; derive the index with `findIndex` on the current items; `set` maps an index back to an id; render the viewer only while the id is on the current page (it closes if the item leaves the page).
  - Test first: add to `e2e/gallery.spec.ts`: open the second card, change the mocked `/images` response to prepend a new item, `page.clock.fastForward(16_000)`, assert the viewer still shows the originally selected item (image URL and counter).
  - Verify: `rtk proxy corepack pnpm e2e --project=chromium -g "viewer"`.

- [x] **Task 14: `Groups` and `AlbumsView` stop sharing a component instance (AUD3-018)**
  - Files: `apps/web/src/workspace.tsx` (export `AlbumsView`; remove the `albums` prop and early return from `Groups`); `apps/web/src/main.tsx` (route `albums` to `<AlbumsView />`).
  - Test first: `e2e/workspace-responsive.spec.ts` (or a new `e2e/routes.spec.ts`): navigate groups, albums, groups, albums five times by sidebar clicks and count `/api/v1/groups` requests over a `page.clock.fastForward(16_000)` window; the count equals one poll per mounted view, not one per visit.
  - Verify: `rtk proxy corepack pnpm typecheck`; `rtk proxy corepack pnpm e2e --project=chromium -g "routes"`.

- [x] **Task 15: Predictable focus in change forms and MFA back (AUD3-019)**
  - Files: `apps/web/src/profile.tsx` (`ChangeForm`, `Profile` triggers), `apps/web/src/main.tsx` (`Login`).
  - Change: give the `ChangeForm` heading `tabIndex={-1}` and focus it on mount; give each trigger a `data-change` key built from action and target, remember it when a form opens, and focus the matching trigger after the form closes. In `Login`, add `setFocus` to the `useForm` destructure (`main.tsx:216`) and call `setFocus("email")` from a `useEffect` that runs when `mfaPending` changes from true to false (the email input is not mounted until React re-renders, so calling it inside the Back click handler does nothing).
  - Test first: in `e2e/profile.spec.ts`, press Enter on "Enable authenticator" and assert the heading is focused; Cancel returns focus to "Enable authenticator". In the MFA spec, after Back the email field is focused.
  - Verify: `rtk proxy corepack pnpm e2e --project=chromium -g "focus"`.

- [x] **Task 16: Health labels, time zone and onboarding copy (AUD3-029)**
  - Files: `apps/web/src/main.tsx` (move `healthLabels` out), `apps/web/src/workspace.tsx` (export `healthLabels`, add `lineCredentials: "LINE credentials"`, use it and `date()` in `Health`), `apps/web/src/onboarding.tsx` (copy depends on `prefs?.collectVideos`).
  - Test first: a Health-page spec with `/health` returning `lineCredentials: "Undecryptable"` and `workerLastProgressAt`: labels read "Database", "LINE credentials", and the time is formatted by `date()` (Bangkok). An onboarding assertion for both `collectVideos` values.
  - Verify: `rtk proxy corepack pnpm typecheck`; targeted e2e.

- [x] **Task 17: Guard storage and apply the theme before paint (AUD3-030)**
  - Files: `apps/web/src/main.tsx` (`App` theme state and effect).
  - Change: wrap `localStorage.getItem` and `setItem` in try/catch; apply `data-theme` in `useLayoutEffect`. No inline script in `index.html`, because the Nginx CSP forbids it. No `prefers-color-scheme` default.
  - Test first: `page.addInitScript` makes `Storage.prototype.getItem` throw, and the app still renders; with `gallery-theme=dark` stored, `html[data-theme="dark"]` is set before the first assertion on content.
  - Verify: `rtk proxy corepack pnpm e2e --project=chromium -g "theme"`.

- [x] **Task 18: Empty state for a page past the end (AUD3-031)**
  - Files: `apps/web/src/photos.tsx` (`Library` empty-state branch).
  - Change: when `items` is empty, `total > 0` and `page > 1`, show a short message with a link to the last page (`Math.ceil(total / 36)`) instead of a blank grid.
  - Test first: `/gallery?page=9` against the paginating mock shows the message and its link; following the link shows items.
  - Verify: `rtk proxy corepack pnpm e2e --project=chromium -g "past the end"`.

- [x] **Task 19: Missing auth-path e2e coverage (AUD3-020, part 2)**
  - Files: `e2e/profile.spec.ts`, `e2e/auth-login.spec.ts` (new), `e2e/fixtures.ts`.
  - Change (specs only): successful `/auth/mfa` 200 ends on the dashboard; `/auth/mfa` 410 shows "Verification expired" and returns to sign-in choices; a rejected passkey sign-in (`navigator.credentials.get` stubbed to reject through `addInitScript`) calls `/auth/cancel` and shows the error; a `ChangeForm` preview shows the QR and recovery codes, a 400 from `/profile/finish` keeps the preview, and a 429 returns to the start; a 503 from login shows the busy message (Task 8).
  - Verify: `rtk proxy corepack pnpm e2e --project=chromium`.

- [x] **Task 20: CI builds the shipped images and checks Compose; pin actions (AUD3-014)**
  - Files: `.github/workflows/ci.yml`, `.github/workflows/release-images.yml`.
  - Change: add an `images` job (no push, `contents: read`) that runs `docker build` for `Dockerfile.api` and `Dockerfile.worker`, checks `/app/VERSION` against `VERSION` as the release job does, and runs `ffmpeg -version` and `ffprobe -version` inside the worker image (proves the Alpine tools the worker needs are present). Add a `compose` step or job running `docker compose --profile edge config --quiet` with the one required variable set inline, and the same for `docs/example-deploy/compose.yml` with dummy values for its required variables. The Ubuntu `ffmpeg` in the integration job stays. `release-images.yml` calls `ci.yml`, and its `publish` job already builds both images, so give the `images` job `if: github.event_name != 'release'`. Inside a reusable workflow the `github` context is the caller's, so the event name is `release` on that path. If that assumption proves wrong, the only cost is a duplicate image build on release. The Compose checks stay unconditional (they are cheap) and still gate releases. The release job's own steps and `packages: write` stay as they are.
  - Pin `actions/checkout`, `actions/setup-go` and `actions/setup-node` to full commit SHAs with the tag in a trailing comment, resolved with `gh api repos/<owner>/<name>/commits/<tag> --jq .sha` (read-only). If the lookup is unavailable, keep the tags and record that in the task notes. Leave `govulncheck@v1.10.0` as is.
  - Verify: `rtk docker compose --profile edge config --quiet`; local `rtk docker build -f Dockerfile.api -t rivosnap-api:ci .` and `-f Dockerfile.worker` if a daemon is available; parse both workflow files (`rtk proxy python3 -I -c "import yaml,sys; yaml.safe_load(open(sys.argv[1]))" <file>` or `actionlint` when installed). The first PR run is the final proof.

- [x] **Task 21: Track agent-parity managed files (AUD3-005)**
  - **Reversed (2026-11-23):** reversed by owner decision on 2026-11-23 after PR #20 opened: `.agent-parity/` is ignored again and the 21 files were untracked with `git rm --cached` (local copies kept); a fresh clone needs a local agent-parity install. The steps below record what was originally done.
  - Inspected first (2026-11-23): `.agent-parity/` holds 21 files and one empty directory. Status: `rtk proxy .agent-parity/bin/agent-parity status` reports `git: IGNORED and will not sync via git: .agent-parity`. The MCP server binary is not inside the directory: `run.sh` downloads and checksum-verifies it into the shared cache (`~/.cache/agent-parity`); `mcp/memory/dist/` is a legacy in-tree location. A search of the directory for secret-like strings found none.
  - Files: `.gitignore` (replace line 42, `.agent-parity/`, which sits outside the managed `agent-parity:begin/end` block); the 21 files below.
  - **Become tracked (21):** `.agent-parity/bin/agent-parity`, `bin/agent-parity.cmd`, `claude/settings.json`, `mcp/memory/RELEASE`, `mcp/memory/VERSION`, `mcp/memory/run.cmd`, `mcp/memory/run.sh`, and under `scripts/`: `common.ps1`, `common.sh`, `merge-memory.sh`, `pre-push.sh`, `self-heal.ps1`, `self-heal.sh`, `status.ps1`, `status.sh`, `sync-claude.ps1`, `sync-claude.sh`, `uninstall.ps1`, `uninstall.sh`, `version.ps1`, `version.sh`. Memory entries (`.agent-parity/memory/*.md`) are tracked when they appear; the directory is empty today (nothing to add, no `.gitkeep`).
  - **Stay ignored:** `.agent-parity/mcp/memory/dist/` (legacy binaries); `.agent-parity/**/*.log`, `*.pid`, `*.lock`, `*.tmp`; `.agent-parity/**/.env*`, `*.key`, `*.pem`; and the temp files the installer writes next to configs (`**/.*.agent-parity.*`). Elsewhere unchanged: `.claude/skills/` (generated copy, ignored by `/.claude/*`), `graphify-out/`, `.env`.
  - Change: edit `.gitignore` as above; run `git check-ignore -v` on a sample of each class; stage only the `.gitignore` change and the 21 files with `git add` (no commit; the owner commits). Before staging, re-run a case-insensitive search for `secret|token|password|api[_-]?key|PRIVATE KEY` over `.agent-parity/` and stop if anything matches.
  - Verify: `rtk proxy git ls-files .agent-parity | wc -l` is 21 after staging; `rtk proxy git status --short --ignored` shows `.agent-parity` no longer ignored and no `dist`, log or secret path staged; `rtk proxy .agent-parity/bin/agent-parity status` prints `git: all artifacts tracked`; the pre-push guard stays registered. Note for the owner: the guard refuses a push until these files are committed.

- [x] **Task 22: Portable Codex graphify hook (AUD3-022)**
  - Files: `.codex/hooks.json` (the `PreToolUse` command only).
  - Change: replace the absolute `/home/san/.local/bin/graphify hook-check` with `sh -c 'command -v graphify >/dev/null 2>&1 || exit 0; exec graphify hook-check'`. Leave the agent-parity `SessionStart` entry untouched.
  - Verify: `rtk proxy node -e "JSON.parse(require('fs').readFileSync('.codex/hooks.json','utf8'))"`; `rtk proxy .agent-parity/bin/agent-parity status` still shows `codex: registered`. Note: running `graphify codex install` later may restore an absolute path; rerun this edit if so.

- [x] **Task 23: Docs for the MFA release and rollback (AUD3-010, docs only)**
  - Files: `docs/DOCKER_DEPLOYMENT.md` (Operations paragraph on MFA), `docs/SECURITY.md` (owner authentication changes), `docs/example-deploy/README.md` (step 2 and the MFA paragraph), `docs/example-deploy/env.example` (IMAGE_TAG comment), `README.md` (Profile sentence).
  - Change: state that owner MFA and passkeys first ship in the release after `v2026.11.7` (the next CalVer; `2026.11.7` and `latest` before that release are password-only, and `VERSION` is still `2026.11.7`); mark the MFA docs "from the first release after 2026.11.7"; replace the `2026.11.7` pin example with a neutral `YYYY.M.N` and warn never to pin `2026.11.7` or older once MFA is enrolled; explain that rolling back is refused by the migration runner with `database has migrations unknown to this binary` and the API will not start, so fix forward. No change to `VERSION`, tags or code.
  - Verify: `rtk proxy grep -rn "2026.11.7" docs README.md` shows only the intended warning text; `rtk proxy node scripts/sync-version.mjs --check` still `Checked 2026.11.7`.

- [x] **Task 24: Backup and repair runbooks (AUD3-011, AUD3-012)**
  - Files: `docs/DOCKER_DEPLOYMENT.md` (Metadata backup and restore), `docs/SECURITY.md` (key consequence list), `docs/example-deploy/README.md` (upgrade paragraph and the repair note), `docs/TROUBLESHOOTING.md` (One-off UTC timestamp repair).
  - Change: add concrete backup steps: copy `.env` (or at least `TOKEN_ENCRYPTION_KEY`) to separate secured storage with `umask 077`; stop the worker and archive the `staging` volume with the pinned `alpine` image used in the repo (`docker run --rm -v <project>_staging:/s -v "$PWD":/b alpine:3.21.10 tar czf /b/staging.tgz -C /s .`; find the real volume name with `docker volume ls`); say a restore without the original key makes TOTP seeds, the Google refresh token and LINE credentials unreadable; add TOTP seeds to `SECURITY.md`'s key consequences. Rewrite repair steps 2–4: `docker compose stop api worker` (plain `stop` would stop PostgreSQL), then `docker compose exec -T postgres psql -v cutover_bkk='<cutover>' -U gallery -d gallery -f - < scripts/repair/2026-11-21-utc-timestamps.sql`, adding `-v apply=1` for the real run; tell GHCR-only operators to download that single file from the repository at the release tag, and fix the "no repository files needed" sentence in the example README accordingly.
  - Verify: `rtk proxy grep -n "psql" docs/TROUBLESHOOTING.md`; read the command against `scripts/repair/2026-11-21-utc-timestamps.sql` (`\set`/`\if` work on stdin); relative-link check in the final task.

- [x] **Task 25: Deployment and security wording (AUD3-015, AUD3-016, AUD3-028)**
  - Files: `docs/DOCKER_DEPLOYMENT.md`, `docs/SECURITY.md`, `docs/example-deploy/README.md`, `.env.example`, `docs/example-deploy/env.example`, `docs/ARCHITECTURE.md`.
  - Change: document `WEB_PORT=127.0.0.1:8080` for a same-host proxy (works with the unchanged `"${WEB_PORT:-8080}:8080"` mapping; a proxy in another container cannot reach loopback) and warn that Docker-published ports bypass ufw/firewalld; add a one-line comment to both env files. Reword `SECURITY.md` line 5 to "per-client-address aggregate guess limit (8 per 5 minutes across restarted attempts)". State in `ARCHITECTURE.md` and `DOCKER_DEPLOYMENT.md` that both `api` and `worker` apply pending migrations at startup under advisory lock `72707369`.
  - Verify: `WEB_PORT=127.0.0.1:8080 rtk docker compose --profile edge config --quiet` exits 0 (output not shown).

- [x] **Task 26: Plan index and Status lines (AUD3-021)**
  - Files: `docs/plans/2026-11-22-mobile-bottom-navigation.md` (`git mv` to `completed/`, fix any relative links), `docs/plans/2026-11-23-owner-profile-mfa.md`, `docs/plans/README.md`.
  - Change per filing rules 4 and 5: the mobile-navigation plan shipped in PR #18 (`4697eec`), tags `v2026.11.6` and `v2026.11.7` contain it; strike the stale "Remaining: run e2e" sentence and record the correction, keeping the physical-device safe-area check as an unrecorded manual item. In `owner-profile-mfa`, strike "Implemented in the working tree" and record that it merged in PR #19 (`a9ac792`) and is unreleased. Reconcile WebKit without inventing results: the plan's own execution evidence records one Chromium and WebKit fixture run (449 passes, one intermittent WebKit failure reproduced on unchanged HEAD), while the review-fix plan records that WebKit could not run on its host; strike "WebKit e2e … have not run" and state both facts with their dates and that a WebKit rerun is still needed before release. Move the mobile entry to **Completed** in the index.
  - Verify: `rtk proxy git status --short docs/plans`; relative-link check in the final task.

- [x] **Task 27: Re-audit changed areas, update docs, record remediation status**
  - Re-audit: re-read each changed function against its finding; grep every caller of changed functions (`loadImage`, `ProcessEvent`, `ResolveAlbum`, `acquireHash`, `Inspect`, `InspectVideo`, `thumbnail`); search for any other `Backoff(` or `n < 8` caller; confirm no secret or expanded Compose config reached a commit or log.
  - Docs for changed behavior: `docs/ARCHITECTURE.md` (retry schedule: 15 retries over about 3–6 hours, reset by owner Retry; interrupted shutdown attempts are not counted; best-effort previews; inspection before thumbnails), `docs/SECURITY.md` (two concurrent password hashes, 503 when busy; no `ffmpeg` on uninspected bytes), `docs/OWNER_GUIDE.md` ("RETRY_PENDING" and "FAILED" text, Retry gives a fresh schedule), `docs/TROUBLESHOOTING.md` (sign-in busy 503; transient `MEDIA_TOOL` faults), `docs/DOCKER_DEPLOYMENT.md` (worker `stop_grace_period` of 110 s and why), `README.md` (test guard variable in the development section if the integration instructions are shown there).
  - `docs/plans/roadmap.md`: add benchmark and backlog lines for AUD3-023 (revisit with a restart policy), AUD3-025 and AUD3-026.
  - Audit report: add a "Remediation status" section at the top of `docs/audit/2026-11-23-audit-report.md` mapping each AUD3 ID to its task, outcome and verifying test, the three no-change decisions with their reasons, and the AUD3-010 docs-only outcome. The audit report is untracked; commit it with the plan changes.
  - Update this plan's Status line and tick the tasks; on shipment move it to `completed/` and update the index (filing rules 2 and 4).
  - Run the final gates below and the plan validator.

### Re-audit round 1 fixes (2026-11-23)

The read-only re-audit of the working tree (backend and frontend/docs) found five new LOW defects and six INFO notes. These tasks close them; run the same final gates afterwards.

- [x] **Task 28: R-1 — retry-row INSERT survives shutdown.** In `internal/worker/worker.go` (failure transaction, about lines 695-701), run the `INSERT INTO "OutboxEvent" … 'PROCESS'` on `pctx` like every other statement in that transaction. Test-first: with `tracedProcessor`, cancel ctx when the `UPDATE "UploadAttempt" SET "status" = $1` runs on a NETWORK fault; assert no open (ACTIVE, `completedAt` NULL) attempt remains and exactly one PROCESS row exists.
- [x] **Task 29: R-2 — `InspectVideo` stat errors (completes AUD3-007).** In `internal/media/media.go` `InspectVideo` (about lines 223-226), classify the `os.Stat` error like `Inspect`: `ErrNotExist` keeps a video-coded unsupported fault; any other error returns the raw error so the worker maps it to transient `PROCESSING`. Extend `TestInspectOpenErrorIsNotUnsupported` with an `InspectVideo` case on an unreadable path.
- [x] **Task 30: R-4 — ffprobe test stub.** In `internal/api/pipeline_test.go` (about lines 89-95), install the stub only when `exec.LookPath("ffprobe")` fails, not when `/usr/bin/ffprobe` is absent.
- [x] **Task 31: R-3 and R-5 — document and log.** In `docs/ARCHITECTURE.md`, add one sentence: an ffprobe killed by SIGKILL (for example the OOM killer) is treated as transient, so such a file can retry for up to the 3–6 hour budget before FAILED. In `dispatch`, log the claim error (no secrets) before returning nil on a cancelled ctx. No change for the hash-slot hold time or the hand-coded `wantPublic` list beyond adding one assertion that each public route does not answer 401.
- [x] **Task 32: F-1 — private staging backup.** In `docs/DOCKER_DEPLOYMENT.md` (about lines 71-76), run `umask 077` inside the container (`sh -c 'umask 077 && tar czf /b/staging.tgz -C /s .'`) and add `sudo chown "$(id -u):$(id -g)" staging.tgz`. Verify on a disposable volume that `stat -c %a staging.tgz` prints `600`, or record "not run".
- [x] **Task 33: F-2 and F-3 — gallery viewer and past-end copy.** In `apps/web/src/photos.tsx`: change the past-the-end message so it does not call the last page newest (say oldest, or drop the clause); clear `selectedId` once data has loaded and the id is absent, so a later refresh cannot reopen the viewer. Add e2e assertions: exact past-end text; refresh without the item then with it again leaves no dialog open.
- [x] **Task 34: F-4 and F-5 — make specs fail when fixes revert.** `routes.spec.ts`: give the albums and groups views distinguishable evidence (for example assert the `/albums` request count or the Groups-only subscription count after several round trips) so the spec fails on the old shared-instance code; `theme.spec.ts`: assert the dark class is present before first paint (for example via an init script that records `document.documentElement` class at `DOMContentLoaded`), so the old `useEffect` fails. Align mock messages and validation in `e2e/fixtures.ts` with the server: 410 "Verification expired. Start again.", 400 "Verification failed. Check your details and try again.", worker status "Heartbeat missing", and 400 on an invalid `page`. Confirm each strengthened spec fails with its fix reverted, then restore. **Outcome (2026-11-23):** theme spec now fails on the old `useEffect` (verified by revert); fixture messages, worker status and `page` validation aligned. `routes.spec.ts` stays a characterization test: with the Task 14 split reverted, measurement showed one `/api/v1/groups` poll per visit and no page errors either way, so the shared-instance issue is a latent rules-of-hooks violation with no observable symptom in this flow. The split stays for hooks correctness; AUD3-018's "leaked polling subscription" impact was not reproduced.
- [x] **Task 35: F-6 and closeout.** Update the owner-profile-mfa line in `docs/plans/README.md` to "merged in PR #19, unreleased". Add R-1..R-5 and F-1..F-6 outcomes to the audit report's Remediation status section and this plan's Status line. Re-run all final gates.

## Acceptance

- Every row of the traceability table has a merged task or the stated no-change reason, and each behavior fix has a test named in its task.
- A staged original with no recorded checksum, after FAILED or a quarantine, is re-inspected on Retry and uploaded; a garbage original stays on disk as UNRECOVERABLE.
- A transient outage of up to about 3 hours never parks an item as FAILED; a manual Retry always starts a new schedule at about 4 seconds.
- SIGTERM leaves no open `ACTIVE` attempt, never repeats `batchCreate`, and a created album is always recorded.
- With both hash slots held, login answers 503 with `Retry-After`; wrong passwords still answer 401.
- `/auth/status` failures do not unmount an open form, the viewer keeps its item across polls, and the other frontend findings pass their specs.
- CI builds both images and checks both Compose files on pull requests; the release workflow still passes and publishes as before.
- `agent-parity status` reports all artifacts tracked, with no secret staged.
- Docs state which release first contains MFA and that rollback is refused.

## Risks and rollback

- **Retry arithmetic (Task 3):** the cycle boundary relies on every FAILED transition writing a `FAILED` attempt row, which the failure transaction guarantees today. `TestBudgetExhaustionThenManualRetryResets` and `TestLegacyExhaustedItemResetsOnRetry` pin that contract; a future writer that sets FAILED without an attempt row would break the reset. No schema change, so rollback is a plain code revert.
- **Context handling (Task 4):** the most subtle change. The rule that keeps it safe: a cancelled run never changes image state, so uncertain writes stay uncertain and reconcile-only. Tests cover `CREATING_MEDIA` explicitly. A SIGTERM during album creation now waits up to about 100 s (`stop_grace_period: 110s`); that window is normally about a second.
- **Frontend polling tests (Tasks 12–14):** use Playwright's clock; if timers prove flaky, fall back to dispatching a `visibilitychange` event instead of advancing time.
- **`mockApi` activity default (Task 11)** may expose layout differences in `responsive.spec.ts`; treat as real findings, not noise.
- **Pinned actions (Task 20)** go stale; add Dependabot only if the owner asks.
- **Gate starvation (Task 8):** an attacker with many addresses can still make sign-in return 503 for a moment; the per-address rate limit and the 5 s wait bound the effect, and the API stays alive.
- **Rollback of any task:** each task is an independent commit; revert the commit. Task 21 is reversible by restoring the `.gitignore` line and `git rm --cached` of the 21 files.

## Verification evidence

Per-task evidence (2026-11-23). Red means the new test failed before the change, green means it passed after. Characterization means the test passed before the change and pins existing behavior.

- Task 1: guard table tests (new package). Red by compile failure before the guard existed. A db test run with a `postgres` host failed at the guard before connecting.
- Task 2: `TestRetryReinspectsStagedOriginalWithoutChecksum` red `FAILED/INTEGRITY`, green `STAGED` then `SYNCED` with one `batchCreate`. `TestRetryOfQuarantinedGarbageStaysUnrecoverableAndKept` and `TestRetryWithRecordedChecksumStillGoesToUpload` are characterization.
- Task 3: red `FAILED` at failure 8 (old `n < 8`) and a compile error for `MaxAutoAttempts`. Green for `TestRetryHorizon`, `TestEightNetworkFailuresThenSuccess`, `TestBudgetExhaustionThenManualRetryResets`, `TestLegacyExhaustedItemResetsOnRetry`, `TestAlbumRepairRequeueResetsBudget`.
- Task 4: red `context canceled` returned from `ProcessEvent` for the four shutdown tests; green for all four. `TestBudgetIgnoresInterruptedAttempts` and `TestRunLoopExitsCleanlyOnCancel` passed before the change (characterization; the Task 3 exclusion was already present, and the cancel race did not occur). The compose change is verified by `docker compose config --quiet` on both files.
- Task 5: red `UNSUPPORTED_VIDEO` for SIGKILL, absent binary and parent cancel, and `UNSUPPORTED_IMAGE` for a directory. Green for all seven fault classes and the open-error test.
- Task 6: red `RETRY_PENDING/PROCESSING`, green `SYNCED` with `thumbnailPath` NULL.
- Task 7: red 200 with an `ffmpeg` run on a quarantined row; green 404 with an empty log, and the validated row still renders. The thumbnail fixture gained `mimeType` (controls stay green).
- Task 8: red compile error before the gate; green gate unit tests, 503 with `Retry-After: 2` while saturated, the Owner row free for `FOR UPDATE NOWAIT`, and 401 for a wrong password after release.
- Task 9: red 200 with zeroed stats when the staging volume is missing; green 500, and 200 again after restore.
- Task 10: red missing top-level `security`; green route table matches the middleware for every operation.
- Task 11: typecheck and Chromium suite green with the new `mockApi` defaults (no layout regression surfaced).
- Task 12: red blocking screen replaced the open forms; green. First-load test is characterization.
- Task 13: red viewer showed the wrong photo after refresh; green.
- Task 14: characterization (passed before). The refactor removes a conditional return before hooks in `Groups`.
- Task 15: red focus stayed on `<body>`; green for both focus tests.
- Task 16: red missing `LINE credentials` label and the old onboarding copy for `collectVideos=true`; green for both specs.
- Task 17: red blank page with blocked storage; green. The stored-dark-theme test is characterization.
- Task 18: red no past-the-end message; green with the last-page link.
- Task 19: five auth-path specs pass against the unchanged frontend (characterization; specs only, as the plan requires).
- Task 20: `docker build` of `Dockerfile.api` and `Dockerfile.worker` succeeded; `/app/VERSION` matches `VERSION`; the worker image runs `ffmpeg` and `ffprobe` (6.1.2). Both workflow files parse with `yaml.safe_load`; `actionlint` is not installed, so the first PR run is the final proof. Action SHAs resolved with `gh api` (checkout `11d5960a…` v4, setup-go `40f1582b…` v5, setup-node `49933ea5…` v4).
- Task 21: 22 paths staged (`.gitignore` and the 21 managed files); `git ls-files .agent-parity` = 21; `git check-ignore` samples match the intended classes; the pre-staging secret search over `.agent-parity/` matched nothing; `agent-parity status` reports `git: all artifacts tracked` and the pre-push guard stays registered.
- Task 22: `.codex/hooks.json` parses; `agent-parity status` reports `codex: registered`.
- Task 23: `sync-version.mjs --check` still reports `Checked 2026.11.7`; the remaining `2026.11.7` mentions are the intended warnings.
- Task 24: the repair command was run through `docker compose exec -T postgres psql -f -` against a disposable Postgres container. `\set` and `\if :{?apply}` were evaluated on stdin, and the script's guard stopped the run on the empty schema, as designed.
- Task 25: `WEB_PORT=127.0.0.1:8080 docker compose --profile edge config --quiet` exits 0.
- Task 26: relative links in all changed Markdown resolve (52 checked). The mobile plan is in `completed/`; the rename is unstaged (plain `mv`, per the staging rule).
- Task 27: callers of `Backoff`, `ProcessEvent`, `ResolveAlbum`, `acquireHash`/`hashPassword`/`verifyPassword`, `Inspect`/`InspectVideo` and `thumbnail` were grepped; the old `n < 8` rule has no remaining caller. Graphify updated with `graphify update .`.

Final gate outputs are summarized in the Status line above; the exact commands are the Final gates list.

Record per task: the failing test output before the change and the passing output after, the exact command, and for Tasks 20 and 21 the workflow parse result and the `agent-parity status` line. No production services, volumes or secrets are touched; integration runs use only the disposable containers started by `scripts/test-integration.sh`.

## Lessons

- Check for existing state before adding schema: a plan review dropped a planned `retryBase` migration because the attempt ledger already marks the budget boundaries (Task 3).
- A fixed timeout on a detached context can be shorter than the normal request timeout. Use `context.WithoutCancel` plus a grace period so shutdown writes are not cut off (Task 4).
- Some audit-claimed impacts did not reproduce on measurement (AUD3-018). Measure the old behavior before writing a "must fail on old code" regression-test task; otherwise record a characterization test.
- Four parallel auditors with a consolidation step gave full file coverage for the audit.

## Final gates

Run after Task 27, all must pass:

- `rtk go test ./...`
- `rtk go vet ./...`
- `rtk proxy gofmt -l cmd internal packages` (empty output)
- `rtk proxy bash scripts/test-integration.sh` (disposable containers only)
- `rtk proxy corepack pnpm typecheck`
- `rtk proxy corepack pnpm build`
- `rtk proxy corepack pnpm e2e --project=chromium` (on this host Chromium needs the private library directory on `LD_LIBRARY_PATH`; see the Playwright row in [Troubleshooting](../TROUBLESHOOTING.md). WebKit is not part of this gate.)
- `rtk docker compose --profile edge config --quiet` (never print the expanded config)
- `rtk proxy node scripts/sync-version.mjs --check` (must still report `Checked 2026.11.7`; the version is not bumped)
- `rtk proxy node .claude/skills/vc-generate-plan/scripts/validate-plan-artifact.mjs docs/plans/2026-11-23-audit3-remediation.md` when that script exists
- `rtk proxy .agent-parity/bin/agent-parity status` reports all artifacts tracked

## Resume and execution handoff

Execute from this single file; there are no supporting phase files. Tasks 1–10 are backend and must go in the order given (Task 3 builds on Task 2, Task 4 on Task 3, Task 6 on Task 4). Tasks 11–19 are frontend and run after Task 11. Tasks 20–26 are independent of each other and of Tasks 2–19 and may be reordered or split into separate PRs. Task 27 goes last. Resume by finding the first unchecked task and re-running its verify step before continuing. Do not commit, tag, release or push without separate authorization; Task 21 stages files only.
