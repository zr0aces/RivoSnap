import { test, expect, type Locator } from "@playwright/test";
import { longName, mockApi, stress } from "./fixtures";

for (const surface of ["gallery", "jobs"]) {
  test(`phone ${surface} filters survive reload and reset while collapsed`, async ({
    page,
  }) => {
    await page.setViewportSize({ width: 320, height: 568 });
    await mockApi(page, stress);
    const status = surface === "gallery" ? "SYNCED" : "NEEDS_REVIEW";
    await page.goto("/" + surface + "?status=" + status);
    const filters = page.getByRole("button", { name: "Filters", exact: true });
    const field = page.getByRole("combobox", { name: "Status", exact: true });
    await expect(filters).toHaveAttribute("aria-expanded", "false");
    await expect(field).toBeHidden();
    await expect(page.locator(".filterSummary")).toHaveText(status);
    if (surface === "gallery") {
      const card = page.locator(".photo").first();
      await expect(card).toBeVisible();
      expect((await card.boundingBox())!.y).toBeLessThan(568);
    }
    await filters.click();
    await field.selectOption("FAILED");
    await expect(page).toHaveURL(/status=FAILED/);
    await page.reload();
    await expect(field).toBeHidden();
    await expect(page.locator(".filterSummary")).toHaveText("FAILED");
    await page.getByRole("button", { name: "Reset" }).click();
    await expect(page).toHaveURL(new RegExp("/" + surface + "$"));
    await expect(page.locator(".filterSummary")).toHaveCount(0);
  });
}

async function box(locator: Locator) {
  await expect(locator).toBeVisible();
  return (await locator.boundingBox())!;
}

for (const [width, height] of [
  [320, 568],
  [568, 320],
]) {
  test(`viewer reserves space for controls and media at ${width}x${height}`, async ({
    page,
  }) => {
    await page.setViewportSize({ width, height });
    await mockApi(page, stress);
    await page.goto("/gallery");
    for (const label of [longName, "VIDEO"]) {
      await page.getByRole("button", { name: new RegExp(label) }).click();
      const dialog = page.getByRole("dialog");
      const title = await box(dialog.locator(".viewerTitle"));
      const previous = await box(
        dialog.getByRole("button", { name: "Previous image" }),
      );
      const next = await box(
        dialog.getByRole("button", { name: "Next image" }),
      );
      const close = dialog.getByRole("button", { name: "Close viewer" });
      const control = await box(close);
      expect(title.y + title.height).toBeLessThanOrEqual(previous.y + 1);
      expect(previous.x).toBeGreaterThanOrEqual(0);
      expect(previous.y).toBe(next.y);
      expect(next.y).toBe(control.y);
      expect(control.x + control.width).toBeLessThanOrEqual(width);
      expect(control.height).toBeGreaterThanOrEqual(44);
      const media = await box(
        dialog.locator(
          label === "VIDEO" ? "video[controls][playsinline]" : "img",
        ),
      );
      expect(media.y).toBeGreaterThanOrEqual(control.y + control.height - 1);
      expect(media.height).toBeGreaterThan(0);
      const foot = await box(dialog.locator(".viewerFoot"));
      expect(media.y + media.height).toBeLessThanOrEqual(foot.y + 1);
      expect(foot.y + foot.height).toBeLessThanOrEqual(height + 1);
      await close.click();
    }
  });
}

test("phone queue separates recovery from discard and cancel sends no write", async ({
  page,
}) => {
  await page.setViewportSize({ width: 320, height: 568 });
  await mockApi(page, stress);
  const writes: string[] = [];
  page.on("request", (r) => {
    if (r.method() !== "GET") writes.push(r.url());
  });
  await page.goto("/jobs");
  const row = page.locator(".queueRow");
  const thumb = await box(row.locator(".queueThumb"));
  const summary = await box(row.locator(".queueSummary b"));
  expect(summary.x).toBeGreaterThanOrEqual(thumb.x + thumb.width);
  const discard = row.getByRole("button", { name: "Discard expired original" });
  await discard.click();
  for (const name of ["Reconcile with Google", "Confirm discard", "Cancel"]) {
    const button = row.getByRole("button", { name });
    await button.click({ trial: true });
    const target = await box(button),
      outer = await box(row),
      error = await box(row.locator(".queueIssue"));
    expect(target.height).toBeGreaterThanOrEqual(44);
    expect(target.y).toBeGreaterThanOrEqual(error.y + error.height - 1);
    expect(target.x).toBeGreaterThanOrEqual(outer.x);
    expect(target.x + target.width).toBeLessThanOrEqual(outer.x + outer.width);
  }
  await row.getByRole("button", { name: "Cancel" }).click();
  await expect(discard).toBeVisible();
  expect(writes).toEqual([]);
});
