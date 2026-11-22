import "./workspace-responsive.css";
import { Onboarding } from "./onboarding";
import { useState, type FormEvent } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { useQueryClient } from "@tanstack/react-query";
import {
  Image as ImageIcon,
  CheckCircle,
  Clock,
  TriangleAlert,
  Copy,
  Check,
  RefreshCw,
} from "lucide-react";
import { Button } from "../../../packages/ui/button";
import {
  api,
  query,
  date,
  bytes,
  base,
  name,
  type Group,
  type Stats,
  type Settings as Prefs,
} from "./data";

export function Title({
  k,
  title,
  sub,
}: {
  k: string;
  title: string;
  sub: string;
}) {
  return (
    <section className="heading">
      <small>{k}</small>
      <h1>{title}</h1>
      <p>{sub}</p>
    </section>
  );
}

export function useAction() {
  const qc = useQueryClient(),
    [msg, set] = useState(""),
    [busy, lock] = useState(false);
  return {
    msg,
    busy,
    run: async (f: () => Promise<unknown>, okMsg = "Saved.") => {
      lock(true);
      set("");
      try {
        const res = await f();
        await qc.invalidateQueries();
        set(typeof res === "string" ? res : okMsg);
      } catch (e) {
        set((e as Error).message);
      } finally {
        lock(false);
      }
    },
  };
}

function CopyButton({ value, label = "Copy" }: { value: string; label?: string }) {
  const [copied, setCopied] = useState(false);
  const copy = async () => {
    try {
      if (navigator.clipboard?.writeText) {
        await navigator.clipboard.writeText(value);
        setCopied(true);
        setTimeout(() => setCopied(false), 1800);
      }
    } catch {
      // LAN HTTP contexts may disallow clipboard writes
    }
  };
  return (
    <Button type="button" variant="secondary" onClick={copy}>
      {copied ? <Check size={13} /> : <Copy size={13} />}
      {copied ? "Copied" : label}
    </Button>
  );
}

export function Dashboard() {
  const [sp] = useSearchParams(),
    q = query<Stats>("/dashboard/stats"),
    prefs = query<Prefs>("/settings"),
    groups = query<Group[]>("/groups");
  if (!q.data) return <p>{q.error?.message ?? "Loading dashboard…"}</p>;
  const d = q.data;
  const totalDisk = Math.max(1, d.stagingBytes + d.thumbnailBytes + d.freeBytes);
  const stagingPct = Math.min(100, (d.stagingBytes / totalDisk) * 100);
  const thumbPct = Math.min(100 - stagingPct, (d.thumbnailBytes / totalDisk) * 100);

  return (
    <>
      <Title
        k="LIBRARY OVERVIEW"
        title="Dashboard"
        sub="Collection activity across your authorized LINE groups."
      />
      {(d.diskPressure || d.warnings > 0 || d.failed > 0) && (
        <div className="warning" role="alert">
          <span>
            {d.diskPressure ? "Low disk space. Ingestion is paused. " : ""}
            {d.warnings > 0 &&
              d.warnings +
                " staged images are approaching or past review deadlines. Originals have been retained. "}
            {d.failed > 0 && d.failed + " items need attention."}
          </span>
          <Link to="/jobs">Review queue →</Link>
        </div>
      )}
      <section className="rail" aria-label="Ingestion pipeline">
        <div className="railNode">
          <div className="railHead">
            <span>1 · LINE Sources</span>
            <ImageIcon size={16} />
          </div>
          <strong>{d.total}</strong>
          <div className="railMeta">
            <span>{d.groups} active groups</span>
            <Link to="/groups">Manage →</Link>
          </div>
        </div>
        <div className="railNode">
          <div className="railHead">
            <span>2 · Local Staging</span>
            {d.failed > 0 ? <TriangleAlert size={16} /> : <Clock size={16} />}
          </div>
          <strong>{d.pending}</strong>
          <div className="railMeta">
            <span>
              {d.failed > 0 ? `${d.failed} need attention` : "In progress"}
            </span>
            <Link to="/jobs">Queue →</Link>
          </div>
        </div>
        <div className="railNode">
          <div className="railHead">
            <span>3 · Google Photos</span>
            <CheckCircle size={16} />
          </div>
          <strong>{d.synced}</strong>
          <div className="railMeta">
            <span className={"pill " + (d.google === "CONNECTED" ? "ok" : "warn")}>
              {d.google}
            </span>
            <Link to="/gallery">Gallery →</Link>
          </div>
        </div>
      </section>
      {(!d.synced || sp.has("setup")) && (
        <Onboarding stats={d} prefs={prefs.data} groups={groups.data ?? []} />
      )}
      <div className="columns">
        <section className="panel">
          <small>STORAGE & CONNECTIONS</small>
          <h2>Local cache & disk</h2>
          <div className="diskBar" aria-hidden="true">
            <div className="diskSegStaging" style={{ width: `${stagingPct}%` }} />
            <div className="diskSegThumb" style={{ width: `${thumbPct}%` }} />
          </div>
          {Object.entries({
            "Google Photos": d.google,
            "Active groups": d.groups,
            "Staged originals": bytes(d.stagingBytes),
            "Thumbnail cache": bytes(d.thumbnailBytes),
            "Available disk": bytes(d.freeBytes),
            "Oldest unuploaded": d.oldest ? date(d.oldest) : "None",
          }).map(([k, v]) => (
            <div className="row" key={k}>
              <span>{k}</span>
              <b className="mono">{v}</b>
            </div>
          ))}
        </section>
        <section className="panel">
          <small>EVENT LOG</small>
          <h2>Recent activity</h2>
          {d.recent.length ? (
            d.recent.map((e) => (
              <div className="row" key={e.id}>
                <span>{e.action}</span>
                <time className="mono">{date(e.createdAt)}</time>
              </div>
            ))
          ) : (
            <p>Connection and group changes will appear here.</p>
          )}
        </section>
      </div>
    </>
  );
}

function AlbumsView() {
  const q = query<Group[]>("/groups");
  const [filter, setFilter] = useState<"all" | "collecting" | "mapped">("all");
  const groups = q.data ?? [];
  const visible = groups.filter((g) => {
    if (filter === "collecting") return g.enabled;
    if (filter === "mapped") return !!g.googleAlbumId;
    return true;
  });
  const totalItems = groups.reduce((sum, g) => sum + g._count.images, 0);

  return (
    <>
      <Title
        k="COLLECTIONS"
        title="Albums"
        sub="One Google Photos album for each authorized LINE group."
      />
      {q.error && (
        <p role="alert" style={{ marginBottom: 12 }}>
          {q.error.message}
        </p>
      )}
      {groups.length > 0 && (
        <div className="albumToolbar">
          <div className="albumTabs" role="group" aria-label="Filter albums">
            {(
              [
                ["all", `All (${groups.length})`],
                ["collecting", `Collecting (${groups.filter((g) => g.enabled).length})`],
                ["mapped", `Mapped (${groups.filter((g) => g.googleAlbumId).length})`],
              ] as const
            ).map(([k, label]) => (
              <button
                key={k}
                type="button"
                aria-pressed={filter === k}
                className={"albumTab" + (filter === k ? " active" : "")}
                onClick={() => setFilter(k)}
              >
                {label}
              </button>
            ))}
          </div>
          <span className="mono" style={{ fontSize: "0.78rem", color: "var(--ink-muted)" }}>
            {totalItems} archived items
          </span>
        </div>
      )}
      <div className="albumGrid">
        {visible.map((g) => {
          const cover = g.images[0];
          const statusDot = g.albumCreating
            ? "warn"
            : g.googleAlbumId
              ? "ok"
              : "";
          const statusText = g.albumCreating
            ? "Needs review"
            : g.googleAlbumId
              ? "Synced to Google"
              : g.enabled
                ? "Waiting for first item"
                : "Paused";
          return (
            <Link
              key={g.id}
              className="albumFolio"
              to={"/gallery?group=" + g.lineGroupId}
            >
              <div className="albumFrame">
                {cover ? (
                  <img
                    loading="lazy"
                    src={base + "/images/" + cover.id + "/thumbnail?size=640"}
                    alt={name(g) + " album cover"}
                  />
                ) : (
                  <div className="albumBlank">
                    <ImageIcon size={26} />
                    <span>No media yet</span>
                  </div>
                )}
                <span className="albumCount mono">
                  {g._count.images} {g._count.images === 1 ? "item" : "items"}
                </span>
              </div>
              <div className="albumFolioBody">
                <h2>{name(g)}</h2>
                <div className="albumFolioMeta">
                  <span className="albumState">
                    <span className={"healthDot " + statusDot} />
                    {statusText}
                  </span>
                  <time className="mono">
                    {cover ? date(cover.lineEventTimestamp) : "—"}
                  </time>
                </div>
              </div>
            </Link>
          );
        })}
      </div>
      {q.data && visible.length === 0 && (
        <div className="empty">
          <ImageIcon size={28} />
          <h2>{groups.length ? "No matching albums" : "No albums yet"}</h2>
          <p>
            {groups.length
              ? "Switch filter above to view all groups."
              : "Invite the LINE bot to a group and enable collection to start an album."}
          </p>
        </div>
      )}
    </>
  );
}

export function Groups({ albums = false }: { albums?: boolean }) {
  if (albums) return <AlbumsView />;
  const q = query<Group[]>("/groups"),
    action = useAction();
  const [checkedMap, setCheckedMap] = useState<Record<string, boolean>>({});

  return (
    <>
      <Title
        k="SOURCES"
        title="LINE groups"
        sub="New groups stay paused until you enable collection."
      />
      <p role="status" aria-live="polite">
        {action.msg || q.error?.message}
      </p>
      <div className="groupgrid">
        {q.data?.map((g) => (
          <article className="panel" key={g.id}>
            <div className="railHead">
              <span
                className={
                  "pill " +
                  (g.membershipStatus === "LEFT"
                    ? "err"
                    : g.enabled
                      ? "ok"
                      : "")
                }
              >
                {g.membershipStatus} · {g.enabled ? "COLLECTING" : "PAUSED"}
              </span>
              <span className="mono" style={{ fontSize: "0.78rem" }}>
                {g._count.images} items
              </span>
            </div>
            <h2 style={{ marginTop: 10 }}>{name(g)}</h2>
            <p>
              {g.googleAlbumId ? (
                <span className="pill ok">Album mapped</span>
              ) : g.albumCreating ? (
                <span className="pill warn">Mapping needs review</span>
              ) : (
                <span className="pill">No album yet</span>
              )}
            </p>
            <p className="mono" style={{ fontSize: "0.78rem" }}>
              {"Last event: " + date(g.lastEventAt)}
            </p>
            <div className="codeRow">
              <code>{g.googleAlbumId ?? g.lineGroupId}</code>
              <CopyButton value={g.googleAlbumId ?? g.lineGroupId} />
            </div>
            {g.albumCreating && (
              <form
                className="resolveBox"
                onSubmit={(e) => {
                  e.preventDefault();
                  if (!checkedMap[g.id]) return;
                  const f = new FormData(e.currentTarget);
                  void action.run(async () => {
                    const res = await api<{ requeued?: number }>(
                      "/groups/" + g.id + "/resolve-album",
                      "POST",
                      {
                        albumId: String(f.get("albumId") ?? ""),
                        confirmation: "I checked Google Photos",
                      },
                    );
                    const n = res.requeued ?? 0;
                    return n > 0
                      ? `Saved. ${n} ${n === 1 ? "photo" : "photos"} re-queued.`
                      : "Saved.";
                  });
                }}
              >
                <label style={{ marginTop: 0 }}>
                  Verified app-created album ID (blank if none created)
                  <input name="albumId" />
                </label>
                <label className="resolveAck">
                  <input
                    type="checkbox"
                    checked={!!checkedMap[g.id]}
                    onChange={(e) =>
                      setCheckedMap((m) => ({
                        ...m,
                        [g.id]: e.target.checked,
                      }))
                    }
                  />
                  I checked Google Photos for an existing album
                </label>
                <Button
                  variant="secondary"
                  disabled={action.busy || !checkedMap[g.id]}
                >
                  Resolve uncertain album
                </Button>
              </form>
            )}
            <div style={{ display: "flex", gap: 8, flexWrap: "wrap" }}>
              <Button
                disabled={action.busy || g.membershipStatus === "LEFT"}
                variant={g.enabled ? "secondary" : "default"}
                onClick={() =>
                  action.run(() =>
                    api("/groups/" + g.id, "PATCH", { enabled: !g.enabled }),
                  )
                }
              >
                {g.enabled ? "Pause collection" : "Enable collection"}
              </Button>
              <Button
                type="button"
                variant="secondary"
                disabled={action.busy || g.membershipStatus === "LEFT"}
                onClick={() =>
                  action.run(async () => {
                    const res = await api<{
                      albumUpdated: boolean;
                      albumError?: string;
                    }>("/groups/" + g.id + "/refresh", "POST");
                    return res.albumUpdated
                      ? "Group and Google Photos album title refreshed."
                      : res.albumError
                        ? "Group name refreshed. " + res.albumError
                        : "Group name refreshed.";
                  })
                }
              >
                <RefreshCw size={14} />
                Refresh name
              </Button>
            </div>
          </article>
        ))}
      </div>
      {q.data?.length === 0 && (
        <p className="empty">
          Invite the LINE bot to a group. Its album will appear here after the
          first authorized image.
        </p>
      )}
    </>
  );
}

const googleErrors: Record<string, string> = {
  state: "Google sign-in expired or was invalid. Start the connection again.",
  denied: "Google consent was declined. Start the connection again.",
  scopes:
    "Grant all requested Google Photos permissions and offline access, then connect again.",
  exchange: "Google connection failed. Try again.",
};

type SettingsSection = "line" | "rules" | "google" | "retention";

export function Settings() {
  const [sp] = useSearchParams(),
    googleError = sp.get("google_error"),
    q = query<Prefs>("/settings"),
    google = query<{ status: string; lastError?: string }>("/google/status"),
    action = useAction(),
    [feedbackSection, setFeedbackSection] = useState<SettingsSection | "">(""),
    [editingLine, setEditingLine] = useState(false);
  if (!q.data) return <p>{q.error?.message ?? "Loading settings…"}</p>;
  const d = q.data;
  const connected = google.data?.status === "CONNECTED";
  const showLineForm = !d.lineConfigured || editingLine;

  function run(
    section: SettingsSection,
    perform: () => Promise<unknown>,
    success: string,
  ) {
    setFeedbackSection(section);
    return action.run(perform, success);
  }
  const feedback = (section: SettingsSection) => (
    <p className="sectionFeedback" role="status">
      {feedbackSection === section ? action.msg : ""}
    </p>
  );

  function save(e: FormEvent<HTMLFormElement>, kind: SettingsSection) {
    e.preventDefault();
    const form = e.currentTarget,
      data = Object.fromEntries(new FormData(form));
    void run(
      kind,
      async () => {
        await api(
          kind === "line" ? "/settings/line" : "/settings",
          kind === "line" ? "POST" : "PATCH",
          kind === "line"
            ? data
            : Object.fromEntries(
                Object.entries(data).map(([k, v]) => [k, Number(v)]),
              ),
        );
        if (kind === "line") {
          form.reset();
          setEditingLine(false);
        }
      },
      kind === "line" ? "Credentials saved." : "Retention saved.",
    );
  }

  return (
    <>
      <Title
        k="SETTINGS"
        title="Settings"
        sub="Control where group media arrives, what gets saved, and how long copies stay local."
      />
      <div className="settingsLedger">
        <div className="routeStrip" aria-label="Active media pipeline">
          <div className="routeNode">
            <span className={"healthDot " + (d.lineConfigured ? "ok" : "warn")} />
            <div>
              <b>LINE channel</b>
              <span className="mono">
                {d.collectVideos
                  ? `Photos + Videos (≤${d.maxVideoMB} MB)`
                  : `Photos only (≤${d.maxImageMB} MB)`}
              </span>
            </div>
          </div>
          <span className="routeArrow" aria-hidden="true">
            →
          </span>
          <div className="routeNode">
            <span className="healthDot ok" />
            <div>
              <b>Local cache</b>
              <span className="mono">{d.stagingHours}h staging · {d.failedDays}d review</span>
            </div>
          </div>
          <span className="routeArrow" aria-hidden="true">
            →
          </span>
          <div className="routeNode">
            <span className={"healthDot " + (connected ? "ok" : "warn")} />
            <div>
              <b>Google Photos</b>
              <span className="mono">{connected ? "App albums only" : "Disconnected"}</span>
            </div>
          </div>
        </div>

        <section className="ledgerRow">
          <div className="ledgerAside">
            <h2>LINE channel</h2>
            <span className={"pill " + (d.lineConfigured ? "ok" : "warn")}>
              {d.lineConfigured ? "Configured" : "Needs credentials"}
            </span>
            <p>Where group messages arrive from.</p>
          </div>
          <form className="ledgerMain" onSubmit={(e) => save(e, "line")}>
            {d.webhook && (
              <div style={{ marginBottom: 14 }}>
                <small>WEBHOOK URL</small>
                <div className="codeRow">
                  <code>{d.webhook}</code>
                  <CopyButton value={d.webhook} />
                </div>
              </div>
            )}
            {showLineForm ? (
              <>
                <label>
                  Access token
                  <input
                    name="token"
                    type="password"
                    required
                    autoComplete="off"
                  />
                </label>
                <label>
                  Channel secret
                  <input
                    name="secret"
                    type="password"
                    required
                    autoComplete="off"
                  />
                </label>
                <div style={{ display: "flex", gap: 8, flexWrap: "wrap" }}>
                  <Button disabled={action.busy}>Save credentials</Button>
                  {d.lineConfigured && (
                    <Button
                      type="button"
                      variant="ghost"
                      disabled={action.busy}
                      onClick={() => setEditingLine(false)}
                    >
                      Cancel
                    </Button>
                  )}
                </div>
              </>
            ) : (
              <div>
                <p>
                  Channel credentials are stored server-side. Rotate them only
                  when issuing a new token or secret.
                </p>
                <Button
                  type="button"
                  variant="secondary"
                  onClick={() => setEditingLine(true)}
                >
                  Update credentials
                </Button>
              </div>
            )}
            {feedback("line")}
          </form>
        </section>

        <section className="ledgerRow">
          <div className="ledgerAside">
            <h2>Collection rules</h2>
            <p>What gets captured from enabled groups.</p>
          </div>
          <div className="ledgerMain">
            <div className="ruleRow">
              <div>
                <b>Collect videos</b>
                <p style={{ margin: 0, fontSize: "0.84rem" }}>
                  Off by default. Up to {d.maxVideoMB} MB per video.
                </p>
              </div>
              <Button
                type="button"
                disabled={action.busy}
                variant={d.collectVideos ? "default" : "secondary"}
                aria-pressed={d.collectVideos}
                onClick={() =>
                  run(
                    "rules",
                    () =>
                      api("/settings", "PATCH", {
                        collectVideos: !d.collectVideos,
                      }),
                    !d.collectVideos
                      ? "Video collection enabled."
                      : "Video collection disabled.",
                  )
                }
              >
                {d.collectVideos ? "Enabled" : "Disabled"}
              </Button>
            </div>
            {feedback("rules")}
            <div className="ruleRow" style={{ borderBottom: 0, paddingBottom: 0 }}>
              <div>
                <b>Collect photos</b>
                <p style={{ margin: 0, fontSize: "0.84rem" }}>
                  Always on for enabled groups. Up to {d.maxImageMB} MB per image.
                </p>
              </div>
              <span className="pill ok">Always on</span>
            </div>
          </div>
        </section>

        <section className="ledgerRow">
          <div className="ledgerAside">
            <h2>Google Photos</h2>
            <span className={"pill " + (connected ? "ok" : "warn")}>
              {google.data?.status ?? "UNKNOWN"}
            </span>
            <p>Where synced albums live.</p>
          </div>
          <div className="ledgerMain">
            {googleError && (
              <p role="alert">{googleErrors[googleError] ?? googleErrors.exchange}</p>
            )}
            {google.data?.lastError && (
              <p className="pill err" style={{ marginBottom: 10 }}>
                {google.data.lastError}
              </p>
            )}
            <p>
              Uploads new media from enabled LINE groups into app-created
              albums. Scope is limited to app-created albums and media.
            </p>
            <div className="actionSplit">
              <div style={{ display: "flex", gap: 8, flexWrap: "wrap" }}>
                {connected && (
                  <Button
                    disabled={action.busy}
                    variant="default"
                    onClick={() =>
                      run(
                        "google",
                        () => api("/google/validate", "POST"),
                        "Google connection validated.",
                      )
                    }
                  >
                    Validate connection
                  </Button>
                )}
                <a
                  className={"button " + (connected ? "secondary" : "primary")}
                  href={base + "/google/connect"}
                >
                  {connected ? "Reconnect" : "Connect Google Photos"}
                </a>
              </div>
              {connected && (
                <Button
                  disabled={action.busy}
                  variant="ghost"
                  className="dangerGhost"
                  onClick={() =>
                    run(
                      "google",
                      () => api("/google/disconnect", "POST"),
                      "Google disconnected.",
                    )
                  }
                >
                  Disconnect
                </Button>
              )}
            </div>
            {feedback("google")}
          </div>
        </section>

        <section className="ledgerRow">
          <div className="ledgerAside">
            <h2>Local retention</h2>
            <p>How long files stay on this server.</p>
          </div>
          <form className="ledgerMain" onSubmit={(e) => save(e, "retention")}>
            <div className="retentionGrid">
              {(
                [
                  ["stagingHours", "Synced originals", "hours", 720],
                  ["failedDays", "Failed review deadline", "days", 90],
                  ["thumbnailDays", "Thumbnail cache", "days", 365],
                ] as const
              ).map(([k, l, unit, max]) => (
                <label key={k}>
                  {l}
                  <div className="unitInput">
                    <input
                      name={k}
                      type="number"
                      min="1"
                      max={max}
                      required
                      defaultValue={d[k]}
                    />
                    <span className="mono">{unit}</span>
                  </div>
                </label>
              ))}
            </div>
            <div className="safetyNote">
              Failed and uncertain originals stay staged until you review them
              and are never silently discarded.
            </div>
            <Button disabled={action.busy}>Save retention</Button>
            {feedback("retention")}
          </form>
        </section>
      </div>
    </>
  );
}

export function healthStatus(
  k: string,
  v: string | boolean,
): "ok" | "warn" | "err" {
  if (k === "diskPressure") return v === false ? "ok" : "warn";
  if (k === "workerLastProgressAt") return "ok"; // informational timestamp
  if (v === "Online" || v === "PONG") return "ok";
  return "err";
}

declare const __APP_VERSION__: string;

export function Health() {
  const q = query<Record<string, string | boolean>>("/health");
  return (
    <>
      <Title
        k="OPERATIONS"
        title="System health"
        sub="Live dependency checks for ingestion and uploads."
      />
      {q.error && <p role="alert">{q.error.message}</p>}
      <div className="healthGrid">
        {Object.entries(q.data ?? {}).map(([k, v]) => {
          const state = healthStatus(k, v);
          const display =
            k === "diskPressure"
              ? v
                ? "Pressured"
                : "Normal"
              : k === "workerLastProgressAt"
                ? new Date(String(v)).toLocaleString()
                : String(v);
          return (
            <section className="healthCard" key={k}>
              <div className="row">
                <b>{k === "workerLastProgressAt" ? "Last worker progress" : k}</b>
                <span className={"pill " + state}>{display}</span>
              </div>
            </section>
          );
        })}
      </div>
      <small className="appVersion">v{__APP_VERSION__}</small>
    </>
  );
}
