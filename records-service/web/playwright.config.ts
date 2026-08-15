import { defineConfig, devices } from '@playwright/test';

// One port, named once. The Go service binds it and serves both the API and the
// built console from the same origin, which is how it is deployed; every spec
// reaches the page through baseURL, so there is no hard-coded host in a spec.
const PORT = 8181;
const ORIGIN = `http://127.0.0.1:${PORT}`;

export default defineConfig({
  testDir: './tests/e2e',
  fullyParallel: false,

  // No retries. An accessibility violation is deterministic, and a keyboard path
  // either exists or does not; a flake budget here would hide a real failure.
  retries: 0,

  // `list` prints one line per test as it runs, which is what a reviewer reads
  // in continuous integration logs.
  reporter: [['list']],

  use: {
    baseURL: ORIGIN,
    screenshot: 'only-on-failure',
    trace: 'off',
  },

  // Chromium alone. The gate checks the markup and the accessibility tree, not
  // rendering differences between engines, so a second browser would multiply
  // the runtime and prove nothing.
  projects: [{ name: 'chromium', use: { ...devices['Desktop Chrome'] } }],

  // The real service, serving the real console: `npm run e2e` builds the console
  // and starts the Go binary, with nothing to start by hand and nothing left
  // running afterwards. The specs therefore exercise the same authorization
  // model, the same event store and the same conflict responses as the service.
  webServer: {
    command: `go run ./cmd/server -addr 127.0.0.1:${PORT} -web ./web/dist`,
    cwd: '..',
    url: ORIGIN,
    reuseExistingServer: !process.env.CI,
    timeout: 120_000,
  },
});
