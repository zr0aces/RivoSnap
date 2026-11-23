import { readFileSync } from "node:fs";
import { expect, test, type Locator, type Page } from "@playwright/test";
import { box, mockApi, normal } from "./fixtures";

test.use({ hasTouch: true, viewport: { width: 320, height: 568 } });

async function expectHitTarget(target: Locator) {
  const bounds = await box(target);
  expect(bounds.width).toBeGreaterThanOrEqual(44);
  expect(bounds.height).toBeGreaterThanOrEqual(44);
  expect(
    await target.evaluate((el) => {
      const r = el.getBoundingClientRect();
      return el.contains(
        document.elementFromPoint(r.x + r.width / 2, r.y + r.height / 2),
      );
    }),
  ).toBe(true);
}

for (const [width, height] of [
  [320, 568],
  [568, 320],
]) {
  test(`bottom navigation bar targets and labels at ${width}x${height}`, async ({
    page,
  }) => {
    await page.setViewportSize({ width, height });
    await mockApi(page, normal);
    await page.goto("/gallery?status=SYNCED");

    const nav = page.getByRole("navigation", { name: "Primary" });
    await expect(nav).toBeVisible();
    await expect(page.locator(".asideTop .mobileHealth")).toBeVisible();
    await expect(nav.getByRole("link")).toHaveCount(3);
    const more = nav.getByRole("button", { name: "More" });
    await expect(more).toBeVisible();
    await expect(more).toHaveAttribute("aria-expanded", "false");

    const targets = [
      nav.getByRole("link", { name: "Home" }),
      nav.getByRole("link", { name: "Gallery" }),
      nav.getByRole("link", { name: "Albums" }),
      more,
    ];
    await expect(targets[1]).toHaveAttribute("aria-current", "page");
    await expect(targets[0]).not.toHaveAttribute("aria-current", "page");

    const widths: number[] = [];
    for (const target of targets) {
      await expectHitTarget(target);
      const b = await box(target);
      widths.push(b.width);
      expect(
        await target.evaluate((el) => {
          const outer = el.getBoundingClientRect();
          return [...el.children].every((child) => {
            const r = child.getBoundingClientRect();
            return r.left >= outer.left - 1 && r.right <= outer.right + 1;
          });
        }),
        "icon and label fit inside bottom target",
      ).toBe(true);
    }
    expect(Math.max(...widths) - Math.min(...widths)).toBeLessThanOrEqual(2);
    expect(
      await nav.evaluate((n) => n.scrollWidth <= n.clientWidth + 1),
    ).toBe(true);

    // 200% text size: labels may wrap, no horizontal overflow on bar or page.
    await page.addStyleTag({ content: ":root { font-size: 29px }" });
    expect(
      await nav.evaluate((n) => n.scrollWidth <= n.clientWidth + 1),
    ).toBe(true);
    expect(
      await page.evaluate(
        () => document.documentElement.scrollWidth <= window.innerWidth + 1,
      ),
    ).toBe(true);
  });
}

test("More sheet lifecycle, keyboard trap, focus/scroll restoration, history, and resize", async ({
  page,
}) => {
  await page.setViewportSize({ width: 320, height: 568 });
  await mockApi(page, normal);
  await page.goto("/settings");

  const nav = page.getByRole("navigation", { name: "Primary" });
  const more = nav.getByRole("button", { name: "More" });
  await expect(more).toHaveAttribute("data-active", "true");
  await expect(more).not.toHaveAttribute("aria-current", "page");

  await page.evaluate(() => window.scrollTo(0, 120));
  const initialHistoryLen = await page.evaluate(() => history.length);
  const initialScrollY = await page.evaluate(() => window.scrollY);

  await more.tap();
  const dialog = page.getByRole("dialog", { name: "More" });
  await expect(dialog).toBeVisible();
  const closeBtn = dialog.getByRole("button", { name: "Close" });
  await expect(closeBtn).toBeFocused();
  expect(await page.evaluate(() => history.length)).toBe(initialHistoryLen);

  // Verify all 5 sheet links in order and active state on Settings
  const sheetLinks = dialog.locator(".moreSheetLinks a");
  await expect(sheetLinks).toHaveCount(5);
  await expect(sheetLinks).toHaveText([
    /Upload queue/,
    /LINE groups/,
    /System health/,
    /Settings/,
    /Profile/,
  ]);
  await expect(sheetLinks.nth(3)).toHaveAttribute("aria-current", "page");

  // Tab stays inside open dialog
  for (let i = 0; i < 10; i++) {
    await page.keyboard.press("Tab");
    expect(
      await dialog.evaluate((d) => d.contains(document.activeElement)),
    ).toBe(true);
  }

  // Dark theme toggle stays inside sheet and persists reload
  const themeBtn = dialog.getByRole("button", { name: "Dark theme" });
  await themeBtn.click();
  await expect(dialog).toBeVisible();
  await expect(themeBtn).toHaveAttribute("aria-pressed", "true");
  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");

  // Escape closes and restores focus to More without changing scroll or URL
  await page.keyboard.press("Escape");
  await expect(dialog).toBeHidden();
  await expect(more).toBeFocused();
  expect(await page.evaluate(() => window.scrollY)).toBe(initialScrollY);
  await expect(page).toHaveURL(/\/settings$/);

  // Backdrop click closes without changing URL
  await more.click();
  await expect(dialog).toBeVisible();
  await dialog.evaluate((d) =>
    d.dispatchEvent(new MouseEvent("click", { bubbles: true })),
  );
  await expect(dialog).toBeHidden();
  await expect(more).toBeFocused();

  // Selecting Health closes sheet, focuses workspace main, and shows version
  await more.click();
  await dialog.getByRole("link", { name: /System health/ }).click();
  await expect(dialog).toBeHidden();
  await expect(page).toHaveURL(/\/health$/);
  await expect(page.locator("main.workspace")).toBeFocused();
  const version = readFileSync(
    new URL("../VERSION", import.meta.url),
    "utf8",
  ).trim();
  await expect(
    page.locator(".workspace").getByText(`v${version}`, { exact: true }),
  ).toBeVisible();

  // Reload persists theme
  await page.reload();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");

  // Back/Forward closes open sheet and respects router destination
  await page.getByRole("navigation", { name: "Primary" }).getByRole("button", { name: "More" }).click();
  await expect(page.getByRole("dialog", { name: "More" })).toBeVisible();
  await page.goBack();
  await expect(page.getByRole("dialog", { name: "More" })).toBeHidden();
  await expect(page).toHaveURL(/\/settings$/);

  // Unknown route clears current states
  await page.goto("/missing");
  await expect(
    page.getByRole("navigation", { name: "Primary" }).locator("[aria-current='page']"),
  ).toHaveCount(0);
  await expect(
    page.getByRole("navigation", { name: "Primary" }).getByRole("button", { name: "More" }),
  ).not.toHaveAttribute("data-active", "true");

  // Live resize 820 -> 821 while More is open closes dialog and focuses matching desktop link
  await page.goto("/health");
  await page.getByRole("navigation", { name: "Primary" }).getByRole("button", { name: "More" }).click();
  await expect(page.getByRole("dialog", { name: "More" })).toBeVisible();
  await page.setViewportSize({ width: 821, height: 768 });
  await expect(page.getByRole("dialog", { name: "More" })).toBeHidden();
  await expect(
    page.locator("aside .sidebarHealthHead a"),
  ).toBeFocused();

  // Returning to compact starts closed
  await page.setViewportSize({ width: 320, height: 568 });
  await expect(page.getByRole("dialog", { name: "More" })).toBeHidden();
});

test("More dot and sheet status reflect queue and health states", async ({
  page,
}) => {
  await page.setViewportSize({ width: 320, height: 568 });

  const setupRoutes = async (opts: {
    stats: { failed: number; pending: number };
    health?: Record<string, string | boolean>;
    healthStatus?: number;
    healthDelayMs?: number;
  }) => {
    await page.unrouteAll({ behavior: "ignoreErrors" });
    await mockApi(page, normal);
    await page.route("**/api/v1/dashboard/stats", (r) =>
      r.fulfill({
        json: {
          ...normal.stats,
          failed: opts.stats.failed,
          pending: opts.stats.pending,
        },
      }),
    );
    await page.route("**/api/v1/health", async (r) => {
      if (opts.healthDelayMs) {
        await new Promise((res) => setTimeout(res, opts.healthDelayMs));
      }
      if (opts.healthStatus && opts.healthStatus >= 400) {
        return r.fulfill({ status: opts.healthStatus, json: { error: "down" } });
      }
      return r.fulfill({
        json: opts.health ?? {
          database: "Online",
          redis: "PONG",
          worker: "Online",
          diskPressure: false,
          workerLastProgressAt: new Date().toISOString(),
        },
      });
    });
  };

  // 1. Healthy idle -> no dot
  await setupRoutes({ stats: { failed: 0, pending: 0 } });
  await page.goto("/");
  await expect(page.locator(".mobileNavDot")).toHaveCount(0);

  // 2. Pending-only -> dot + "2 pending"
  await setupRoutes({ stats: { failed: 0, pending: 2 } });
  await page.goto("/");
  await expect(page.locator(".mobileNavDot")).toHaveCount(1);
  await expect(page.locator("#more-nav-status")).toHaveText("Upload queue: 2 pending");

  // 3. Failed + pending -> "1 failed, 2 pending"
  await setupRoutes({ stats: { failed: 1, pending: 2 } });
  await page.goto("/");
  await expect(page.locator("#more-nav-status")).toContainText("1 failed, 2 pending");

  // 4. Degraded health + idle queue -> dot + "Needs attention"
  await setupRoutes({
    stats: { failed: 0, pending: 0 },
    health: { database: "Online", redis: "PONG", worker: "Heartbeat missing", diskPressure: false },
  });
  await page.goto("/");
  await expect(page.locator(".mobileNavDot")).toHaveCount(1);
  await expect(page.locator("#more-nav-status")).toHaveText("System health: Needs attention");

  // 5. Loading health while queue is known -> preserves queue text + "Checking…"
  await setupRoutes({ stats: { failed: 0, pending: 2 }, healthDelayMs: 600 });
  await page.goto("/");
  await expect(page.locator("#more-nav-status")).toContainText("Upload queue: 2 pending");
  await expect(page.locator("#more-nav-status")).toContainText("System health: Checking…");

  // 6. Failed health -> "Unavailable" + dot
  await setupRoutes({ stats: { failed: 0, pending: 0 }, healthStatus: 503 });
  await page.goto("/");
  await expect(page.locator(".mobileNavDot")).toHaveCount(1);
  await expect(page.locator("#more-nav-status")).toHaveText("System health: Unavailable");
});

test("last control on long Gallery and Settings scrolls above bottom bar", async ({
  page,
}) => {
  await page.setViewportSize({ width: 320, height: 568 });
  await mockApi(page, normal);
  const manyImages = Array.from({ length: 60 }, (_, i) => ({
    ...normal.images[0],
    id: `img-${i}`,
  }));
  await page.route("**/api/v1/images**", (r) =>
    r.fulfill({ json: { items: manyImages, total: 120, page: 1 } }),
  );
  await page.goto("/gallery");
  const nextBtn = page.getByRole("button", { name: "Next" });
  await nextBtn.scrollIntoViewIfNeeded();
  await expectHitTarget(nextBtn);
  const barBox = await box(page.getByRole("navigation", { name: "Primary" }));
  const nextBox = await box(nextBtn);
  expect(nextBox.y + nextBox.height).toBeLessThanOrEqual(barBox.y + 1);

  await page.goto("/settings");
  const saveRetention = page.getByRole("button", { name: /Save retention/i });
  await saveRetention.scrollIntoViewIfNeeded();
  await expectHitTarget(saveRetention);
  const saveBox = await box(saveRetention);
  expect(saveBox.y + saveBox.height).toBeLessThanOrEqual(barBox.y + 1);

  // Header height on short Albums page matches Dashboard header height
  await page.goto("/");
  const dashAside = await box(page.locator(".shell > aside"));
  await page.goto("/albums");
  const albumsAside = await box(page.locator(".shell > aside"));
  expect(Math.abs(albumsAside.height - dashAside.height)).toBeLessThanOrEqual(1);
});

test("logged-out states omit compact bar and issue no stats or health requests", async ({
  page,
}) => {
  await page.setViewportSize({ width: 320, height: 568 });
  const forbidden: string[] = [];
  page.on("request", (r) => {
    if (r.url().includes("/dashboard/stats") || r.url().includes("/health")) {
      forbidden.push(r.url());
    }
  });
  for (const auth of ["login", "setup"] as const) {
    await page.unrouteAll({ behavior: "ignoreErrors" });
    await mockApi(page, normal, auth);
    await page.goto("/");
    await expect(page.locator(".mobileNav")).toHaveCount(0);
  }
  expect(forbidden).toEqual([]);
});

test("desktop sidebar scrolls independently on short displays", async ({
  page,
}) => {
  await page.setViewportSize({ width: 1024, height: 425 });
  await mockApi(page, normal);
  await page.goto("/?setup");
  const sidebar = page.locator(".shell > aside");
  await expect(page.locator(".stepItem").first()).toBeVisible();
  await page.evaluate(() => window.scrollBy(0, 400));
  expect(await sidebar.evaluate((n) => n.getBoundingClientRect().top)).toBe(0);
  const signOut = sidebar.getByRole("button", { name: "Sign out" });
  await signOut.focus();
  await expect(signOut).toBeInViewport({ ratio: 1 });
});

for (const auth of ["setup", "login"] as const) {
  test(`short phone prioritizes the ${auth} form with natural input order`, async ({
    page,
  }) => {
    await mockApi(page, normal, auth);
    await page.goto("/");
    const form = page.locator(".entry form");
    await expect(form).toBeVisible();
    expect((await form.boundingBox())!.y).toBeLessThan(
      (await page.locator(".entrySide").boundingBox())!.y,
    );
    const inputs = form.locator("input");
    await expect(inputs).toHaveCount(auth === "setup" ? 3 : 2);
    const names = [
      "email",
      "password",
      ...(auth === "setup" ? ["bootstrap"] : []),
    ];
    const passkey = form.getByRole("button", { name: "Sign in with a passkey" });
    if (auth === "login" && await passkey.isEnabled()) {
      await page.keyboard.press("Tab");
      await expect(passkey).toBeFocused();
      await expectHitTarget(passkey);
    }
    for (const [index, input] of (await inputs.all()).entries()) {
      await page.keyboard.press("Tab");
      await expect(input).toBeFocused();
      await expect(input).toHaveAttribute("name", names[index]);
      expect(
        await input.evaluate((el) => parseFloat(getComputedStyle(el).fontSize)),
      ).toBeGreaterThanOrEqual(16);
    }
    const submit = form.getByRole("button", {name: auth === "login" ? "Sign in" : "Create owner account", exact:true});
    await page.keyboard.press("Tab");
    await expect(submit).toBeFocused();
    await expectHitTarget(submit);
  });
}

test("header and health statuses contain double-size text", async ({
  page,
}) => {
  await mockApi(page, normal);
  await page.goto("/health");
  await expect(page.getByText("Heartbeat missing", { exact: true }).last()).toBeVisible();
  await expect(page.locator(".workspace header .pill")).toBeVisible();
  await page.addStyleTag({ content: ":root { font-size: 29px }" });
  for (const el of await page.locator(".healthCard, .workspace header").all()) {
    expect(
      await el.evaluate((n) => {
        const outer = n.getBoundingClientRect();
        return [...n.querySelectorAll(".row > *, :scope > span")].every((e) => {
          const r = e.getBoundingClientRect();
          return (
            r.left >= outer.left &&
            r.right <= outer.right + 1 &&
            r.top >= outer.top &&
            r.bottom <= outer.bottom + 1 &&
            e.scrollWidth <= e.clientWidth + 1
          );
        });
      }),
    ).toBe(true);
  }
  expect(
    await page.locator("header").evaluate((n) => n.clientHeight),
  ).toBeGreaterThan(48);
});

test("auth error, missing route and sign-out failure offer keyboard recovery", async ({
  page,
}) => {
  await mockApi(page, normal);
  await page.route("**/auth/status", (r) => r.fulfill({ status: 503 }));
  await page.goto("/missing");
  await expect(page.getByRole("alert")).toContainText("Cannot reach");
  await page.unroute("**/auth/status");
  await page.keyboard.press("Tab");
  await expect(page.getByRole("button", { name: "Try again" })).toBeFocused();
  await page.keyboard.press("Enter");
  const back = page.getByRole("link", { name: "Return to dashboard" });
  await expect(back).toBeVisible();
  await page.getByRole("navigation", { name: "Primary" }).getByRole("button", { name: "More" }).focus();
  await back.focus();
  await page.keyboard.press("Enter");
  await expect(
    page.getByRole("heading", { name: "Dashboard", exact: true }),
  ).toBeVisible();

  let attempts = 0;
  await page.route("**/auth/logout", (r) =>
    r.fulfill({ status: ++attempts === 1 ? 500 : 200, json: {} }),
  );
  await page.getByRole("navigation", { name: "Primary" }).getByRole("button", { name: "More" }).click();
  const dialog = page.getByRole("dialog", { name: "More" });
  const signOut = dialog.getByRole("button", { name: "Sign out" });
  await signOut.focus();
  await page.keyboard.press("Enter");
  const alert = dialog
    .getByRole("alert")
    .filter({ hasText: "Sign-out could not be confirmed" });
  await expect(alert).toBeVisible();
  await page.route("**/auth/status", (r) =>
    r.fulfill({ json: { setup: false, authenticated: false } }),
  );
  await expect(signOut).toBeFocused();
  await page.keyboard.press("Enter");
  await expect(page.getByRole("button", { name: "Sign in", exact: true })).toBeVisible();
  expect(attempts).toBe(2);
});
