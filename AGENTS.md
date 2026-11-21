# RivoSnap project instructions

Keep project rules here. Claude Code imports this file through CLAUDE.md; Codex and Antigravity use it directly or through GEMINI.md. Use Go 1.26+ for the backend (`cmd/rivosnap`, `internal/*`) and Node 24 + the root pnpm workspace through Corepack for the web frontend (`apps/web`) and release scripts.

- For ingestion, uploads, retries or cleanup, read docs/ARCHITECTURE.md. Collect only new images (and videos when `collectVideos` is enabled) from enabled LINE groups. Preserve channel/message deduplication and the transactional outbox. Reconcile uncertain Google writes instead of repeating them. Never silently delete an unuploaded original.
- For authentication, OAuth or media access, read docs/SECURITY.md. Keep secrets server-side. Do not print private environment files or expanded Compose configuration.
- For deployment changes, read docs/DOCKER_DEPLOYMENT.md. Keep one Nginx web service, the existing host bindings and WEB_PORT contract. Do not remove named volumes or run tests against production.
- Check Go changes with `go test ./...` and TypeScript/frontend changes with `corepack pnpm typecheck` and `corepack pnpm build`. For pipeline changes run `bash scripts/test-integration.sh` against disposable services; for UI behavior use Playwright as described in README.md. Check Compose edits with `docker compose --profile edge config --quiet`.

## Plans

When creating, revising, or shipping an implementation plan, follow the [plan index and filing rules](docs/plans/README.md). Keep the task list inside the plan rather than maintaining a second checklist.

## AI tool workflow

- **RTK:** Prefix each shell command with `rtk`, including each command in a chain. Follow /home/san/.codex/RTK.md on the deployment host. Do not assume a client hook supplies the prefix.
- **Graphify:** For codebase questions, follow the [graph lookup rules](#graphify).
- **Caveman:** Keep replies concise while preserving technical facts, exact paths, commands and errors. Write docs, commits and PRs in normal prose. Explain security warnings and irreversible actions clearly rather than abbreviating them.

## Versioning

- Application release policy: use root `VERSION` as the sole authority for CalVer `YYYY.M.N`, where the year and month follow Asia/Bangkok and N is the release sequence within that month. A release check must fail if the source is missing or malformed; never infer a version from a package manifest.
- Check is read-only: `rtk proxy node scripts/sync-version.mjs --check` validates release metadata. Plain `rtk proxy node scripts/release.mjs` prepares the version and synchronizes and checks owned metadata; it does not install, build, commit, tag or push. The optional `--build` runs the workspace build, not a deployment.
- Use `--tag` only with explicit authorization for both the release-file commit (if changes are needed) and the annotated local tag. Pushing or publishing requires separate authorization. RivoSnap’s OpenAPI version identifies the API contract; do not bump it for a calendar release.

## Graphify

`graphify-out/` is optional, Git-ignored local tooling output. A fresh checkout does not need to generate or commit a graph. For an explicit `/graphify` request, consult the installed Graphify skill first.

- For codebase questions, if the CLI and `graphify-out/graph.json` are available, run `rtk proxy graphify query "<question>"` before the first source search or read. Include this rule in any delegated code exploration. Local graph changes alone are not a reason to skip it.
- Use `rtk proxy graphify path "<A>" "<B>"` for relationships or `rtk proxy graphify explain "<concept>"` for a symbol. The local wiki index can help with navigation; consult `graphify-out/GRAPH_REPORT.md` for broad architecture or missing context.
- If the CLI or graph is missing, the query fails or gives incomplete results, the graph itself is under investigation, or the user asks to skip it, use targeted source searches and reads. Verify relevant behavior in the source before editing.
- After code changes, refresh an existing local graph with `rtk proxy graphify update .` when the CLI is available.

<!-- agent-parity:begin -->
## Shared memory and agent state

This project uses `agent-parity` to keep memory, skills, instructions, and agent wiring portable across agents and machines; treat its managed files as repository state, not disposable generated files.

A shared `memory` MCP server persists context across sessions and agents (`memory_recent`, `memory_add`, `memory_search`, `memory_get`). Each tool's description says when to call it — follow them. Store durable context, not secrets, one-off chatter, or facts another source already enforces.

Cross-agent bootstrap: at the start of the first user turn, if the `memory` tools are unavailable, repair only the one config your own client reads — Claude Code `.mcp.json`, Cursor `.cursor/mcp.json`, Codex `.codex/config.toml`, Antigravity `.agents/mcp_config.json` — and leave the other three untouched, because a different-OS agent may share this working tree and need the other launcher. In that file, on native Windows replace an exact `.agent-parity/mcp/memory/run.sh` value with `.agent-parity/mcp/memory/run.cmd`, and on Unix-like systems make the inverse replacement. Use built-in file read/edit tools, not project scripts or a command the user must type. Preserve a missing, malformed, or custom `memory` registration. If you changed the file, tell the user that the current agent must be restarted and do not claim the memory tools are available in the current session.

When asked for agent-parity status in a conversation, run the project-local `status` command and call `memory_recent` to verify the current session; never infer tool availability from a static tool list because MCP tools may load lazily. If wiring is healthy but that call is unavailable, tell the user to restart the agent session. If wiring is missing, stale, conflicting, or invalid, offer to inspect the named configuration files because unrelated user settings may also prevent the agent from loading them.

When pushing or handing off through Git, commit and include every changed agent-parity-managed file. Never select only some managed changes or bypass the pre-push guard.
<!-- agent-parity:end -->
