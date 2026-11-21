import { appendFileSync } from "node:fs";
import { join } from "node:path";
import { expect, test } from "@playwright/test";
import { mockApi, normal, type Auth } from "./fixtures";

const viewports = [[320,568],[375,812],[430,932],[768,1024],[819,1024],[820,1024],[821,1024],[1024,768],[1366,768],[1920,1080]];
const surfaces = [
  ["/?setup", ".stepItem"], ["/gallery?status=SYNCED", ".photo"], ["/jobs", ".queueRow"],
  ["/groups", ".groupgrid article"], ["/albums", ".albumFolio"], ["/settings?google_error=exchange", ".settingsLedger"],
  ["/health", ".healthCard"], ["/missing", 'a[href="/"]'],
] as const;
const cases = [
  ...surfaces.map(([route, ready]) => ({route, ready, auth: "owner" as Auth})),
  ...(["login", "setup"] as const).map(auth => ({route: "/", ready: ".entry form", auth})),
];

for (const [width, height] of viewports) {
  for (const { route, ready, auth } of cases) {
    test(`normal ${width}x${height} ${auth} ${route}`, async ({ page }, info) => {
      await page.setViewportSize({ width, height });
      await mockApi(page, normal, auth);
      await page.goto(route);
      await expect(page.locator(ready).first()).toBeVisible();
      if (auth === "owner") {
        await expect(page.locator(".navBadge")).toHaveText("2");
        await expect(page.locator(".mobileHealth [title]").first()).toBeAttached();
      } else {
        await expect(page.getByRole("button", {name: auth === "login" ? "Sign in" : "Create owner account"})).toBeVisible();
      }
      const metrics = await page.evaluate(async () => {
        await document.fonts.ready;
        const rect = (s: string) => document.querySelector(s)?.getBoundingClientRect();
        const columns = (s: string) => {
          const grid = document.querySelector(s);
          return grid ? getComputedStyle(grid).gridTemplateColumns.split(" ").length : null;
        };
        const bounds = (s: string) => {
          const r = rect(s);
          return r ? [r.left, r.right] : null;
        };
        const mobile = innerWidth <= 820;
        return {
          scrollWidth: document.documentElement.scrollWidth, workspace: rect(".workspace")?.width ?? null,
          utilities: document.querySelector(".shell") ? {
            health: bounds(mobile ? ".mobileHealth" : ".sidebarHealthHead a"),
            theme: bounds(mobile ? ".mobileActions [aria-label='Dark theme']" : "header .desktopOnly"),
            signOut: bounds(mobile ? ".mobileActions [aria-label='Sign out']" : ".asideFooter button"),
          } : null,
          galleryCols: columns(".photos"), healthCols: columns(".healthGrid"), railCols: columns(".rail"),
          filterWidths: [...document.querySelectorAll(".filters select, .filters input")].map(e => e.getBoundingClientRect().width),
          firstCardTop: rect(".photo")?.top ?? null, submitBottom: rect(".entry form button")?.bottom ?? null,
          mobileHealthHeight: mobile ? rect(".mobileHealth")?.height ?? null : null,
          sidebarWidth: rect("aside")?.width ?? null, footerTop: mobile ? null : rect(".asideFooter")?.top ?? null,
        };
      });
      appendFileSync(join(info.project.outputDir, "responsive-baseline.jsonl"),
        JSON.stringify({ width, height, route, auth, dataset: "normal", ...metrics }) + "\n");
      test.fail(auth === "owner" && width <= 430, "Known oversized shell");
      expect(metrics.scrollWidth, "document fits viewport").toBeLessThanOrEqual(width + 1);
    });
  }
}
