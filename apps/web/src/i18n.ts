// English is the initial catalog. Keep display locale independent of the ingestion timezone.
export const locale = "en-GB",
  date = (v: string) =>
    new Intl.DateTimeFormat(locale, {
      timeZone: "Asia/Bangkok",
      dateStyle: "medium",
      timeStyle: "short",
    }).format(new Date(v)),
  bytes = (n: number) =>
    n >= 1048576
      ? (n / 1048576).toFixed(1) + " MB"
      : Math.round(n / 1024) + " KB",
  name = (g: { displayName: string | null; lineGroupId: string }) =>
    g.displayName || "Group " + g.lineGroupId.slice(-8);
export const labels = {
  dashboard: "Dashboard",
  gallery: "Gallery",
  albums: "Albums",
  groups: "LINE groups",
  jobs: "Upload queue",
  profile: "Profile",
  settings: "Settings",
  health: "System health",
} as const;
