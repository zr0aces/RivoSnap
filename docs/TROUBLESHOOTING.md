# Troubleshooting

| Symptom | Operator action |
|---|---|
| Setup denied | Use the deployment's bootstrap key only for first claim. If an owner exists, sign in. A mismatched APP_URL or proxy rewriting Origin blocks browser writes. |
| Passkey unavailable or not found | Open the exact HTTPS `APP_URL` in a supported browser and choose a device or password manager holding a registered passkey. If the hostname changed, use password sign-in and enroll a new passkey. Enrolled authenticator MFA still applies to passwords. |
| Authenticator code rejected or phone lost | Check that the phone and server clocks are synchronized and try the next code, not one already used. If the attempt expired, restart sign-in. Use a registered passkey or your password plus an unused recovery code, then [replace the lost method in Profile](OWNER_GUIDE.md#secure-your-owner-account). |
| Webhook Verify fails | Check public TLS, exact URL, unmodified body and matching channel secret. Enable redelivery. A full staging disk returns a temporary error rather than dropping images. |
| Missing group | Enable LINE group participation and invite the correct Official Account. Direct messages and rooms never register. An already joined group can be discovered by a new group image but stays disabled. |
| No image after enabling | Send a **new image** after enablement. Check membership, configuration and worker heartbeat. LINE_404/410 means content is no longer available; sending a new image is required. |
| OAuth consent fails | Match redirect URI exactly, enable Library API, add test user if needed, grant all requested scopes (`appendonly`, `readonly.appcreateddata`, `edit.appcreateddata`). Reauthorize the original account/project after invalid_grant. |
| Sign-in is busy (503) | The API is already hashing its maximum number of passwords. Wait a few seconds and try again; wrong passwords still answer 401. Repeated busy responses point to a flood of sign-in attempts; check the rate limit and the source addresses. |
| `MEDIA_TOOL` fault | `ffprobe` or `ffmpeg` was missing, killed, or interrupted by a shutdown. The item retries automatically and nothing is discarded. A file that makes `ffprobe` fail or hang is `UNSUPPORTED_VIDEO` instead. |
| Rate limit/network | Wait for bounded backoff and Retry-After. Inspect the queue and health before retrying. Do not replay the original webhook to force another upload. |
| Album capacity or account storage | Resolve the provider limit, then retry FAILED. No overflow album is created; changing strategy needs explicit approval. |
| ALBUM_REVIEW | Inspect the app-created destination albums in Google. In LINE groups resolve the flag using a verified **Library API album ID**, not the browser sharing URL. Leave it blank only after confirming no album was created. Resolving re-queues every image in that group that failed with `ALBUM_REVIEW`; the UI shows how many. Never guess based solely on a title. |
| NEEDS_REVIEW | Inspect the expected app-assigned filename (line- plus internal image UUID and extension). Reconcile performs a read-only exact filename search in the mapped album. No unique match leaves the job in review; it does not upload again. Do not resubmit to bypass review. |
| Disk pressure | Free unrelated files or expand the volume. Resolve uploads, wait for synced cleanup, or explicitly discard an expired stopped original after inspecting it. Do not manually delete active staging files. Discard is irreversible and never deletes Google Photos media. |
| Cache miss | Synced images are served by resolving their Google IDs. Reconnect and Validate if remote reads fail. A manually deleted or moved remote item may no longer resolve. |
| Stale worker | Check worker logs, Redis AOF volume and PostgreSQL readiness. Restart worker; unacknowledged outbox rows replay. Never flush production Redis or drop the outbox to clear the queue. |
| Playwright fails to launch: missing shared libraries | `playwright install --with-deps` needs sudo. Without it, fetch the missing library packages into a private directory and set `LD_LIBRARY_PATH` to it for that run. Chromium has been verified this way. WebKit also needs the GTK libraries and has not been run without them, so run WebKit e2e on a host where `--with-deps` works. |
| Timestamps 7 hours ahead / first photos after enabling were missed | Older builds wrote database timestamps in Asia/Bangkok wall clock instead of UTC, so `enabledSince` and the outbox schedule ran 7 hours ahead. Run the one-off [UTC timestamp repair](#one-off-utc-timestamp-repair) before starting the build that pins the session timezone to UTC, with `api` and `worker` stopped. |

## Check the network path

These commands assume the default `WEB_PORT=8080`. If you configured a different `WEB_PORT` in `.env` (for example `8068`), substitute the port shown by `docker compose ps web` in both HTTP checks and in the proxy upstream.

1. On the app host, run `docker compose ps` and `curl -fsS http://127.0.0.1:8080/api/v1/auth/status`. Expect HTTP 200 with setup and authentication flags; no credentials are needed.
2. From the LAN proxy, run `curl -fsS http://<host-ip>:8080/api/v1/auth/status`. If only this hop fails, check the host address, port binding, routing and firewall.
3. From outside the LAN, set the shell variable `APP_URL` to the deployment’s public HTTPS origin, then run `curl -fsS "${APP_URL}/api/v1/auth/status"`. If DNS fails, check the public record. A certificate error points to proxy TLS configuration; a 502 generally points to the proxy's upstream. Do not fix these by exposing the published web port publicly or disabling secure cookies.

## First live acceptance

For the exact screens and success checks, follow [Set up your first group](OWNER_GUIDE.md#set-up-your-first-group).

Sign in over HTTPS → verify worker online → connect Google → invite bot → explicitly enable a test group → send a personal test photo. Confirm one image, its expected album, SYNCED status and private thumbnail. In a signed-out browser the content endpoint must return 401. Test the real OAuth consent and provider account quotas before relying on ongoing collection; mocks cannot verify them.

## One-off UTC timestamp repair

Applies once, to a database written by a build that ran in a Bangkok-timezone container before the session timezone was pinned to UTC. Those rows hold Bangkok wall clock (UTC+7) in `timestamp without time zone` columns. [`scripts/repair/2026-11-21-utc-timestamps.sql`](../scripts/repair/2026-11-21-utc-timestamps.sql) shifts every listed value at or after the cutover back by 7 hours. It never touches `Image.lineEventTimestamp`, `LineGroup.membershipAt` or `LineGroup.lastEventAt`. It runs in one transaction and refuses to run twice (`AuditLog` action `UTC timestamp repair`).

**Never run it after the fixed build has started.** Pre-fix Bangkok values and post-fix UTC values overlap for 7 hours and cannot be told apart.

Before the production run, the owner must rehearse on a restored copy of a recent backup (never the live database): run the script with the planned cutover and `-v apply=1`, confirm that post-cutover `Image.createdAt` was about 7 hours after `lineEventTimestamp` before and within seconds after, and that pre-cutover rows were already within seconds of `lineEventTimestamp`. If legacy rows do not look UTC, adjust the cutover or column list and record why in the script header. Then rerun it and expect the `already applied` exception.

Production run, in this order. The cutover is the time the Go backend first ran, as Bangkok wall clock.

1. Back up the database.
2. Stop the application containers: `docker compose stop api worker`. A plain `docker compose stop` also stops PostgreSQL, which the repair needs.
3. Dry run, from the directory holding your Compose file (GHCR-only operators: first download `scripts/repair/2026-11-21-utc-timestamps.sql` from the repository at the release tag that matches your images): `docker compose exec -T postgres psql -v cutover_bkk='<cutover, YYYY-MM-DD HH:MM:SS>' -U gallery -d gallery -f - < scripts/repair/2026-11-21-utc-timestamps.sql`. It prints the per-column preview, then rolls back and changes nothing. Check the counts against what you expect for that cutover. A wrong cutover otherwise commits a silent shift that only the step-1 backup can undo.
4. Apply: rerun the same command with `-v apply=1` added before `-v cutover_bkk`, as `docker compose exec -T postgres psql -v apply=1 -v cutover_bkk='<cutover>' -U gallery -d gallery -f - < scripts/repair/2026-11-21-utc-timestamps.sql`. It commits; a failure rolls everything back.
5. Deploy and start the fixed build. On startup the API migrates every timestamp column to `timestamptz` (`202611210001_timestamptz`). After that the repair refuses to run and cannot be redone safely.
