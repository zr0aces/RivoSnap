import { expect, test } from "@playwright/test";
import { mockApi, normal } from "./fixtures";

test("daily media chart shows seven days and exact completed uploads on a phone", async ({
  page,
}) => {
  await page.setViewportSize({ width: 360, height: 740 });
  await mockApi(page, normal);
  const days = Array.from({ length: 7 }, (_, i) => ({
    date: "2026-11-" + (17 + i),
    imagesReceived: 0,
    videosReceived: 0,
    imagesUploaded: 0,
    videosUploaded: 0,
  }));
  days[6] = {
    ...days[6],
    imagesReceived: 2,
    videosReceived: 1,
    imagesUploaded: 4,
    videosUploaded: 3,
  };
  await page.route("**/api/v1/dashboard/activity", (route) =>
    route.fulfill({ json: days }),
  );
  await page.goto("/");
  const chart = page.getByRole("region", { name: "Media activity" });
  await expect(chart.locator(".activityDay")).toHaveCount(7);
  await expect(chart.getByTitle("23 Nov · Uploaded: 7")).toBeVisible();
  await chart.getByText("Daily counts and upload breakdown").click();
  await expect(chart.getByRole("row").last().locator("th, td")).toHaveText([
    "23 Nov",
    "2",
    "1",
    "4",
    "3",
    "7",
  ]);
  expect(
    await chart.evaluate((el) => el.scrollWidth <= el.clientWidth + 1),
  ).toBe(true);
});
