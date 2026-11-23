# Deploy RivoSnap from GHCR

**Upgrading an existing installation?** Before running Compose, set `COMPOSE_PROJECT_NAME` in the deployment environment to the existing project name (shown by `docker compose ls`). The new default is `rivosnap`; changing the project name selects different volumes and can start an empty database. Keep the original environment and volumes.

Upgrading from a build without the UTC session fix: follow [One-off UTC timestamp repair](../TROUBLESHOOTING.md#one-off-utc-timestamp-repair) before starting the new images; `pull` + `up -d` alone migrates the columns and closes the repair window.

Copy **this entire directory** to a fresh Linux host, for example as `/opt/rivosnap`. It includes a standalone `compose.yml` tracking the `:latest` GHCR release images (or pin a specific `:<VERSION>` tag if desired), `web.conf` for Nginx, and an `env.example`. No other repository files, source checkout, or build tools are needed. The host needs Docker Engine, Compose v2, and an existing HTTPS reverse proxy.

1. Confirm the publisher provides the two GHCR package names in `compose.yml` (`rivosnap-api` and `rivosnap-worker`) for your host architecture, using the tags shown in that file. The `web` service pulls the stock `nginxinc/nginx-unprivileged` image (pinned in `compose.yml`) directly. Change the example package paths if necessary. For private packages, sign in to `ghcr.io` with a read-only package credential using `docker login --password-stdin`.
2. From the copied directory, run `install -m 600 env.example .env` **once**, before adding secrets. Set the lowercase `GHCR_OWNER` (default `zr0aces`), `IMAGE_TAG` (default `latest`, or pin a CalVer release like `2026.11.7`), `WEB_PORT` (default `8080`), and public HTTPS `APP_URL` (must start with `https://`, no trailing slash).
3. Generate the four required secrets with `openssl rand -hex 32` (one 64-character hex string per field):
   - `POSTGRES_PASSWORD`: URL-safe hex password interpolated into `DATABASE_URL`
   - `SESSION_SECRET`: at least 32 characters
   - `TOKEN_ENCRYPTION_KEY`: **must be exact 64 hex characters** (`/^[a-fA-F0-9]{64}$/`) for AES-256-GCM; keep backed up separately
   - `BOOTSTRAP_SECRET`: at least 24 characters (used once to claim the owner account)
4. Configure `GOOGLE_CLIENT_ID` and `GOOGLE_CLIENT_SECRET` if using Google Photos OAuth (`LINE_CHANNEL_ACCESS_TOKEN` and `LINE_CHANNEL_SECRET` can also be saved later in the web UI under **Settings**).
5. Forward HTTPS from the proxy to the loopback or trusted LAN listener on `WEB_PORT` (default `8080`, configured in `.env`). Do not expose that HTTP port directly to the internet. Register the Google callback at `APP_URL/api/v1/google/callback`.

Run from that directory in one shell:

```sh
set -eu
docker compose -f compose.yml config --quiet
docker compose -f compose.yml pull
docker compose -f compose.yml up -d --no-build --wait
docker compose -f compose.yml ps
```

If `rivosnap-api` fails its healthcheck on first boot, inspect `docker compose -f compose.yml logs --tail 50 api`. Any `.env` edit requires `docker compose -f compose.yml up -d` (not `docker compose restart`, which does not reload `.env`). If you change `POSTGRES_PASSWORD` after the `postgres` volume was already initialized with a different password, either restore the original `POSTGRES_PASSWORD` or remove the empty volume before retrying.

Sign in through your HTTPS URL and confirm the sidebar/Health version matches the image tag. To inspect the embedded release, run `docker compose -f compose.yml exec api cat /app/VERSION` (also available in `worker`; `web` is stock Nginx without `/app/VERSION`). No host version file or release variable is required.

Owner passkeys and authenticator MFA are enrolled in **Profile**, not in the environment file. Keep the HTTPS hostname stable for passkeys. After enrollment, do not roll back to a password-only API: it would ignore MFA.

For upgrades, back up the database, encryption key, and pending originals, and check migration compatibility. Preserve the private `.env`, project name, and named volumes. Pull the new release images, stop the worker before migrations, then start the stack. Never use `down -v` during an upgrade. When upgrading to owner MFA and passkeys, keep the existing `APP_URL` hostname and `TOKEN_ENCRYPTION_KEY`; no new environment variables are required, and existing sessions must sign in again.
