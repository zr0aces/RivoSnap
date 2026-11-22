import { defineConfig } from "@playwright/test";
export default defineConfig({
  testDir: "./e2e",
  workers: 1,
  projects: [
    { name: "chromium", use: { browserName: "chromium" } },
    { name: "webkit", use: { browserName: "webkit" } },
  ],
  use: { baseURL: process.env["E2E_URL"] ?? "http://127.0.0.1:4173" },
  webServer: process.env["E2E_URL"]
    ? undefined
    : {
        command:
          "corepack pnpm exec vite apps/web --host 127.0.0.1 --port 4173",
        url: "http://127.0.0.1:4173",
        reuseExistingServer: false,
      },
});
