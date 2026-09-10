import { defineConfig } from "@playwright/test";
import { execFileSync } from "node:child_process";
const gateway = execFileSync(
  "docker",
  [
    "network",
    "inspect",
    "k3d-room-pass-e2e",
    "--format",
    "{{(index .IPAM.Config 0).Gateway}}",
  ],
  { encoding: "utf8" },
).trim();
export default defineConfig({
  testDir: ".",
  testMatch: "room-auth.spec.js",
  workers: 1,
  fullyParallel: false,
  timeout: 45000,
  expect: { timeout: 15000 },
  reporter: [["list"], ["html", { open: "never" }]],
  use: {
    baseURL: "https://demo.roompass.test:18443",
    ignoreHTTPSErrors: true,
    browserName: "chromium",
    viewport: { width: 390, height: 844 },
    launchOptions: {
      args: [
        `--host-resolver-rules=MAP *.roompass.test ${gateway}`,
        "--no-proxy-server",
      ],
    },
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
    video: "on",
  },
});
