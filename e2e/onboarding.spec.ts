import { expect, test } from "@playwright/test";
import { mockApi, normal } from "./fixtures";

// AUD3-029: the test-image step says videos are collected only when collectVideos is on.
for (const collectVideos of [true, false]) {
  test(`onboarding describes media collection with collectVideos=${collectVideos}`, async ({ page }) => {
    await mockApi(page, normal);
    await page.route("**/api/v1/settings", (route) =>
      route.fulfill({
        json: {
          stagingHours: 24,
          failedDays: 7,
          thumbnailDays: 30,
          collectVideos,
          lineConfigured: true,
          webhook: "https://example.test/webhook/line",
          maxImageMB: 10,
          maxVideoMB: 100,
        },
      }),
    );
    await page.goto("/?setup");
    // Every phase is complete in the fixture, so this one may already be expanded; clicking would collapse it.
    const phase = page.getByRole("button", { name: /First sync verification/ });
    if ((await phase.getAttribute("aria-expanded")) !== "true") await phase.click();
    await expect(page.getByText("Send a test image")).toBeVisible();
    await expect(
      page.getByText(
        collectVideos
          ? "Post a new image or video in an enabled group (old messages and DMs are skipped)."
          : "Post a new image in an enabled group (old messages, videos, and DMs are skipped).",
        { exact: true },
      ),
    ).toBeVisible();
  });
}
