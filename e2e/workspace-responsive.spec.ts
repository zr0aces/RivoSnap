import { expect, test, type Locator } from "@playwright/test";
import { box, longName, mockApi, normal, stress } from "./fixtures";

async function fits(el: Locator, parent: Locator) {
  const [item, card] = await Promise.all([box(el), box(parent)]);
  expect(item.x).toBeGreaterThanOrEqual(card.x - 1);
  expect(item.x + item.width).toBeLessThanOrEqual(card.x + card.width + 1);
}

test("group acknowledgment is tappable without submitting", async ({
  browser,
  baseURL,
}) => {
  const page = await browser.newPage({
    baseURL,
    hasTouch: true,
    viewport: { width: 320, height: 568 },
  });
  try {
    const writes: string[] = [];
    page.on("request", (r) => {
      if (r.method() !== "GET") writes.push(r.method());
    });
    await mockApi(page, stress);
    await page.goto("/groups");
    const card = page
      .locator(".groupgrid article")
      .filter({ hasText: longName });
    const ack = card.locator(".resolveAck"),
      submit = card.getByRole("button", { name: "Resolve uncertain album" });
    await expect(submit).toBeDisabled();
    expect((await box(ack)).height).toBeGreaterThanOrEqual(44);
    await ack.tap();
    await expect(ack.getByRole("checkbox")).toBeChecked();
    await expect(submit).toBeEnabled();
    await expect(card.locator(".codeRow code")).toHaveText("x".repeat(64));
    for (const el of await card.locator(".codeRow code, button, label").all())
      await fits(el, card);
    expect(writes).toEqual([]);
  } finally {
    await page.close();
  }
});

for (const width of [320, 821]) {
  test(`settings sections adapt to available width at ${width}px`, async ({
    page,
  }) => {
    await page.setViewportSize({ width, height: 768 });
    await mockApi(page, normal);
    await page.route("**/api/v1/google/validate", (r) =>
      r.fulfill({ status: 400, json: { error: "Validation unavailable." } }),
    );
    await page.goto("/settings?google_error=exchange");
    const strip = page.locator(".routeStrip");
    // Wait for populated settings rather than passing on an empty node list.
    await expect(strip.locator(".routeNode b")).toHaveText([
      "LINE channel",
      "Local cache",
      "Google Photos",
    ]);
    for (let i = 0; i < 2; i++) {
      const prev = await box(strip.locator(".routeNode").nth(i));
      const arrow = await box(strip.locator(".routeArrow").nth(i));
      const next = await box(strip.locator(".routeNode").nth(i + 1));
      expect(arrow.y).toBeGreaterThanOrEqual(prev.y + prev.height - 1);
      expect(next.y).toBeGreaterThanOrEqual(arrow.y + arrow.height - 1);
    }
    await page.getByRole("button", { name: "Update credentials" }).click();
    await expect(page.getByLabel("Channel secret")).toBeEditable();
    for (const pair of await page.locator(".unitInput").all()) {
      const input = await box(pair.locator("input")),
        unit = await box(pair.locator("span"));
      expect(unit.x).toBeGreaterThanOrEqual(input.x + input.width - 1);
      expect(unit.y).toBeLessThan(input.y + input.height);
      await fits(pair.locator("span"), pair);
    }
    const google = page
      .locator(".ledgerRow")
      .filter({ has: page.getByRole("heading", { name: "Google Photos" }) });
    await expect(google.getByRole("alert")).toBeVisible();
    const validate = google.getByRole("button", {
      name: "Validate connection",
    });
    const reconnect = google.getByRole("link", { name: "Reconnect" });
    const disconnect = google.getByRole("button", { name: "Disconnect" });
    for (const control of [validate, reconnect, disconnect]) {
      await fits(control, google);
      await control.click({ trial: true });
    }
    const reconnectBox = await box(reconnect),
      disconnectBox = await box(disconnect);
    expect(disconnectBox.y).toBeGreaterThan(reconnectBox.y + reconnectBox.height);
    await validate.click();
    await expect(google.getByRole("status")).toHaveText(
      "Validation unavailable.",
    );
  });
}

test("album filters support keyboard selection and filtered Gallery links", async ({
  page,
}) => {
  await page.setViewportSize({ width: 320, height: 568 });
  await mockApi(page, stress);
  await page.goto("/albums");
  const filters = page.getByRole("group", { name: "Filter albums" });
  const all = filters.getByRole("button", { name: /^All/ }),
    mapped = filters.getByRole("button", { name: /^Mapped/ });
  await expect(filters.getByRole("button")).toHaveCount(3);
  for (const button of await filters.getByRole("button").all())
    expect((await box(button)).height).toBeGreaterThanOrEqual(44);
  await mapped.focus();
  await page.keyboard.press("Enter");
  await expect(mapped).toHaveAttribute("aria-pressed", "true");
  await expect(
    page.getByRole("heading", { name: "No matching albums" }),
  ).toBeVisible();
  await all.focus();
  await page.keyboard.press("Space");
  await expect(all).toHaveAttribute("aria-pressed", "true");
  const album = page.locator(".albumFolio").filter({ hasText: longName });
  await fits(album.getByRole("heading"), album);
  await album.click();
  await expect(page).toHaveURL(
    new RegExp("/gallery\\?group=" + "x".repeat(64)),
  );
});
