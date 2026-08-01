import { defineConfig, devices } from "@playwright/test";

// This config executes under Node; the project deliberately carries no
// @types/node dependency, so declare the one global it reads (the same
// accommodation vite.config.ts already makes).
declare const process: { env: Record<string, string | undefined> };

/**
 * P6-TEST-001 (1b): the real-browser suites.
 *
 * Shape mirrors Design_System/tests/playwright.config.ts (read as the
 * reference, never edited) — one chromium project, a webServer, list
 * reporter, strict pixel budget. What differs, and why:
 *
 *  - it serves the PRODUCTION BUILD (`vite preview` over dist/), not a dev
 *    server. These suites are the last thing standing between a build and an
 *    owner, so they test the artifact that actually ships: minified, tree
 *    shaken, with the real CSS bundle rather than dev-injected styles.
 *
 *  - the build ALWAYS runs first. Reusing whatever happens to be in dist/
 *    would let a stale bundle pass a suite whose whole purpose is catching
 *    what changed, and a stale green is worse than a slow red. The build is
 *    ~6s, so there is nothing to buy by skipping it.
 *
 *  - baselines live under tests/visual/ (the default snapshot path for a spec
 *    in that directory), never in Design_System/ — the package's baselines
 *    belong to the package, and this app's belong to the app.
 */
export default defineConfig({
  testDir: "./tests",
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  // No retries, deliberately, and not only in CI. A retry turns a flaky
  // assertion into a green run, which is precisely the signal this suite
  // exists to surface. If something here is flaky, it is a defect in the
  // harness (a wait on a timer instead of on state, an unpinned clock) and it
  // should be fixed rather than retried away.
  retries: 0,
  reporter: [["list"]],
  timeout: 60_000,
  expect: {
    timeout: 15_000,
    // maxDiffPixels, not a ratio: a ratio scales permissively with the size of
    // a full-page screenshot, so a large page could hide a real regression
    // inside the allowance.
    toHaveScreenshot: { maxDiffPixels: 40, animations: "disabled", caret: "hide" },
  },
  use: {
    baseURL: "http://127.0.0.1:4174",
    trace: "retain-on-failure",
    // Fixed viewport: a screenshot suite whose viewport depends on the host
    // window has no baselines, only coincidences.
    viewport: { width: 1280, height: 900 },
  },
  webServer: {
    // --host 127.0.0.1 is load-bearing: `vite preview` otherwise binds
    // `localhost`, which resolves to ::1 first, and the baseURL below (and
    // every Venom loopback convention) is the IPv4 literal. Without it the
    // server starts fine and Playwright waits for it forever on the wrong
    // stack.
    command: "npm run build && npm run preview -- --port 4174 --strictPort --host 127.0.0.1",
    url: "http://127.0.0.1:4174/",
    reuseExistingServer: !process.env.CI,
    timeout: 120_000,
  },
  projects: [
    {
      name: "chromium",
      use: { ...devices["Desktop Chrome"] },
    },
  ],
});
