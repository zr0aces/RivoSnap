# RivoSnap

A single-owner, private photo and video inbox for authorized personal LINE groups. New group images (and optional videos when enabled in Settings) are staged locally, validated, and uploaded into one Google Photos album per LINE group. Google Photos is the permanent home for originals.

**First time using RivoSnap?** Follow the [step-by-step owner guide](docs/OWNER_GUIDE.md) to connect your accounts, collect your first photo, and manage groups and uploads.

## Overview

- **Pipeline**: LINE webhook events → transactional outbox & Redis queue → local staging & thumbnail generation → Google Photos album upload.
- **Reliability**: Channel/message deduplication, uncertain Google write reconciliation, disk-pressure guards, and no silent deletion of unuploaded originals.
- **Media support**: Validated JPEG/PNG/WebP/GIF images and optional MP4/MOV/WebM videos (`collectVideos` toggle in Settings, off by default).
- **Responsive web UI**: Desktop, tablet, and mobile-friendly workspace with **Clementine & Sky** light/dark themes ([theme preview](docs/theme-preview.html)).

## Technology stack

| Layer | Technologies |
| --- | --- |
| **Backend (`api` & `worker`)** | Go 1.26 (`cmd/rivosnap`, `internal/*`), `pgx/v5`, `go-redis/v9`, `golang.org/x/crypto`, `golang.org/x/image`, `ffmpeg` / `ffprobe` (video processing) |
| **Database & Queue** | PostgreSQL 18.7 (embedded SQL migrations, transactional outbox), Redis 7.4 (AOF persistence, `noeviction`) |
| **Frontend (`apps/web`)** | React 19, TypeScript 5.9, Vite 7, Tailwind CSS 4, React Router 7, TanStack Query 5, React Hook Form 7, Lucide icons |
| **Web & Edge** | Nginx Unprivileged 1.30 (Alpine), optional Caddy TLS edge (`--profile edge`), Docker Compose |
| **Testing & Tooling** | Go `testing`, disposable Docker integration suite, Playwright 1.65 (Chromium & WebKit), pnpm 10 via Corepack, Node 24 LTS |

## How to use

### 1. Deploy with Docker Compose

1. Install Docker with Compose. Choose a public HTTPS hostname (for example, `https://gallery.example.com`), point DNS to your reverse proxy, and provision TLS. Set `APP_URL` to that origin without a path or trailing slash.
2. Copy `.env.example` to `.env`, set `APP_URL`, `GOOGLE_REDIRECT_URI`, OAuth credentials, and a URL-safe `POSTGRES_PASSWORD`. Generate **separate** 32-byte secrets with `openssl rand -hex 32` for `SESSION_SECRET`, `TOKEN_ENCRYPTION_KEY`, and `BOOTSTRAP_SECRET`. Restrict `.env` permissions (`chmod 600 .env`).
3. Start the stack:
   ```sh
   docker compose up -d --build
   ```
   Forward `APP_URL` traffic to `http://127.0.0.1:${WEB_PORT:-8080}` (same-host proxy) or `http://<host-ip>:${WEB_PORT:-8080}` (LAN proxy). Default `WEB_PORT` is `8080`. See [Docker deployment](docs/DOCKER_DEPLOYMENT.md).

### 2. Claim & configure

1. Open `APP_URL` and claim the owner account using `BOOTSTRAP_SECRET`. In [Profile](docs/OWNER_GUIDE.md#secure-your-owner-account), add a passkey for passwordless sign-in or enable an authenticator for password sign-in (both ship in the first release after `v2026.11.7`). You can use both; neither needs an extra environment variable.
2. In **Settings**, save your LINE Messaging API credentials **before** verifying the webhook in the LINE Developers Console.
3. Connect Google Photos via OAuth.
4. Enable the LINE groups you want to collect from, optionally toggle **Collect videos**, and monitor sync jobs in **Dashboard**, **Gallery**, and **Albums**.

See [Owner Guide](docs/OWNER_GUIDE.md), [LINE setup](docs/LINE_SETUP.md), and [Google Cloud setup](docs/GOOGLE_CLOUD_SETUP.md) for detailed instructions.

## Development and tests

Start with the [plan index](docs/plans/README.md) to find or propose work. Claude Code, Codex, and Antigravity share the [AI tool workflow](AGENTS.md#ai-tool-workflow).

The [versioning policy](AGENTS.md#versioning) specifies Bangkok-calendar CalVer (`YYYY.M.N`) in root `VERSION`. Validate with `node scripts/sync-version.mjs --check`, or prepare a release with `node scripts/release.mjs` (`--help` for build/tag options).

Run from the repository root (Go 1.26+ and Node 24 LTS + pnpm 10 via Corepack):

```sh
corepack pnpm install --frozen-lockfile
corepack pnpm typecheck
corepack pnpm build
go test -v ./...
bash scripts/test-integration.sh
corepack pnpm exec playwright install --with-deps chromium webkit
corepack pnpm e2e
```

- `scripts/test-integration.sh` spins up disposable PostgreSQL and Redis containers on loopback, runs embedded SQL migrations via `go run ./cmd/rivosnap migrate`, and tests the webhook-to-gallery flow against mocked providers. The destructive tests refuse to run unless `DATABASE_URL` and `REDIS_URL` point at loopback (`internal/testguard`).
- Playwright runs gallery and responsive fixtures locally in Chromium and WebKit. To test owner login and worker health against a **disposable deployment only**, set `E2E_URL` and `E2E_SETUP_KEY`.
- For host development, export server settings pointing `DATABASE_URL` and `REDIS_URL` to dedicated instances, set `NODE_ENV=development` and `APP_URL=http://localhost:5173`, and run `corepack pnpm migrate`, `corepack pnpm api`, `corepack pnpm worker`, and `corepack pnpm exec vite apps/web` in separate terminals.

- For owner-security browser coverage, run `corepack pnpm exec playwright test e2e/profile.spec.ts --project=chromium` with `E2E_PROFILE_FIXTURE=1` and `E2E_URL` pointing at a **fresh disposable localhost deployment** started with the same `E2E_SETUP_KEY` the test receives. This test claims the fixture owner and exercises a Chromium virtual passkey, authenticator enrollment, recovery and revocation.

## Handbook

- [Owner guide](docs/OWNER_GUIDE.md)
- [Architecture and retention](docs/ARCHITECTURE.md)
- [LINE setup](docs/LINE_SETUP.md)
- [Google Cloud setup](docs/GOOGLE_CLOUD_SETUP.md) and [OAuth restrictions](docs/GOOGLE_PHOTOS_OAUTH.md)
- [Docker, proxy, backup and restore](docs/DOCKER_DEPLOYMENT.md)
- [Security and privacy](docs/SECURITY.md)
- [Troubleshooting](docs/TROUBLESHOOTING.md)
- [Theme preview](docs/theme-preview.html)

Signed-in API documentation: **/api/openapi.json**. Display timezone: `Asia/Bangkok`.
