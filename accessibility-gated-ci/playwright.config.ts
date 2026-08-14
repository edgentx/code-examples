import { defineConfig, devices } from '@playwright/test';

// One port, named once. The webServer below binds it and every test reaches the page through
// baseURL, so there is no hard-coded host anywhere in the specs.
const PORT = 4173;
const ORIGIN = `http://127.0.0.1:${PORT}`;

export default defineConfig({
  testDir: './tests',
  fullyParallel: true,

  // No retries. An accessibility violation is deterministic: if the gate is allowed to retry, a
  // real failure can be hidden by a flake budget it should never have had.
  retries: 0,

  // `list` prints one line per test as it runs, which is what a reviewer reads in CI logs.
  reporter: [['list']],

  use: {
    baseURL: ORIGIN,
    // Failure artifacts only, and only in a gitignored directory.
    screenshot: 'only-on-failure',
    trace: 'off',
  },

  // Chromium alone. The gate checks the markup and the accessibility tree, not rendering
  // differences between engines, so a second browser would triple the runtime and prove nothing.
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],

  // The static server is part of the test run: `npm test` is the whole command, with nothing to
  // start by hand and nothing left running afterwards. The root is the example directory rather
  // than public/ so the same server also serves tests/fixtures/violations.html.
  webServer: {
    command: `npx http-server . -p ${PORT} -c-1 --silent`,
    url: `${ORIGIN}/public/index.html`,
    reuseExistingServer: !process.env.CI,
    timeout: 60_000,
  },
});
