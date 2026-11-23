import { expect, type Locator, type Page } from "@playwright/test";

export async function box(locator: Locator) {
  await expect(locator).toBeVisible();
  return (await locator.boundingBox())!;
}
import type { Group, Photo, Stats, Settings } from "../apps/web/src/data";

const timestamp = "2026-05-01T12:00:00Z";
export const longName = "G".repeat(150);

function group(id: string, displayName: string, count: number): Group {
  return {
    id,
    lineGroupId: id,
    displayName,
    enabled: true,
    membershipStatus: "JOINED",
    googleAlbumId: null,
    albumCreating: true,
    lastEventAt: timestamp,
    _count: { images: count },
    images: [{ id, lineEventTimestamp: timestamp, uploadStatus: "SYNCED" }],
  };
}

function photo(g: Group, id: string): Photo {
  return {
    id,
    lineGroupId: g.lineGroupId,
    group: { displayName: g.displayName },
    createdAt: timestamp,
    lineEventTimestamp: timestamp,
    uploadStatus: "SYNCED",
    width: 320,
    height: 240,
    mimeType: "image/png",
    fileSize: 1234,
    hasStaging: false,
    lastError: null,
    errorCode: null,
    sha256: null,
    productUrl: null,
  };
}

function dataset(stressed: boolean) {
  const groups = [group("source", "Family", 1), group("trips", "Trips", 2)];
  if (stressed)
    groups.push(
      group("x".repeat(64), longName, 12001),
      group("thai", "ครอบครัว", 1),
    );
  const images = groups.map((g) => photo(g, g.id));
  images[1] = { ...images[1], width: 240, height: 320 };
  images.push({
    ...images[1],
    id: "video",
    mediaType: "VIDEO",
    mimeType: "video/mp4",
  });
  images.forEach((image, index) => { image.dayNumber = images.length - index; });
  const jobs: Photo[] = [
    {
      ...images[stressed ? 2 : 0],
      uploadStatus: "NEEDS_REVIEW",
      hasStaging: true,
      lastError: stressed ? "E".repeat(300) : "Upload needs review.",
    },
  ];
  const stats: Stats = {
    total: stressed ? 12004 : 4,
    synced: 3,
    failed: 1,
    pending: 1,
    groups: groups.length,
    google: "CONNECTED",
    stagingBytes: 1024,
    thumbnailBytes: 512,
    freeBytes: 100000,
    diskPressure: false,
    warnings: 1,
    recent: [],
  };
  return { groups, images, jobs, stats };
}
export const normal = dataset(false),
  stress = dataset(true);
export type Auth = "owner" | "login" | "setup";
const settings: Settings = {
  stagingHours: 24,
  failedDays: 7,
  thumbnailDays: 30,
  collectVideos: true,
  lineConfigured: true,
  webhook: "https://example.test/webhook/line",
  maxImageMB: 10,
  maxVideoMB: 100,
};

export async function mockApi(
  page: Page,
  data: typeof normal,
  auth: Auth = "owner",
) {
  await page.route("**/api/v1/**", async (route) => {
    const { pathname, searchParams } = new URL(route.request().url());
    const path = pathname.slice("/api/v1".length);
    if (path === "/auth/status")
      return route.fulfill({
        json: { authenticated: auth === "owner", setup: auth === "setup" },
      });
    if (auth !== "owner" || route.request().method() !== "GET")
      return route.fulfill({ status: 403 });
    if (/^\/images\/[^/]+\/(thumbnail|content)$/.test(path))
      return route.fulfill({
        contentType: "image/png",
        body: Buffer.from(
          "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jRZkAAAAASUVORK5CYII=",
          "base64",
        ),
      });
    if (path === "/jobs" || path === "/images") {
      const items = (path === "/jobs" ? data.jobs : data.images).filter(
        (p) =>
          (!searchParams.has("status") ||
            searchParams.get("status") === p.uploadStatus) &&
          (!searchParams.has("group") ||
            searchParams.get("group") === p.lineGroupId),
      );
      return route.fulfill({ json: { items, total: items.length, page: 1 } });
    }
    const values: Record<string, unknown> = {
      "/groups": data.groups,
      "/dashboard/stats": data.stats,
      "/settings": settings,
      "/google/status": { status: "CONNECTED" },
      "/health": {
        database: "Online",
        redis: "Online",
        worker: "Offline",
        diskPressure: false,
        workerLastProgressAt: timestamp,
      },
    };
    return path in values
      ? route.fulfill({ json: values[path] })
      : route.fulfill({ status: 404 });
  });
}
