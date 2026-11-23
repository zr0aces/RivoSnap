import React, {
  useState,
  useEffect,
  useLayoutEffect,
  useRef,
  useSyncExternalStore,
} from "react";
import { createRoot } from "react-dom/client";
import {
  BrowserRouter,
  NavLink,
  Routes,
  Route,
  Link,
  useLocation,
} from "react-router-dom";
import {
  QueryClient,
  QueryClientProvider,
  useQueryClient,
  type UseQueryResult,
} from "@tanstack/react-query";
import { useForm } from "react-hook-form";
import {
  UserRound,
  LayoutDashboard,
  Images,
  Layers,
  Users,
  Settings as Gear,
  CloudUpload,
  HeartPulse,
  MoreHorizontal,
  X,
  LogOut,
  Moon,
  Sun,
  ArrowUpRight,
} from "lucide-react";
import { Button } from "../../../packages/ui/button";
import { api, ApiError, query, labels, type Stats } from "./data";
import {
  AlbumsView,
  Dashboard,
  Groups,
  Settings,
  Health,
  healthLabels,
  healthStatus,
} from "./workspace";
import { Library } from "./photos";
import { Profile, passkeyAvailable, passkeyLogin } from "./profile";
import "./style.css";

const navSections = [
  {
    title: "LIBRARY",
    items: [
      ["dashboard", "/", LayoutDashboard],
      ["gallery", "/gallery", Images],
      ["albums", "/albums", Layers],
    ] as const,
  },
  {
    title: "PIPELINE",
    items: [
      ["groups", "/groups", Users],
      ["jobs", "/jobs", CloudUpload],
      ["settings", "/settings", Gear],
    ] as const,
  },
  { title: "ACCOUNT", items: [["profile", "/profile", UserRound]] as const },
];

const compactPrimary = [
  ["Home", "/", LayoutDashboard],
  ["Gallery", "/gallery", Images],
  ["Albums", "/albums", Layers],
] as const;

const secondaryDestinations = [
  ["jobs", "/jobs", CloudUpload],
  ["groups", "/groups", Users],
  ["health", "/health", HeartPulse],
  ["settings", "/settings", Gear],
  ["profile", "/profile", UserRound],
] as const;

const secondaryPaths = new Set<string>(
  secondaryDestinations.map(([, to]) => to),
);

const COMPACT_MEDIA = "(max-width: 820px)";

function subscribeCompact(cb: () => void) {
  const mql = window.matchMedia(COMPACT_MEDIA);
  mql.addEventListener("change", cb);
  return () => mql.removeEventListener("change", cb);
}

function useCompactShell() {
  return useSyncExternalStore(
    subscribeCompact,
    () => window.matchMedia(COMPACT_MEDIA).matches,
    () => false,
  );
}

type HealthQuery = UseQueryResult<Record<string, string | boolean>>;

function summarizeQueue(stats?: Stats) {
  if (!stats) return { count: 0, text: "", active: false };
  const { failed, pending } = stats;
  const count = failed + pending;
  if (count <= 0) return { count: 0, text: "", active: false };
  const parts: string[] = [];
  if (failed > 0) parts.push(`${failed} failed`);
  if (pending > 0) parts.push(`${pending} pending`);
  return { count, text: parts.join(", "), active: true };
}

function summarizeHealth(q: HealthQuery) {
  const entries = Object.entries(q.data ?? {}).filter(
    ([k]) => k !== "workerLastProgressAt",
  );
  if (q.error) return { text: "Unavailable", alarm: true };
  if (q.isLoading && !q.data) return { text: "Checking…", alarm: false };
  if (entries.length === 0) return { text: "Unknown", alarm: false };
  if (entries.some(([k, v]) => healthStatus(k, v) !== "ok"))
    return { text: "Needs attention", alarm: true };
  return { text: "", alarm: false };
}

function SidebarHealth({
  q,
  mobile = false,
}: {
  q: HealthQuery;
  mobile?: boolean;
}) {
  const entries = Object.entries(q.data ?? {}).filter(
    ([k]) => k !== "workerLastProgressAt",
  );
  if (mobile) {
    return (
      <Link
        to="/health"
        className="mobileHealth"
        aria-label="System health"
        title="System health"
      >
        Health
        <span className="mobileHealthDots">
          {entries.length ? (
            entries.map(([k, v]) => (
              <span
                key={k}
                className={"healthDot " + healthStatus(k, v)}
                title={`${healthLabels[k] ?? k}: ${String(v)}`}
              />
            ))
          ) : (
            <span className={"healthDot " + (q.error ? "err" : "warn")} />
          )}
        </span>
      </Link>
    );
  }
  return (
    <div className="sidebarHealth">
      <div className="sidebarHealthHead">
        <Link to="/health">System health</Link>
        <ArrowUpRight size={13} />
      </div>
      {q.error ? (
        <div className="healthMiniRow">
          <span className="healthDot err" />
          <span>API unreachable</span>
        </div>
      ) : entries.length ? (
        entries.map(([k, v]) => {
          const state = healthStatus(k, v);
          const display =
            k === "diskPressure" ? (v ? "Pressured" : "Normal") : String(v);
          return (
            <div className="healthMiniRow" key={k}>
              <span className={"healthDot " + state} />
              <span>{healthLabels[k] ?? k}</span>
              <span className="mono">{display}</span>
            </div>
          );
        })
      ) : (
        <div className="healthMiniRow">
          <span className="healthDot warn" />
          <span>Checking…</span>
        </div>
      )}
    </div>
  );
}

const client = new QueryClient({ defaultOptions: { queries: { retry: 1 } } });

declare const __APP_VERSION__: string;

function Brand() {
  return (
    <div className="brand">
      <img src="/rivosnap-mark.svg" width="28" height="28" alt="" />
      RivoSnap
    </div>
  );
}

function Login({ setup }: { setup: boolean }) {
  const { register, handleSubmit, formState, reset, setFocus } = useForm<{
    email: string;
    password: string;
    bootstrap: string;
  }>();
  const [error, set] = useState("");
  const [mfaPending, setMfaPending] = useState(false),
    [code, setCode] = useState(""),
    [busy, setBusy] = useState(false);
  const wasMfaPending = useRef(false);
  useEffect(() => {
    // The email input mounts again on the render after MFA ends, so focus it from an effect.
    if (wasMfaPending.current && !mfaPending) setFocus("email");
    wasMfaPending.current = mfaPending;
  }, [mfaPending, setFocus]);
  return (
    <main className="entry">
      <div className="entrySide">
        <Brand />
        <div>
          <small>PRIVATE PHOTO ARCHIVE</small>
          <h1>
            Your groups.
            <br />
            One contact sheet.
          </h1>
          <p>
            A private home for the photos your LINE groups send to Google
            Photos — staged safely, never silently dropped.
          </p>
        </div>
        <div className="contactMotif" aria-hidden="true">
          <div className="contactCell" />
          <div className="contactCell lit" />
          <div className="contactCell" />
          <div className="contactCell lit" />
          <div className="contactCell" />
          <div className="contactCell lit" />
        </div>
        <span className="mono">LINE → Staging → Google Photos</span>
      </div>
      <form
        onSubmit={
          mfaPending
            ? async (event) => {
                event.preventDefault();
                setBusy(true);
                set("");
                try {
                  await api("/auth/mfa", "POST", { code });
                  await client.invalidateQueries();
                } catch (e) {
                  set((e as Error).message);
                  if (e instanceof ApiError && (e.status === 410 || e.status === 429))
                    setMfaPending(false);
                } finally {
                  setCode("");
                  setBusy(false);
                }
              }
            : handleSubmit(async (data) => {
                try {
                  set("");
                  const result = await api<{ mfaRequired?: boolean }>(
                    "/auth/" + (setup ? "setup" : "login"),
                    "POST",
                    data,
                  );
                  if (result.mfaRequired) {
                    reset();
                    setMfaPending(true);
                    return;
                  }
                  await client.invalidateQueries();
                } catch (e) {
                  set((e as Error).message);
                }
              })
        }
      >
        <small>OWNER ACCESS</small>
        <h2>{setup ? "Create your account" : "Welcome back"}</h2>
        {setup && (
          <p>Use the setup key from your deployment to claim this library.</p>
        )}
        {mfaPending ? (
          <>
            <label>
              Authenticator or recovery code
              <input
                autoFocus
                required
                value={code}
                onChange={(e) => setCode(e.target.value)}
                autoComplete="one-time-code"
              />
            </label>
            <Button disabled={busy}>Verify and sign in</Button>
            <Button
              type="button"
              variant="ghost"
              disabled={busy}
              onClick={async () => {
                try {
                  await api("/auth/cancel", "POST");
                  setMfaPending(false);
                  setCode("");
                  set("");
                } catch (e) {
                  set((e as Error).message);
                }
              }}
            >
              Back to sign-in choices
            </Button>
          </>
        ) : (
          <>
            {!setup && (
              <>
                <Button
                  type="button"
                  variant="secondary"
                  disabled={
                    busy || formState.isSubmitting || !passkeyAvailable()
                  }
                  onClick={async () => {
                    setBusy(true);
                    set("");
                    try {
                      await passkeyLogin();
                      await client.invalidateQueries();
                    } catch (e) {
                      try {
                        await api("/auth/cancel", "POST");
                      } catch {}
                      set((e as Error).message);
                    } finally {
                      setBusy(false);
                    }
                  }}
                >
                  Sign in with a passkey
                </Button>
                <small>
                  {passkeyAvailable()
                    ? "Or use your password"
                    : "Passkeys are unavailable in this browser or connection. Use your password."}
                </small>
              </>
            )}
            <label>
              Email
              <input
                required
                autoComplete="username"
                type="email"
                {...register("email")}
              />
            </label>
            <label>
              Password
              <input
                required
                type="password"
                autoComplete={setup ? "new-password" : "current-password"}
                minLength={setup ? 12 : 1}
                aria-describedby={setup ? "password-help" : undefined}
                {...register("password")}
              />
            </label>
            {setup && <small id="password-help">Minimum 12 characters</small>}
            {setup && (
              <label>
                Setup key
                <input type="password" required {...register("bootstrap")} />
              </label>
            )}
            <Button disabled={formState.isSubmitting || busy}>
              {setup ? "Create owner account" : "Sign in"}{" "}
              <ArrowUpRight size={16} />
            </Button>
          </>
        )}
        <p role="alert">{error}</p>
      </form>
    </main>
  );
}

function CompactShellNav({
  stats,
  healthQuery,
  dark,
  onToggleDark,
  onSignOut,
  logoutFailed,
  moreOpen,
  setMoreOpen,
  workspaceRef,
}: {
  stats?: Stats;
  healthQuery: HealthQuery;
  dark: boolean;
  onToggleDark: () => void;
  onSignOut: () => void;
  logoutFailed: boolean;
  moreOpen: boolean;
  setMoreOpen: React.Dispatch<React.SetStateAction<boolean>>;
  workspaceRef: React.RefObject<HTMLElement | null>;
}) {
  const { pathname, key } = useLocation();
  const barRef = useRef<HTMLElement>(null);
  const moreBtnRef = useRef<HTMLButtonElement>(null);
  const dialogRef = useRef<HTMLDialogElement>(null);
  const closeBtnRef = useRef<HTMLButtonElement>(null);
  const prevKeyRef = useRef(key);

  const queueSummary = summarizeQueue(stats);
  const healthSummary = summarizeHealth(healthQuery);
  const hasDot = queueSummary.active || healthSummary.alarm;
  const isSecondary = secondaryPaths.has(pathname);

  const descParts: string[] = [];
  if (queueSummary.text) descParts.push(`Upload queue: ${queueSummary.text}`);
  if (healthSummary.text) descParts.push(`System health: ${healthSummary.text}`);
  const moreDesc = descParts.join("; ");

  useLayoutEffect(() => {
    const bar = barRef.current;
    if (!bar) return;
    const sync = () => {
      document.documentElement.style.setProperty(
        "--mobile-nav-height",
        `${Math.ceil(bar.getBoundingClientRect().height)}px`,
      );
    };
    sync();
    const ro = new ResizeObserver(sync);
    ro.observe(bar);
    return () => {
      ro.disconnect();
      document.documentElement.style.removeProperty("--mobile-nav-height");
    };
  }, []);

  useEffect(() => {
    const dialog = dialogRef.current;
    if (!dialog) return;
    if (moreOpen) {
      if (!dialog.open) dialog.showModal();
      closeBtnRef.current?.focus({ preventScroll: true });
    } else if (dialog.open) {
      dialog.close();
    }
  }, [moreOpen]);

  useEffect(() => {
    if (prevKeyRef.current !== key) {
      prevKeyRef.current = key;
      if (moreOpen) setMoreOpen(false);
    }
  }, [key, moreOpen, setMoreOpen]);

  const closeToTrigger = () => {
    const dialog = dialogRef.current;
    if (dialog?.open) dialog.close();
    setMoreOpen(false);
    requestAnimationFrame(() => {
      moreBtnRef.current?.focus({ preventScroll: true });
    });
  };

  const navigateFromSheet = () => {
    const dialog = dialogRef.current;
    if (dialog?.open) dialog.close();
    setMoreOpen(false);
    requestAnimationFrame(() => {
      workspaceRef.current?.focus({ preventScroll: true });
    });
  };

  return (
    <>
      <nav ref={barRef} className="mobileNav" aria-label="Primary">
        {compactPrimary.map(([label, to, Icon]) => (
          <NavLink
            key={to}
            end={to === "/"}
            to={to}
            className="mobileNavItem"
          >
            <span className="mobileNavIconWrap">
              <Icon size={18} aria-hidden="true" />
            </span>
            <span>{label}</span>
          </NavLink>
        ))}
        <button
          ref={moreBtnRef}
          type="button"
          className="mobileNavItem"
          data-active={isSecondary ? "true" : undefined}
          aria-haspopup="dialog"
          aria-expanded={moreOpen}
          aria-controls="more-sheet"
          aria-describedby={moreDesc ? "more-nav-status" : undefined}
          onClick={() => setMoreOpen(true)}
        >
          <span className="mobileNavIconWrap">
            <MoreHorizontal size={18} aria-hidden="true" />
            {hasDot && <span className="mobileNavDot" aria-hidden="true" />}
          </span>
          <span>More</span>
        </button>
      </nav>
      {moreDesc && (
        <span id="more-nav-status" className="srOnly">
          {moreDesc}
        </span>
      )}
      <dialog
        ref={dialogRef}
        id="more-sheet"
        className="moreSheet"
        aria-labelledby="more-sheet-title"
        onKeyDown={(event) => {
          if (event.key !== "Tab") return;
          const controls = event.currentTarget.querySelectorAll<HTMLElement>(
            "button:not(:disabled), a[href]",
          );
          const first = controls[0],
            last = controls[controls.length - 1];
          const next =
            event.shiftKey && event.target === first
              ? last
              : !event.shiftKey && event.target === last
                ? first
                : null;
          if (next) {
            event.preventDefault();
            next.focus();
          }
        }}
        onCancel={(e) => {
          e.preventDefault();
          closeToTrigger();
        }}
        onClick={(e) => {
          if (e.target === dialogRef.current) closeToTrigger();
        }}
      >
        <div className="moreSheetInner">
          <div className="moreSheetHead">
            <h2 id="more-sheet-title">More</h2>
            <button
              ref={closeBtnRef}
              type="button"
              className="button ghost"
              aria-label="Close"
              onClick={closeToTrigger}
            >
              <X size={18} aria-hidden="true" />
            </button>
          </div>
          <div className="moreSheetLinks">
            {secondaryDestinations.map(([k, to, Icon]) => {
              const rowStatus =
                k === "jobs"
                  ? queueSummary.text
                  : k === "health"
                    ? healthSummary.text
                    : "";
              const rowAlarm =
                (k === "jobs" && queueSummary.active) ||
                (k === "health" && healthSummary.alarm);
              return (
                <NavLink
                  key={k}
                  end
                  to={to}
                  className="moreSheetLink"
                  onClick={navigateFromSheet}
                >
                  <Icon size={18} aria-hidden="true" />
                  <span>{labels[k]}</span>
                  {rowStatus && (
                    <span className={"moreRowMeta" + (rowAlarm ? " alarm" : "")}>
                      {rowStatus}
                    </span>
                  )}
                </NavLink>
              );
            })}
          </div>
          <hr className="moreSheetDivider" />
          <div className="moreSheetActions">
            <Button
              variant="secondary"
              aria-pressed={dark}
              onClick={onToggleDark}
            >
              {dark ? (
                <Sun size={16} aria-hidden="true" />
              ) : (
                <Moon size={16} aria-hidden="true" />
              )}
              <span>Dark theme</span>
            </Button>
            <Button variant="ghost" onClick={onSignOut}>
              <LogOut size={16} aria-hidden="true" />
              <span>Sign out</span>
            </Button>
          </div>
          {logoutFailed && (
            <p className="moreSheetError" role="alert">
              Sign-out could not be confirmed. Try again.
            </p>
          )}
        </div>
      </dialog>
    </>
  );
}

// Storage can be blocked (private modes, strict browser settings). The app then runs without a saved theme.
function readTheme(): boolean {
  try {
    return localStorage.getItem("gallery-theme") === "dark";
  } catch {
    return false;
  }
}

function saveTheme(dark: boolean) {
  try {
    localStorage.setItem("gallery-theme", dark ? "dark" : "light");
  } catch {
    // The choice lasts for this page only.
  }
}

function App() {
  const [logoutFailed, setLogoutFailed] = useState(false);
  const [moreOpen, setMoreOpen] = useState(false);
  const [dark, theme] = useState(readTheme);
  const compact = useCompactShell();
  const prevCompactRef = useRef(compact);
  const asideRef = useRef<HTMLElement>(null);
  const workspaceRef = useRef<HTMLElement>(null);
  const { pathname } = useLocation();

  // Layout effect: the theme is set before the browser paints the shell.
  useLayoutEffect(() => {
    document.documentElement.dataset.theme = dark ? "dark" : "light";
    saveTheme(dark);
  }, [dark]);

  useEffect(() => {
    const wasCompact = prevCompactRef.current;
    prevCompactRef.current = compact;
    if (wasCompact && !compact && moreOpen) {
      setMoreOpen(false);
      requestAnimationFrame(() => {
        const aside = asideRef.current;
        const target =
          aside?.querySelector<HTMLAnchorElement>(`a[href="${pathname}"]`) ??
          workspaceRef.current;
        target?.focus({ preventScroll: true });
      });
    }
  }, [compact, moreOpen, pathname]);

  const auth = query<{ setup: boolean; authenticated: boolean }>(
      "/auth/status",
    ),
    qc = useQueryClient(),
    authenticated = auth.data?.authenticated === true;
  // Gated on auth: api() reloads the page on a 401, which would loop on the login screen.
  const stats = query<Stats>("/dashboard/stats", authenticated).data;
  const healthQuery = query<Record<string, string | boolean>>(
    "/health",
    authenticated,
  );

  // Cached status keeps the open workspace (forms, lightbox, MFA step) through a failed refresh.
  if (auth.error && !auth.data)
    return (
      <main className="authState">
        <p role="alert">Cannot reach the gallery. Check the API connection.</p>
        <Button onClick={() => void auth.refetch()}>Try again</Button>
      </main>
    );
  if (!auth.data)
    return (
      <main className="authState" role="status">
        Opening library…
      </main>
    );
  if (!auth.data.authenticated) return <Login setup={auth.data.setup} />;

  const signOut = async () => {
    setLogoutFailed(false);
    try {
      await api("/auth/logout", "POST");
    } catch {
      setLogoutFailed(true);
      return;
    }
    qc.clear();
    location.assign("/");
  };
  const attentionCount = (stats?.failed ?? 0) + (stats?.pending ?? 0);

  return (
    <div className="shell">
      <aside ref={asideRef}>
        <div className="asideTop">
          <Brand />
          {compact && (
            <div className="mobileActions">
              <SidebarHealth q={healthQuery} mobile />
            </div>
          )}
        </div>
        {!compact && (
          <>
            <nav aria-label="Primary">
              {navSections.map((sec) => (
                <div className="navGroup" key={sec.title}>
                  <small>{sec.title}</small>
                  {sec.items.map(([k, to, Icon]) => (
                    <NavLink end key={k} to={to}>
                      <Icon size={17} />
                      <span>{labels[k]}</span>
                      {k === "jobs" && attentionCount > 0 && (
                        <span className="navBadge">{attentionCount}</span>
                      )}
                    </NavLink>
                  ))}
                </div>
              ))}
            </nav>
            <div className="asideFooter">
              <SidebarHealth q={healthQuery} />
              <small className="appVersion">v{__APP_VERSION__}</small>
              <Button variant="ghost" onClick={signOut}>
                <LogOut size={16} />
                Sign out
              </Button>
            </div>
          </>
        )}
      </aside>
      <main className="workspace" ref={workspaceRef} tabIndex={-1}>
        <header>
          <span className="dot" />
          <span>Private workspace</span>
          {stats?.google && (
            <span
              className={
                "pill " + (stats.google === "CONNECTED" ? "ok" : "warn")
              }
            >
              Google {stats.google.toLowerCase()}
            </span>
          )}
          <span className="tz">Asia/Bangkok</span>
          {!compact && (
            <Button
              className="desktopOnly"
              variant="ghost"
              aria-label="Dark theme"
              aria-pressed={dark}
              onClick={() => theme(!dark)}
            >
              {dark ? <Sun size={16} /> : <Moon size={16} />}
            </Button>
          )}
        </header>
        {!compact && logoutFailed && (
          <p role="alert">Sign-out could not be confirmed. Try again.</p>
        )}
        <Routes>
          <Route index element={<Dashboard />} />
          <Route path="gallery" element={<Library key="gallery" />} />
          <Route path="jobs" element={<Library jobs key="jobs" />} />
          <Route path="groups" element={<Groups />} />
          <Route path="albums" element={<AlbumsView />} />
          <Route path="profile" element={<Profile />} />
          <Route path="settings" element={<Settings />} />
          <Route path="health" element={<Health />} />
          <Route
            path="*"
            element={
              <Link className="fallbackLink" to="/">
                Return to dashboard
              </Link>
            }
          />
        </Routes>
      </main>
      {compact && (
        <CompactShellNav
          stats={stats}
          healthQuery={healthQuery}
          dark={dark}
          onToggleDark={() => theme(!dark)}
          onSignOut={signOut}
          logoutFailed={logoutFailed}
          moreOpen={moreOpen}
          setMoreOpen={setMoreOpen}
          workspaceRef={workspaceRef}
        />
      )}
    </div>
  );
}

createRoot(document.getElementById("root")!).render(
  <React.StrictMode>
    <QueryClientProvider client={client}>
      <BrowserRouter>
        <App />
      </BrowserRouter>
    </QueryClientProvider>
  </React.StrictMode>,
);
