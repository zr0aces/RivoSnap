import { useState, type FormEvent } from "react";
import {
  startAuthentication,
  startRegistration,
  browserSupportsWebAuthn,
  type PublicKeyCredentialCreationOptionsJSON,
  type PublicKeyCredentialRequestOptionsJSON,
} from "@simplewebauthn/browser";
import { Button } from "../../../packages/ui/button";
import { api, ApiError, query } from "./data";
import { Title } from "./workspace";

type Assertion = { publicKey: PublicKeyCredentialRequestOptionsJSON };
type Preview = {
  confirmation: string;
  qr?: string;
  secret?: string;
  codes?: string[];
  registration?: { publicKey: PublicKeyCredentialCreationOptionsJSON };
};
type Summary = {
  email: string;
  totp: boolean;
  passkeys: Record<string, string>;
  recoveryRemaining: number;
};
const actions = {
  enable: "Enable authenticator",
  disable: "Disable authenticator",
  replace: "Replace authenticator",
  regenerate: "Replace recovery codes",
  add: "Add passkey",
  remove: "Remove passkey",
};
type Action = keyof typeof actions;

export function passkeyAvailable() {
  return window.isSecureContext && browserSupportsWebAuthn();
}
export async function passkeyLogin() {
  const options = await api<Assertion>("/auth/passkey/start", "POST");
  const credential = await startAuthentication({
    optionsJSON: options.publicKey,
  });
  await api("/auth/passkey/finish", "POST", { credential });
}
export function Profile() {
  const profile = query<Summary>("/profile");
  const [selection, setSelection] = useState<{
    action: Action;
    target: string;
  } | null>(null);
  const owner = profile.data;
  return (
    <>
      <Title
        k="OWNER ACCOUNT"
        title="Profile"
        sub="Manage how you sign in to your private archive."
      />
      {!owner ? (
        <p role="status">
          {profile.isError
            ? "Profile could not be loaded."
            : "Loading profile…"}
        </p>
      ) : (
        <div className="profileSecurity">
          <section className="panel">
            <small>SIGNED IN AS</small>
            <h2>{owner.email}</h2>
          </section>
          {selection ? (
            <ChangeForm
              owner={owner}
              {...selection}
              close={() => setSelection(null)}
            />
          ) : (
            <>
              <section className="panel">
                <h2>Authenticator app</h2>
                <p>
                  {owner.totp
                    ? "Enabled. Password sign-in also requires an authenticator or recovery code."
                    : "Not enabled. Add a verification code to password sign-in."}
                </p>
                <Button
                  variant="secondary"
                  onClick={() =>
                    setSelection({
                      action: owner.totp ? "disable" : "enable",
                      target: "",
                    })
                  }
                >
                  {owner.totp ? actions.disable : actions.enable}
                </Button>
                {owner.totp && (
                  <>
                    <Button
                      variant="secondary"
                      onClick={() => setSelection({ action: "replace", target: "" })}
                    >
                      {actions.replace}
                    </Button>
                    <p>{owner.recoveryRemaining} unused recovery codes</p>
                    <Button
                      variant="secondary"
                      onClick={() =>
                        setSelection({ action: "regenerate", target: "" })
                      }
                    >
                      {actions.regenerate}
                    </Button>
                  </>
                )}
              </section>
              <section className="panel">
                <h2>Passkeys</h2>
                <p>
                  Sign in directly with your device. Adding a passkey does not
                  enable MFA for password sign-in.
                </p>
                {Object.entries(owner.passkeys).map(([id, name]) => (
                  <p key={id}>
                    {name}{" "}
                    <Button
                      variant="ghost"
                      aria-label={actions.remove + ": " + name}
                      onClick={() => setSelection({ action: "remove", target: id })}
                    >
                      Remove
                    </Button>
                  </p>
                ))}
                <Button
                  disabled={!passkeyAvailable()}
                  variant="secondary"
                  onClick={() => setSelection({ action: "add", target: "" })}
                >
                  Add passkey
                </Button>
                {!passkeyAvailable() && (
                  <p>
                    Passkeys require a supported browser and secure connection.
                  </p>
                )}
              </section>
            </>
          )}
        </div>
      )}
    </>
  );
}
function ChangeForm({
  owner,
  action,
  target,
  close,
}: {
  owner: Summary;
  action: Action;
  target: string;
  close: () => void;
}) {
  const [preview, setPreview] = useState<Preview | null>(null),
    [busy, setBusy] = useState(false),
    [error, setError] = useState("");
  const [method, setMethod] = useState("password");
  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const form = event.currentTarget,
      fields = new FormData(form);
    setBusy(true);
    setError("");
    try {
      if (!preview) {
        const input = {
          action,
          target: action === "add" ? fields.get("name") : target,
          method,
        };
        let next: Preview;
        if (method === "passkey") {
          const options = await api<Assertion>("/profile/start", "POST", input);
          const credential = await startAuthentication({
            optionsJSON: options.publicKey,
          });
          next = await api<Preview>("/profile/prove", "POST", { credential });
        } else {
          next = await api<Preview>("/profile/start", "POST", {
            ...input,
            password: fields.get("password"),
            code: fields.get("proof") ?? "",
          });
        }
        form.reset();
        setPreview(next);
      } else {
        const credential = preview.registration
          ? await startRegistration({
              optionsJSON: preview.registration.publicKey,
            })
          : undefined;
        await api("/profile/finish", "POST", {
          confirmation: preview.confirmation,
          credential,
          saved: fields.has("saved"),
          code: fields.get("code") ?? "",
        });
        window.location.assign("/profile");
      }
    } catch (e) {
      setError((e as Error).message);
      if (
        preview &&
        !(e instanceof ApiError && e.status === 400 && preview.secret)
      )
        setPreview(null);
    } finally {
      setBusy(false);
    }
  }
  return (
    <form className="panel" onSubmit={submit}>
      <h2>{actions[action]}</h2>
      {action === "remove" && (
        <p>Remove “{owner.passkeys[target]}” from this account?</p>
      )}
      {action === "disable" && (
        <p>
          Password sign-in will no longer need a second step. Your remaining
          recovery codes will stop working.
        </p>
      )}
      {(action === "regenerate" || action === "replace") && (
        <p>
          Your old codes will stop working after you confirm the replacement.
        </p>
      )}
      {preview ? (
        <>
          {preview.qr && (
            <>
              <img
                width="240"
                height="240"
                src={preview.qr}
                alt="Scan this QR code with your authenticator app"
              />
              <label>
                Manual setup key <code>{preview.secret}</code>
              </label>
              <label>
                First authenticator code
                <input
                  autoFocus
                  required
                  name="code"
                  inputMode="numeric"
                  pattern="[0-9]{6}"
                  autoComplete="one-time-code"
                />
              </label>
            </>
          )}
          {preview.codes && (
            <>
              <h3>Save your recovery codes</h3>
              <p>Store these offline. Each works once, after your password.</p>
              <ul>
                {preview.codes.map((code) => (
                  <li key={code}>
                    <code>{code}</code>
                  </li>
                ))}
              </ul>
              <label>
                <input name="saved" type="checkbox" required /> I saved these
                codes
              </label>
            </>
          )}
          <p>After you confirm this change, sign in again.</p>
        </>
      ) : (
        <>
          {action === "add" && (
            <label>
              Passkey name
              <input
                name="name"
                required
                maxLength={80}
                placeholder="e.g. Personal phone"
              />
            </label>
          )}
          <p>Verify your identity to make this change.</p>
          {Object.keys(owner.passkeys).length > 0 && passkeyAvailable() && (
            <label>
              Verify with
              <select
                value={method}
                onChange={(e) => setMethod(e.target.value)}
              >
                <option value="password">Password</option>
                <option value="passkey">Existing passkey</option>
              </select>
            </label>
          )}
          {method === "password" && (
            <>
              <label>
                Current password
                <input
                  type="password"
                  name="password"
                  required
                  autoComplete="current-password"
                />
              </label>
              {owner.totp && (
                <label>
                  Authenticator or recovery code
                  <input name="proof" required autoComplete="one-time-code" />
                </label>
              )}
            </>
          )}
        </>
      )}
      <div>
        <Button disabled={busy}>
          {busy ? "Working…" : preview ? "Confirm change" : "Continue"}
        </Button>{" "}
        <Button
          type="button"
          variant="ghost"
          disabled={busy}
          onClick={async () => {
            setBusy(true);
            try {
              await api("/auth/cancel", "POST");
              close();
            } catch (e) {
              setError((e as Error).message);
              setBusy(false);
            }
          }}
        >
          Cancel
        </Button>
      </div>
      <p role="alert">{error}</p>
    </form>
  );
}
