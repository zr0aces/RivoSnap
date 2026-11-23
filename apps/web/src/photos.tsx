import { useLayoutEffect, useRef, useState } from "react";
import { useSearchParams } from "react-router-dom";
import {
  ChevronLeft,
  ChevronRight,
  X,
  Image as ImageIcon,
  RotateCcw,
} from "lucide-react";
import { Button } from "../../../packages/ui/button";
import {
  api,
  base,
  bytes,
  date,
  name,
  query,
  states,
  type Group,
  type Photo,
  type Settings,
} from "./data";
import { Title, useAction } from "./workspace";
import "./library-responsive.css";

function statusTone(status: string): string {
  if (status === "SYNCED") return "ok";
  if (status === "FAILED" || status === "UNRECOVERABLE") return "err";
  if (status === "NEEDS_REVIEW") return "warn";
  return "";
}

function formatDuration(ms?: number | null) {
  if (!ms || ms <= 0) return "VIDEO";
  const sec = Math.round(ms / 1000);
  return `${Math.floor(sec / 60)}:${String(sec % 60).padStart(2, "0")}`;
}

function Lightbox({
  items,
  index,
  set,
  close,
}: {
  items: Photo[];
  index: number;
  set: (n: number) => void;
  close: () => void;
}) {
  const ref = useRef<HTMLDialogElement>(null);
  useLayoutEffect(() => {
    const d = ref.current;
    d?.showModal();
    return () => d?.close();
  }, []);
  const p = items[index];
  return (
    <dialog
      className="viewer"
      ref={ref}
      onCancel={close}
      onKeyDown={(e) => {
        if (e.target instanceof HTMLVideoElement) return;
        if (e.key === "ArrowLeft") set(Math.max(0, index - 1));
        if (e.key === "ArrowRight") set(Math.min(items.length - 1, index + 1));
      }}
      aria-label="Image viewer"
    >
      <div className="viewerHead">
        <div className="viewerTitle">
          <b>{name({ ...p.group, lineGroupId: p.lineGroupId })}</b>
          <span className={"pill " + statusTone(p.uploadStatus)}>
            {p.uploadStatus}
          </span>
        </div>
        <div className="viewerControls">
          <Button
            variant="secondary"
            aria-label="Previous image"
            disabled={!index}
            onClick={() => set(index - 1)}
          >
            <ChevronLeft size={16} />
          </Button>
          <span className="mono" style={{ fontSize: "0.82rem" }}>
            {index + 1} / {items.length}
          </span>
          <Button
            variant="secondary"
            aria-label="Next image"
            disabled={index + 1 === items.length}
            onClick={() => set(index + 1)}
          >
            <ChevronRight size={16} />
          </Button>
          <Button variant="secondary" aria-label="Close viewer" onClick={close}>
            <X size={16} />
          </Button>
        </div>
      </div>
      {p.mediaType === "VIDEO" ? (
        <video
          controls
          playsInline
          src={base + "/images/" + p.id + "/content"}
        />
      ) : (
        <img
          src={base + "/images/" + p.id + "/content"}
          alt={"Photo received " + date(p.lineEventTimestamp)}
        />
      )}
      <div className="viewerFoot">
        <p className="mono" style={{ margin: 0, fontSize: "0.78rem" }}>
          {date(p.lineEventTimestamp)} · {p.width} × {p.height} ·{" "}
          {bytes(p.fileSize ?? 0)} · {p.mimeType}
        </p>
        <div style={{ display: "flex", alignItems: "center", gap: 14 }}>
          <span className="mono" style={{ fontSize: "0.74rem", opacity: 0.7 }}>
            ← → navigate · Esc close
          </span>
          {p.productUrl?.startsWith("https://photos.google.com/") && (
            <a href={p.productUrl} target="_blank" rel="noreferrer">
              Open in Google Photos ↗
            </a>
          )}
        </div>
      </div>
    </dialog>
  );
}

export function Library({ jobs = false }: { jobs?: boolean }) {
  const [sp, setParams] = useSearchParams(),
    [filtersOpen, setFiltersOpen] = useState(false),
    [index, setIndex] = useState<number | null>(null),
    [confirmDiscard, setConfirmDiscard] = useState<string | null>(null);
  const prefs = query<Settings>("/settings"),
    action = useAction(),
    groups = query<Group[]>("/groups"),
    q = query<{ items: Photo[]; total: number; page: number }>(
      (jobs ? "/jobs" : "/images") + "?" + sp,
    );
  const set = (k: string, v: string) => {
    const p = new URLSearchParams(sp);
    if (k !== "page") p.delete("page");
    if (v) p.set(k, v);
    else p.delete(k);
    setIndex(null);
    setParams(p);
  };
  const groupFilter = sp.get("group");
  const selectedGroup = groups.data?.find((g) => g.lineGroupId === groupFilter);
  const criteria = [
    groupFilter && (selectedGroup ? name(selectedGroup) : groupFilter),
    sp.get("status"),
    sp.get("from") && "From " + sp.get("from"),
    sp.get("to") && "Through " + sp.get("to"),
  ].filter(Boolean);
  const d = q.data;

  return (
    <>
      <Title
        k={jobs ? "OPERATIONS" : "PHOTO LIBRARY"}
        title={jobs ? "Upload queue" : "Gallery"}
        sub={
          jobs
            ? "Track processing, retry failed uploads and reconcile uncertain results."
            : "Photos received from your authorized LINE groups."
        }
      />
      <div className="filterContainer">
        <div className="filters">
          <Button
            type="button"
            variant="secondary"
            className="filterToggle"
            aria-controls="library-filter-fields"
            aria-expanded={filtersOpen}
            onClick={() => setFiltersOpen(!filtersOpen)}
          >
            Filters
          </Button>
          {criteria.length > 0 && (
            <span className="filterSummary">{criteria.join(" · ")}</span>
          )}
          <div
            className="filterFields"
            id="library-filter-fields"
            data-open={filtersOpen}
          >
            <label>
              Group
              <select
                value={sp.get("group") ?? ""}
                onChange={(e) => set("group", e.target.value)}
              >
                <option value="">All groups</option>
                {groups.data?.map((g) => (
                  <option key={g.id} value={g.lineGroupId}>
                    {name(g)}
                  </option>
                ))}
              </select>
            </label>
            <label>
              Status
              <select
                value={sp.get("status") ?? ""}
                onChange={(e) => set("status", e.target.value)}
              >
                <option value="">All states</option>
                {states
                  .filter((s) => !jobs || s !== "SYNCED")
                  .map((s) => (
                    <option key={s}>{s}</option>
                  ))}
              </select>
            </label>
            {["from", "to"].map((k) => (
              <label key={k}>
                {k === "from" ? "From" : "Through"}
                <input
                  type="date"
                  value={sp.get(k) ?? ""}
                  onChange={(e) => set(k, e.target.value)}
                />
              </label>
            ))}
          </div>
          {criteria.length > 0 && (
            <Button
              type="button"
              variant="ghost"
              onClick={() => {
                setIndex(null);
                setParams({});
              }}
            >
              <RotateCcw size={14} />
              Reset
            </Button>
          )}
        </div>
      </div>
      <p role="status" aria-live="polite">
        {action.msg || q.error?.message}
      </p>
      {d && (
        <>
          <div className="row">
            <small>
              {d.total} {jobs ? "JOBS" : "IMAGES"}
            </small>
            <span className="mono" style={{ fontSize: "0.78rem" }}>
              Newest first
            </span>
          </div>
          {!d.total ? (
            <div className="empty">
              <ImageIcon size={28} />
              <h2>{jobs ? "Queue is clear" : "No images found"}</h2>
              <p>
                {jobs
                  ? "Unfinished uploads will appear here."
                  : "Try a different filter or send a new image to an enabled group."}
              </p>
            </div>
          ) : (
            <div className={jobs ? "queue" : "photos"}>
              {d.items.map((p, i) => {
                const groupName = name({
                  ...p.group,
                  lineGroupId: p.lineGroupId,
                });
                const canDiscard =
                  p.hasStaging &&
                  prefs.data &&
                  ["FAILED", "UNRECOVERABLE", "NEEDS_REVIEW"].includes(
                    p.uploadStatus,
                  ) &&
                  Date.now() - Date.parse(p.createdAt) >=
                    prefs.data.failedDays * 86400000;

                return jobs ? (
                  <article className="queueRow" key={p.id}>
                    <img
                      className="queueThumb"
                      loading="lazy"
                      src={base + "/images/" + p.id + "/thumbnail?size=640"}
                      alt=""
                    />
                    <div className="queueSummary" style={{ minWidth: 0 }}>
                      <div
                        style={{
                          display: "flex",
                          alignItems: "center",
                          gap: 8,
                          flexWrap: "wrap",
                        }}
                      >
                        <span className={"pill " + statusTone(p.uploadStatus)}>
                          {p.uploadStatus}
                        </span>
                        <b>{groupName}</b>
                        <time className="mono">
                          {date(p.lineEventTimestamp)}
                        </time>
                      </div>
                      {(p.errorCode || p.lastError) && (
                        <p
                          className="queueIssue"
                          style={{ margin: "6px 0 0", fontSize: "0.86rem" }}
                        >
                          {p.errorCode && (
                            <span
                              className="mono"
                              style={{ marginRight: 6, fontWeight: 600 }}
                            >
                              [{p.errorCode}]
                            </span>
                          )}
                          {p.lastError}
                        </p>
                      )}
                    </div>
                    <div className="queueActions">
                      {["FAILED", "NEEDS_REVIEW"].includes(p.uploadStatus) && (
                        <Button
                          disabled={action.busy}
                          onClick={() =>
                            action.run(() =>
                              api("/jobs/" + p.id + "/retry", "POST"),
                            )
                          }
                        >
                          {p.uploadStatus === "NEEDS_REVIEW"
                            ? "Reconcile with Google"
                            : "Retry upload"}
                        </Button>
                      )}
                      {canDiscard &&
                        (confirmDiscard === p.id ? (
                          <>
                            <Button
                              variant="secondary"
                              className="danger"
                              disabled={action.busy}
                              onClick={() => {
                                setConfirmDiscard(null);
                                void action.run(() =>
                                  api("/jobs/" + p.id + "/discard", "POST", {
                                    confirmation: "Discard staged original",
                                  }),
                                );
                              }}
                            >
                              Confirm discard
                            </Button>
                            <Button
                              variant="ghost"
                              disabled={action.busy}
                              onClick={() => setConfirmDiscard(null)}
                            >
                              Cancel
                            </Button>
                          </>
                        ) : (
                          <Button
                            variant="ghost"
                            disabled={action.busy}
                            onClick={() => setConfirmDiscard(p.id)}
                          >
                            Discard expired original
                          </Button>
                        ))}
                    </div>
                  </article>
                ) : (
                  <button
                    className="photo"
                    key={p.id}
                    onClick={() => setIndex(i)}
                  >
                    <img
                      loading="lazy"
                      src={base + "/images/" + p.id + "/thumbnail?size=640"}
                      alt={"Photo from " + groupName}
                    />
                    {p.mediaType === "VIDEO" && (
                      <span className="videoPip pill">
                        ▶ {formatDuration(p.durationMs)}
                      </span>
                    )}
                    {p.uploadStatus !== "SYNCED" && (
                      <span
                        className={
                          "photoPip pill " + statusTone(p.uploadStatus)
                        }
                      >
                        {p.uploadStatus}
                      </span>
                    )}
                    <div className="photoMeta">
                      <b>{groupName}</b>
                      {p.dayNumber != null && (
                        <span className="frameNo">#{String(p.dayNumber).padStart(3, "0")}</span>
                      )}
                    </div>
                    <time>{date(p.lineEventTimestamp)}</time>
                  </button>
                );
              })}
            </div>
          )}
          <footer>
            <Button
              variant="secondary"
              disabled={d.page <= 1}
              onClick={() => set("page", String(d.page - 1))}
            >
              Previous
            </Button>
            <span className="mono" style={{ fontSize: "0.84rem" }}>
              Page {d.page}
            </span>
            <Button
              variant="secondary"
              disabled={d.page * 36 >= d.total}
              onClick={() => set("page", String(d.page + 1))}
            >
              Next
            </Button>
          </footer>
          {index !== null && d.items[index] && (
            <Lightbox
              items={d.items}
              index={index}
              set={setIndex}
              close={() => setIndex(null)}
            />
          )}
        </>
      )}
    </>
  );
}
