# Application-wide responsive design audit and implementation plan
**Status:** 22 Nov 2026 — Audit complete; plan revised after review. Nothing shipped. Tasks 1–4 are executable now; Task 5 rewrites the page work from post-containment measurements; Tasks 6–7 remain.

## Executive summary

Fix the authenticated shell first. On 320px, 375px and 430px phones, every authenticated route laid out its workspace at 716px with ordinary data. At 320px the health, theme and sign-out cluster started at x=547. The horizontal navigation itself had no overflow: its intrinsic width expanded the page instead. Gallery appeared to fit three columns only because most of that workspace was off-screen. Remeasure every page after containing the shell; do not tune cards to the current phone baseline.

Separate shell breakpoints from component density. Increasing the viewport from 820px to 821px currently shrinks each gallery filter control from 356px to 94px. Also address long names independently of breakpoints: the Groups page overflowed even at 1920px. Keep the existing desktop workspace cap, theme and focus tokens, native controls, and media aspect ratios. No new dependencies or backend changes are required.

## Application structure and evidence

The audit at revision `c966f4e` used the existing built UI in isolated Chromium with synthetic API responses. It covered the seven authenticated routes and the login/setup states at the seven viewports below. Fixtures included queue attention, mapping review, an OAuth error, populated lists and expanded onboarding. A separate stress pass used a 150-character unbroken group name. Dimensions are CSS pixels. “Measured” means observed in this fixture, not on a physical device; “source” describes inspected behavior; “risk” needs visual or interaction testing.

| Surface | Source and ownership |
| --- | --- |
| Authenticated shell, six primary links, separate Health link, theme and account actions; owner login/setup and unknown-route fallback | `apps/web/src/main.tsx`: App, SidebarHealth, Login. Owner access is a state at the requested URL, not a separate login route. |
| Dashboard, group management, album shelf, settings ledger and health cards | `apps/web/src/workspace.tsx`: Dashboard, Groups, AlbumsView, Settings and Health |
| Dashboard checklist | `apps/web/src/onboarding.tsx`: Onboarding, shown before first sync or with `/?setup` |
| Gallery, queue, shared filters, pagination and media viewer | `apps/web/src/photos.tsx`: Library and Lightbox |
| Styling and shared controls | `apps/web/src/style.css`, `packages/ui/button.tsx`, `apps/web/index.html` |

The application uses cards and lists, not data tables. The dashboard visualization is a segmented disk bar with text values. Lightbox is a native dialog; discard confirmation and album resolution are inline. Native selects and date fields, not custom dropdowns, own their popup behavior. Provider-hosted Google consent is out of scope; its return state in Settings is in scope.

### Measured baseline

| Viewport | Result |
| --- | --- |
| 320 × 568 | All authenticated pages 716px wide. Filtered Gallery's first card started at y=561. Login/setup fit horizontally; submit buttons ended at y=591/735. |
| 375 × 812 | Same 716px workspace. Setup submit ended at y=765 before opening a keyboard. |
| 430 × 932 | Same 716px workspace; Gallery still rendered three columns against that width. |
| 768 × 1024 | Ordinary data fit. Gallery and Health each had three columns; the three-stage dashboard rail used two. Mobile Health link was 22px high. |
| 1024 × 768 | Sidebar occupied 224px, leaving 800px for the workspace. Filter fields were about 143px wide. Dashboard sidebar footer began around y=1254 with onboarding expanded. |
| 1366 × 768 | Ordinary data fit; Gallery had five columns. Long-name fixture widened Groups to 1943px and overflowed queue rows and album cards internally. |
| 1920 × 1080 | Workspace capped at 1480px and Gallery had six columns. Long-name fixture widened Groups to 2220px. |

At 820px the gallery workspace occupied 820px, with two filter columns. At 821px it occupied 597px, with four fields and Reset on one row. Neither page showed root overflow: measuring only document width misses this regression.

### Task 1 fixture baseline (Chromium, rounded CSS pixels)

| Viewport width | Workspace | Sign-out right | Gallery cols | Health cols | Rail cols | Filter min | First card top | Login submit bottom | Setup submit bottom | Mobile Health height | Sidebar width | Dashboard footer top |
|---|---|---|---|---|---|---|---|---|---|---|---|---|
| 320 | 710 | 696 | 3 | 2 | 2 | 316 | 561 | 591 | 735 | 22 | 710 | — |
| 375 | 710 | 696 | 3 | 2 | 2 | 316 | 561 | 729 | 765 | 22 | 710 | — |
| 430 | 710 | 696 | 3 | 2 | 2 | 316 | 584 | 814 | 844 | 22 | 710 | — |
| 768 | 768 | 754 | 3 | 3 | 2 | 334 | 626 | 877 | 907 | 22 | 768 | — |
| 819 | 819 | 805 | 3 | 3 | 2 | 358 | 620 | 877 | 907 | 22 | 819 | — |
| 820 | 820 | 806 | 3 | 3 | 2 | 358 | 620 | 877 | 907 | 22 | 820 | — |
| 821 | 597 | 207 | 2 | 2 | 3 | 96 | 342 | 636 | 708 | — | 224 | 1313 |
| 1024 | 800 | 207 | 3 | 3 | 3 | 143 | 345 | 508 | 569 | — | 224 | 1070 |
| 1366 | 1142 | 207 | 5 | 4 | 3 | 222 | 352 | 508 | 569 | — | 224 | 1045 |
| 1920 | 1480 | 207 | 6 | 5 | 3 | 303 | 352 | 664 | 725 | — | 224 | 1045 |

## Prioritized findings and page treatment

Rows marked † rest wholly or partly on phone measurements taken while the shell was 716px wide. Treat their phone-width treatment as provisional until Task 5 remeasures them; desktop and long-name evidence is unaffected.

| Priority / surface | Evidence and affected widths | Recommendation and expected result |
| --- | --- | --- |
| Critical — shared shell | Measured on all three phones. The one-column grid at `style.css:1235` uses an automatic track minimum; the no-wrap nav determines the sidebar width. | Use a shrinkable track and zero minimum inline size on grid children. Bound navigation within the track. The page, including its controls, should fit without hiding root overflow. |
| High — navigation and utilities † | Measured: off-screen phone utilities, small mobile Health target and desktop footer below the fold on long pages. Source: compact health uses colored dots with hover titles. | Use a labeled disclosure on phones with all destinations, textual health, account actions and version. Preserve desktop sidebar access on long pages; support short-height scrolling. |
| High — component density | Measured at 820/821px. One media query changes shell, rail, filters, queue, login and ledger together. | Choose component layouts by available content width. A newly visible sidebar must not force narrow components into their densest layout. |
| High — touch controls and forms | Measured: editable login text was 12.18px, filters 36–37px tall and mobile icon-button row about 30px. iOS focus zoom is a risk, not a verified result. | Increase mobile field text and hit areas through shared styles; preserve visible labels and desktop density. Verify native keyboard, date and select behavior on devices. |
| High — Dashboard and onboarding † | Source: two rail columns below 820px; counters, status and links share each card. Step text/webhook copy sits next to a no-wrap CTA inside a clipping phase container. Risk increases once the phone width is corrected. | Stack stages on phones; use three columns when counters and labels fit. Stack constrained step text, copy and CTA. Preserve warning links, stage order and textual disk metrics. |
| High — Gallery † | Measured: first filtered result at y=561 on phones. Source: dates, group/status and conditional Reset precede results; video and status pills share card corners. | Provide a compact Filters disclosure with active summary and Reset. Preserve URL filters, pagination and labels. Start with one readable card at 320px; add columns only when captions, timestamps and badges fit. |
| High — image/video viewer | Measured: ordinary image dialog fit, but long headings overflowed at 320, 768 and 1024. Source: inner control groups do not wrap; media mixes a 74vh cap with a 100dvh dialog. | Reserve a control row for navigation and Close; wrap the title separately. Fit contained media to remaining height and safe areas. Keep Escape, focus return, inline playback and native video controls. |
| High — upload queue | Measured: long names widened the document to 1036px at 768 and 1339px at 1024. The wide layout reserves an auto-sized action column. | Pair thumbnail with shrinkable summary; place error and actions below on narrow cards. Confirm discard, Cancel and Reconcile must remain distinguishable without sideways scrolling. |
| High — LINE groups | Measured: long unbroken headings expanded the document at phone through desktop widths. Cards also contain ID/copy, mapping acknowledgment and actions. | Wrap names and identifiers inside the card; stack copy, resolution and action groups as needed. Keep the entire checkbox label actionable and the full identity readable. |
| High — Settings | Source: 220px ledger label column above 820px; arrows wrap independently of service nodes. Rule descriptions and controls compete for width; feedback can be far from the action. | Stack pipeline steps and ledger labels in narrow containers. Keep units beside inputs, place feedback near the affected section and separate Disconnect from Validate/Reconnect. Verify connected, error and credential-edit states. |
| Medium — owner login/setup | Measured at 320×568: promotional block precedes both forms; submit is below the first viewport. Vertical scroll itself is not a defect. | Prioritize the form on short phones; shorten the introductory block. Keep setup instructions, password-manager semantics and errors. Focused fields and submit must remain reachable with the keyboard open. |
| Medium — Albums | Measured: titles clipped inside cards even at desktop widths. Source: filter controls share one inline-flex group; high counts and enlarged text need validation. | Wrap headings and metadata; let filters reflow. Use pressed filter buttons or complete tab keyboard semantics. Preserve covers, blank states and links to filtered Gallery. |
| Medium — Health † | Measured: two columns at 320px on the oversized shell. Risk: fixed 220px track minimum, timestamps and degraded status strings after containment. | Cap track minimum to container width, allow a single column, and stack value beneath label when needed. Preserve textual as well as color status. |
| Medium — typography and header | Source: fixed 48px workspace header, 14.5px base and some 0.68–0.78rem labels. Long status text and enlarged fonts require testing. | Allow header height to grow and use shared type/spacing scales; wrap status and timezone rather than clipping them. |
| Low — empty, fallback and failure states | Source: bare auth loading/error surfaces and inline alerts contrast with panel-based empty states. Short-screen recovery and announcements need testing. | Use readable narrow containers and reachable recovery actions; retain live announcements. Check unknown route, filtered-empty, OAuth denial and logout failure. |

## Responsive design standards

- Start with a single shrinkable column, fluid gutters and a capped reading width. Retain the 1480px desktop workspace cap and narrower settings ledger. Use Grid for tracks, Flexbox for actions; apply minimum inline size and overflow wrapping at the element that owns the content. Never hide document overflow as a substitute for reflow.
- Use under 480px, 480–767px, 768–1023px and 1024px upward as validation bands, not mandatory breakpoints. Switch the shell independently. Prefer intrinsic grids (`repeat(auto-fit, minmax(min(<n>px, 100%), 1fr))`) over viewport breakpoints for the rail, filter bar and health cards; use container queries only where an intrinsic grid cannot express the layout, such as the ledger and queue rows. Keep horizontal navigation wherever all destinations remain reachable.
- Keep the existing scrollable navigation unless Task 5 shows destinations or utilities are still unreachable once the shell is contained. Only then add an in-flow phone menu with current destination, expanded state and focus handling, closing on navigation. Avoid a modal drawer; if one becomes necessary, add Escape, focus containment, background inertness and focus return.
- Aim for 44×44px primary touch targets and at least 16px editable text on phones. Keep labels above fields, DOM order consistent with reading order and destructive actions separate. Retain visible focus, both themes and reduced-motion behavior.
- Keep preview images cropped, originals contained and storage values available as text. Use semantic spacing/type scales, readable secondary text and targeted wrapping for identifiers. Do not introduce a table, chart library or custom picker to solve an unrelated sizing problem.

## Task list

Each task ships, and is reviewed, on its own. Run `corepack pnpm typecheck`, `corepack pnpm build` and `corepack pnpm e2e` after every task. The Playwright browsers need system libraries that this host currently lacks (`libatk-1.0.so.0`). Either install them as the README describes (`corepack pnpm exec playwright install --with-deps chromium`), or start `corepack pnpm exec vite apps/web --host 127.0.0.1 --port 4173` and run the suite inside `mcr.microsoft.com/playwright:v1.65.1-noble` with `--network host` and `E2E_URL=http://127.0.0.1:4173`. The container image version must match the installed `@playwright/test`. Tasks 1–4 are fully specified. Task 5 replaces Tasks 6–7 with surface-by-surface tasks written from the remeasured evidence.

- [x] **Task 1: Commit the responsive harness and baseline (no CSS changes).**
  - Create `e2e/fixtures.ts`. Export `mockApi(page, data)`, which installs the `page.route("**/api/v1/**", …)` handler used in `e2e/gallery.spec.ts`. Also export two datasets:
    - `normal`, with an owner session, populated groups, images (portrait, landscape, video), queue items, albums, degraded health and a connected Google account;
    - `stress`, which is `normal` plus a 150-character unbroken group name, a Thai group name, a 64-character LINE group ID, a 300-character queue error and counts above 10,000.
  - Move the existing fixture objects in `gallery.spec.ts` to `fixtures.ts` without changing that spec's assertions.
  - Create `e2e/responsive.spec.ts`. Until Task 4, it uses `normal` data only. With `stress` data, long names still overflow at desktop widths, which Task 4 fixes and asserts. Loop over:
    - the viewports 320×568, 375×812, 430×932, 768×1024, 819×1024, 820×1024, 821×1024, 1024×768, 1366×768 and 1920×1080;
    - the routes `/`, `/gallery`, `/jobs`, `/groups`, `/albums`, `/settings`, `/health` and `/missing`, plus the logged-out login and setup states.
  - Once the expected page and fixture content have rendered, write one JSON line per combination to `test-results/responsive-baseline.jsonl`. Key it by viewport, route (including query), dataset and auth state. Record document scroll width, workspace width and the horizontal bounds of each visible shell utility. For login/setup, record shell metrics as `null`; assert the corresponding form is visible instead.
  - From the same initial scroll position, record the surface metrics used in the baseline: column counts for Gallery, Health and the dashboard rail; filter field widths and first-card top for a fixed filtered Gallery with results; login/setup submit bottom; mobile Health link height; sidebar width and footer top with onboarding expanded. Record Task 1's Chromium results in the plan before changing CSS; do not rely on `test-results/` surviving a later run.
  - Write each JSON line before asserting, so failing cases are still recorded.
  - Assertion `document fits viewport`: `scrollWidth <= innerWidth + 1`. Mark it `test.fail()` only for the authenticated routes at 320, 375 and 430px. Login and setup already fit at those widths, and Playwright reports an expected failure that passes as an error. This keeps the suite green while the baseline records the known defect.
  - Exit: the spec passes locally. At every phone viewport, the JSONL shows the authenticated `.workspace` wider than the viewport and the sign-out control's right edge beyond it. Exact widths depend on the fixture: the audit measured 716px, and the review probe measured 683px.

- [ ] **Task 2: Contain the shell (Critical).**
  - In `apps/web/src/style.css`, inside `@media (max-width: 820px)`, change `.shell, .entry` to `grid-template-columns: minmax(0, 1fr)`.
  - Add `min-width: 0` to `.shell > *` and `.entry > *`.
  - Add `max-width: 100%` to `nav` so the existing `overflow-x: auto` scrolls inside the track.
  - Do not add `overflow-x: hidden` to `html`, `body`, `.shell` or `.workspace`.
  - In `responsive.spec.ts`, remove `test.fail()` from the phone viewports. Add assertion `utilities on screen`: on authenticated routes only, at every viewport, the visible instance of each of these controls has a bounding box within `[0, innerWidth]` horizontally:
    - `getByRole("button", { name: "Dark theme" })`;
    - `getByRole("button", { name: "Sign out" })`;
    - the health link: `.mobileHealth` at 820px and below, and the `System health` link above 820px.

    The desktop sign-out sits in `.asideFooter`, below the fold on long pages. That vertical problem belongs to Task 6.
  - Exit: document width passes in every state at all ten viewports with `normal` data. Utilities fit on authenticated routes; login and setup each show their expected form.
  - Pre-verified by the review probe, which injected these exact rules into `/gallery`. Workspace width:

    | Viewport | Before | After |
    | --- | --- | --- |
    | 320px | 683px | 320px |
    | 375px | 683px | 375px |
    | 430px | 683px | 430px |

    The rightmost utility moved from x=669 to x=306 at 320px. Widths of 768px and above were unchanged.

- [ ] **Task 3: Make the rail, filters and health grids intrinsic (fixes the 820/821px discontinuity).**
  - In `style.css`, change `.rail` (currently `repeat(3, minmax(0, 1fr))`) and `.healthGrid` (currently `minmax(220px, 1fr)`) to `repeat(auto-fit, minmax(min(<n>px, 100%), 1fr))`.
  - Change `.filters` (currently `repeat(4, minmax(0, 1fr)) auto`) the same way, with Reset placed in its own grid cell.
  - Use these values for `<n>`:
    - `.filters`: 160px (probe-verified);
    - `.healthGrid`: 220px, which keeps the current desktop track and adds `min(…, 100%)`;
    - `.rail`: the narrowest width at which a stage card's counters and labels fit without truncation at 14.5px base text. Record the chosen value here.
  - Delete the `.rail, .filters` rule from the 820px media query.
  - Add assertion `no density cliff` to `responsive.spec.ts`: at 820 and 821px on `/gallery`, every filter control is at least 140px wide.
  - Pre-verified by the review probe, using `minmax(min(160px, 100%), 1fr)` on `.filters`. Filter control widths:

    | Viewport | Before | After |
    | --- | --- | --- |
    | 820px | 358px | 173px |
    | 821px | 114px | 161px |
    | 1366px | 241px | 244px |

    The trade-off: at 320px and 375px each field takes a full row. The Task 6 filter disclosure must absorb the extra height.
  - Exit: that assertion and the Task 2 assertions pass. The desktop column counts at 1366px and 1920px match the baseline: Gallery 5 and 6 columns, Health 3 columns at 768px.

- [ ] **Task 4: Contain long content (fixes overflow at desktop widths).**
  - In `style.css`, add `min-width: 0` and `overflow-wrap: anywhere` at the elements that own each piece of long content:
    - group and album names: the `<h2>{name(g)}</h2>` in `workspace.tsx:291` (Albums, inside `.albumFolioBody`) and `:357` (Groups, inside `article.panel`);
    - LINE IDs: the `.mono` spans in the group cards;
    - queue file names and errors: the summary and error children of `.queueRow` in `photos.tsx:261`;
    - the Lightbox title: the `<b>` group name in `.viewerHead`.
  - Make `.queueRow`'s action column `minmax(0, auto)` or wrap it below the summary.
  - Do not truncate the full group identity.
  - Add assertion `stress data fits`: with `stress` data, `scrollWidth <= innerWidth + 1` on every route at every viewport, and no `.workspace h2` and no `.viewerHead b` has `scrollWidth > clientWidth`. Also compare the long title’s rendered left and right edges with its owning Groups card, Albums card or dialog (1px tolerance); a heading can fit its own box yet extend outside a clipped card. In each case assert the full stress name is present and visible; open the Gallery card belonging to that group for the Lightbox check.
  - Exit: that assertion passes, including at 1366px and 1920px, where Groups previously widened to 1943px and 2220px.

- [ ] **Task 5: Remeasure, then rewrite Tasks 6–7 (checkpoint, no CSS changes).**
  - Rerun the Task 1 measurements in Chromium with identical fixtures, filters, onboarding state, viewports and scroll position. Preserve the original audit table and Task 1 readings; add Task 5 values alongside the matching Task 1 rows. Report Task 4 stress results separately from the normal-data comparison.
  - For every † finding, decide whether it still applies, and strike through or correct the row as the plan filing rules require.
  - Decide from evidence whether the phone navigation still needs a disclosure menu. Keep the scrollable navigation if every destination and utility is reachable by touch and keyboard.
  - Rewrite Tasks 6–7 as one task per surface. Each task names its selectors and adds an assertion to `responsive.spec.ts`, in the style of Tasks 2–4.
  - Exit: the plan carries the before/after comparison and concrete page tasks. The user approves the rewritten tasks before Task 6 starts.

- [ ] **Task 6: Page and interaction work (written in Task 5).** Placeholder scope from the current findings:
  - touch targets and 16px editable text on phones;
  - order of the login and setup blocks;
  - Gallery filter disclosure;
  - Lightbox control row and media height;
  - queue, Groups and Settings stacking;
  - Albums filter semantics;
  - header height and type scale.

- [ ] **Task 7: Release gate.**
  - Add a Playwright `webkit` project next to the default Chromium project in `playwright.config.ts`.
  - Extend the viewer test in `gallery.spec.ts`: focus the triggering Gallery card, press Enter, verify the dialog opens, press Escape, verify dismissal and assert that the same card is focused without explicitly refocusing it. Run both specs on both projects.
  - Exit: every automated proof in the next section passes on Chromium and WebKit. The manual checklist is recorded, with any step not performed marked as not performed.

## Responsive testing and release criteria

**Automated (required, Chromium and WebKit):** `e2e/responsive.spec.ts` with `normal` and `stress` data at all ten Task 1 viewports on every route and the login and setup states. It proves:
- `document fits viewport`: `scrollWidth <= innerWidth + 1`;
- `utilities on screen` on authenticated routes; expected form visible in login/setup states;
- `no density cliff`;
- `stress data fits`;
- the per-surface assertions added by Task 6.

~~Lightbox Escape and focus restoration are already proved in `gallery.spec.ts`.~~ The existing test checks dismissal only; the release gate requires Task 7’s keyboard activation and focus-return assertions in both browsers. It runs alongside `corepack pnpm typecheck` and `corepack pnpm build`. Playwright does not run in CI yet, so run it locally before each merge and record the result in the PR.

**Manual checklist (recorded, not required):**

| Area | Check |
| --- | --- |
| Content and states | Thai names; no groups; no results; disabled or paused groups; mapping review; expired and uncertain jobs. |
| Navigation and input | Focus order; current destination; native date and select pickers; password-manager autofill; setup with the keyboard open; destructive confirmation with mocked writes. |
| Media | Panorama; missing thumbnail or dimensions; letterboxing; previous and next at the boundaries; native video controls. |
| Accessibility and devices | Both themes; reduced motion; 200% text; keyboard-only use; a screen-reader pass. Where available: phone landscape, split screen, Firefox, and a physical iOS or Android device for notches, rotation and the virtual keyboard. |

## Risks and boundaries

The shell currently conceals page-level defects by allocating excess width. Task 5 repeats the measurements after containment. Do not treat the shell fix alone as completion, and do not implement a † treatment without its remeasured evidence. Desktop pages with ordinary data generally fit, so preserve working layouts and target the demonstrated failures. Insets, virtual keyboards, native popup geometry, actual video playback and screen-reader behavior remain unverified by the original Chromium audit. Use fixture data only; no deployment, production writes, schema changes or provider modifications are part of this plan.
