// One-off rehearsal against PRODUCTION (voter.koudijs.dev): enrol through Room
// Pass the way an attendee does, change a price in the editor, press save, and
// let the Git sink answer the only question the fixture cannot -- whose name
// ends up on the commit.
//
// Run it from this directory:
//
//   node rehearse-production.mjs
//
// It needs a kubeconfig with read access to the voter namespace (the join code
// rotates every 15s and lives in Room status, which participants never read).
// It creates one real Participant and one real commit in
// ConfigButler/k8s-audit-trail. Clean up afterwards with:
//
//   kubectl -n voter delete participant -l ''  --field-selector ... # or by name
//   kubectl -n voter get participants
//
// Flux owns spec.products, so the price change is reverted at the next
// reconcile (<=10m) -- which produces a second commit authored by
// kustomize-controller. That is the drift behaviour, not a failure.
import { chromium } from "@playwright/test";
import { execFileSync } from "node:child_process";

const kube = (...args) => execFileSync("kubectl", args, { encoding: "utf8" });
const joinCode = () =>
  kube("-n", "voter", "get", "room", "demo", "-o", "jsonpath={.status.joinCode.code}").trim();

const APP = "https://voter.koudijs.dev";
const displayName = process.argv[2] ?? "Ada Lovelace";

const browser = await chromium.launch();
const context = await browser.newContext({ viewport: { width: 1280, height: 900 } });
const page = await context.newPage();
page.on("console", (m) => {
  if (/Content Security Policy|violates/i.test(m.text())) console.log("CSP:", m.text());
});

try {
  // Straight at the application: Voter starts the OIDC flow, Dex hands off to
  // Room Pass, and the room form is what the browser is shown.
  await page.goto(`${APP}/admin`);
  await page.getByLabel("Room code").waitFor({ timeout: 30000 });
  const code = joinCode();
  console.log("join code:", code);
  await page.getByLabel("Room code").fill(code);
  await page.getByLabel("Display name").fill(displayName);
  await page.getByRole("button", { name: "Continue", exact: true }).click();

  await page.waitForURL(`${APP}/admin`, { timeout: 45000 });
  const shopName = page.locator("label", { hasText: "Shop name" }).locator("input");
  await shopName.waitFor({ timeout: 30000 });
  console.log("signed in as:", displayName);

  // Espresso is the second product card, so its price is the second
  // "Base price (cents)" field on the page.
  const price = page
    .locator("label", { hasText: "Base price (cents)" })
    .locator("input")
    .nth(1);
  await price.fill("295");
  await price.blur();

  const reason = page
    .locator("label", { hasText: "Why are you making this change?" })
    .locator("textarea");
  if (await reason.count()) await reason.fill("Espresso costs more than it used to");

  const save = page.getByRole("button", { name: /Save \d+ Change/ });
  await save.waitFor({ timeout: 15000 });
  await save.click();
  await page.waitForTimeout(6000);

  const receipt = (await page.locator("body").innerText())
    .split("\n")
    .filter((l) => /sav|commit|error/i.test(l))
    .slice(0, 6)
    .join(" | ");
  console.log("receipt:", receipt);
  console.log("now check: git -C ../../../external/k8s-audit-trail fetch && git log --format='%an <%ae> | %s' origin/main | head -3");
} finally {
  await page.screenshot({ path: "rehearsal.png" });
  await browser.close();
}
