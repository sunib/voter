import { defineConfig } from "@playwright/test";
import { execFileSync } from "node:child_process";
const gateway = execFileSync(
  "docker",
  [
    "network",
    "inspect",
    "k3d-voter-e2e",
    "--format",
    "{{(index .IPAM.Config 0).Gateway}}",
  ],
  { encoding: "utf8" },
).trim();
export default defineConfig({
  testDir: ".",
  testMatch: /url-probe\.spec\.js/,
  workers: 1,
  fullyParallel: false,
  timeout: 45000,
  expect: { timeout: 15000 },
  reporter: [["list"], ["html", { open: "never" }]],
  use: {
    baseURL: "https://app.voter.test:19443",
    ignoreHTTPSErrors: true,
    browserName: "chromium",
    viewport: { width: 390, height: 844 },
    launchOptions: {
      args: [
        `--host-resolver-rules=MAP *.voter.test ${gateway}`,
        "--no-proxy-server",
      ],
    },
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
    video: "off",
  },
});
