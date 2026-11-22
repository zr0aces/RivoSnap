# Clementine & Sky Theme (Light + Dark)

**Status:** 2026-11-22 — Shipped. Updated `apps/web/src/style.css` with Clementine & Sky light/dark tokens, secondary/sunshine accents, warmer geometry, and verified `corepack pnpm typecheck` and `corepack pnpm build`.

Adopt the **Clementine & Sky** theme (Option 3 from [theme-preview.html](../../theme-preview.html)) across `apps/web` for both Light and Dark modes, giving RivoSnap a warmer, more playful, family-friendly look while keeping Orange (`#FF6B2C` / `#FF8552`) as the primary brand color.

## Design Goals

- **Warm & Family-Friendly Light Mode**: Keep `#FFFBF7` warm background, `#FFFFFF` cards, `#FFF0E5` soft peach tints, `#F0E4DA` warm borders, and `#1E1B18` / `#6E655F` warm charcoal text.
- **Cozy Plum-Charcoal Dark Mode**: Replace `#131110` flat dark background with `#16141A` background, `#211D26` surface, `#2C2733` elevated/interactive surface, `#36303E` borders, `#FAF8FC` / `#ACA3B5` text, and softer `#FF8552` clementine primary.
- **Playful Secondary & Accent Tokens**: Introduce Sky Blue (`#0EA5E9` light / `#38BDF8` dark) and Sunshine Yellow (`#FACC15` light / `#FDE047` dark) tokens for badges, status pills, disk bar segments, and subtle decorative accents.
- **Friendly Geometry & Warm Depth**: Soften border radii on panels (`16px`), buttons (`10px`–`999px`), nav links (`10px`), and inputs (`10px`), and add subtle warm shadows (`rgba(120, 50, 20, 0.06)` in light mode).
- **Accessibility**: Preserve WCAG AA text contrast on all primary buttons (`--brand-ink: #180d06` / `#16141A`), links, muted text, and focus rings.

## Task list

- [x] Review plan against `apps/web/src/style.css`, responsive stylesheets, and Playwright e2e selectors
- [x] Update Light and Dark CSS variables in `apps/web/src/style.css` (`:root` and `:root[data-theme="dark"]`) to Clementine & Sky tokens
- [x] Add `--secondary`, `--secondary-soft`, `--secondary-ink`, `--accent-sun`, `--brand-hover`, and `--shadow-soft` tokens and apply them to buttons, pills, rail nodes, nav links, panels, inputs, and `.entrySide`
- [x] Refine border radii and hover transitions in `apps/web/src/style.css` for a friendlier, warmer feel without breaking responsive layout
- [x] Verify TypeScript and production bundle (`corepack pnpm typecheck` and `corepack pnpm build`)
- [ ] Run Playwright responsive/UI checks if dev/test environment is available, or verify no layout/selector regressions (not run; no live dev server started in this session)
- [x] Move plan to `docs/plans/completed/` and update `docs/plans/README.md`
