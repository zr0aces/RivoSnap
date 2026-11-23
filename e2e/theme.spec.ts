import { expect, test } from "@playwright/test";
import { mockApi, normal } from "./fixtures";

// AUD3-030: blocked storage must not stop the app, and the saved theme applies from the first render.
test("theme storage that throws does not block the app", async ({ page }) => {
  await page.addInitScript(() => {
    const denied = () => {
      throw new DOMException("storage is blocked", "SecurityError");
    };
    Storage.prototype.getItem = denied;
    Storage.prototype.setItem = denied;
  });
  await mockApi(page, normal);
  await page.goto("/");
  await expect(page.getByRole("heading", { name: "Dashboard" })).toBeVisible();
});

test("saved dark theme is applied before the first rendered content", async ({ page }) => {
  // Record data-theme at the first mutation that puts a child into #root. A layout effect has
  // already set the attribute in that same commit; a passive effect has not run yet.
  await page.addInitScript(() => {
    localStorage.setItem("gallery-theme", "dark");
    const observer = new MutationObserver(() => {
      if (document.getElementById("root")?.childElementCount) {
        (window as unknown as { themeAtFirstContent: string }).themeAtFirstContent =
          document.documentElement.dataset.theme ?? "unset";
        observer.disconnect();
      }
    });
    observer.observe(document, { childList: true, subtree: true });
  });
  await mockApi(page, normal);
  await page.goto("/");
  await expect(page.getByRole("heading", { name: "Dashboard" })).toBeVisible();
  const atFirstContent = await page.evaluate(
    () => (window as unknown as { themeAtFirstContent?: string }).themeAtFirstContent,
  );
  expect(atFirstContent).toBe("dark");
  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
});
