import { defineConfig, devices } from '@playwright/test';

const PORT = Number(process.env.ARGUS_TEST_PORT || 7744);

export default defineConfig({
  testDir: './tests',
  fullyParallel: false, // single backing daemon — keep serial
  workers: 1,
  retries: 0,
  reporter: [['list']],
  timeout: 30_000,
  use: {
    baseURL: `http://127.0.0.1:${PORT}`,
    trace: 'retain-on-failure',
    video: 'retain-on-failure',
    screenshot: 'only-on-failure',
  },
  projects: [
    {
      name: 'iphone',
      use: { ...devices['iPhone 14 Pro'] },
    },
    {
      name: 'desktop',
      use: { ...devices['Desktop Chrome'], viewport: { width: 1280, height: 800 } },
    },
  ],
  webServer: process.env.ARGUS_NO_WEBSERVER ? undefined : {
    command: '/tmp/argus-test-server -port ' + PORT + ' -token test-token',
    port: PORT,
    reuseExistingServer: false,
    timeout: 10_000,
  },
});
