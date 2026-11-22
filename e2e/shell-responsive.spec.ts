import { readFileSync } from "node:fs";
import { expect, test, type Locator } from "@playwright/test";
import { mockApi, normal } from "./fixtures";

test.use({ hasTouch: true, viewport: { width: 320, height: 568 } });

async function expectHitTarget(target: Locator) {
  const bounds = (await target.boundingBox())!;
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
  test(`navigation targets across the scroll strip at ${width}x${height}`, async ({
    page,
  }) => {
    await page.setViewportSize({ width, height });
    await mockApi(page, normal);
    await page.goto("/health");
    const version = readFileSync(
      new URL("../VERSION", import.meta.url),
      "utf8",
    ).trim();
    await expect(
      page.locator(".workspace").getByText(`v${version}`, { exact: true }),
    ).toBeVisible();
    const utilities = page.locator(".mobileActions").locator("a, button");
    const links = page.getByRole("navigation").getByRole("link");
    await expect(links).toHaveCount(6);
    if (width === 320) {
      expect(
        await page
          .getByRole("navigation")
          .evaluate((n) => n.scrollWidth > n.clientWidth),
      ).toBe(true);
    }
    for (const link of await links.all()) {
      expect(
        await link.evaluate((n) => {
          const outer = n.getBoundingClientRect();
          return [...n.children].every((child) => {
            const r = child.getBoundingClientRect();
            return r.left >= outer.left && r.right <= outer.right + 1;
          });
        }),
        "icon, label and badge fit the link",
      ).toBe(true);
    }
    for (const target of [...(await utilities.all()), ...(await links.all())]) {
      await page.keyboard.press("Tab");
      await expect(target).toBeFocused();
      await expectHitTarget(target);
    }
    await links.last().tap();
    await expect(
      page.getByRole("heading", { name: "Settings", exact: true }),
    ).toBeVisible();
    await expect(links.last()).toHaveAttribute("aria-current", "page");
  });
}

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
    for (const [index, input] of (await inputs.all()).entries()) {
      await page.keyboard.press("Tab");
      await expect(input).toBeFocused();
      await expect(input).toHaveAttribute("name", names[index]);
      expect(
        await input.evaluate((el) => parseFloat(getComputedStyle(el).fontSize)),
      ).toBeGreaterThanOrEqual(16);
    }
    const submit = form.getByRole("button");
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
  await expect(page.getByText("Offline", { exact: true }).last()).toBeVisible();
  await expect(page.locator(".workspace header .pill")).toBeVisible();
  await page.addStyleTag({ content: ":root { font-size: 29px }" });
  for (const box of await page
    .locator(".healthCard, .workspace header")
    .all()) {
    expect(
      await box.evaluate((n) => {
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
  await page.getByRole("navigation").getByRole("link").last().focus();
  await page.keyboard.press("Tab");
  await expect(back).toBeFocused();
  await page.keyboard.press("Enter");
  await expect(
    page.getByRole("heading", { name: "Dashboard", exact: true }),
  ).toBeVisible();
  let attempts = 0;
  await page.route("**/auth/logout", (r) =>
    r.fulfill({ status: ++attempts === 1 ? 500 : 200, json: {} }),
  );
  const signOut = page.getByRole("button", { name: "Sign out" });
  await signOut.focus();
  await page.keyboard.press("Enter");
  const alert = page
    .getByRole("alert")
    .filter({ hasText: "Sign-out could not be confirmed" });
  await expect(alert).toBeInViewport();
  await page.route("**/auth/status", (r) =>
    r.fulfill({ json: { setup: false, authenticated: false } }),
  );
  await expect(signOut).toBeFocused();
  await page.keyboard.press("Enter");
  await expect(page.getByRole("button", { name: "Sign in" })).toBeVisible();
  expect(attempts).toBe(2);
});
