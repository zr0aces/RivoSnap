import { useState } from "react";
import { Link } from "react-router-dom";
import { Check, ChevronDown, ChevronRight, Copy } from "lucide-react";
import { Button } from "../../../packages/ui/button";
import type { Stats, Settings, Group } from "./data";

async function copyText(text: string, onDone: () => void) {
  try {
    if (navigator.clipboard?.writeText) {
      await navigator.clipboard.writeText(text);
      onDone();
    }
  } catch {
    // LAN HTTP contexts may block clipboard API
  }
}

export function Onboarding({
  stats,
  prefs,
  groups,
}: {
  stats: Stats;
  prefs?: Settings;
  groups: Group[];
}) {
  const phases = [
    {
      title: "1. Accounts & credentials",
      steps: [
        {
          title: "Owner account created",
          desc: "Signed in as the sole library owner.",
          to: "/",
          cta: "Dashboard",
          done: true,
        },
        {
          title: "Configure LINE credentials",
          desc: "Enter the access token and channel secret for your LINE Official Account.",
          to: "/settings",
          cta: "Open settings",
          done: !!prefs?.lineConfigured,
        },
        {
          title: "Register webhook URL",
          desc: "Paste into LINE Developers, verify, and enable webhooks + redelivery.",
          to: "/settings",
          cta: "View settings",
          done: groups.length > 0,
          webhook: prefs?.webhook,
        },
        {
          title: "Connect Google Photos",
          desc: "Authorize both Photos permissions for the destination account.",
          to: "/settings",
          cta: "Connect account",
          done: stats.google === "CONNECTED",
        },
      ],
    },
    {
      title: "2. Group participation",
      steps: [
        {
          title: "Invite bot to a LINE group",
          desc: "Enable group participation in LINE Developers and invite the Official Account.",
          to: "/groups",
          cta: "View groups",
          done: groups.length > 0,
        },
        {
          title: "Enable collection",
          desc: "Enable only groups whose new photos belong in your personal archive.",
          to: "/groups",
          cta: "Manage groups",
          done: stats.groups > 0,
        },
      ],
    },
    {
      title: "3. First sync verification",
      steps: [
        {
          title: "Send a test image",
          desc: prefs?.collectVideos
            ? "Post a new image or video in an enabled group (old messages and DMs are skipped)."
            : "Post a new image in an enabled group (old messages, videos, and DMs are skipped).",
          to: "/jobs",
          cta: "Monitor queue",
          done: stats.total > 0,
        },
        {
          title: "Verify upload & open gallery",
          desc: "Wait for SYNCED status, then inspect the photo in your private gallery.",
          to: stats.synced > 0 ? "/gallery" : "/jobs",
          cta: stats.synced > 0 ? "Open gallery" : "Inspect queue",
          done: stats.synced > 0,
        },
      ],
    },
  ];

  const firstOpen = phases.findIndex((p) => p.steps.some((s) => !s.done));
  const [openPhase, setOpenPhase] = useState<number>(
    firstOpen < 0 ? 2 : firstOpen,
  );
  const [copied, setCopied] = useState(false);

  const completedSteps = phases.flatMap((p) => p.steps).filter((s) => s.done).length;
  const totalSteps = phases.flatMap((p) => p.steps).length;

  return (
    <section className="panel">
      <div className="railHead">
        <small>FIRST COLLECTION CHECKLIST</small>
        <span className="pill ok">
          {completedSteps} / {totalSteps} ready
        </span>
      </div>
      <h2>Setup pipeline</h2>
      <div className="checklist">
        {phases.map((phase, idx) => {
          const allDone = phase.steps.every((s) => s.done);
          const isOpen = openPhase === idx;
          return (
            <div
              key={phase.title}
              className={"phase" + (isOpen ? " active" : "")}
            >
              <button
                type="button"
                className="phaseHead"
                aria-expanded={isOpen}
                onClick={() => setOpenPhase(isOpen ? -1 : idx)}
              >
                <span>{phase.title}</span>
                <span style={{ display: "inline-flex", alignItems: "center", gap: 8 }}>
                  <span className={"pill " + (allDone ? "ok" : "warn")}>
                    {allDone ? "Done" : "In progress"}
                  </span>
                  {isOpen ? <ChevronDown size={16} /> : <ChevronRight size={16} />}
                </span>
              </button>
              {isOpen && (
                <div className="phaseBody">
                  {phase.steps.map((s) => (
                    <div
                      key={s.title}
                      className={"stepItem" + (s.done ? " done" : "")}
                    >
                      <div>
                        <b>
                          {s.done && <Check size={13} style={{ display: "inline", marginRight: 5 }} />}
                          {s.title}
                        </b>
                        <p>{s.desc}</p>
                        {s.webhook && (
                          <div className="codeRow">
                            <code>{s.webhook}</code>
                            <Button
                              type="button"
                              variant="secondary"
                              onClick={() =>
                                copyText(s.webhook!, () => {
                                  setCopied(true);
                                  setTimeout(() => setCopied(false), 1800);
                                })
                              }
                            >
                              <Copy size={13} />
                              {copied ? "Copied" : "Copy"}
                            </Button>
                          </div>
                        )}
                      </div>
                      <Link to={s.to}>
                        {s.cta} ↗
                      </Link>
                    </div>
                  ))}
                </div>
              )}
            </div>
          );
        })}
      </div>
    </section>
  );
}
