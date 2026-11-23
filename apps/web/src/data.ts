import { useQuery } from "@tanstack/react-query";
export const states = [
  "RECEIVED",
  "DOWNLOAD_PENDING",
  "DOWNLOADING",
  "STAGED",
  "UPLOAD_PENDING",
  "UPLOADING",
  "CREATING_MEDIA",
  "SYNCED",
  "RETRY_PENDING",
  "NEEDS_REVIEW",
  "FAILED",
  "UNRECOVERABLE",
] as const;
export type State = (typeof states)[number];
export const base = "/api/v1";
export class ApiError extends Error {
  constructor(
    readonly status: number,
    message: string,
  ) {
    super(message);
  }
}
export async function api<T>(
  path: string,
  method = "GET",
  body?: unknown,
): Promise<T> {
  const r = await fetch(base + path, {
    method,
    headers: {
      "X-Gallery-Request": "1",
      ...(body ? { "Content-Type": "application/json" } : {}),
    },
    body: body ? JSON.stringify(body) : undefined,
  });
  const isJson = (r.headers.get("content-type") ?? "").includes(
    "application/json",
  );
  const data = isJson ? await r.json() : undefined;
  if (!r.ok) {
    if (r.status === 401 && !path.startsWith("/auth/"))
      window.location.assign("/");
    throw new ApiError(r.status, data?.error ?? "Request failed.");
  }
  if (!isJson) throw Error("Unexpected response.");
  return data;
}
export const query = <T>(p: string, enabled = true) =>
  useQuery<T>({
    queryKey: [p],
    queryFn: () => api<T>(p),
    refetchInterval: 15000,
    enabled,
  });
export interface Photo {
  dayNumber?: number; // Gallery list only.
  id: string;
  hasStaging: boolean;
  createdAt: string;
  lineGroupId: string;
  lineEventTimestamp: string;
  mediaType?: "IMAGE" | "VIDEO";
  durationMs?: number | null;
  group: { displayName: string | null };
  uploadStatus: State;
  width: number | null;
  height: number | null;
  fileSize: number | null;
  mimeType: string | null;
  sha256: string | null;
  productUrl: string | null;
  lastError: string | null;
  errorCode: string | null;
}
export interface Group {
  id: string;
  lineGroupId: string;
  displayName: string | null;
  enabled: boolean;
  membershipStatus: string;
  googleAlbumId: string | null;
  albumCreating: boolean;
  lastEventAt: string;
  _count: { images: number };
  images: Pick<Photo, "id" | "uploadStatus" | "lineEventTimestamp">[];
}
export interface Stats {
  total: number;
  synced: number;
  failed: number;
  pending: number;
  groups: number;
  google: string;
  stagingBytes: number;
  thumbnailBytes: number;
  freeBytes: number;
  diskPressure: boolean;
  oldest?: string;
  warnings: number;
  recent: { id: string; action: string; createdAt: string }[];
}
export interface Settings {
  stagingHours: number;
  failedDays: number;
  thumbnailDays: number;
  collectVideos: boolean;
  lineConfigured: boolean;
  webhook: string;
  maxImageMB: number;
  maxVideoMB: number;
}
export { date, bytes, name, labels } from "./i18n";
