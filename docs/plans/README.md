# RivoSnap plan index

Add a link under **Active** when a plan is created; move it to **Completed** when the work ships. Completing a plan does not bump the application version; see the [release policy](../../AGENTS.md#versioning).

## Active

- [Application-wide responsive design](2026-11-22-responsive-design.md) — Contain the mobile shell, adapt page layouts and verify all seven target viewports.

## Completed

- [Clementine & Sky Theme (Light + Dark)](completed/2026-11-22-clementine-sky-theme.md) — Adopt warm Clementine & Sky light/dark palette and friendlier geometry across `apps/web`.
- [Audit 2 Remediation (AUD2-001 to AUD2-012)](completed/2026-11-21-audit2-remediation.md) — Fix all findings from the 2026-11-21 audit, starting with the UTC session-timezone ingestion blackout.
- [Frontend design refactor](completed/2026-11-20-frontend-design-refactor.md) — Darkroom Contact Sheet & Archival Ledger UI refactor across all `apps/web` pages.
- [Video collection support](completed/2026-11-20-video-collection-support.md) — Ingest, process (`ffmpeg`), upload, and play back LINE group videos.
- [Production-compatible embedded migration runner](completed/2026-11-21-production-auto-migrations.md) — Embed ordered SQL migrations in Go binary with `_prisma_migrations` ledger continuity, advisory locking, and automatic startup execution.
- [Migrate API & Worker services to Go](completed/2026-11-21-go-backend-migration.md) — Replace Node/TypeScript API and worker runtimes with a compiled Go binary while preserving Nginx `web`, Postgres schema, encrypted tokens, and Compose contracts.
- [Refresh LINE group name & sync Google Photos album title](completed/2026-11-20-refresh-line-group-name.md) — Manual refresh button and API endpoint to sync renamed LINE group titles and mapped Google Photos album titles.
- [Audit Remediation (AUD-001 to AUD-013)](completed/2026-11-20-audit-remediation.md) — Fix all findings from the 2026-11-20 full codebase engineering audit.
- [Sidebar System Health & Settings refactor](completed/2026-11-20-sidebar-health-settings-refactor.md) — Live color-dot health strip in sidebar and Pipeline Bridge Settings layout.

## Filing rules

1. Keep active plans in this directory. Name each file with its actual creation date: `YYYY-MM-DD-<issue>-<slug>.md`. If there is no issue, omit that segment rather than inventing an ID.
2. Immediately below the title, add a **Status:** line with the date, what shipped and what remains. Put the only implementation checklist under `## Task list`.
3. If a plan needs a separate design, link it under `specs/`. Put standing backlog in `roadmap.md`; create it when there is an item to track.
4. On shipment, move the plan to `completed/` and its paired design to `completed/specs/`. Update the status and links together. Preserve the files; use `git mv` when tracked.
5. Correct inaccurate claims inline: strike through the original and give the correction alongside it. Record dated review fixes with no associated plan in `docs/development/review-fix-log.md`, creating the log on first use.
