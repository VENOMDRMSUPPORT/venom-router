import { defineConfig, devices } from "@playwright/test";

/**
 * Shared config for both suites (`npm run test:a11y`, `npm run test:visual`). Boots the
 * real Vite dev server (esbuild-transformed, not in-browser Babel) against the package
 * root so every specimen page resolves at the same relative path it ships at. Network is
 * otherwise untouched by Playwright itself — the offline claim is verified by the explicit
 * route-blocking assertions inside tests/a11y/offline.spec.ts, not by this config.
 */
export default defineConfig({
  testDir: ".",
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: process.env.CI ? 1 : 0,
  reporter: [["list"]],
  timeout: 30_000,
  expect: {
    // Screenshot capture can briefly queue behind the preceding a11y suite on
    // Windows when all eight workers are active. Keep the pixel threshold
    // strict while allowing the capture itself enough time to complete.
    timeout: 20_000,
    // Tight on purpose: a real visual regression should fail this. maxDiffPixels (not
    // ratio) so it doesn't scale permissively with full-page screenshot size.
    toHaveScreenshot: { maxDiffPixels: 40 },
  },
  use: {
    baseURL: "http://127.0.0.1:4173",
    trace: "retain-on-failure",
    viewport: { width: 1280, height: 900 },
  },
  webServer: {
    command: "npm run dev",
    url: "http://127.0.0.1:4173/storybook/index.html",
    cwd: "..",
    reuseExistingServer: !process.env.CI,
    timeout: 60_000,
  },
  projects: [
    {
      name: "chromium",
      use: { ...devices["Desktop Chrome"] },
    },
  ],
});
