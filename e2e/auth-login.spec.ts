import { expect, test } from "@playwright/test";
import { mockApi, normal, statusSequence } from "./fixtures";

const passkeyOptions = {
  publicKey: {
    challenge: "AAAAAAAAAAAAAAAAAAAAAA",
    rpId: "127.0.0.1",
    allowCredentials: [],
    userVerification: "preferred",
    timeout: 60000,
  },
};

async function submitPassword(page: import("@playwright/test").Page) {
  await page.getByLabel("Email", { exact: true }).fill("owner@example.org");
  await page.getByLabel("Password", { exact: true }).fill("long-fixture-password");
  await page.getByRole("button", { name: "Sign in", exact: true }).click();
}

test("correct authenticator code ends on the dashboard", async ({ page }) => {
  await mockApi(page, normal);
  await statusSequence(page, [
    { status: 200, body: { authenticated: false, setup: false } },
    { status: 200, body: { authenticated: true, setup: false } },
  ]);
  await page.route("**/api/v1/auth/login", (route) =>
    route.fulfill({ json: { mfaRequired: true } }),
  );
  await page.route("**/api/v1/auth/mfa", (route) => route.fulfill({ json: { ok: true } }));
  await page.goto("/");
  await submitPassword(page);
  await page.getByLabel("Authenticator or recovery code").fill("123456");
  await page.getByRole("button", { name: "Verify and sign in" }).click();
  await expect(page.getByRole("heading", { name: "Dashboard" })).toBeVisible();
});

test("expired verification returns to sign-in choices", async ({ page }) => {
  await mockApi(page, normal, "login");
  await page.route("**/api/v1/auth/login", (route) =>
    route.fulfill({ json: { mfaRequired: true } }),
  );
  await page.route("**/api/v1/auth/mfa", (route) =>
    route.fulfill({
      status: 410,
      json: { error: "Verification expired. Start again." },
    }),
  );
  await page.goto("/");
  await submitPassword(page);
  await page.getByLabel("Authenticator or recovery code").fill("123456");
  await page.getByRole("button", { name: "Verify and sign in" }).click();
  await expect(page.getByRole("alert")).toContainText("Verification expired");
  await expect(page.getByRole("button", { name: "Sign in with a passkey" })).toBeVisible();
  await expect(page.getByLabel("Email", { exact: true })).toBeVisible();
});

test("rejected passkey sign-in cancels the attempt and shows the error", async ({ page }) => {
  await page.addInitScript(() => {
    navigator.credentials.get = () =>
      Promise.reject(new DOMException("Passkey sign-in was cancelled.", "NotAllowedError"));
  });
  await mockApi(page, normal, "login");
  await page.route("**/api/v1/auth/passkey/start", (route) => route.fulfill({ json: passkeyOptions }));
  await page.route("**/api/v1/auth/cancel", (route) => route.fulfill({ json: { ok: true } }));
  await page.goto("/");
  const cancelled = page.waitForRequest(
    (req) => req.url().endsWith("/api/v1/auth/cancel") && req.method() === "POST",
  );
  await page.getByRole("button", { name: "Sign in with a passkey" }).click();
  await cancelled;
  await expect(page.getByRole("alert")).toContainText("Passkey sign-in was cancelled.");
});

test("busy sign-in explains the 503 and keeps the form for a retry", async ({ page }) => {
  await mockApi(page, normal, "login");
  await page.route("**/api/v1/auth/login", (route) =>
    route.fulfill({
      status: 503,
      json: { error: "Sign-in is busy. Try again shortly." },
    }),
  );
  await page.goto("/");
  await submitPassword(page);
  await expect(page.getByRole("alert")).toContainText("Sign-in is busy. Try again shortly.");
  await expect(page.getByLabel("Email", { exact: true })).toBeVisible();
});
