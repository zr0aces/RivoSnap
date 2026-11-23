import { createHmac } from "node:crypto";
import { expect, test } from "@playwright/test";
import { mockApi, normal } from "./fixtures";

test("mobile Profile separates password MFA from passwordless passkeys", async ({
  page,
}) => {
  await page.setViewportSize({ width: 360, height: 740 });
  await mockApi(page, normal);
  await page.route("**/api/v1/profile", (route) =>
    route.fulfill({
      json: {
        email: "owner@example.org",
        passkeys: {},
        totp: false,
        recoveryRemaining: 0,
      },
    }),
  );
  await page.goto("/");
  await page.getByRole("button", { name: "More", exact: true }).click();
  await page.getByRole("link", { name: "Profile", exact: true }).click();
  await expect(
    page.getByRole("heading", { name: "Profile", exact: true }),
  ).toBeVisible();
  await page.getByRole("button", { name: "Enable authenticator" }).click();
  await expect(page.getByLabel("Current password")).toBeVisible();
});

// Explicit opt-in, fresh fixture account, localhost only. Never run against a live library.
test("disposable owner enrolls TOTP and a discoverable passkey, then signs in without a password", async ({
  page,
  context,
  browserName,
  baseURL,
}) => {
  test.skip(
    browserName !== "chromium" ||
      process.env["E2E_PROFILE_FIXTURE"] !== "1" ||
      !process.env["E2E_SETUP_KEY"] ||
      !baseURL ||
      new URL(baseURL).hostname !== "localhost",
  );
  const cdp = await context.newCDPSession(page);
  await cdp.send("WebAuthn.enable");
  await cdp.send("WebAuthn.addVirtualAuthenticator", {
    options: {
      protocol: "ctap2",
      transport: "internal",
      hasResidentKey: true,
      hasUserVerification: true,
      isUserVerified: true,
      automaticPresenceSimulation: true,
    },
  });
  const password = "temporary-profile-fixture",
    email = "profile@example.org";
  await page.goto("/profile");
  await page.getByLabel("Email", { exact: true }).fill(email);
  await page.getByLabel("Password", { exact: true }).fill(password);
  await page.getByLabel("Setup key").fill(process.env["E2E_SETUP_KEY"]!);
  await page.getByRole("button", { name: "Create owner account" }).click();
  await page.getByRole("button", { name: "Add passkey" }).click();
  await page.getByLabel("Passkey name").fill("Fixture phone");
  await page.getByLabel("Current password").fill(password);
  await page.getByRole("button", { name: "Continue", exact: true }).click();
  await page.getByRole("button", { name: "Confirm change" }).click();
  const passkey = page.getByRole("button", { name: "Sign in with a passkey" });
  await passkey.click();
  await page.getByRole("button", { name: "Enable authenticator" }).click();
  await page.getByLabel("Verify with").selectOption("passkey");
  await page.getByRole("button", { name: "Continue", exact: true }).click();
  await expect(
    page.getByAltText("Scan this QR code with your authenticator app"),
  ).toBeVisible();
  const secret = await page.locator("label code").innerText();
  const bits = [...secret]
    .map((x) =>
      "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567"
        .indexOf(x)
        .toString(2)
        .padStart(5, "0"),
    )
    .join("");
  const key = Buffer.from(bits.match(/.{8}/g)!.map((x) => parseInt(x, 2)));
  const counter = Buffer.alloc(8);
  counter.writeBigUInt64BE(BigInt(Math.floor(Date.now() / 30000)));
  const digest = createHmac("sha1", key).update(counter).digest();
  const code = String(
    (digest.readUInt32BE(digest[19] & 15) & 0x7fffffff) % 1000000,
  ).padStart(6, "0");
  await page.getByLabel("First authenticator code").fill(code);
  const recovery = await page.locator("li code").first().innerText();
  await page.getByLabel("I saved these codes").check();
  await page.getByRole("button", { name: "Confirm change" }).click();
  await expect(page.getByLabel("Password", { exact: true })).toHaveValue("");
  await passkey.click();
  await expect(page.getByText("8 unused recovery codes")).toBeVisible();
  await page.getByRole("button", { name: "Sign out", exact: true }).click();
  await page.getByLabel("Email", { exact: true }).fill(email);
  await page.getByLabel("Password", { exact: true }).fill(password);
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await page.getByLabel("Authenticator or recovery code").fill(recovery);
  await page.getByRole("button", { name: "Verify and sign in" }).click();
  await page.getByRole("link", { name: "Profile", exact: true }).click();
  await expect(page.getByText("7 unused recovery codes")).toBeVisible();
  await page.getByRole("button", { name: "Replace recovery codes" }).click();
  await page.getByLabel("Verify with").selectOption("passkey");
  await page.getByRole("button", { name: "Continue", exact: true }).click();
  await page.getByLabel("I saved these codes").check();
  const replacement = await page.locator("li code").first().innerText();
  await page.getByRole("button", { name: "Confirm change" }).click();
  await passkey.click();
  await page
    .getByRole("button", { name: "Remove passkey: Fixture phone" })
    .click();
  await page.getByLabel("Verify with").selectOption("passkey");
  await page.getByRole("button", { name: "Continue", exact: true }).click();
  await page.getByRole("button", { name: "Confirm change" }).click();
  // The authenticator still holds its local credential, but the server no longer accepts it.
  await passkey.click();
  await expect(page.getByRole("alert")).not.toBeEmpty();
  await page.getByLabel("Email", { exact: true }).fill(email);
  await page.getByLabel("Password", { exact: true }).fill(password);
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await page.getByLabel("Authenticator or recovery code").fill(replacement);
  await page.getByRole("button", { name: "Verify and sign in" }).click();
  await expect(page.getByText("7 unused recovery codes")).toBeVisible();
  await expect(
    page.getByRole("button", { name: "Remove passkey: Fixture phone" }),
  ).toHaveCount(0);
});

test("exhausted sign-in attempt returns to sign-in choices", async ({
  page,
}) => {
  await mockApi(page, normal, "login");
  await page.route("**/api/v1/auth/login", (route) =>
    route.fulfill({ json: { mfaRequired: true } }),
  );
  await page.route("**/api/v1/auth/mfa", (route) =>
    route.fulfill({
      status: 429,
      json: { error: "Too many incorrect attempts. Start sign-in again." },
    }),
  );
  await page.goto("/");
  await page.getByLabel("Email", { exact: true }).fill("owner@example.org");
  await page.getByLabel("Password", { exact: true }).fill("long-fixture-password");
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await page.getByLabel("Authenticator or recovery code").fill("000000");
  await page.getByRole("button", { name: "Verify and sign in" }).click();
  await expect(page.getByRole("alert")).toContainText(
    "Too many incorrect attempts. Start sign-in again.",
  );
  await expect(
    page.getByRole("button", { name: "Sign in with a passkey" }),
  ).toBeVisible();
  await expect(page.getByLabel("Email", { exact: true })).toBeVisible();
});

test("change form moves focus in and returns it to the trigger", async ({ page }) => {
  await mockApi(page, normal);
  await page.route("**/api/v1/profile", (route) =>
    route.fulfill({
      json: { email: "owner@example.org", passkeys: {}, totp: false, recoveryRemaining: 0 },
    }),
  );
  await page.route("**/api/v1/auth/cancel", (route) => route.fulfill({ json: { ok: true } }));
  await page.goto("/profile");
  const trigger = page.getByRole("button", { name: "Enable authenticator" });
  await trigger.focus();
  await page.keyboard.press("Enter");
  await expect(
    page.getByRole("heading", { name: "Enable authenticator", exact: true }),
  ).toBeFocused();
  await page.getByRole("button", { name: "Cancel", exact: true }).click();
  await expect(page.getByRole("button", { name: "Enable authenticator" })).toBeFocused();
});

test("Back to sign-in choices focuses the email field", async ({ page }) => {
  await mockApi(page, normal, "login");
  await page.route("**/api/v1/auth/login", (route) =>
    route.fulfill({ json: { mfaRequired: true } }),
  );
  await page.route("**/api/v1/auth/cancel", (route) => route.fulfill({ json: { ok: true } }));
  await page.goto("/");
  await page.getByLabel("Email", { exact: true }).fill("owner@example.org");
  await page.getByLabel("Password", { exact: true }).fill("long-fixture-password");
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
  await expect(page.getByLabel("Authenticator or recovery code")).toBeVisible();
  await page.getByRole("button", { name: "Back to sign-in choices" }).click();
  await expect(page.getByLabel("Email", { exact: true })).toBeFocused();
});

// A 1x1 GIF stands in for the authenticator QR code.
const qrImage = "data:image/gif;base64,R0lGODlhAQABAIAAAP///wAAACH5BAEAAAAALAAAAAABAAEAAAICRAEAOw==";

test("change preview keeps its QR and codes through a 400 and returns to the start on 429", async ({ page }) => {
  await mockApi(page, normal);
  await page.route("**/api/v1/profile", (route) =>
    route.fulfill({
      json: { email: "owner@example.org", passkeys: {}, totp: false, recoveryRemaining: 0 },
    }),
  );
  await page.route("**/api/v1/profile/start", (route) =>
    route.fulfill({
      json: {
        confirmation: "fixture-confirmation",
        qr: qrImage,
        secret: "JBSWY3DPEHPK3PXP",
        codes: ["AAAA-1111", "BBBB-2222"],
      },
    }),
  );
  let finishStatus = 400;
  await page.route("**/api/v1/profile/finish", (route) =>
    route.fulfill({
      status: finishStatus,
      json:
        finishStatus === 400
          ? { error: "Verification failed. Check your details and try again." }
          : { error: "Too many attempts. Try again later." },
    }),
  );
  await page.goto("/profile");
  await page.getByRole("button", { name: "Enable authenticator" }).click();
  await page.getByLabel("Current password").fill("long-fixture-password");
  await page.getByRole("button", { name: "Continue", exact: true }).click();
  await expect(page.getByAltText("Scan this QR code with your authenticator app")).toBeVisible();
  await expect(page.getByText("AAAA-1111")).toBeVisible();

  await page.getByLabel("First authenticator code").fill("000000");
  await page.getByLabel("I saved these codes").check();
  await page.getByRole("button", { name: "Confirm change" }).click();
  await expect(page.getByRole("alert")).toContainText("Verification failed. Check your details and try again.");
  await expect(page.getByAltText("Scan this QR code with your authenticator app")).toBeVisible();

  finishStatus = 429;
  await page.getByRole("button", { name: "Confirm change" }).click();
  await expect(page.getByRole("alert")).toContainText("Too many attempts. Try again later.");
  await expect(page.getByLabel("Current password")).toBeVisible();
  await expect(page.getByAltText("Scan this QR code with your authenticator app")).toHaveCount(0);
});
