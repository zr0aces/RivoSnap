import React, { useState, useEffect } from "react";
import { createRoot } from "react-dom/client";
import { BrowserRouter, NavLink, Routes, Route, Link } from "react-router-dom";
import {
  QueryClient,
  QueryClientProvider,
  useQueryClient,
} from "@tanstack/react-query";
import { useForm } from "react-hook-form";
import {
  LayoutDashboard,
  Images,
  Layers,
  Users,
  Settings as Gear,
  CloudUpload,
  LogOut,
  Moon,
  Sun,
  ArrowUpRight,
} from "lucide-react";
import { Button } from "../../../packages/ui/button";
import { api, query, labels, type Stats } from "./data";
import { Dashboard, Groups, Settings, Health, healthStatus } from "./workspace";
import { Library } from "./photos";
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
];

const healthLabels: Record<string, string> = {
  database: "Database",
  redis: "Redis",
  worker: "Worker",
  diskPressure: "Disk",
};

function SidebarHealth({ mobile = false }: { mobile?: boolean }) {
  const q = query<Record<string, string | boolean>>("/health");
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
  const { register, handleSubmit, formState } = useForm<{
    email: string;
    password: string;
    bootstrap: string;
  }>();
  const [error, set] = useState("");
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
        onSubmit={handleSubmit(async (data) => {
          try {
            await api("/auth/" + (setup ? "setup" : "login"), "POST", data);
            await client.invalidateQueries();
          } catch (e) {
            set((e as Error).message);
          }
        })}
      >
        <small>OWNER ACCESS</small>
        <h2>{setup ? "Create your account" : "Welcome back"}</h2>
        {setup && (
          <p>Use the setup key from your deployment to claim this library.</p>
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
            {...register("password")}
          />
          {setup && <small>Minimum 12 characters</small>}
        </label>
        {setup && (
          <label>
            Setup key
            <input type="password" required {...register("bootstrap")} />
          </label>
        )}
        <Button disabled={formState.isSubmitting}>
          {setup ? "Create owner account" : "Sign in"}{" "}
          <ArrowUpRight size={16} />
        </Button>
        <p role="alert">{error}</p>
      </form>
    </main>
  );
}

function App() {
  const [logoutFailed, setLogoutFailed] = useState(false);
  const [dark, theme] = useState(
    () => localStorage.getItem("gallery-theme") === "dark",
  );
  useEffect(() => {
    document.documentElement.dataset.theme = dark ? "dark" : "light";
    localStorage.setItem("gallery-theme", dark ? "dark" : "light");
  }, [dark]);

  const auth = query<{ setup: boolean; authenticated: boolean }>(
      "/auth/status",
    ),
    qc = useQueryClient();
  // Gated on auth: api() reloads the page on a 401, which would loop on the login screen.
  const stats = query<Stats>(
    "/dashboard/stats",
    auth.data?.authenticated === true,
  ).data;

  if (auth.error)
    return (
      <main className="authState">
        <p role="alert">Cannot reach the gallery. Check the API connection.</p>
        <Button onClick={() => void auth.refetch()}>Try again</Button>
      </main>
    );
  if (!auth.data) return <main className="authState" role="status">Opening library…</main>;
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
      <aside>
        <div className="asideTop">
          <Brand />
          <div className="mobileActions">
            <SidebarHealth mobile />
            <Button
              variant="ghost"
              aria-label="Dark theme"
              aria-pressed={dark}
              onClick={() => theme(!dark)}
            >
              {dark ? <Sun size={16} /> : <Moon size={16} />}
            </Button>
            <Button variant="ghost" aria-label="Sign out" onClick={signOut}>
              <LogOut size={16} />
            </Button>
          </div>
        </div>
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
          <SidebarHealth />
          <small className="appVersion">v{__APP_VERSION__}</small>
          <Button variant="ghost" onClick={signOut}>
            <LogOut size={16} />
            Sign out
          </Button>
        </div>
      </aside>
      <main className="workspace">
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
          <Button
            className="desktopOnly"
            variant="ghost"
            aria-label="Dark theme"
            aria-pressed={dark}
            onClick={() => theme(!dark)}
          >
            {dark ? <Sun size={16} /> : <Moon size={16} />}
          </Button>
        </header>
        {logoutFailed && <p role="alert">Sign-out could not be confirmed. Try again.</p>}
        <Routes>
          <Route index element={<Dashboard />} />
          <Route path="gallery" element={<Library key="gallery" />} />
          <Route path="jobs" element={<Library jobs key="jobs" />} />
          <Route path="groups" element={<Groups />} />
          <Route path="albums" element={<Groups albums />} />
          <Route path="settings" element={<Settings />} />
          <Route path="health" element={<Health />} />
          <Route path="*" element={<Link className="fallbackLink" to="/">Return to dashboard</Link>} />
        </Routes>
      </main>
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
