import { appendFileSync } from "node:fs";
import { join } from "node:path";
import { expect, test } from "@playwright/test";
import { longName, mockApi, normal, stress } from "./fixtures";

const viewports = [
  [320, 568],
  [375, 812],
  [430, 932],
  [768, 1024],
  [819, 1024],
  [820, 1024],
  [821, 1024],
  [1024, 768],
  [1366, 768],
  [1920, 1080],
];
const surfaces = [
  ["/?setup", ".stepItem"],
  ["/gallery?status=SYNCED", ".photo"],
  ["/jobs", ".queueRow"],
  ["/groups", ".groupgrid article"],
  ["/albums", ".albumFolio"],
  ["/settings?google_error=exchange", ".settingsLedger"],
  ["/health", ".healthCard"],
  ["/profile", ".profileSecurity"],
  ["/missing", '.workspace > a[href="/"]'],
] as const;
const cases = [
  ...surfaces.map(([route, ready]) => ({
    route,
    ready,
    auth: "owner" as const,
  })),
  ...(["login", "setup"] as const).map((auth) => ({
    route: "/",
    ready: ".entry form",
    auth,
  })),
];

for (const [dataset, data] of Object.entries({ normal, stress })) {
  for (const [width, height] of viewports) {
    for (const { route, ready, auth } of cases) {
      test(`${dataset} ${width}x${height} ${auth} ${route}`, async ({
        page,
      }, info) => {
        await page.setViewportSize({ width, height });
        await mockApi(page, data, auth);
        await page.goto(route);
        await expect(page.locator(ready).first()).toBeVisible();
        if (auth === "owner") {
          if (width > 820) {
            await expect(page.locator(".navBadge")).toHaveText("2");
            await expect(page.locator(".healthMiniRow").first()).toBeVisible();
          } else {
            await expect(page.locator("#more-nav-status")).toContainText(
              "Upload queue: 1 failed, 1 pending; System health: Needs attention",
            );
          }
        } else {
          await expect(
            page.getByRole("button", {
              exact: true, name: auth === "login" ? "Sign in" : "Create owner account",
            }),
          ).toBeVisible();
        }
        if (width < 480) {
          const smallFields = await page
            .locator('input:not([type="checkbox"]):visible, select:visible')
            .evaluateAll((fields) =>
              fields
                .filter((el) => parseFloat(getComputedStyle(el).fontSize) < 16)
                .map((el) => el.getAttribute("name") ?? el.tagName),
            );
          expect(
            smallFields,
            "phone fields stay readable without focus zoom",
          ).toEqual([]);
        }
        if (route === "/?setup") {
          const phase = page.getByRole("button", {
            name: /Accounts & credentials/,
          });
          if ((await phase.getAttribute("aria-expanded")) !== "true")
            await phase.click();
          await expect(page.locator(".stepItem code")).toBeVisible();
          await page.evaluate(() => scrollTo(0, 0));
        }
        const metrics = await page.evaluate(async () => {
          await document.fonts.ready;
          const rect = (s: string) =>
            document.querySelector(s)?.getBoundingClientRect();
          const columns = (s: string) => {
            const grid = document.querySelector(s);
            return grid
              ? getComputedStyle(grid)
                  .gridTemplateColumns.split(" ")
                  .filter((track) => parseFloat(track) > 0).length
              : null;
          };
          const bounds = (s: string) => {
            const r = rect(s);
            return r ? [r.left, r.right] : null;
          };
          const mobile = innerWidth <= 820;
          return {
            scrollWidth: document.documentElement.scrollWidth,
            workspace: rect(".workspace")?.width ?? null,
            utilities: document.querySelector(".shell")
              ? mobile
                ? { mobileNav: bounds(".mobileNav") }
                : {
                    health: bounds(".sidebarHealthHead a"),
                    theme: bounds("header .desktopOnly"),
                    signOut: bounds(".asideFooter button"),
                  }
              : null,
            galleryCols: columns(".photos"),
            healthCols: columns(".healthGrid"),
            railCols: columns(".rail"),
            filterWidths: [
              ...document.querySelectorAll(".filters select, .filters input"),
            ].map((e) => e.getBoundingClientRect().width),
            firstCardTop: rect(".photo")?.top ?? null,
            submitBottom: rect(".entry form button")?.bottom ?? null,
            mobileNavHeight: mobile
              ? (rect(".mobileNav")?.height ?? null)
              : null,
            sidebarWidth: rect("aside")?.width ?? null,
            footerTop: mobile ? null : (rect(".asideFooter")?.top ?? null),
          };
        });
        appendFileSync(
          join(info.project.outputDir, "responsive-baseline.jsonl"),
          JSON.stringify({
            browser: info.project.use.browserName ?? "chromium",
            width,
            height,
            route,
            auth,
            dataset,
            ...metrics,
          }) + "\n",
        );
        if (route.startsWith("/gallery")) {
          if (width === 820 || width === 821) {
            expect(metrics.filterWidths).toHaveLength(4);
            expect(
              Math.min(...metrics.filterWidths),
              "no density cliff",
            ).toBeGreaterThanOrEqual(140);
          }
          if (width === 1366 || width === 1920)
            expect(metrics.galleryCols).toBe(width === 1366 ? 5 : 6);
        }
        if (route === "/health" && width === 768)
          expect(metrics.healthCols).toBe(3);
        if (auth === "owner") {
          expect(metrics.utilities).not.toBeNull();
          for (const bounds of Object.values(metrics.utilities!)) {
            expect(bounds, "visible utility").not.toBeNull();
            expect(bounds![0]).toBeGreaterThanOrEqual(0);
            expect(bounds![1]).toBeGreaterThan(bounds![0]);
            expect(bounds![1]).toBeLessThanOrEqual(width);
          }
        }
        expect(
          metrics.scrollWidth,
          "document fits viewport",
        ).toBeLessThanOrEqual(width + 1);
        if (dataset === "stress" && auth === "owner") {
          for (const title of await page.locator(".workspace h2").all()) {
            expect(
              await title.evaluate((n) => n.scrollWidth <= n.clientWidth + 1),
            ).toBe(true);
          }
          let container: string;
          if (route.startsWith("/gallery")) {
            await page
              .getByRole("button", {
                name: new RegExp("Photo from " + longName),
              })
              .click();
            container = "dialog";
          } else if (route === "/albums") container = ".albumFolio";
          else if (route === "/groups") container = ".groupgrid article";
          else return;
          const owner = page.locator(container).filter({ hasText: longName });
          const title = owner.getByText(longName, { exact: true });
          await expect(title).toBeVisible();
          const outer = (await owner.boundingBox())!,
            inner = (await title.boundingBox())!;
          expect(inner.x).toBeGreaterThanOrEqual(outer.x - 1);
          expect(inner.x + inner.width).toBeLessThanOrEqual(
            outer.x + outer.width + 1,
          );
          expect(
            await title.evaluate((n) => n.scrollWidth <= n.clientWidth + 1),
          ).toBe(true);
        }
      });
    }
  }
}

test("dashboard checklist actions remain inside the expanded phase at double text size", async ({
  page,
}) => {
  await page.setViewportSize({ width: 320, height: 568 });
  await mockApi(page, normal);
  await page.goto("/?setup");
  await expect(page.locator(".railNode .railHead span")).toHaveText([
    "1 · LINE Sources",
    "2 · Local Staging",
    "3 · Google Photos",
  ]);
  await page.addStyleTag({ content: ":root {font-size:29px}" });
  for (const phase of await page.locator(".phase").all()) {
    const toggle = phase.locator(".phaseHead");
    if ((await toggle.getAttribute("aria-expanded")) !== "true")
      await toggle.click();
    await expect(toggle).toHaveAttribute("aria-expanded", "true");
    await expect(phase.locator(".stepItem").first()).toBeVisible();
    const controls = phase.locator(".stepItem a, .stepItem button");
    for (const element of await controls.all()) {
      await expect(element).toBeVisible();
      const box = (await phase.boundingBox())!,
        control = (await element.boundingBox())!;
      expect(control.x).toBeGreaterThanOrEqual(box.x);
      expect(control.x + control.width).toBeLessThanOrEqual(box.x + box.width);
      expect(control.x + control.width).toBeLessThanOrEqual(320);
    }
  }
  const warning = page.locator(".warning a");
  await expect(warning).toHaveAttribute("href", "/jobs");
  const link = (await warning.boundingBox())!;
  expect(link.x).toBeGreaterThanOrEqual(0);
  expect(link.x + link.width).toBeLessThanOrEqual(320);
});
