# Compose deployment

**Upgrading an existing installation?** Before running Compose, set `COMPOSE_PROJECT_NAME` in the deployment environment to the existing project name (shown by `docker compose ls`). The new default is `rivosnap`; changing the project name selects different volumes and can start an empty database. Keep the original environment and volumes.

Publishing a GitHub Release tagged `v<root VERSION>` triggers the release workflow. It builds Linux/amd64 images for the API (with embedded React assets) and worker services and pushes them to `ghcr.io/<owner>/rivosnap-{api,worker}:<VERSION>` and `:latest`. The `web` service runs the upstream `nginxinc/nginx-unprivileged` image (pinned in `docker-compose.yml`) directly with no custom build. Draft releases and tag pushes alone do not publish images.

To run published GHCR images on another server without a local build, see the [image deployment example](example-deploy/README.md).

Use a supported Linux Docker Engine and Compose. Reserve space for database, Redis AOF, pending originals and disposable thumbnails. Restrict Docker access to trusted administrators. Keep the deployment environment and separately backed-up encryption key outside version control.

The `web` service in `docker-compose.yml` uses the stock `nginxinc/nginx-unprivileged` image (pinned in `docker-compose.yml`), mounts `infra/docker/web.conf`, and proxies all traffic to `api:3000` (which serves both `/api/*` and the embedded React SPA). No custom `web` image build or override file is required. Start without the edge profile:

```sh
docker compose up -d --build
docker compose ps
docker compose logs --tail=100 -f api worker
```

Only the web service is published on `WEB_PORT` from `.env` (default: `8080`, bound to port only: `"${WEB_PORT:-8080}:8080"`). For example, set `WEB_PORT=8068` in `.env`, run `docker compose up -d --no-deps web`, and point your proxy upstream to port 8068. Check the published ports with `docker compose ps web`. The container still listens on 8080; APP_URL, the LINE webhook and the Google callback do not change. PostgreSQL and Redis have no published ports. Bundled Caddy is optional for a standalone installation; do not enable its edge profile alongside the existing reverse proxy.

**Same-host proxy on loopback:** set `WEB_PORT=127.0.0.1:8080` in `.env`. The unchanged `"${WEB_PORT:-8080}:8080"` mapping then publishes Nginx on loopback only, and the proxy upstream is `127.0.0.1:8080`, not `127.0.0.1:${WEB_PORT}`. A proxy in another container cannot reach loopback; use a LAN address there. Docker-published ports bypass ufw and firewalld rules, so a port published on all interfaces stays reachable even when the host firewall blocks it.

**Existing Nginx/Caddy:** point DNS for the hostname in `APP_URL` to your proxy, terminate TLS there, and forward the entire HTTPS origin to **<host-ip>:${WEB_PORT:-8080}** from a LAN proxy or **127.0.0.1:${WEB_PORT:-8080}** from the same host. Sign in through the canonical HTTPS origin; the direct HTTP listener is for the proxy, not for submitting credentials. Preserve Host and the raw webhook bytes. The bundled web proxy takes the client address from `X-Forwarded-For` only when the connection comes from a private or loopback address, and passes the result to the API as `X-Real-IP`; the API honours `X-Real-IP` only from a private or loopback peer. An external Nginx must send `proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;` (Caddy sends it by default). Without it, all non-webhook visitors share the proxy host's rate-limit bucket. Before deploying, check that the production external proxy sends `X-Forwarded-For`: requests from two different client addresses must land in separate rate-limit buckets (for example, exhaust the login limit from one address and confirm a second address can still sign in). Disable query-string logging for OAuth callback routes on every proxy. Keep credentials in the server environment. For Nginx on another LAN host, use `proxy_pass http://<host-ip>:8080;` (or your configured `WEB_PORT`) and `proxy_set_header Host $host;`. In a proxy container, loopback refers to that container, not the Docker host. See [connectivity checks](TROUBLESHOOTING.md#check-the-network-path).

A fresh Go installation applies the same checked-in SQL and records the same migration history as Prisma. Both `api` and `worker` apply pending migrations at startup under advisory lock `72707369`; `docker compose run --rm api /app/rivosnap migrate` runs the same step on demand. An unfinished or mismatched migration stops startup; investigate it on a restored copy rather than deleting migration records or volumes. An untracked pre-release schema requires a verified manual baseline before cutover.

## Operations

**Upgrading to owner MFA and passkeys (from the first release after `2026.11.7`):** `2026.11.7` and `latest` published before that release are password-only; `VERSION` is still `2026.11.7` until the next CalVer release. Keep the existing `APP_URL` hostname and `TOKEN_ENCRYPTION_KEY`; no new environment variables are required. Existing sessions must sign in again after this upgrade. Replace all password-only API instances before owners enroll. Once MFA is enrolled, never pin `2026.11.7` or an older release: the migration runner refuses to start an older binary against this database (`database has migrations unknown to this binary`), so the API does not start and a rollback is not possible; fix forward instead. See [owner authentication changes](SECURITY.md#owner-authentication-changes).

Upgrading from a build without the UTC session fix: follow [One-off UTC timestamp repair](TROUBLESHOOTING.md#one-off-utc-timestamp-repair) before starting the new images; `pull` + `up -d` alone migrates the columns and closes the repair window.

```sh
# Stop without removing volumes
docker compose stop

# Manual migration (API also migrates before listening)
docker compose run --rm api /app/rivosnap migrate

# Update after taking a backup and checking release compatibility
docker compose stop worker
docker compose pull postgres redis
docker compose build --pull
docker compose up -d
docker compose ps
```

### Updating base images

Every base image is pinned to an exact patch tag (`Dockerfile.api`, `Dockerfile.worker`, `infra/caddy/Dockerfile`, `docker-compose.yml`, `docs/example-deploy/compose.yml`), so a rebuild never silently picks up a different image. To update, look the new tag up in the registry, change the tag in every file that uses that image, and run `docker compose --profile edge config --quiet` and a local `docker build` of both Go images. Move the Go patch version in `.github/workflows/ci.yml` (`go-version`) and in the `golang:` tag of both Dockerfiles together. Keep PostgreSQL on major 18 unless you have a tested upgrade path for the data volume.

Do not use down -v, erase cache mounts while uploads are active, or scale workers independently during migrations. Defaults use /data/staging and /data/thumbnails; if changing these paths also change mounts, ownership and server environment together. Containers run without root. Check the dashboard for cache usage, free space, retention warnings, oldest pending image and worker heartbeat.

The worker has `stop_grace_period: 110s`. A shutdown waits up to 90 seconds for an album creation already in flight (the normal Google request timeout) and up to 10 seconds to record its outcome, so Compose does not kill the worker mid-create. SIGTERM never cancels an album creation. An upload cut short by a shutdown is recorded as INTERRUPTED and resumes on the next start.

## Metadata backup and restore

```sh
umask 077
docker compose exec -T postgres pg_dump -U gallery -d gallery -Fc > gallery.dump
# Inspect and restore into a NEW, empty recovery database, never overwrite production
docker compose exec -T postgres pg_restore --list < gallery.dump
docker compose exec -T postgres createdb -U gallery gallery_recovery
docker compose exec -T postgres pg_restore -U gallery -d gallery_recovery --exit-on-error < gallery.dump
```

A complete backup has three parts, kept in separate secured storage (not on the same disk or volume as the dump):

1. The database dump above.
2. The `staging` volume, which holds originals not yet uploaded. Stop the worker so no file is written during the copy, then archive it with the pinned Alpine image used by this repository:

   ```sh
   umask 077
   docker compose stop worker
   docker run --rm -v rivosnap_staging:/s -v "$PWD":/b alpine:3.21.10 sh -c 'umask 077 && tar czf /b/staging.tgz -C /s .'
   sudo chown "$(id -u):$(id -g)" staging.tgz
   docker compose start worker
   ```

   The volume name is `<project>_staging`; the project is `rivosnap` unless `COMPOSE_PROJECT_NAME` changed it. Confirm the name with `docker volume ls`.
3. The application key: with `umask 077`, copy `.env`, or at least `TOKEN_ENCRYPTION_KEY`. A restore without the original key makes TOTP seeds, the Google refresh token and saved LINE credentials unreadable.

Thumbnails are not backed up; the API regenerates them from the originals.

A database backup contains **metadata, password hashes, passkey public credentials, recovery-code hashes, encrypted authenticator seeds and provider credentials, outbox and audit state, not original image backups**. Keep its separate application encryption key secure. Redis is transport, not the recovery authority. Thumbnails are never backups.

For a coordinated recovery point stop API and worker before the dump and retain existing staging volumes. Do not point a live worker at a restored database until the operator has reconciled every remote write since the snapshot, especially album mappings and nonterminal images. Restoring older state alone can cause duplicates or lose track of newer Google media. Test restoration on an isolated host, then update DATABASE_URL to the verified database and restart; retain the former database for rollback. A database restore also restores the sign-in methods and recovery-code hashes present at backup time. Review those methods and replace recovery codes before reopening access. For application rollback, the prior API must enforce all enrolled methods; schema compatibility alone is insufficient. If it does not, keep the API offline and fix forward.
