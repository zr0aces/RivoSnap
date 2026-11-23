# RivoSnap Full Codebase Engineering Audit Report

- **Audit date:** 2026-11-23 (Asia/Bangkok)
- **Revision audited:** `main` at `a9ac792` (clean worktree)
- **Scope:** Every tracked file (142 files from `git ls-files`). Four auditors split the work: A (`internal/api`), B (the rest of `internal/`, `cmd/`, `packages/`, `scripts/`, `infra/`, `.github/` and root operational files), C (`apps/web`, `e2e`) and D (documentation and agent/tool configuration). This report merges their results, re-verifies every MEDIUM finding against the source and renumbers everything as `AUD3-*`. Each finding keeps its working ID in brackets.
- **Previous reports:** `2026-11-20-audit-report.md` (`AUD-*`), `2026-11-21-audit-report.md` and `2026-11-21-audit-report-2.md` (`AUD2-*`). No finding below re-reports a fixed issue.
- **Coverage verdict:** Full file coverage, partial behavioral verification. Overall: **partial** (see §9).

## Remediation status

Status (2026-11-23): remediated in the working tree on top of `a9ac792`, uncommitted and about to be committed on a branch with a PR, following [the remediation plan](../plans/2026-11-23-audit3-remediation.md). Two fix rounds ran; the round-2 delta re-audit was clean. The plan's Status line records the gates run and not run (WebKit, first CI run, real SIGTERM check). Each row names the task that changed the code or docs and the test or check that verifies it.

| ID | Task | Outcome | Verifying test or check |
|---|---|---|---|
| AUD3-001 | 8 | Two process-wide hash slots; a saturated gate answers 503 with `Retry-After: 2` before any Owner row lock | `TestAcquireHashWaitsThenGivesUp`, `TestAcquireHashPeakConcurrencyIsTwo`, `TestHashGateBusyAnswers503` |
| AUD3-002 | 3, 4 | 15 automatic retries (about 3 to 6 h); owner Retry or album repair starts a new cycle; INTERRUPTED attempts do not count; no migration | `TestRetryHorizon`, `TestEightNetworkFailuresThenSuccess`, `TestBudgetExhaustionThenManualRetryResets`, `TestLegacyExhaustedItemResetsOnRetry`, `TestAlbumRepairRequeueResetsBudget`, `TestBudgetIgnoresInterruptedAttempts` |
| AUD3-003 | 2 | A retry re-inspects a staged original that has no recorded checksum instead of failing with INTEGRITY | `TestRetryReinspectsStagedOriginalWithoutChecksum`, `TestRetryOfQuarantinedGarbageStaysUnrecoverableAndKept`, `TestRetryWithRecordedChecksumStillGoesToUpload` |
| AUD3-004 | 12 | A failed `/auth/status` refetch keeps the cached workspace and the MFA step; the blocking screen appears only without cached data | `e2e/auth-resilience.spec.ts` (three tests) |
| AUD3-005 | 21 | The 21 agent-parity managed files are tracked (staged, not committed); runtime, cache, log and secret paths stay ignored | `agent-parity status` reports `git: all artifacts tracked`; `git check-ignore` samples per class |
| AUD3-006 | 4 | Shutdown records outcomes on detached contexts, never cancels album creation, marks the attempt INTERRUPTED and exits 0; worker `stop_grace_period: 110s` | `TestShutdownKeepsItemRecoverable`, `TestShutdownAfterCreateAlbumPersistsMapping`, `TestShutdownBeforeCreateAlbumReleasesClaim`, `TestShutdownInCreatingMediaStaysReconcileOnly`, `TestRunLoopExitsCleanlyOnCancel` |
| AUD3-007 | 5 | SIGKILL, a missing binary, a cancelled parent and filesystem errors are transient; bad exits, other signals and the inner timeout stay unsupported | `TestInspectVideoToolFaults`, `TestInspectOpenErrorIsNotUnsupported` |
| AUD3-008 | 6 | A failed preview leaves `thumbnailPath` NULL and the upload continues | `TestPreviewFailureDoesNotBlockUpload` |
| AUD3-009 | 7 | No on-demand `ffmpeg` for uninspected or UNRECOVERABLE rows | `TestQuarantinedRowGetsNoThumbnailRun` |
| AUD3-010 | 23 | Docs only (see below) | Text review; `sync-version.mjs --check` still reports `2026.11.7` |
| AUD3-011 | 24 | Backup runbook: key copy, staging archive with the pinned Alpine image, consequences of a lost key | Review against the compose volume names |
| AUD3-012 | 24 | The repair runs through the Compose `postgres` service on stdin | Command run against a disposable Postgres container: the script's guard stopped it on the empty schema, as designed |
| AUD3-013 | 9 | Row, scan and stats-volume errors return 5xx instead of zeros or 200 | `TestDashboardStatsStagingErrorIs500`, `TestGroupsLatestImage` |
| AUD3-014 | 20 | CI builds both images, checks `/app/VERSION` and the worker's `ffmpeg`/`ffprobe`, checks both Compose files; actions pinned to commit SHAs | Local image builds and `compose config` checks; the first PR run is the final proof |
| AUD3-015 | 25 | `WEB_PORT=127.0.0.1:8080` documented for same-host proxies, with the Docker firewall caveat | `WEB_PORT=127.0.0.1:8080 docker compose --profile edge config --quiet` |
| AUD3-016 | 25 | The guess limit is described as per client address | Review against the rate keying in `internal/api` |
| AUD3-017 | 13 | The viewer follows the photo id, not the list index | `viewer keeps the selected photo when the list refreshes` (`e2e/gallery.spec.ts`) |
| AUD3-018 | 14 | `AlbumsView` and `Groups` are separate components | `e2e/routes.spec.ts` (characterization: it also passed before the change) |
| AUD3-019 | 15 | Focus moves into change forms and back to the opener; Back from MFA focuses the email field | `change form moves focus in and returns it to the trigger`, `Back to sign-in choices focuses the email field` (`e2e/profile.spec.ts`) |
| AUD3-020 | 11, 19 | `mockApi` paginates, filters by Bangkok day and serves activity; auth-path specs added | `e2e/auth-login.spec.ts`; `change preview keeps its QR and codes through a 400 and returns to the start on 429` |
| AUD3-021 | 26 | Mobile plan moved to `completed/`; owner-profile Status corrected; index updated | Index and link review; `git status` |
| AUD3-022 | 22 | Portable graphify hook command | `.codex/hooks.json` parses; `agent-parity status` shows Codex registered |
| AUD3-023 | 27 | No change (reason below) | Roadmap backlog |
| AUD3-024 | 1 | Integration tests refuse non-loopback `DATABASE_URL` and `REDIS_URL` | `internal/testguard` table tests; db tests against the `postgres` host fail at the guard before connecting |
| AUD3-025 | 27 | No change (reason below) | Roadmap benchmark candidate |
| AUD3-026 | 27 | No change (reason below) | Roadmap benchmark candidate |
| AUD3-027 | 10 | OpenAPI security metadata and the `/api/openapi.json` and `/ready` paths | `TestOpenAPISecurityMatchesMiddleware` (route-table test) |
| AUD3-028 | 25 | Docs state that `api` and `worker` both migrate under advisory lock `72707369` | Review against `internal/db/migrate.go` and `cmd/rivosnap/main.go` |
| AUD3-029 | 16 | Shared health labels with "LINE credentials", Bangkok time for the worker, onboarding copy by `collectVideos` | `e2e/health.spec.ts`, `e2e/onboarding.spec.ts` |
| AUD3-030 | 17 | Storage access is guarded; the theme is applied in a layout effect before paint | `e2e/theme.spec.ts` |
| AUD3-031 | 18 | A page past the end shows a message and a link to the last page | `page past the end links to the last page` (`e2e/gallery.spec.ts`) |

**Re-audit round 1 (2026-11-23, Tasks 28–35):**

| ID | Task | Outcome | Verifying test or check |
|---|---|---|---|
| R-1 | 28 | Retry-row INSERT runs on the detached context, so shutdown cannot roll back the failure commit and leave an open attempt | `TestRetryOutboxRowSurvivesShutdown` |
| R-2 | 29 | `InspectVideo` stat errors other than not-exist return the raw error (transient `PROCESSING`); completes AUD3-007 | `TestInspectOpenErrorIsNotUnsupported` (InspectVideo cases) |
| R-3 | 31 | ARCHITECTURE states that an ffprobe SIGKILL is transient and can retry up to the budget; dispatch logs the claim error type on shutdown | Doc review; `dispatch` log line |
| R-4 | 30 | Pipeline test installs its ffprobe stub only when `exec.LookPath` fails | `TestFullPipelineIntegration` |
| R-5 | 31 | Shutdown claim error is logged by type; public routes asserted not to answer 401 | `TestOpenAPISecurityMatchesMiddleware` |
| F-1 | 32 | Staging archive written under `umask 077` inside the container, then `chown` to the operator | Disposable volume run: `stat -c %a staging.tgz` printed `600` |
| F-2 | 33 | Past-the-end copy says the last page holds the oldest matching items | `page past the end links to the last page` (exact text) |
| F-3 | 33 | The viewer forgets its photo once the list loads without it, so a later refresh cannot reopen it | `viewer stays closed when its photo returns after leaving the page` |
| F-4 | 34 | Theme spec records `data-theme` at the first rendered content; reverting to a passive effect fails it (`Received: "unset"`), restoring the layout effect passes | `saved dark theme is applied before the first rendered content` |
| F-5 | 34 | **Accepted as characterization.** With the split reverted, measurement showed one `/groups` poll per visit and no page errors, so the leaked-subscription impact of AUD3-018 was not reproduced. The split stays for rules-of-hooks correctness. | `e2e/routes.spec.ts` (characterization) |
| F-6 | 34 | Mock messages and validation aligned with the server: `Verification expired. Start again.`, `Verification failed. Check your details and try again.`, `Heartbeat missing`, 400 for an invalid `page` | `e2e/auth-login.spec.ts`, `e2e/profile.spec.ts`, `e2e/shell-responsive.spec.ts`, past-the-end spec |

**No-change decisions (INFO):**

- **AUD3-023 (worker healthcheck, no `statement_timeout`).** A global `statement_timeout` would also cut the migrations that `api` and `worker` run at startup, and it does not cure a hung socket. The API already shows a stalled worker through `worker:lastProgress` on `/health`, and Docker does not restart unhealthy containers here, so a health tweak buys no recovery. Revisit with a restart policy (roadmap backlog).
- **AUD3-025 (per-request revision lookup, `no-store` on thumbnails).** `no-store` is the safer default for a private library on shared devices: `private, max-age` would keep thumbnails in the browser cache after logout. The lookup is one Redis read and one primary-key read per request, which is negligible at single-owner scale. Benchmark candidate in the roadmap.
- **AUD3-026 (cleanup queries scale with history).** The audit rates it negligible at personal scale. Two extra indexes would add write cost to every `Image` and `WebhookEvent` insert for no measured benefit. Benchmark candidate in the roadmap, to promote near 100k images.

**AUD3-010 outcome (docs only):** owner MFA and passkeys are documented as first shipping in the release after `v2026.11.7`. `2026.11.7` and `latest` before that release stay password-only. No release commit, tag, `VERSION` change or code guard was made. The migration runner already refuses an older binary against an MFA database (`database has migrations unknown to this binary`), so a rollback fails closed and the documented answer is to fix forward.

---

## 1. Executive Summary

### Architecture understood

RivoSnap is a single-owner service. It copies new images, and videos when `collectVideos` is enabled, from enabled LINE groups into app-created Google Photos albums.

- **Edge:** An external HTTPS proxy forwards to one unprivileged Nginx container (`web`, published at `${WEB_PORT:-8080}:8080`). Nginx serves the React SPA and proxies `/api/` to the Go API. It trusts `X-Forwarded-For` only from private and loopback peers (`infra/docker/web.conf:11-15`).
- **API (`internal/api`):** A `net/http` mux behind middleware. The middleware applies security headers, an in-memory rate limiter keyed by client address, an exact-Origin plus `X-Gallery-Request` write guard, and a Redis session check bound to the owner's `securityRevision`. Since the last audit the API also has owner MFA (TOTP, recovery codes, discoverable passkeys), profile security changes, gallery day numbers and weekly activity. The LINE webhook is verified by HMAC over raw bytes and ingested in one transaction per event: `WebhookEvent` dedup, `LineGroup` lock, authorization window, `Image` unique on `(channelId, lineMessageId)`, disk-pressure check, then the `PROCESS` outbox row.
- **Worker (`internal/worker`):** A singleton under a session advisory lock. It claims outbox rows with `FOR UPDATE SKIP LOCKED` and a one-minute lease. Each item goes through download, bounded staging, inspection, previews, upload, `batchCreate`, and reconcile on uncertainty. Retention cleanup runs every minute.
- **State:** PostgreSQL 18 (`timestamptz` since `202611210001`), Redis 7, and the `staging` and `thumbnails` volumes. Both app containers share a 768 MiB `mem_limit` anchor (`docker-compose.yml:31`).
- **Frontend (`apps/web`):** React 19 with TanStack Query v5, polling every 15 s. No secrets are held on the client.

### Findings at a glance

| Severity | Count |
|---|---|
| CRITICAL | 0 |
| HIGH | 0 |
| MEDIUM | 5 |
| LOW | 17 |
| INFO | 9 |

### Most important risks

1. **Availability, unauthenticated (MEDIUM):** `AUD3-001`. Concurrent Argon2id verifications on `POST /api/v1/auth/login` (64 MiB each) are not bounded. Two client addresses can push the API past its 768 MiB limit.
2. **Practical image loss after outages (MEDIUM):** `AUD3-002`. The automatic retry budget lasts about 4–8.5 minutes. A longer outage parks every in-flight item as FAILED, and a manual retry then gets one attempt.
3. **Broken recovery path (MEDIUM):** `AUD3-003`. A FAILED item whose original was staged but never inspected always fails `INTEGRITY` on retry. Discard is the only action left.
4. **UI state loss on every backend blip (MEDIUM):** `AUD3-004`. A failed `/auth/status` refetch replaces the signed-in workspace, including an in-progress MFA enrollment, with a full-page error.
5. **Developer tooling (MEDIUM):** `AUD3-005`. Every tracked agent MCP and hook config points into the git-ignored `.agent-parity/`, so a fresh clone cannot start them.

### Previous findings confirmed fixed

`AUD-002`, `AUD-007`, `AUD-009`, `AUD-010`, `AUD-013`, `AUD2-003`, `AUD2-005`, `AUD2-006`, `AUD2-007`, `AUD2-009` (web part and `handleListImages`), `AUD2-011` and `AUD2-012` (stale queue badge). `AUD3-013` lists the `AUD2-009` residuals in sites that report did not name.

### Areas needing deeper investigation

- Argon2id and `ffmpeg` memory inside the real API cgroup. `AUD3-001` uses a scratch measurement outside the container, and `AUD3-009` could not be measured because `ffmpeg` is not installed on the audit host.
- Alpine 3.21 `ffmpeg`/`ffprobe` behavior in the shipped worker image (`AUD3-007`, `AUD3-008`). No image was built.
- LINE content retention, which sets the real loss window for `AUD3-002`.
- Browser reproduction of `AUD3-004`, `AUD3-017` and `AUD3-018`. No Playwright browser could launch on the audit host.

## 2. Findings

### [AUD3-001] [MEDIUM] — Unbounded concurrent Argon2id verification on the unauthenticated login route can OOM-kill the API container (was A-1)

- **Category:** Security / availability (resource exhaustion)
- **Location:** `internal/api/server.go:474-479` and `:528-538` (`handleAuth`, login branch); `internal/crypto/crypto.go:104-110`, `:145` (`HashPassword`/`VerifyPassword`, `m=65536` KiB); `docker-compose.yml:31` (`mem_limit: 768m` in the `x-service` anchor inherited by `api`)
- **Confidence:** High for the mechanism (measured in a scratch test); medium for the exact request count needed in production.
- **Problem:** Each password verification allocates a 64 MiB Argon2id work area. Nothing limits how many verifications run at the same time. The only guard is the per-client-address bucket `login:<ip>`, 8 per 5 minutes (`server.go:476`). That bucket counts requests but does not serialize them, so one address can start 8 verifications at once, and each further address can start 8 more. Once an owner exists, the handler runs `VerifyPassword` before it compares the email (`server.go:535`), so the work happens for any email.
- **Evidence:**
  - Re-verified in source: `server.go:535` evaluates `crypto.VerifyPassword(passwordHash, b.Password)` before `email != b.Email`. `crypto.go:104-110` fixes `memory = 65536`, `timeCost = 3`, `threads = 4`.
  - The auditor's scratch program (outside the repository) ran `argon2.IDKey(pw, salt, 3, 65536, 4, 32)` concurrently on a 6-CPU host. It measured max RSS 66 MiB for 1 concurrent run, 516 MiB and 432 ms for 8, and 1,028 MiB and 815 ms for 16. These are scratch-test numbers, not measurements inside the API container.
  - `GOMEMLIMIT` is not set in any Dockerfile, Compose file or `cmd/`. `infra/docker/web.conf` has no `limit_conn` or `limit_req`.
- **Impact:** An unauthenticated internet client with two addresses can OOM-kill `rivosnap-api`. One IPv6 /64 provides many addresses. A restart clears the in-memory rate limits (`docs/SECURITY.md:5`), so the burst can be repeated at once. While the API restarts, the owner UI is down and LINE webhooks fail. Webhooks are lost unless LINE redelivery is enabled. CPU saturation of about 0.5–1 s per burst also delays other requests.
- **How it can occur:** After setup, send `POST /api/v1/auth/login` with the correct `Origin`, `X-Gallery-Request: 1` and any email, 8 requests concurrently from each of 2 or more public addresses. Nginx forwards the real client address, so each address gets its own bucket.
- **Recommended fix:** Put a small process-wide semaphore (for example 2 slots, like `thumbSem`) around `crypto.VerifyPassword` and `crypto.HashPassword` in the API layer. Wait a bounded time (for example 5 s), then answer 429 or 503 with `Retry-After`. Share the semaphore with `handleChangeStart` (`security.go:472`) and setup. Optionally set `GOMEMLIMIT` for the API service as defense in depth.
- **Expected impact:** Peak Argon2 memory is capped at slots × 64 MiB, whatever the attacker's address count. Sign-in latency is unchanged at normal load.

### [AUD3-002] [MEDIUM] — The automatic retry budget is about 4–8.5 minutes; a longer outage makes every in-flight item FAILED, and a manual retry then gets one attempt (was B-2)

- **Category:** Reliability / data-loss risk
- **Location:** `internal/worker/worker.go:219-241` (`n := count + 1` over all `UploadAttempt` rows), `worker.go:588` (`f.Transient && n < 8`), `internal/model/model.go:105-114` (`Backoff`)
- **Confidence:** High (arithmetic from code; re-verified)
- **Problem:** Transient faults (NETWORK, LINE or Google 5xx/408/429, LINE_PREPARING, DISK_PRESSURE, PROCESSING) retry only while `n < 8`, where `n` counts every attempt ever recorded for the image. `Backoff(n)` is `2000·2^n · U(0.5,1)` ms, so seven retries sum to about 254–508 s. The 1-hour cap and `min(n,12)` in `Backoff` are never reached. After that the image is FAILED. Because `n` never resets, a manual retry starts at `n ≥ 9`, and one more transient error sends it straight back to FAILED with no backoff.
- **Evidence:** Re-verified: `worker.go:241` `n := count + 1`; `worker.go:588` `else if f.Transient && n < 8 { next = model.StateRetryPending }`; `model.go:107` `base := min(3600000.0, 2000.0*math.Pow(2, float64(exp)))`. `handleRetryJob` (`server.go:1934-1972`) moves FAILED to RETRY_PENDING and does not touch the attempt history. `DISK_PRESSURE` uses a 60 s floor (`worker.go:403`), so its seven retries take about 7–9 minutes. The only retry route is per item (`POST /api/v1/jobs/{id}/retry`).
- **Impact:** For a home-hosted service, a 10-minute ISP drop, a Google or LINE incident, or a disk-pressure episode longer than about 8 minutes leaves every image received in that window FAILED. Each needs a separate manual retry with a single attempt. If the owner does not act before LINE stops serving the content, the retry ends `LINE_404/410` → UNRECOVERABLE and the image is lost. This is not a silent deletion, but it is a practical loss path against the goal in `docs/ARCHITECTURE.md`.
- **How it can occur:** A network outage makes `httpclient.Request` return `NETWORK` (transient) for every download. After 7 retries the item is FAILED.
- **Recommended fix:** Reset the budget on manual retry, or count only automatic transient attempts since the last owner action (for example a `retryBase` column, or attempts after the last `Retry requested` audit row). Use a schedule that spans hours (allow `n` up to about 12–15 so the existing 1-hour cap applies). Optionally add a bulk "retry FAILED with transient codes" action.
- **Expected impact:** Short and medium outages recover automatically, and a manual retry gets the same backoff as a fresh item.

### [AUD3-003] [MEDIUM] — A retried FAILED job with a staged but uninspected original always fails INTEGRITY and can never upload (was B-1)

- **Category:** Correctness / data recovery
- **Location:** `internal/worker/worker.go:362-367` (RETRY_PENDING routing), `worker.go:486-488` (integrity check); quarantine writers at `worker.go:393`, `:417`, `:560-566` and the failure update at `:614` (`COALESCE($6, "stagingPath")`)
- **Confidence:** High (traced through code; matches the existing integration fixture state; re-verified)
- **Problem:** The worker routes a RETRY_PENDING image to `UPLOAD_PENDING` whenever `stagingPath` is set. In several paths the failure transaction sets `stagingPath` from `quarantinePath`, but `sha256` and `mimeType` stay NULL because `moveStaged` never ran. The upload branch re-inspects the file and requires `i.Sha256 != nil && i.MimeType != nil`. That check always fails, so the image goes to `FAILED/INTEGRITY` ("Staged bytes changed; inspect storage.") on every retry.
- **Evidence:**
  - `worker.go:362-366`: `if i.StagingPath != nil && *i.StagingPath != "" { next = model.StateUploadPending }`.
  - `worker.go:486`: `if i.Sha256 == nil || i.MimeType == nil || info.Sha256 != *i.Sha256 ...` returns non-transient `INTEGRITY`.
  - The crash-loop path (`worker.go:560-567`) sets `quarantinePath` for any file at the staging path and fails with `WORKER_INTERRUPTED`. `TestCrashLoopKeepsStagedFileDiscardable` (`internal/api/pipeline_test.go`) asserts exactly this state: FAILED, `stagingPath` set, no checksum.
  - `handleRetryJob` (`server.go:1947-1972`) accepts FAILED and does not touch `stagingPath` or `sha256`.
- **Impact:** A fully downloaded, valid original cannot reach Google through the supported retry. The only in-product action left is "Discard expired original" after the 7-day deadline, which destroys the only local copy. LINE content may have expired by then.
- **How it can occur:** (1) The worker is interrupted three times while an item is in DOWNLOADING after `Stage` renamed the file (OOM kill, restarts, or the shutdown case in `AUD3-006`), which gives FAILED/WORKER_INTERRUPTED with the file kept. (2) `media.Digest` hits an I/O error inside `Stage`. That error is not a `Fault`, so it maps to PROCESSING (transient) and RETRY_PENDING with `quarantinePath` set, and the next automatic retry hits INTEGRITY.
- **Recommended fix:** Route RETRY_PENDING to `DOWNLOAD_PENDING` unless `sha256` and `mimeType` are recorded: `if i.StagingPath != nil && *i.StagingPath != "" && i.Sha256 != nil`. The DOWNLOADING branch (`worker.go:386-396`) already re-inspects a file present at the staging path and persists STAGED metadata via `moveStaged`, without re-downloading.
- **Expected impact:** Interrupted or quarantined but valid originals become uploadable through the normal Retry. Invalid ones still end UNRECOVERABLE through inspection.

### [AUD3-004] [MEDIUM] — A failed `/auth/status` refetch replaces the signed-in workspace and the login MFA step with the "Cannot reach" screen (was C-1)

- **Category:** Reliability / frontend state handling
- **Location:** `apps/web/src/main.tsx:667-692` (`App`); `apps/web/src/data.ts:51-57` (`query`, `refetchInterval: 15000`); `apps/web/src/main.tsx:202` (`retry: 1`)
- **Confidence:** High for the control flow (TanStack Query v5 semantics and source trace; re-verified). Not reproduced in a browser, because no Playwright browser could launch on the audit host.
- **Problem:** `App` checks `if (auth.error)` before it checks `auth.data`. `/auth/status` is polled every 15 s and refetched on window focus. In TanStack Query v5 a failed refetch keeps the old `data` but sets `error`. When two attempts about 1 s apart fail, `App` returns the error `<main>` even though the owner is still signed in. React unmounts the whole tree: shell, routes, `Profile`/`ChangeForm`, an open `Lightbox`, and `Login` during the MFA step.
- **Evidence:** Re-verified: `main.tsx:679` `if (auth.error) return (<main className="authState"><p role="alert">Cannot reach the gallery…` runs before `if (!auth.data)` and before the `authenticated` check. `/auth/status` (`internal/api/server.go:333-349`) queries PostgreSQL and Redis and returns 5xx on either failure. An Nginx 502 during an API restart has the same effect.
- **Impact:** Every open tab shows a full-page error during each API restart or deploy, or a brief DB or Redis hiccup. Unsaved state is lost: the security-change preview in `ChangeForm` (QR, manual key and one-time recovery codes), typed settings and filters, the open media viewer, and the pending MFA step on the login screen. The page recovers by itself on the next successful poll, but the lost state does not come back. No lockout and no data loss occur, because an unfinished change is not committed.
- **How it can occur:** Sign in, open Profile, start "Replace recovery codes" and verify. While the codes are on screen, restart `api` (or make Redis or PostgreSQL unavailable) for more than about 1–2 s across a poll.
- **Recommended fix:** Show the blocking error only when there is no data: `if (auth.error && !auth.data)`. When data exists and the refetch failed, keep rendering and show a non-blocking banner, or reuse the sidebar "API unreachable" health state. Session expiry is already handled: `api()` reloads on 401, and a successful poll that returns `authenticated:false` shows the login form.
- **Expected impact:** Short backend outages no longer destroy in-progress UI state.

### [AUD3-005] [MEDIUM] — `.agent-parity/` is git-ignored, so every tracked MCP and hook config points to files a fresh checkout does not have (was D-4)

- **Category:** Agent/tool configuration
- **Location:** `.gitignore:42`; `.mcp.json:4`; `.cursor/mcp.json:4`; `.agents/mcp_config.json:4`; `.codex/config.toml:2`; `.claude/settings.json:11`, `:19`; `.cursor/hooks.json:5`; `.agents/hooks.json:5`; `.codex/hooks.json:18`; `.gitattributes:2-4`; `AGENTS.md` ("Shared memory and agent state")
- **Confidence:** High (re-verified)
- **Problem:** `.gitignore:42` ignores `.agent-parity/` completely. The line sits outside the `agent-parity:begin/end` block. `git ls-files .agent-parity` returns 0 files. Yet all four MCP registrations launch `.agent-parity/mcp/memory/run.sh`, and the SessionStart and PreInvocation hooks run `.agent-parity/bin/agent-parity`. `AGENTS.md` says to treat the managed files as repository state and to commit every changed managed file. The memory tool says memories are committed to the repository. `.gitattributes` adds merge rules for `.agent-parity/memory/*.md`, which can never be tracked.
- **Evidence:** `git check-ignore -v .agent-parity/mcp/memory/run.sh` reports `.gitignore:42:.agent-parity/`. `.mcp.json` registers `"command": ".agent-parity/mcp/memory/run.sh"`. The files exist only in the local working tree.
- **Impact:** On any other clone or machine, the MCP memory server fails to start and each agent's session-start hook errors. The cross-agent bootstrap rule in `AGENTS.md` cannot repair this, because it only swaps `run.sh` and `run.cmd`. Shared memory is never shared through Git, contrary to the documented design. Production is not affected; this is the reason it ranks last among the MEDIUM findings.
- **How it can occur:** A fresh `git clone` used by Claude Code, Codex, Cursor or Antigravity.
- **Recommended fix:** Decide the policy. Either remove `.gitignore:42` and commit the managed files, or keep them local and state in `AGENTS.md` and the configs that an `agent-parity` install step is required after cloning.
- **Expected impact:** Agent tooling works on a fresh checkout, and the docs match the actual sharing model.

### [AUD3-006] [LOW] — Graceful shutdown leaves ACTIVE attempts and unpersisted results, which count toward the crash-loop cap and can strand a created album (was B-3)

- **Category:** Reliability / context cancellation
- **Location:** `internal/worker/worker.go:605` (`p.DB.Pool.Begin(ctx)` in the failure path), `worker.go:223-231` (crash-loop count), `worker.go:125-131` (`ResolveAlbum` persists the album ID with `ctx`), `cmd/rivosnap/main.go:87` (signal context), `docker-compose.yml` (no `stop_grace_period`, so 10 s)
- **Confidence:** Medium-high
- **Problem:** On SIGTERM the worker's root context is cancelled. The in-flight HTTP call fails (mapped to `NETWORK`), and the failure bookkeeping runs on the same cancelled `ctx`, so `Begin` fails. The UploadAttempt stays `ACTIVE` with `completedAt NULL`. Three such interruptions on one item trigger `WORKER_INTERRUPTED` → FAILED, which then hits `AUD3-003` if the file was staged. If SIGTERM lands between a successful `CreateAlbum` and the `UPDATE "LineGroup" SET "googleAlbumId"` (`worker.go:125`), the album exists in Google, `albumCreating` stays true, and later images in that group fail `ALBUM_REVIEW` until the owner repairs the mapping. A cancelled in-flight `CreateAlbum` also maps to `NETWORK` and so to `ALBUM_REVIEW` (`worker.go:118`).
- **Evidence:** The cited lines. Any non-nil `ProcessEvent` error also exits the process (`dispatch` returns it at `worker.go:750-751`, then `main.go:125-127` calls `os.Exit(1)`), so a transient DB error mid-item leaves an open attempt the same way.
- **Impact:** Routine deploys and restarts count as crashes. Items can be parked FAILED or NEEDS_REVIEW, or a whole group blocked on album review, with no real fault.
- **How it can occur:** `docker compose up -d` (image update) while a video upload (600 s timeout) or album creation is in flight: three times for one item, or once at the wrong moment for an album.
- **Recommended fix:** Run post-I/O persistence and failure bookkeeping on `context.WithoutCancel(ctx)` with a short timeout (for example 10 s). Stop claiming new work on cancel, but let the current item commit its state. Set `stop_grace_period` on `worker`. Optionally record attempts interrupted by our own shutdown as `INTERRUPTED` and do not count them.
- **Expected impact:** Restarts no longer consume the crash-loop budget or strand album mappings.

### [AUD3-007] [LOW] — Local tool and filesystem failures are classified as invalid media (UNRECOVERABLE, not retryable) (was B-4)

- **Category:** Error classification
- **Location:** `internal/media/media.go:216-233` (`InspectVideo`: any `ffprobe` exec error → `UNSUPPORTED_VIDEO`), `media.go:170-177` (`Inspect`: `os.Stat`/`os.Open` errors → `UNSUPPORTED_IMAGE`), `internal/worker/worker.go:584` (`unrecCodes`)
- **Confidence:** Medium
- **Problem:** `exec.CommandContext(...).Output()` fails with the same non-transient `UNSUPPORTED_VIDEO` whether the file is invalid, `ffprobe` hit its 30 s deadline, `ffprobe` was OOM-killed under the 768 MiB limit, or the binary is missing. Image open and stat errors (EIO, EMFILE) also become `UNSUPPORTED_IMAGE`. The worker maps both codes to UNRECOVERABLE, and the API retries only FAILED and NEEDS_REVIEW, so the only remaining action is discard. The file is kept, so this is not a silent deletion.
- **Evidence:** `media.go:231-233` `if err != nil { return ... NewFault("UNSUPPORTED_VIDEO", ..., false, 0) }`. The same applies at upload-time re-inspection (`worker.go:479`).
- **Impact:** A valid video or image can be permanently parked as "invalid" because of a transient host condition. This is the opposite direction from `AUD-002`.
- **How it can occur:** Memory pressure in the shared container, a slow volume, or an image build that drops `ffprobe`.
- **Recommended fix:** Detect a deadline or signal (`cctx.Err() != nil`, `*exec.ExitError` with a signal, `exec.ErrNotFound`) and filesystem errors, and return a transient `PROCESSING`/`MEDIA_TOOL` fault for them. Keep `UNSUPPORTED_*` for an `ffprobe` that completes and rejects the format.
- **Expected impact:** Only genuinely invalid media becomes UNRECOVERABLE.

### [AUD3-008] [LOW] — A failed preview thumbnail blocks the Google upload of a verified original (was B-5)

- **Category:** Reliability / coupling
- **Location:** `internal/worker/worker.go:451-456`; `internal/media/media.go:277-333`; `Dockerfile.worker:12` (Alpine 3.21 `ffmpeg`)
- **Confidence:** Medium (the mechanism is certain; real-world frequency is unverified)
- **Problem:** Before an item can move to UPLOAD_PENDING, the worker requires both 320 and 640 WebP previews from `ffmpeg`. Any `ffmpeg` failure returns a raw exec error, which maps to PROCESSING (transient). After the `AUD3-002` budget the item is FAILED, and a retry repeats the same failure. The upload does not depend on the preview: the original already passed `Inspect`/`InspectVideo`.
- **Evidence:** `worker.go:452-455` `if _, err := p.Store.Thumbnail(...); err != nil { return err }` runs before `moveState(UploadPending)`. Inputs the Alpine 3.21 FFmpeg build may fail to decode (for example animated WebP) would block sync. Not reproduced.
- **Impact:** Valid media that Google would accept is never uploaded.
- **How it can occur:** A group member sends media that passes the magic-byte and `ffprobe` checks but that this `ffmpeg` build cannot render.
- **Recommended fix:** Treat preview generation as best effort. Record the preview error, leave `thumbnailPath` NULL (the API already regenerates or falls back), and continue the upload.
- **Expected impact:** Preview problems degrade only the gallery view, never the sync.

### [AUD3-009] [LOW] — On-demand thumbnail generation runs `ffmpeg` in the API on quarantined bytes that failed validation, bypassing the 100 MP decode limit (was A-2)

- **Category:** Security / resource safety
- **Location:** `internal/api/server.go:1683-1692` (`handleMediaStream`, `!hasRemote` branch to `s.Store.Thumbnail`); `internal/media/media.go:306-328` (`Thumbnail`), `:277-293` (`runFFmpegThumbnail`: `ffmpeg -i <file>`, format auto-probe, no size guard); bypassed check at `media.go:169-199` (`Inspect`, `> 100_000_000` pixels → `UNSUPPORTED_IMAGE`)
- **Confidence:** High that the path runs; medium on real-world exploitability. Whether LINE delivers images above 100 MP or arbitrary bytes was not verified, and `ffmpeg` is not installed on the audit host, so its memory could not be measured.
- **Problem:** Since `AUD2-003`, a file that fails inspection is kept as `UNRECOVERABLE` or `FAILED` with `stagingPath` set and `mimeType` NULL. The worker never makes previews for such rows. When the owner views Jobs or Gallery, the thumbnail handler finds no cached thumbnail and no Google ID, sees `stagingPath`, and calls `Store.Thumbnail`. That runs `ffmpeg` with auto-probing on the unvalidated file inside the API container. Nothing on this path checks `uploadStatus`, `mimeType` or the pixel limit.
- **Evidence:** `server.go:1684` gates only on `hasRemote` and `stagingPath`. The UI requests `/thumbnail?size=640` for every queue row (`apps/web/src/photos.tsx:288-292`) and every gallery card (`:386-390`). The quarantine state is confirmed by `TestRestageInspectFailureKeepsStagingPath` and the `bad-image`/`bad-video` assertions in `TestFullPipelineIntegration` (`pipeline_test.go:646-661`).
- **Impact:** `docs/SECURITY.md:11` states that decoders restrict decoded size to 100 MP. On this path an image rejected for exceeding 100 MP is fully decoded by `ffmpeg`. Two runs may be concurrent (`maxThumbJobs`) inside the 768 MiB API cgroup. Failures are not cached, so each page view re-triggers up to 30 s of decode. Bytes that matched no allowed format also reach `ffmpeg`'s full demuxer set in the request path.
- **How it can occur:** A member of an enabled group sends an image that LINE delivers above 100 MP or in an unsupported format. The worker quarantines it, and the owner opens Jobs or Gallery.
- **Recommended fix:** In `handleMediaStream`, generate an on-demand thumbnail only for rows that passed inspection (`mimeType` non-NULL and status not `UNRECOVERABLE`). Otherwise return 404 "Preview unavailable" and show a placeholder. Optionally pass an explicit demuxer and keep a pixel check in `runFFmpegThumbnail`.
- **Expected impact:** No unvalidated bytes reach `ffmpeg` from the API, and the 100 MP control holds on every path.

### [AUD3-010] [LOW] — MFA docs describe a feature that no published image has, and the example pins a pre-MFA release (was D-3; lowered from MEDIUM)

- **Category:** Documentation / upgrade and rollback
- **Location:** `docs/example-deploy/README.md:10`, `:33`, `:35`; `docs/example-deploy/env.example:2`, `:7`; `docs/DOCKER_DEPLOYMENT.md:27`; `README.md:38`
- **Confidence:** High
- **Severity change:** Lowered from MEDIUM. The input claimed that the `v2026.11.7` migration runner ignores unknown ledger rows, so a pre-MFA binary would start against an MFA-enrolled database and accept password-only login. Re-verification shows the opposite: `git show v2026.11.7:internal/db/migrate.go` refuses with `database has migrations unknown to this binary` (line 145), and `cmd/rivosnap/main.go:97-99` at that tag exits 1. A rollback therefore fails closed (the API does not start) rather than bypassing MFA. What remains is inaccurate documentation.
- **Problem:** The docs say passkeys and authenticator MFA are enrolled in Profile. The example deployment uses `IMAGE_TAG=latest` and suggests pinning "a CalVer release like `2026.11.7`". Root `VERSION` is still `2026.11.7`, and tag `v2026.11.7` does not contain `internal/api/security.go`: MFA was merged after that release (PR #19, `a9ac792`). Both `:latest` and the suggested pin are password-only images. The docs never say which release first includes MFA.
- **Evidence:** `git cat-file -e v2026.11.7:internal/api/security.go` → `fatal: path 'internal/api/security.go' exists on disk, but not in 'v2026.11.7'`. `example-deploy/README.md:10` and `:33` as quoted above.
- **Impact:** GHCR-only operators are told to look for a Profile MFA page that their image does not have. An operator who enrolls MFA on a source build and later switches to the GHCR example with the suggested tag gets an API that refuses to start, with no doc explaining why.
- **How it can occur:** Moving from a source build to GHCR, or following the example's pinning advice for a rollback.
- **Recommended fix:** State the minimum release that contains owner MFA (the next CalVer). Change the pin example to that version or later, and mark the MFA docs "from release X". Until it ships, note that `latest` does not include MFA. Optionally document the `unknown to this binary` refusal as the expected rollback symptom.
- **Expected impact:** Operators can follow the "never roll back to password-only" rule because they know which tags qualify.

### [AUD3-011] [LOW] — Backup runbook does not include a step to copy the encryption key or back up pending originals (was D-1; lowered from MEDIUM)

- **Category:** Operations / data recovery
- **Location:** `docs/DOCKER_DEPLOYMENT.md:52-65`; `docs/SECURITY.md:10`, `:19`; `docs/example-deploy/README.md:35`
- **Confidence:** High
- **Severity change:** Lowered from MEDIUM. The input said the docs do not tell the operator to back up `TOKEN_ENCRYPTION_KEY` and that `SECURITY.md` does not mention TOTP seeds. Re-verification found that `SECURITY.md:10` says "Preserve the key separately when backing up", `SECURITY.md:19` says "The TOTP seed (including pending enrollment) is encrypted with TOKEN_ENCRYPTION_KEY. Back up that key separately", and `example-deploy/README.md:14` says "keep backed up separately". The requirement is documented. The gap is that the concrete backup procedure does not carry it out, and the staging volume has no procedure.
- **Problem:** The only concrete backup procedure (`DOCKER_DEPLOYMENT.md:52-61`) is a `pg_dump`. It has no step that copies `.env` or `TOKEN_ENCRYPTION_KEY`, and no procedure for the `staging` volume, which holds originals not yet uploaded. Line 63 says only "Keep its separate application encryption key secure". `example-deploy/README.md:35` says "back up the database, encryption key, and pending originals" with no commands. `SECURITY.md:10` lists the consequences of changing the key for Google and LINE only; the TOTP consequence is on line 19.
- **Evidence:** The cited lines. `internal/config/config.go:101-104` requires the key to boot; `internal/db/db.go:168-181` decrypts saved LINE credentials with it; `internal/api/totp.go` rejects a code when the seed cannot be decrypted. Recovery codes are hashes and still work. The staging volume is `staging:/data/staging` (`docker-compose.yml:29`).
- **Impact:** An operator who follows only the runbook after a host failure may restore the database without the key. The Google refresh token, LINE credentials and TOTP seeds are then unreadable. An owner with only an authenticator and no recovery codes has no documented way back in, because there is no bootstrap bypass. Losing the staging volume loses originals Google never received.
- **How it can occur:** An operator follows the "Metadata backup and restore" section literally.
- **Recommended fix:** Add explicit steps to the backup section: copy `.env` (or at least `TOKEN_ENCRYPTION_KEY`) to separate secured storage; back up the `staging` volume with the worker stopped, for example `docker run --rm -v <project>_staging:/s -v "$PWD":/b alpine tar czf /b/staging.tgz -C /s .`; and state that a restore without the original key makes TOTP seeds unreadable. Add TOTP seeds to the consequence list in `SECURITY.md:10`.
- **Expected impact:** A restore brings back working Google, LINE and MFA state, plus the originals not yet uploaded.

### [AUD3-012] [LOW] — The one-off UTC repair command cannot run as written in either documented deployment (was D-2; lowered from MEDIUM)

- **Category:** Operations / migration runbook
- **Location:** `docs/TROUBLESHOOTING.md:44-50`; `docs/example-deploy/README.md:5`, `:7`; `docs/DOCKER_DEPLOYMENT.md:19`, `:29`
- **Confidence:** High
- **Severity change:** Lowered from MEDIUM. The command fails visibly at connect time, before any change, and the runbook orders the dry run and apply (steps 3 and 4) before deploying the fixed build (step 5). The irreversible outcome requires the operator to continue after a failed step, which the input assumed but did not show. The repair also applies only to installations upgrading from a build older than the UTC fix.
- **Problem:** Step 3 says to run `psql -v cutover_bkk=... -U <user> -d <database> -f scripts/repair/2026-11-21-utc-timestamps.sql`. PostgreSQL has no published port and sits only on an `internal: true` network, so a host `psql` cannot connect. The script is not inside the postgres container either. The GHCR example says no repository files are needed (line 7), yet line 5 sends the operator to this repair.
- **Evidence:** `TROUBLESHOOTING.md:48`. `docker-compose.yml:34-45` and `docs/example-deploy/compose.yml:34-45` give `postgres` no `ports:`; `private: {internal: true}` at `docker-compose.yml:125` and `docs/example-deploy/compose.yml:109`. `DOCKER_DEPLOYMENT.md:19` says "PostgreSQL and Redis have no published ports."
- **Impact:** The repair must run after the old build stops and before the new build starts. If the operator gives up on the failing command and deploys anyway, migration `202611210001_timestamptz` closes the window for good and Bangkok-shifted timestamps, including `enabledSince`, stay wrong.
- **How it can occur:** An operator upgrading from a pre-UTC-fix build follows the runbook as written.
- **Recommended fix:** Give a command that works in Compose: `docker compose exec -T postgres psql -v cutover_bkk='...' -U gallery -d gallery -f - < scripts/repair/2026-11-21-utc-timestamps.sql` (add `-v apply=1` for the real run). Tell GHCR-only operators to download that one file from the release tag first. Say `docker compose stop api worker`, because a plain `stop` also stops postgres.
- **Expected impact:** The one-shot repair can run inside its window.

### [AUD3-013] [LOW] — Row-iteration and secondary-query errors are swallowed into 200 responses with truncated or zeroed data (was A-3)

- **Category:** Correctness / observability (residual of `AUD2-009` in sites that report did not list)
- **Location:** `internal/api/server.go:1025-1033` (`handleGetGroups`: no `rows.Err()`); `:1570-1588` (`handleGetImage`: attempt `Scan` errors skipped, no `rows.Err()`); `:2029-2039` (`handleDashboardStats`: `oldest` query error discarded; `Usage`, `Free`, `Pressured` errors become `0`/`false`); `:2041-2065` (`recent` query and scan errors discarded)
- **Confidence:** High (pgx `rows.Next()` returns false on a mid-stream error, and only `rows.Err()` reports it)
- **Problem:** If the connection drops, a statement is cancelled, or a type mismatch occurs during iteration, these handlers return 200 with partial lists or zeros. `handleListImages` (`:1514`) checks `rows.Err()` correctly; these do not.
- **Evidence:** The cited lines.
- **Impact:** During a database or disk incident, `/groups` can show fewer groups, the dashboard can report `diskPressure:false` and zero staging bytes when `statfs` fails, and image detail can omit attempts. This is the misleading-healthy state `AUD2-009` set out to remove.
- **How it can occur:** A Postgres restart or failover in the middle of a query, a pool connection reset, or an unreadable volume causing a `statfs` or `ReadDir` error.
- **Recommended fix:** Add `if err := rows.Err(); err != nil { writeErr(w, err); return }` after each loop and return on `Scan` failure. In `handleDashboardStats`, propagate the `Pressured` error as `handleHealth` does (`:603-607`), and mark byte counts unknown rather than 0 when `Usage` fails.
- **Expected impact:** Outages surface as 5xx instead of plausible but wrong data.

### [AUD3-014] [LOW] — CI does not build the shipped images or check Compose; integration media tooling differs from production; actions pinned to mutable tags (was B-6)

- **Category:** CI / release gates / supply chain
- **Location:** `.github/workflows/ci.yml`; `.github/workflows/release-images.yml:28-49`
- **Confidence:** High
- **Problem:** PR and main CI run vet, gofmt, unit and integration tests, govulncheck, typecheck, build and version checks. They never run `docker build` for `Dockerfile.api`/`Dockerfile.worker` or `docker compose --profile edge config --quiet`. Images are first built in the release job, after the release is published. Integration installs Ubuntu `ffmpeg`, while the images ship Alpine 3.21 `ffmpeg`, so `AUD3-007` and `AUD3-008` are never exercised against the shipped binaries. Actions use mutable major tags (`actions/checkout@v4`, `setup-go@v5`, `setup-node@v4`), and the release job holds `packages: write`.
- **Evidence:** Workflow contents as cited. `AUD2-006` (no test gate) is fixed: `release-images.yml` calls `ci.yml`. These gaps are new.
- **Impact:** A Dockerfile or Compose regression is found only at release time. A tag-retarget compromise of a first-party action would run with GHCR push rights (low likelihood).
- **How it can occur:** A PR that breaks a Dockerfile or Compose file merges green.
- **Recommended fix:** Add a job that runs `docker build` for both Dockerfiles (no push) and `docker compose --profile edge config --quiet` with a dummy `.env`, and optionally runs the media tests inside the worker image. Pin actions by commit SHA.
- **Expected impact:** Build and deploy regressions are caught on the PR.

### [AUD3-015] [LOW] — `WEB_PORT` is published on all interfaces, and the docs rely on "do not forward" without a loopback option (was D-7)

- **Category:** Deployment security documentation
- **Location:** `docs/DOCKER_DEPLOYMENT.md:19`, `:21`; `docs/SECURITY.md:3-4`; `docs/example-deploy/README.md:17`; `docker-compose.yml:95-96`; `docs/example-deploy/compose.yml:95-96`
- **Confidence:** Medium
- **Problem:** `"${WEB_PORT:-8080}:8080"` binds 0.0.0.0. The docs say not to forward or expose the port. On a host with a public IP, Docker-published ports are reachable regardless of host firewalls such as ufw, because Docker inserts its own iptables rules. The docs never say how to bind loopback-only for a same-host proxy. `WEB_PORT=127.0.0.1:8080` works with the existing `"${WEB_PORT}:8080"` contract but is undocumented.
- **Evidence:** The cited Compose lines; `DOCKER_DEPLOYMENT.md:19` "bound to port only".
- **Impact:** The plain-HTTP listener is exposed on VPS-style hosts. Credential POSTs fail the Origin check (`server.go:424`), which limits the damage, but the auth surface and media URLs are reachable over HTTP. `SECURITY.md:4` also notes that a direct client from a private address can choose its own rate-limit key.
- **How it can occur:** Deploying on a host with a public interface and relying on ufw.
- **Recommended fix:** Document `WEB_PORT=127.0.0.1:8080` for same-host proxies, and warn that Docker publishing bypasses ufw/firewalld. This keeps the existing `WEB_PORT` contract.
- **Expected impact:** Same-host deployments can close the HTTP port without a Compose edit.

### [AUD3-016] [LOW] — `SECURITY.md` calls the guess limit "owner aggregate", but the code keys it by client address (was D-6)

- **Category:** Security documentation
- **Location:** `docs/SECURITY.md:5`
- **Confidence:** High
- **Problem:** "the owner aggregate guess limit (8 per 5 minutes across restarted sign-in attempts)" reads as one limit for the owner across all clients. The code keys it as `path+":"+clientHost(r)`, so every client address gets its own 8 guesses per 5 minutes. The review-fix plan (row S2) and the owner-profile plan both say "keyed by client address".
- **Evidence:** `internal/api/security.go:296` `s.checkRate(s.authRates, path+":"+clientHost(r), 8, 5*time.Minute)`; `internal/api/server.go:476` uses the same pattern for login.
- **Impact:** Operators may overestimate how well TOTP holds against attackers spread across many addresses. The password is still required first, which limits the real exposure. The same per-address keying is what lets `AUD3-001` scale with address count.
- **How it can occur:** An operator relies on the documented limit when assessing risk.
- **Recommended fix:** Reword to "per-client-address aggregate limit (8 per 5 minutes across restarted attempts)".
- **Expected impact:** The documented threat model matches the code.

### [AUD3-017] [LOW] — The media viewer tracks the open item by array index, so background polling switches it to a different item (was C-2)

- **Category:** Correctness / race with polling
- **Location:** `apps/web/src/photos.tsx:56`, `:136`, `:149`, `:384`, `:436-443` (`Library`, `Lightbox`)
- **Confidence:** High (source trace; not browser-reproduced)
- **Problem:** Clicking a card stores `index` (`setIndex(i)`), and the viewer renders `d.items[index]`. The list refetches every 15 s and is ordered newest first. When new media arrives, every item moves down one slot, but `index` stays the same, so the viewer silently shows the neighbouring item. If the list shrinks below `index`, the viewer closes.
- **Evidence:** `const p = items[index]` (`photos.tsx:56`). Nothing stores `p.id`. `index` is reset only by filter or page changes (`set()` at `:149`).
- **Impact:** On page 1 of an active group, the owner can look at, or open "Open in Google Photos" for, a different item than the one clicked, with no sign of the change.
- **How it can occur:** Open `/gallery`, open the second card, and send a new image to an enabled group. Within 15 s the viewer shows the image that used to be first.
- **Recommended fix:** Store the selected item's `id` and derive `index = d.items.findIndex(x => x.id === selectedId)`. Close the viewer, or keep the cached item, when it leaves the page.
- **Expected impact:** The viewer always shows the item the owner selected.

### [AUD3-018] [LOW] — `Groups` returns before calling its hooks, and the same instance is reused between `/groups` and `/albums` (was C-3)

- **Category:** Correctness / resource leak (Rules of Hooks)
- **Location:** `apps/web/src/workspace.tsx:323-327` (`Groups`); `apps/web/src/main.tsx:780-781` (routes)
- **Confidence:** Medium (clear source trace of React Router 7.18.6 and React 19 internals; the leak was not measured at runtime)
- **Problem:** `Groups({ albums })` runs `if (albums) return <AlbumsView />;` before `query()`, `useAction()` and `useState()`. React Router does not key route elements, so client navigation from `/groups` to `/albums` updates the same `Groups` fiber with zero hooks. React does not throw, but the earlier render's passive effects are never cleaned up, including the `useSyncExternalStore` subscription inside `useQuery`. The `/groups` observer and its 15 s timer keep running. Each groups → albums round trip leaves one more polling observer until a full reload.
- **Evidence:** `Library` avoids the problem with explicit keys (`main.tsx:778-779`: `key="gallery"`, `key="jobs"`); `Groups` has none. No e2e test navigates between these routes on the client.
- **Impact:** More background requests and timers in long-lived tabs. Development builds log "Expected static flag was missing". A later edit that adds a hook on the `AlbumsView` path would break.
- **How it can occur:** On desktop, click "LINE groups" then "Albums" in the sidebar several times and watch `/api/v1/groups` request frequency.
- **Recommended fix:** Route `albums` straight to `<AlbumsView />`, or add `key="groups"` and `key="albums"` to the two route elements as `Library` does. Then remove the early return.
- **Expected impact:** One observer per mounted view, and hook usage that follows the rules.

### [AUD3-019] [LOW] — Profile change forms and the MFA "Back" action drop keyboard focus to `<body>` (was C-4)

- **Category:** Accessibility (focus management)
- **Location:** `apps/web/src/profile.tsx:73-151`, `:339-355`; `apps/web/src/main.tsx:308-324`
- **Confidence:** High
- **Problem:** Activating "Enable authenticator", "Replace…", "Remove" or "Add passkey" unmounts the focused button and renders `ChangeForm` in its place. Nothing moves focus, so it falls to `<body>` with no announcement. Cancel closes the form the same way. In `Login`, "Back to sign-in choices" unmounts itself with the same result.
- **Evidence:** `ChangeForm` has no `autoFocus` or ref focus on its heading or first field. The only `autoFocus` is on the TOTP code field after the preview arrives (`profile.tsx:259`).
- **Impact:** Keyboard and screen-reader users must tab again from the top of the document in the most security-sensitive flow (WCAG 2.4.3).
- **How it can occur:** Tab to "Enable authenticator", press Enter, then press Tab.
- **Recommended fix:** Give the `ChangeForm` `<h2>` `tabIndex={-1}` and focus it on mount (or `autoFocus` the first field). On close, return focus to the trigger button. In `Login`, focus the passkey button or the email field after Back.
- **Expected impact:** Predictable focus order.

### [AUD3-020] [LOW] — E2E gaps on the critical auth path, and mocks that cannot detect contract drift (was C-5)

- **Category:** Test coverage
- **Location:** `e2e/fixtures.ts:101-155`; `e2e/profile.spec.ts`; `e2e/gallery.spec.ts`; `playwright.config.ts`
- **Confidence:** High
- **Problem:**
  - Untested paths: a successful `/auth/mfa` (200) ending on the dashboard; the 410 "Verification expired" path (only 429 is tested, `profile.spec.ts:134`); a cancelled or failed passkey sign-in (the `/auth/cancel` fallback, `main.tsx:342-346`); any `ChangeForm` preview (QR, recovery codes, the 400-keeps-preview rule at `profile.tsx:217-221`, 429 exhaustion); and the behaviors in `AUD3-004`, `AUD3-017` and `AUD3-018`.
  - The real profile flow is skipped by default. It needs `E2E_PROFILE_FIXTURE=1`, `E2E_SETUP_KEY` and a `baseURL` with hostname `localhost`; the default is `127.0.0.1` (`playwright.config.ts`). This is documented in `README.md:67`.
  - `mockApi` drifts from the real API. It ignores `page`, `from` and `to` and always returns `page: 1`. It answers `/dashboard/activity` with 404, so `responsive.spec.ts` only covers the `WeeklyActivity` error state. It returns 403 for every non-GET, so write flows pass only because tests assert that no write was sent.
- **Evidence:** The cited files.
- **Impact:** Regressions in MFA completion, recovery-code display and passkey fallback would pass CI.
- **How it can occur:** Any change to those flows.
- **Recommended fix:** Add mocked specs for MFA 200 and 410, passkey failure with cancel, and a `ChangeForm` preview with codes and a 400 retry. Make `mockApi` honour `page` and `from`/`to`, and return a default 7-day `/dashboard/activity`. Add the regression tests listed in §6.
- **Expected impact:** Coverage of the auth-critical UI without a disposable backend.

### [AUD3-021] [LOW] — Plan index and active plan Status lines are out of sync with what shipped (was D-5)

- **Category:** Plan index / status accuracy
- **Location:** `docs/plans/README.md:7-9`; `docs/plans/2026-11-22-mobile-bottom-navigation.md:2`, `:35`; `docs/plans/2026-11-23-owner-profile-mfa.md:2`, `:39`
- **Confidence:** High
- **Problem:** The mobile bottom navigation plan is listed as Active, and its Status (2026-11-22) says e2e is still pending. The work merged in PR #18 (`4697eec`) and shipped in v2026.11.6 and v2026.11.7, and the review-fix plan records the relevant Chromium specs passing. The owner-profile-mfa Status says "Implemented in the working tree", but the work is merged (`a9ac792`). It also says "WebKit e2e ... have not run", while line 39 of the same plan reports a Chromium/WebKit fixture run with 449 passes and one intermittent WebKit failure.
- **Evidence:** The cited lines; `docs/plans/README.md` filing rules 4 and 5.
- **Impact:** Readers cannot tell what shipped or which release gates are outstanding. Together with `AUD3-010`, this hides that MFA is merged but unreleased.
- **How it can occur:** Status lines not updated at merge or release.
- **Recommended fix:** Move the mobile-nav plan to `completed/` with its outstanding device check recorded, or state that it shipped in v2026.11.6. Settle which WebKit statement is true and strike the other.
- **Expected impact:** The index reflects what shipped.

### [AUD3-022] [LOW] — `.codex/hooks.json` hard-codes a user-specific binary path (was D-8)

- **Category:** Agent/tool configuration
- **Location:** `.codex/hooks.json:7`
- **Confidence:** Medium (Codex's reaction to a missing hook binary was not tested)
- **Problem:** The tracked PreToolUse hook for every Bash call runs an absolute path under one user's home directory (`.local/bin/graphify hook-check`). The SessionStart hook in the same file correctly resolves its path from `git rev-parse --show-toplevel`.
- **Evidence:** The cited line.
- **Impact:** For any other user or machine, every Codex Bash call triggers a failing hook, which is noisy or may block depending on Codex's policy. `AGENTS.md` says the graph is optional tooling.
- **How it can occur:** Using the repository's Codex config on another account or machine.
- **Recommended fix:** Use `sh -c 'command -v graphify >/dev/null || exit 0; graphify hook-check'`, or move the hook to user-level Codex config.
- **Expected impact:** Portable Codex config.

### [AUD3-023] [INFO] — Worker healthcheck proves only the heartbeat goroutine; pool queries have no statement timeout (was B-7)

- **Category:** Reliability / observability
- **Location:** `internal/worker/worker.go:790-794` (independent 1 s heartbeat); `cmd/rivosnap/main.go:54-69`; `internal/db/db.go:36-61` (no `statement_timeout`)
- **Confidence:** Medium (by reading)
- **Problem:** A dispatch blocked on a hung Postgres socket would keep `worker:heartbeat` fresh, so the container stays healthy.
- **Evidence:** The heartbeat runs in its own goroutine, independent of dispatch progress. No `statement_timeout` or `idle_in_transaction_session_timeout` is set on the pool.
- **Impact:** Small. The API's `worker:lastProgress` already shows a stall to the owner, and Docker does not restart unhealthy containers anyway.
- **How it can occur:** A network partition or server-side hang leaves a query waiting indefinitely.
- **Recommended fix:** Set `statement_timeout`/`idle_in_transaction_session_timeout` as pool runtime parameters, or make the healthcheck also consider `lastProgress` when the outbox has due rows.
- **Expected impact:** A stuck worker becomes visible to health tooling and eventually errors out.

### [AUD3-024] [INFO] — Integration tests are destructive and guarded only by an environment variable (was B-8)

- **Category:** Testing safety
- **Location:** `internal/db/migrate_test.go:99` (`DROP SCHEMA public CASCADE`); integration fixtures in `internal/api/pipeline_test.go`
- **Confidence:** High
- **Problem:** `scripts/test-integration.sh` is safe: it uses disposable `--rm` containers on random loopback ports and overrides `DATABASE_URL`. A manual `GALLERY_INTEGRATION=1 go test ./...` with a production `DATABASE_URL` in the shell would wipe that database.
- **Evidence:** The cited line; the script's container setup.
- **Impact:** Operator error only, but irreversible.
- **How it can occur:** Running the tests by hand from a shell that has the production environment loaded.
- **Recommended fix:** Also require a fixture marker, for example refuse unless `current_database()` and host match the script's container, or unless a `GALLERY_INTEGRATION_DB_OK=<dbname>` token matches.
- **Expected impact:** The destructive tests cannot run against a non-disposable database by accident.

### [AUD3-025] [INFO] — Per-request Postgres revision lookup plus `Cache-Control: no-store` on thumbnails multiplies load on gallery views (was A-5)

- **Category:** Performance
- **Location:** `internal/api/server.go:411` (global `no-store`), `:437` → `security.go:30-49` (`sessionRevision`: Redis `GET` and `SELECT "securityRevision"` on every authenticated `/api/*` request)
- **Confidence:** High for the mechanism; not benchmarked.
- **Problem:** Each gallery card's thumbnail is a separate authenticated request with one Redis and one Postgres round trip. The global `no-store` stops the browser reusing thumbnails across navigations. One 36-card page means 36 DB queries every time the owner returns to it. The 600/min per-session bucket (`:417`) is shared with 15 s polling.
- **Evidence:** The cited lines.
- **Impact:** Small at current scale. It is the main per-request DB cost on media routes and adds pool pressure under heavy browsing.
- **How it can occur:** Normal gallery browsing.
- **Recommended fix:** Keep `no-store` for JSON. For thumbnails (URLs keyed by image UUID and size), use `private, max-age=<thumbnailDays>`. Optionally cache the owner revision in process for 1–2 s, invalidated on `finishChange`.
- **Expected impact:** Fewer DB round trips and faster gallery revisits.

### [AUD3-026] [INFO] — Per-minute cleanup queries scale with total history (was B-10)

- **Category:** Performance
- **Location:** `internal/worker/worker.go:663-672`, `:724`
- **Confidence:** Medium (by reading; not measured)
- **Problem:** The staging-expiry query filters `uploadStatus='SYNCED' AND stagingPath IS NOT NULL AND uploadedAt < $1`. Its candidate indexes (`Image_uploadStatus_createdAt_idx`, partial `Image_uploadedAt_idx`) cover every synced row ever, nearly all of which already have `stagingPath` NULL. `WebhookEvent."receivedAt"` has no index for the 90-day delete.
- **Evidence:** The cited query and the migration indexes.
- **Impact:** Negligible at personal scale; grows with history.
- **How it can occur:** Long-running installations with large history.
- **Recommended fix:** A partial index `ON "Image"("uploadedAt") WHERE "stagingPath" IS NOT NULL AND "uploadStatus"='SYNCED'`, and an index on `WebhookEvent."receivedAt"`, if history grows large.
- **Expected impact:** Constant-cost cleanup.

### [AUD3-027] [INFO] — OpenAPI document does not mark owner-only routes as requiring the session and omits some routes (was A-4)

- **Category:** API contract / documentation
- **Location:** `internal/api/server.go:2094-2202` (`openAPISpec`)
- **Confidence:** High
- **Problem:** The spec defines the `session` scheme but applies it only to non-public `securityPaths` (`:2196-2198`). With no top-level `security`, every core route (`/images`, `/settings`, `/jobs/{id}/discard` and so on) is described as unauthenticated, although the middleware (`:429-447`) requires a revision-bound session. `/api/openapi.json` and `/ready` are missing.
- **Evidence:** The cited lines.
- **Impact:** Generated clients and reviewers get a wrong picture of the auth surface. Runtime authorization is unaffected, and the document itself is owner-only.
- **How it can occur:** Anyone reading or generating from the spec.
- **Recommended fix:** Add top-level `"security": [{"session": []}]` and `"security": []` on public operations (`webhooks/line`, `auth/status`, `auth/login`, `auth/setup`, public security routes). Add the two missing paths.
- **Expected impact:** The contract matches the middleware.

### [AUD3-028] [INFO] — The worker also runs migrations at startup; the docs say only the API does (was D-9)

- **Category:** Documentation accuracy
- **Location:** `docs/ARCHITECTURE.md:23`; `docs/DOCKER_DEPLOYMENT.md:35`
- **Confidence:** High
- **Problem:** The docs say the API applies migrations ("API also migrates before listening"). `cmd/rivosnap/main.go:95-101` calls `database.Migrate` for every command except the healthchecks, so `worker` migrates too.
- **Evidence:** The cited lines. Runs are serialized by `pg_advisory_xact_lock(72707369)` (`internal/db/migrate.go:59`), so this is safe.
- **Impact:** Starting a new worker image migrates the database even if the API has not run, which matters for the "stop the worker before migrations" guidance.
- **How it can occur:** Upgrading with the worker started first.
- **Recommended fix:** State that both `api` and `worker` apply pending migrations under an advisory lock.
- **Expected impact:** Operators can predict when the schema changes.

### [AUD3-029] [INFO] — Some UI copy and labels are out of date or not localized (was C-6)

- **Category:** i18n / consistency
- **Location:** `apps/web/src/workspace.tsx:833`, `:838`; `apps/web/src/main.tsx:101-106`; `apps/web/src/onboarding.tsx:86`
- **Confidence:** High
- **Problem:** The Health page prints raw keys (`database`, `redis`, `worker`, `lineCredentials`) instead of `healthLabels`, and formats `workerLastProgressAt` with `toLocaleString()` (browser locale and zone) while every other timestamp uses `date()` (`en-GB`, Asia/Bangkok). `healthLabels` has no entry for `lineCredentials` (which `/health` can return as `"Undecryptable"`, `server.go:618-619`). Onboarding says "old messages, videos, and DMs are skipped", which is wrong when `collectVideos` is enabled.
- **Evidence:** The cited lines.
- **Impact:** Mixed timezones and developer keys in the UI. Behavior is not affected.
- **How it can occur:** Viewing Health or onboarding.
- **Recommended fix:** Use `healthLabels` and `date()` on the Health page, add `lineCredentials: "LINE credentials"`, and make the onboarding copy depend on `prefs.collectVideos`.
- **Expected impact:** Consistent labels and times.

### [AUD3-030] [INFO] — The theme preference uses unguarded `localStorage` and is applied only after first paint (was C-7)

- **Category:** Robustness / rendering
- **Location:** `apps/web/src/main.tsx:638-650`
- **Confidence:** High
- **Problem:** `localStorage.getItem` runs in a `useState` initializer and `setItem` in an effect, with no try/catch. `data-theme` is set in `useEffect`, so dark-theme users see a light flash on load. There is no `prefers-color-scheme` default.
- **Evidence:** The cited lines.
- **Impact:** Where storage access throws (some embedded or privacy-hardened contexts), the whole app fails to render. Otherwise cosmetic.
- **How it can occur:** Opening the app in a context that blocks storage.
- **Recommended fix:** Wrap storage access in try/catch, and set `data-theme` before first paint (an inline script in `index.html` if the CSP permits, or `useLayoutEffect`).
- **Expected impact:** The app renders everywhere without a theme flash.

### [AUD3-031] [INFO] — A page beyond the end shows an empty grid with no empty state (was C-8)

- **Category:** UX correctness
- **Location:** `apps/web/src/photos.tsx:260-271`, `:417-435`
- **Confidence:** High
- **Problem:** The empty state shows only when `!d.total`. If `page` is past the end, `items` is `[]` but `total > 0`, so the page shows a blank grid with a "Page N" footer.
- **Evidence:** The cited lines.
- **Impact:** Confusing blank page; no data effect.
- **How it can occur:** Retrying, syncing or discarding the last job on a later queue page, or following a stale `?page=` link.
- **Recommended fix:** If `d.items.length === 0 && d.total > 0 && d.page > 1`, show a short message with a link to the last page, or step back to `Math.ceil(d.total / 36)`.
- **Expected impact:** The owner always sees content or a clear empty state.

## 3. Performance & Resource Optimization

- **High impact (resource bounds):**
  - `AUD3-001`: bound Argon2id concurrency. This is the largest memory consumer reachable without authentication.
  - `AUD3-009`: stop on-demand `ffmpeg` on unvalidated bytes; failures are not cached, so each page view repeats up to 30 s of decode.
- **Medium impact:**
  - `AUD3-018`: leaked `/groups` polling observers grow with client navigation.
  - `AUD3-025`: per-request revision lookup and `no-store` on thumbnails.
- **Low impact:** `AUD3-026` (cleanup queries scale with history).
- **Not merged:** `AUD3-025` (was A-5) and `AUD3-026` (was B-10) were candidates for a merge. They have different causes (request-path auth and caching versus maintenance query indexing) and different fixes, so they stay separate.
- **Benchmark before changing:** Argon2id memory inside the real API cgroup (the `AUD3-001` numbers are from a scratch test outside the container); `ffmpeg` memory on inputs above 100 MP (not measurable on the audit host); thumbnail request cost at realistic gallery sizes.
- **Verified sound:** downloads, hashing and Google uploads stream from file. Image inspection is header-only via `DecodeConfig` (`AUD2-003` fixed). The worker performs no whole-file reads. Dashboard stats use one aggregate `COUNT FILTER` query with 10 s cached usage. `/groups` is a single `LATERAL` query (`AUD2-007` fixed). Gallery day numbers and weekly activity are served by `Image_gallery_order_idx` and `Image_uploadedAt_idx`. The production bundle is 401.17 kB JS (127.42 kB gzip) and 32.21 kB CSS.

## 4. Security Findings

- **Findings:** `AUD3-001` (unauthenticated memory exhaustion), `AUD3-009` (unvalidated bytes to `ffmpeg`), `AUD3-015` (HTTP listener on all interfaces), `AUD3-016` (rate-limit wording), `AUD3-010` (MFA release mapping), `AUD3-014` (mutable action tags with `packages: write`), `AUD3-027` (OpenAPI security metadata).
- **Controls verified by reading:**
  - Middleware order: headers, rate bucket, exact Origin and `X-Gallery-Request` for non-GET, auth allowlist, then `sessionRevision`. Path normalization, case and `%2F` variants do not bypass auth.
  - Setup uses a constant-time bootstrap check and a singleton insert.
  - MFA: per-host and per-attempt limits, proof under `FOR UPDATE`, TOTP replay blocked by `totpStep`, single-use recovery codes, flow consumed via `DEL` count 1, cookie set after commit.
  - Passkeys: discoverable login with user-handle and credential checks, UV required, clone warning rejects. Profile changes are SID-bound and revision-checked. Each change advances the revision, which invalidates all sessions.
  - Webhook: 1 MB body cap, HMAC over raw bytes in constant time, destination pin.
  - OAuth: session-bound state, `GETDEL` plus SID match, scope and album probe, sealed upsert. Errors redirect without detail.
  - Media: UUID-validated IDs, server-derived paths, remote fetch restricted to https `*.googleusercontent.com`, no redirects, MIME allowlist, 10 MB thumbnail cap.
  - Crypto: AES-256-GCM with a fresh 12-byte nonce per seal; constant-time HMAC and cookie comparison; Argon2id with constant-time compare.
  - Frontend: QR, manual key and recovery codes live only in `ChangeForm` state, never in the query cache, URL or storage. A successful change does a full reload. Logout clears the cache. Only `google_error` codes appear in URLs.
  - Containers run as uid 1000 with `no-new-privileges` and `mem_limit`. Postgres and Redis are on an `internal: true` network. `.dockerignore` excludes `.env`, keys and pem files.
- **Verification limits:** No live penetration test. The external reverse proxy and host firewall are outside the repository. Real WebAuthn and TOTP flows were not exercised against a running backend. `pnpm-lock.yaml` was not audited and `pnpm audit` was not run in this audit. `govulncheck` results are in §9 and are not a finding.

## 5. Reliability & Concurrency Findings

- **Findings:** `AUD3-002` (short retry budget), `AUD3-003` (INTEGRITY on retry), `AUD3-006` (shutdown bookkeeping on a cancelled context), `AUD3-007` (tool failures classified as invalid media), `AUD3-008` (preview blocks upload), `AUD3-013` (swallowed errors), `AUD3-004` (frontend error state replaces the workspace), `AUD3-017` (viewer index race), `AUD3-023` (healthcheck and statement timeout).
- **Linked failure chain:** a routine restart (`AUD3-006`) or a transient tool failure can lead to FAILED with a staged file, which `AUD3-003` then makes unrecoverable by Retry. Fixing `AUD3-003` first removes the worst outcome of the others.
- **Verified as sound:**
  - Dedup: `WebhookEvent_webhookEventId_key` and `Image_channelId_lineMessageId_key` with `ON CONFLICT`, in one transaction with the `PROCESS` outbox insert. Disk pressure is checked inside that transaction and rolls back with a retryable fault.
  - Claiming: single worker under session `pg_advisory_lock(72707370)`; `FOR UPDATE SKIP LOCKED` with a `publishedAt` lease; serial processing; ack only in the same transaction as the state checkpoint (`AUD-009` fixed).
  - Uncertain writes: CREATING_MEDIA is persisted before `batchCreate`. Non-definitive errors reconcile by the deterministic `line-<uuid>.<ext>` filename, and 0 or more than 1 match gives NEEDS_REVIEW. Uncertain album creation is held as `ALBUM_REVIEW`, and owner repair requeues parked images (`AUD2-005` fixed). A SQL CHECK prevents SYNCED without a media ID.
  - Retention: only SYNCED rows with `googleMediaItemId` and aged originals are removed automatically. Failed or uncertain originals are removed only through the audited DISCARD. Undecodable bytes are kept.
  - Migrations: one transaction under `pg_advisory_xact_lock(72707369)`, checksum verification, and refusal of unfinished or unknown applied migrations.
  - HTTP: per-request timeouts (90 s API, 600 s content), redirects refused, error bodies read through `LimitReader(2048)`, singleflighted token refresh detached from caller cancellation.
  - Jobs: retry and discard use CAS updates with outbox and audit rows in one transaction. Discard requires the confirmation phrase and `createdAt + failedDays`.

## 6. Testing Gaps

1. No test covers Retry of a FAILED item with a staged, uninspected file (`AUD3-003`). Extend `crashLoopFixture`: reach FAILED/WORKER_INTERRUPTED, retry as `handleRetryJob` does, and assert the status passes UPLOADING (or reaches SYNCED with a stub Google transport), never `INTEGRITY`.
2. No test sets the retry horizon (`AUD3-002`): sum `Backoff(1..7)` against the intended minimum; fail LINE 8 times with a network error then succeed and assert SYNCED; assert a manual retry after exhaustion gets more than one attempt.
3. No concurrency bound test for password hashing (`AUD3-001`): fire 20 logins from 20 `X-Real-IP` values through a loopback peer and assert no more than N verifications overlap and the excess gets 429 or 503.
4. No shutdown test (`AUD3-006`): cancel the context from a stub transport during `CreateAlbum` or upload and assert the attempt completes and a returned album ID is persisted.
5. Media-tool fault classification (`AUD3-007`, `AUD3-008`): a stub `ffprobe` that sleeps past the deadline or kills itself must give a transient fault; a stub `ffmpeg` that exits 1 must not block a staged JPEG from reaching SYNCED.
6. No test for the quarantined-thumbnail path (`AUD3-009`): seed an UNRECOVERABLE IMAGE row with `stagingPath` set and `mimeType` NULL, request `/thumbnail`, and assert 404 with an empty stub-`ffmpeg` log.
7. Error propagation (`AUD3-013`): use the `closedPoolHandler` pattern for `/groups` and a removed staging path for `/dashboard/stats`, and assert 5xx or an explicit unknown marker.
8. No route-table test asserts that every `/api/*` route outside the allowlist returns 401 without a session. Auditor A verified this only by reading. An OpenAPI walk (`AUD3-027`) can share the allowlist.
9. Frontend (`AUD3-020`): MFA 200 and 410, passkey cancel, `ChangeForm` preview and 400 retry; regression tests for `AUD3-004` (503 on `/auth/status` must not unmount a form), `AUD3-017` (viewer keeps its item across a poll) and `AUD3-018` (request count after repeated groups/albums navigation); focus assertions for `AUD3-019`. `mockApi` should honour pagination, date filters and `/dashboard/activity`.
10. 30 `internal/api` integration tests skip without `GALLERY_INTEGRATION=1`. They pass in `scripts/test-integration.sh`, but tests that need a real `ffmpeg` cannot exercise it on hosts without one, and CI uses Ubuntu `ffmpeg` rather than the shipped Alpine build (`AUD3-014`).
11. Destructive integration tests rely only on an environment variable (`AUD3-024`).

## 7. Architecture Recommendations

- **Bound every expensive operation behind a shared gate.** Argon2id (`AUD3-001`) and `ffmpeg` (`AUD3-009`) both allocate large, attacker- or input-driven memory in a 768 MiB cgroup. A small semaphore per class with a bounded wait and a 429/503 answer follows the existing `thumbSem` pattern.
- **Separate retry budget from attempt history.** The attempt ledger is an audit record. The transient-retry budget should be its own counter, reset by owner action, with a horizon of hours (`AUD3-002`). Routing on retry should depend on what was recorded (`sha256`, `mimeType`), not on whether a file exists (`AUD3-003`).
- **Persist outcomes past cancellation.** Work started before SIGTERM should record its result on a detached, short-timeout context (`AUD3-006`).
- **Keep rendering best effort.** Previews should never block upload (`AUD3-008`), and only validated bytes should be rendered (`AUD3-009`). This continues the `AUD2-003`/`AUD2-004` direction.
- **Stale-while-error in the UI.** Treat a failed refetch with existing data as degraded, not fatal (`AUD3-004`), and key selection state by ID, not position (`AUD3-017`).
- **Tie docs to releases.** Name the first release that contains each security feature (`AUD3-010`) and keep plan Status lines in step with merges and releases (`AUD3-021`).

## 8. Recommended Action Plan

- **Immediate:**
  1. `AUD3-001`: semaphore around Argon2id with a bounded wait; optional `GOMEMLIMIT`.
  2. `AUD3-003`: route RETRY_PENDING to download unless `sha256` and `mimeType` are recorded.
  3. `AUD3-002`: reset the retry budget on manual retry and lengthen the automatic horizon.
- **Short term:**
  - `AUD3-006` (detached persistence on shutdown, `stop_grace_period`).
  - `AUD3-007`, `AUD3-008` (media-tool fault classification; best-effort previews).
  - `AUD3-009` (no on-demand thumbnails for unvalidated rows).
  - `AUD3-004` (stale-while-error auth gate).
  - `AUD3-010` (cut or name the first MFA release; fix the pin example).
  - `AUD3-011`, `AUD3-012` (backup and repair runbooks).
  - `AUD3-013` (error propagation).
- **Medium term:**
  - `AUD3-014` (image build and Compose check in CI; SHA-pinned actions).
  - `AUD3-005`, `AUD3-022` (agent tooling portability).
  - `AUD3-015`, `AUD3-016` (deployment and security docs).
  - `AUD3-017`, `AUD3-018`, `AUD3-019`, `AUD3-020` (frontend correctness, accessibility and e2e coverage).
  - `AUD3-021` (plan index).
- **Optional:** `AUD3-023` through `AUD3-031`, and the §3 benchmark candidates. Update the local Go toolchain to go1.27.4 for parity (see §9).

## 9. Audit Coverage

**Status: partial.** File coverage is full: all 142 tracked files are accounted for below, 126 reviewed (2 at reduced depth) and 16 excluded with reasons. Behavioral verification is partial: no browser-based e2e run happened during this audit, no Docker image was built, `ffmpeg` was not available on the audit host, real LINE and Google APIs were not exercised, and the two lockfiles were not audited. Because those runtime and dependency checks are missing, the overall verdict is partial rather than full.

Untracked local paths are outside scope: `graphify-out/`, `node_modules/`, `.agent-parity/` (relevant to `AUD3-005`) and the git-ignored `apps/web/dist/` produced by the build check. `git status` stayed clean.

### Reviewed (read in full unless noted)

- **Root:** `.dockerignore`, `.env.example`, `.gitattributes`, `.gitignore`, `.mcp.json`, `AGENTS.md`, `CLAUDE.md`, `Caddyfile`, `Dockerfile.api`, `Dockerfile.worker`, `GEMINI.md`, `README.md`, `VERSION`, `docker-compose.override.yml.example`, `docker-compose.yml`, `go.mod`, `package.json`, `playwright.config.ts`, `pnpm-workspace.yaml`, `tsconfig.json` (B, D)
- **`.agents/`:** `hooks.json`, `mcp_config.json`, `skills/agent-parity/SKILL.md`, `skills/write-governance/SKILL.md`, `skills/write-requirement/SKILL.md` (D)
- **`.claude/`:** `settings.json` (D)
- **`.codex/`:** `config.toml`, `hooks.json` (D)
- **`.cursor/`:** `cli.json`, `hooks.json`, `mcp.json` (D)
- **`.github/workflows/`:** `ci.yml`, `release-images.yml` (B)
- **`apps/web/`:** `index.html`, `package.json`, `vite.config.mjs` (C)
- **`apps/web/public/`:** `rivosnap-mark.svg`, `rivosnap.svg` (C; static assets)
- **`apps/web/src/`:** `data.ts`, `i18n.ts`, `library-responsive.css`, `main.tsx`, `onboarding.tsx`, `photos.tsx`, `profile.tsx`, `weekly-activity.css`, `weekly-activity.tsx`, `workspace-responsive.css`, `workspace.tsx` (C)
- **`cmd/rivosnap/`:** `main.go` (B)
- **`docs/`:** `ARCHITECTURE.md`, `DOCKER_DEPLOYMENT.md`, `GOOGLE_CLOUD_SETUP.md`, `GOOGLE_PHOTOS_OAUTH.md`, `LINE_SETUP.md`, `OWNER_GUIDE.md`, `SECURITY.md`, `TROUBLESHOOTING.md`, `theme-preview.html` (D)
- **`docs/example-deploy/`:** `README.md`, `compose.yml`, `env.example`, `web.conf` (D)
- **`docs/plans/`:** `README.md`, `roadmap.md`, `2026-11-22-mobile-bottom-navigation.md`, `2026-11-23-owner-mfa-review-fixes.md`, `2026-11-23-owner-profile-mfa.md` (D)
- **`e2e/`:** `fixtures.ts`, `gallery.spec.ts`, `library-responsive.spec.ts`, `profile.spec.ts`, `responsive.spec.ts`, `shell-responsive.spec.ts`, `weekly-activity.spec.ts`, `workspace-responsive.spec.ts` (C)
- **`infra/`:** `caddy/Dockerfile`, `docker/web.conf` (B)
- **`internal/api/`:** `dashboard_activity.go`, `dashboard_activity_test.go`, `gallery_number_test.go`, `ingest.go`, `pipeline_test.go`, `rate_test.go`, `security.go`, `security_integration_test.go`, `security_test.go`, `server.go`, `totp.go`, `totp_test.go` (A; 12 files, 5,665 lines)
- **`internal/config/`:** `config.go` (B)
- **`internal/crypto/`:** `crypto.go`, `crypto_test.go` (B)
- **`internal/db/`:** `db.go`, `migrate.go`, `migrate_test.go` (B)
- **`internal/google/`:** `google.go` (B)
- **`internal/httpclient/`:** `http.go` (B)
- **`internal/line/`:** `line.go`, `line_test.go` (B)
- **`internal/media/`:** `media.go`, `media_test.go` (B)
- **`internal/model/`:** `model.go`, `model_test.go` (B)
- **`internal/web/`:** `web.go`, `dist/.keep` (B; `.keep` is an empty placeholder)
- **`internal/worker/`:** `worker.go` (B)
- **`packages/database/migrations/`:** `embed.go`, `migration_lock.toml`, and `migration.sql` in `202611190001_initial`, `202611190002_authorization_window`, `202611200001_video_support`, `202611210001_timestamptz`, `202611230001_owner_security`, `202611230002_gallery_order`, `202611230003_upload_activity` (B)
- **`packages/ui/`:** `button.tsx`, `package.json` (B)
- **`scripts/`:** `release.mjs`, `sync-version.mjs`, `version-control.mjs`, `version-targets.mjs`, `test-integration.sh`, `repair/2026-11-21-utc-timestamps.sql` (B)

### Reviewed at reduced depth

- `apps/web/src/style.css` (1,667 lines): lines 760–1190 read in condensed form (C).
- `scripts/version-control.test.mjs`: reviewed by execution (20/20 pass); content not line-audited beyond test names (B).

### Excluded or unverified

- `apps/web/public/rivosnap-mark.png`: binary image asset. Its reference in `index.html` was confirmed.
- `pnpm-lock.yaml`, `go.sum`: generated lockfiles. Not line-reviewed, and no `pnpm audit` was run in this audit. `govulncheck` covers Go modules only (see Checks run).
- `docs/audit/2026-11-20-audit-report.md`, `docs/audit/2026-11-21-audit-report.md`, `docs/audit/2026-11-21-audit-report-2.md`: historical records. Their finding IDs were used to separate new issues from fixed ones.
- `docs/plans/completed/` (10 files: `2026-11-20-audit-remediation.md`, `2026-11-20-frontend-design-refactor.md`, `2026-11-20-refresh-line-group-name.md`, `2026-11-20-sidebar-health-settings-refactor.md`, `2026-11-20-video-collection-support.md`, `2026-11-21-audit2-remediation.md`, `2026-11-21-go-backend-migration.md`, `2026-11-21-production-auto-migrations.md`, `2026-11-22-clementine-sky-theme.md`, `2026-11-22-responsive-design.md`): historical records, listed and not reviewed.

### Flows traced end to end

- **Ingest:** LINE webhook → credentials → 1 MB cap → HMAC over raw bytes → destination pin → `ParseEnvelope` → per-event transaction (dedup, group lock, membership ordering, authorization window, unique `Image`, disk pressure, `PROCESS` outbox).
- **Worker:** claim (advisory lock, SKIP LOCKED, 1-min lease) → crash-loop cap and attempt ledger → download → bounded `Stage` with inspect, atomic rename and quarantine → STAGED checkpoint and UPLOAD outbox → previews → integrity re-inspect → `ResolveAlbum` CAS → capacity check → upload bytes → CREATING_MEDIA → `batchCreate` → reconcile → SYNCED. Also failure classification, backoff, RETRY_PENDING routing, manual retry, RECONCILE, DISCARD, album repair, `Cleanup`, token refresh and cache, LINE credential decryption fallback, migrations ledger.
- **Owner auth:** middleware order; setup and login; MFA (rates, `FOR UPDATE` proof, replay, recovery codes, flow consumption, cookie after commit); passkey login; profile start/prove/finish with revision checks; profile read.
- **OAuth:** connect state → callback (GETDEL, SID, exchange, scopes, validate, album probe, sealed upsert).
- **Media:** UUID IDs, `PathFor`, `ServeContent`, on-demand thumbnail singleflight and semaphore, remote proxy with host and MIME allowlists and Range for video.
- **Jobs, settings, groups, dashboard, gallery day numbers, weekly activity, OpenAPI.**
- **Frontend:** setup and onboarding, password/MFA/passkey login, profile changes, gallery, jobs, albums and groups, dashboard, settings, health, mobile shell, session expiry and logout.
- **Release and deployment:** versioning scripts, release workflow, Compose topology, Nginx proxying, documented commands and environment variables checked against code.

### Checks run (exact commands and outcomes)

| Command | Auditor | Outcome |
|---|---|---|
| `rtk proxy graphify query "internal/api server routes auth session MFA passkey webhook media serving"` | A | Ran first; route and handler map used for orientation |
| `rtk proxy graphify query "ingestion pipeline outbox worker claim upload reconciliation"` | B | Ran; truncated graph context used for orientation |
| `rtk proxy graphify query "web frontend routes login MFA passkey profile gallery"` | C | OK; 196 nodes, truncated output used for orientation |
| `rtk proxy graphify query ...` (docs) | D | Ran first; orientation only |
| `rtk go vet ./internal/api/...` | A | Exit 0, no findings |
| `rtk go vet ./...` | B | Exit 0, no findings |
| `rtk go test ./internal/api/...` | A | Pass (24 passed) |
| `rtk proxy go test -count=1 -v ./internal/api/...` | A | `ok`; 30 integration tests SKIP without `GALLERY_INTEGRATION=1` |
| `rtk go test ./...` | B | 40 passed in 13 packages (integration tests skipped without env) |
| `rtk proxy bash scripts/test-integration.sh` | B | Exit 0; all packages `ok` (api 10.9 s); 59 `--- PASS` lines, no FAIL; disposable containers |
| `~/go/bin/staticcheck ./internal/api/...` | A | Exit 0, no findings |
| `~/go/bin/govulncheck ./internal/api/...` | A | 7 reachable stdlib vulnerabilities on local go1.27.1, all fixed by go1.27.4 |
| `~/go/bin/govulncheck ./...` | B | Exit 3; 12 reachable stdlib vulnerabilities (GO-2026-6603…6859) on local go1.27.1, all fixed in go1.27.2–1.27.4; module findings (1 package, 2 modules) not called |
| `rtk proxy node scripts/sync-version.mjs --check` | B, D | `Checked 2026.11.7`, exit 0 |
| `rtk proxy node --test scripts/version-control.test.mjs` | B | 20 pass, 0 fail |
| `rtk docker compose --profile edge config --quiet` | B | Exit 0 (output not printed) |
| `rtk proxy corepack pnpm typecheck` | C | Pass, exit 0, no diagnostics |
| `rtk proxy corepack pnpm build` | C | Pass, exit 0, 1.90 s; `index.html` 0.61 kB, CSS 32.21 kB (gzip 7.82 kB), JS 401.17 kB (gzip 127.42 kB); only dependency `"use client"` notices |
| Scratch Argon2id concurrency probe (outside the repository, `GOPROXY=off`) | A | Numbers cited in `AUD3-001` |
| Ad-hoc Playwright check against a local Vite dev server on port 4199 | C | Could not run: Chromium exit 127 (missing libraries); WebKit missing `libatk-bridge-2.0`, `libGLESv2`, `libx264`; Firefox not installed; MCP browser missing `/opt/google/chrome` |
| JSON.parse on 8 JSON configs; `tomllib` on `.codex/config.toml` | D | All valid |
| Relative link and anchor check over README, AGENTS, GEMINI, `docs/*.md`, `docs/plans/*.md`, example-deploy README and skills | D | 0 missing targets, 0 bad anchors |
| `git check-ignore -v .agent-parity/mcp/memory/run.sh`; `git ls-files .agent-parity` | D, compile | `.gitignore:42:.agent-parity/`; 0 tracked files |
| `git cat-file -e v2026.11.7:internal/api/security.go` | D, compile | Path absent from `v2026.11.7` |
| `git show v2026.11.7:internal/db/migrate.go`, `git show v2026.11.7:cmd/rivosnap/main.go` | compile | Tag refuses unknown applied migrations and exits 1 (basis for lowering `AUD3-010`) |
| `rtk git ls-files` | compile | 142 tracked files; reconciled above |
| Source re-reads of every MEDIUM candidate (`server.go:470-540`, `:1934-1972`; `crypto.go:100-150`; `worker.go:215-245`, `:355-500`, `:550-630`; `model.go:100-116`; `media.go:357-401`; `main.tsx:660-700`; `data.ts:45-60`; `docker-compose.yml:1-60`; `web.conf:11-15`; `DOCKER_DEPLOYMENT.md:50-66`; `SECURITY.md:1-22`; `TROUBLESHOOTING.md:40-52`; `example-deploy/README.md:1-35`; `.gitignore:36-48`; `.mcp.json`) | compile | 5 MEDIUM confirmed; 3 lowered to LOW (see `AUD3-010`, `AUD3-011`, `AUD3-012`) |

**`govulncheck` is not a finding** (merges A's coverage note and B-9). Both runs used the local toolchain go1.27.1. The counts differ (7 vs 12) because A scanned `./internal/api/...` and B scanned `./...`. CI (`setup-go 1.27.4`) and both Dockerfiles (`golang:1.27.4-alpine`) use the fixed toolchain, so shipped binaries are not affected. Update the local toolchain for parity.

No `.env` file or expanded Compose configuration was printed. Production was not touched.

### Candidates rejected or re-rated

- **B-9 (local toolchain behind the pinned one):** rejected as a finding. It affects only the local development toolchain; shipped images use go1.27.4. Recorded above.
- **D-3:** lowered MEDIUM → LOW (`AUD3-010`). The claimed silent MFA bypass on rollback does not hold: `v2026.11.7` refuses to start against a database with unknown migrations.
- **D-1:** lowered MEDIUM → LOW (`AUD3-011`). `SECURITY.md:10`, `:19` and `example-deploy/README.md:14` already tell the operator to back up the key separately and say TOTP seeds depend on it. The remaining gap is the runbook procedure.
- **D-2:** lowered MEDIUM → LOW (`AUD3-012`). The command fails before changing anything, and the runbook orders it before deployment; the irreversible outcome needs the operator to proceed after a failed step.
- **A-5 / B-10:** considered for merging; kept separate (different causes and fixes; see §3).

### Not verified

- **No e2e browser run happened during this audit.** No Playwright browser could launch on the audit host, so `AUD3-004`, `AUD3-017` and `AUD3-018` rest on source traces and documented library semantics. The full e2e suite was not run, and the real profile fixture flow was not exercised.
- **`ffmpeg` was not available on the audit host.** `ffmpeg` memory on inputs above 100 MP (`AUD3-009`) could not be measured, and real thumbnailing was not exercised.
- No Docker image was built and no container was run. Alpine 3.21 `ffmpeg`/`ffprobe` behavior and the presence of `ffprobe` in the Alpine package (`AUD3-007`, `AUD3-008`) were inferred.
- Argon2id memory inside the API cgroup and the real OOM-killer victim selection (`AUD3-001`, `AUD3-009`). The cited numbers come from the auditor's scratch test outside the container.
- Real LINE and Google Photos behavior, including LINE's content retention window, which bounds the loss path in `AUD3-002`.
- Real WebAuthn and TOTP behavior against a backend; screen-reader output; colour contrast beyond reading the tokens.
- Browser-dependent wording in `OWNER_GUIDE.md` and `SECURITY.md` (passkey prompts, Lax cookies across the OAuth redirect), checked only against code.
- The 300 s total client timeout on remote video streaming (`server.go:1788-1801`) during long playback through one open-ended Range request: possible mid-playback truncation, not reproduced, not reported.
- Whether bursts of LINE webhooks from one LINE egress address can exceed the 600/min bucket (`server.go:417`); considered unlikely for a single-owner deployment.
- How Codex reacts to a missing hook binary (`AUD3-022`).
- `pnpm-lock.yaml` and `go.sum` contents.
- External reverse-proxy configuration and host firewall.
