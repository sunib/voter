import { test, expect } from "@playwright/test";
import { execFileSync } from "node:child_process";
import { resolve } from "node:path";

// The claim under test is the one the audience actually sees, and the one no
// unit test can make: somebody changes the menu, and a DIFFERENT browser that
// nobody touched shows the new value.
//
// Two independent browser contexts, so two independent cookie jars and two
// independent managed fetch streams — a shared context would prove only that
// one page can re-read its own write.
//
// This is deliberately end-to-end through the real parts: Chromium, Traefik,
// Dex, Room Pass enrolment, the Voter session cookie, the krm-stream gateway,
// and a real CoffeeConfig in a real API server. The only thing the test asserts
// directly against Kubernetes is the setup and the restore.

const kubeconfig = resolve("../../.local/kubeconfig");
const kube = (...args) =>
  execFileSync("kubectl", ["--kubeconfig", kubeconfig, ...args], {
    encoding: "utf8",
  });
const room = () =>
  JSON.parse(kube("-n", "room-pass", "get", "room", "demo", "-o", "json"));
const participants = () =>
  JSON.parse(kube("-n", "room-pass", "get", "participants", "-o", "json"))
    .items;
const coffeeConfig = () =>
  JSON.parse(
    kube("-n", "room-pass", "get", "coffeeconfig", "demo-coffee", "-o", "json"),
  );

const APP = "https://app.roompass.test:18443";

/** The editor's Shop name input. Each admin field is an <input> wrapped in a
 *  <label>, so the visible label addresses it — no test id needed, and the
 *  locator breaks loudly if the label a person reads ever changes. */
/** The editor's fields, addressed through their own <label> rather than by
 *  accessible name.
 *
 *  getByLabel is the right instinct and the wrong tool here. The moment a field
 *  is dirty or conflicted, FieldStateMarker renders extra text AND an "apply the
 *  server value" button inside that label: the accessible name stops being
 *  "Shop name" and the label resolves to the button rather than the control.
 *  These tests deliberately put fields into exactly that state, so they must
 *  address the control itself. */
const shopName = (page) =>
  page.locator("label", { hasText: "Shop name" }).locator("input");
const bannerText = (page) =>
  page.locator("label", { hasText: "Banner text" }).locator("textarea");
const names = [];

/** Enrol a fresh browser context and land it on the Voter application.
 *
 *  Each caller gets its own display name so the cleanup below can tell the
 *  contexts apart, and so a failure names which browser was which. */
async function signIn(browser, label, prepare = async () => {}) {
  const displayName = `Stream ${label} ${Date.now()}`;
  names.push(displayName);

  const context = await browser.newContext({
    ignoreHTTPSErrors: true,
    viewport: { width: 1280, height: 900 },
  });
  const page = await context.newPage();
  page.on("pageerror", (error) => {
    throw error;
  });
  await prepare(page);

  // CSP violations are reported ONLY on the browser console -- not in a
  // response status, not in a server log. A blocked form submission looks
  // exactly like a click that did nothing, and cost most of an afternoon once.
  // See room-pass/docs/csp-form-action.md.
  page.on("console", (msg) => {
    if (/Content Security Policy|violates/i.test(msg.text())) {
      throw new Error(`${label}: CSP blocked something -- ${msg.text()}`);
    }
  });

  // Straight at the application: Voter starts the OIDC flow, Dex hands off to
  // Room Pass, and the room form is what the browser is shown.
  await page.goto(`${APP}/admin`);
  await expect(page.getByLabel("Room code")).toBeVisible();
  await page.getByLabel("Room code").fill(room().status.joinCode.code);
  await page.getByLabel("Display name").fill(displayName);
  await page.getByRole("button", { name: "Continue", exact: true }).click();

  // Wait for the round trip to finish and assert we actually arrived. Without
  // this the tests would fail at whatever assertion came next and blame the
  // stream for a login problem -- and a half-finished handoff would leave the
  // next context racing this one through Dex.
  await page.waitForURL(`${APP}/admin`, { timeout: 30000 });
  await expect(
    shopName(page),
    `${label}: signed in but the editor never rendered`,
  ).toBeVisible({ timeout: 20000 });

  return { context, page, displayName };
}

test.afterEach(async () => {
  // Restore the menu so the suite can run twice, and remove only the
  // Participants this test created.
  kube(
    "-n",
    "room-pass",
    "patch",
    "coffeeconfig",
    "demo-coffee",
    "--type=merge",
    "-p",
    JSON.stringify({ spec: { shopName: "Fixture Coffee", bannerText: "Edit this menu" } }),
  );
  for (const p of participants().filter((p) =>
    names.includes(p.spec.displayName),
  )) {
    kube("-n", "room-pass", "delete", "participant", p.metadata.name);
  }
  names.length = 0;
});

test("a change saved in one browser appears in another without a reload", async ({
  browser,
}, testInfo) => {
  const editor = await signIn(browser, "editor");
  const viewer = await signIn(browser, "viewer");

  try {
    // Both browsers are looking at the same object, each over its own stream.
    await expect(shopName(editor.page)).toHaveValue("Fixture Coffee");
    await expect(shopName(viewer.page)).toHaveValue("Fixture Coffee");

    // Pin the viewer's navigation. If the value below arrives because the page
    // reloaded rather than because the stream delivered it, this test would be
    // asserting nothing, so make that failure impossible to miss.
    let viewerNavigated = false;
    viewer.page.on("framenavigated", (frame) => {
      if (frame === viewer.page.mainFrame()) viewerNavigated = true;
    });

    const changed = `Streamed ${Date.now()}`;
    await shopName(editor.page).fill(changed);
    await editor.page.getByRole("button", { name: /^Save \d+ Change/ }).click();

    // The editor's own save landing proves only that the write happened.
    await expect(shopName(editor.page)).toHaveValue(changed);
    expect(coffeeConfig().spec.shopName).toBe(changed);

    // This is the assertion the whole fixture exists for.
    await expect(shopName(viewer.page)).toHaveValue(changed, {
      timeout: 20000,
    });
    expect(
      viewerNavigated,
      "the viewer reloaded, so this proves nothing about the stream",
    ).toBe(false);

    await viewer.page.screenshot({
      path: testInfo.outputPath("viewer-live-update.png"),
      fullPage: true,
    });
  } finally {
    await editor.context.close();
    await viewer.context.close();
  }
});

test("a change made directly in Kubernetes reaches an open browser", async ({
  browser,
}) => {
  // The same path, but with no application write at all: the stream is a
  // Kubernetes watch, so an operator running kubectl must reach the room too.
  // If this passes while the test above fails, the bug is in the save path; if
  // both fail, it is in the stream.
  const viewer = await signIn(browser, "kubectl-viewer");

  try {
    await expect(shopName(viewer.page)).toHaveValue("Fixture Coffee");

    const changed = `From kubectl ${Date.now()}`;
    kube(
      "-n",
      "room-pass",
      "patch",
      "coffeeconfig",
      "demo-coffee",
      "--type=merge",
      "-p",
      JSON.stringify({ spec: { shopName: changed } }),
    );

    await expect(shopName(viewer.page)).toHaveValue(changed, {
      timeout: 20000,
    });
  } finally {
    await viewer.context.close();
  }
});

test("an unsaved edit survives a concurrent change to a different field", async ({
  browser,
}) => {
  // The reason the editor uses a three-way merge rather than replacing its
  // state on every event. Someone is typing; the server moves underneath them;
  // what they typed must still be there.
  const editor = await signIn(browser, "merge-editor");

  try {
    const banner = bannerText(editor.page);
    await expect(banner).toHaveValue("Edit this menu");

    const typed = `Typing ${Date.now()}`;
    await banner.fill(typed);

    // A different field changes upstream while that edit is still unsaved.
    const changed = `Concurrent ${Date.now()}`;
    kube(
      "-n",
      "room-pass",
      "patch",
      "coffeeconfig",
      "demo-coffee",
      "--type=merge",
      "-p",
      JSON.stringify({ spec: { shopName: changed } }),
    );

    // The incoming change lands...
    await expect(shopName(editor.page)).toHaveValue(changed, {
      timeout: 20000,
    });
    // ...and the unsaved edit is still there. Replacing state wholesale on
    // every event is exactly what would lose it.
    await expect(banner).toHaveValue(typed);
  } finally {
    await editor.context.close();
  }
});


test("a rejected save keeps the editor and unsaved input visible", async ({ browser }) => {
  const editor = await signIn(browser, "rejected-save");
  try {
    await editor.page.route("**/public/coffeeconfig", async (route) => {
      if (route.request().method() !== "PATCH") return route.continue();
      await route.fulfill({
        status: 409,
        contentType: "application/json",
        body: JSON.stringify({ error: "Configuration changed; refresh and try again." }),
      });
    });
    await bannerText(editor.page).fill("Keep this unsaved edit");
    await editor.page.getByRole("button", { name: /^Save \d+ Change/ }).click();
    await expect(editor.page.getByText("Configuration refreshed. Your edits are intact; review and save again.")).toBeVisible();
    await expect(bannerText(editor.page)).toHaveValue("Keep this unsaved edit");
    await expect(shopName(editor.page)).toBeVisible();
  } finally {
    await editor.context.close();
  }
});

test("usage read failures do not block the editor and a live update retries them", async ({ browser }) => {
  let usageAvailable = false;
  let successfulReads = 0;
  const editor = await signIn(browser, "usage-recovery", async (page) => {
    await page.route("**/public/vouchers", async (route) => {
      if (!usageAvailable) {
        await route.fulfill({ status: 503, body: "Usage unavailable" });
        return;
      }
      successfulReads++;
      await route.fulfill({
        contentType: "application/json",
        body: JSON.stringify({ voucherUsage: { testnet: 2 }, scope: "process" }),
      });
    });
  });
  try {
    await expect(editor.page.getByRole("status").filter({ hasText: "Voucher usage unavailable" })).toContainText("Counts may be out of date");
    await bannerText(editor.page).fill("Preserve this while refreshing usage");
    usageAvailable = true;
    kube("-n", "room-pass", "patch", "coffeeconfig", "demo-coffee", "--type=merge", "-p",
      JSON.stringify({ spec: { shopName: `Usage refresh ${Date.now()}` } }));
    await expect.poll(() => successfulReads).toBeGreaterThan(0);
    await expect(editor.page.getByText("Voucher usage unavailable", { exact: true })).toHaveCount(0);
    await expect(bannerText(editor.page)).toHaveValue("Preserve this while refreshing usage");
    await expect(editor.page.getByText(/Used 2/)).toBeVisible();
  } finally {
    await editor.context.close();
  }
});


test("a real Kubernetes 409 preserves input and a new reviewed save succeeds", async ({ browser }) => {
  const editor = await signIn(browser, "real-conflict");
  let patches = 0;
  try {
    await editor.page.route("**/public/coffeeconfig", async route => {
      if (route.request().method() !== "PATCH") return route.continue();
      patches++;
      if (patches === 1) {
        // The browser has already frozen its intent; a real persisted change
        // now invalidates that resourceVersion before the host submits it.
        kube("-n", "room-pass", "patch", "coffeeconfig", "demo-coffee", "--type=merge", "-p",
          JSON.stringify({ spec: { shopName: "Concurrent winner" } }));
      }
      await route.continue();
    });
    await bannerText(editor.page).fill("Reviewed local banner");
    const rejected = editor.page.waitForResponse(response => response.url().endsWith("/public/coffeeconfig") && response.request().method() === "PATCH");
    await editor.page.getByRole("button", { name: /^Save \d+ Change/ }).click();
    expect((await rejected).status()).toBe(409);
    await expect(editor.page.getByText("Configuration refreshed. Your edits are intact; review and save again.")).toBeVisible();
    await expect(bannerText(editor.page)).toHaveValue("Reviewed local banner");
    expect(coffeeConfig().spec.shopName).toBe("Concurrent winner");
    expect(patches).toBe(1);
    const saved = editor.page.waitForResponse(response => response.url().endsWith("/public/coffeeconfig") && response.request().method() === "PATCH");
    await editor.page.getByRole("button", { name: /^Save \d+ Change/ }).click();
    const receipt = await (await saved).json();
    expect(receipt.saved).toBe(true);
    expect(receipt).not.toHaveProperty("config");
    expect(coffeeConfig().spec.bannerText).toBe("Reviewed local banner");
    expect(coffeeConfig().spec.shopName).toBe("Concurrent winner");
  } finally { await editor.context.close(); }
});

test("overlapping edits require an explicit conflict choice", async ({ browser }) => {
  const editor = await signIn(browser, "overlap");
  try {
    await shopName(editor.page).fill("My chosen name");
    kube("-n", "room-pass", "patch", "coffeeconfig", "demo-coffee", "--type=merge", "-p",
      JSON.stringify({ spec: { shopName: "Their chosen name" } }));
    await expect(editor.page.getByRole("button", { name: "Keep Mine", exact: true })).toBeVisible();
    await expect(editor.page.getByRole("button", { name: /^Save \d+ Change/ })).toBeDisabled();
    await editor.page.getByRole("button", { name: "Keep Mine", exact: true }).click();
    await expect(shopName(editor.page)).toHaveValue("My chosen name");
    const saved = editor.page.waitForResponse(response => response.url().endsWith("/public/coffeeconfig") && response.request().method() === "PATCH");
    await editor.page.getByRole("button", { name: /^Save \d+ Change/ }).click();
    expect((await saved).status()).toBe(200);
    expect(coffeeConfig().spec.shopName).toBe("My chosen name");
  } finally { await editor.context.close(); }
});

test("shared watches isolate RBAC withdrawal and deny access to a warm cache", async ({ browser }) => {
  test.setTimeout(75000);
  const retained = await signIn(browser, "retained-access");
  const withdrawn = await signIn(browser, "withdrawn-access");
  const binding = JSON.parse(kube("-n", "room-pass", "get", "rolebinding", "voter-audience", "-o", "json"));
  const pod = JSON.parse(kube("-n", "room-pass", "get", "pods", "-l", "app=voter", "-o", "json")).items[0].metadata.name;
  const metrics = () => kube("get", "--raw", `/api/v1/namespaces/room-pass/pods/${pod}:9090/proxy/metrics`);
  try {
    expect(await retained.page.evaluate(async () => (await fetch("/metrics")).status)).toBe(404);
    // Both viewers attached to the shared watch. These are balanced gauges from
    // the library's own observations, so they read the same whether this test
    // runs alone or after every other one in the file.
    //
    // What they deliberately do NOT claim is how many physical API-server
    // watches are open. Voter cannot honestly say: it would be asking itself.
    // The rehearsal asks the API server (apiserver_longrunning_requests), and
    // krm-stream's own tests assert the invariant. This test's job is that
    // withdrawal isolates one viewer and leaves the other's cache warm.
    await expect.poll(metrics).toContain("voter_stream_subscribers 2\n");
    await expect.poll(metrics).toContain("voter_stream_shared_subscriptions 2\n");
    await shopName(withdrawn.page).fill("Keep this unsaved draft");
    const identity = await retained.page.evaluate(async () => (await fetch("/auth/session")).json());
    expect(identity.username).toBeTruthy();
    const started = Date.now();
    kube("-n", "room-pass", "patch", "rolebinding", "voter-audience", "--type=merge", "-p", JSON.stringify({
      subjects: [{ kind: "User", name: identity.username, apiGroup: "rbac.authorization.k8s.io" }],
    }));
    await expect(withdrawn.page.getByRole("status").getByText(/^FORBIDDEN: Kubernetes refused/)).toBeVisible({ timeout: 60000 });
    expect(Date.now() - started).toBeLessThan(60000);
    await expect(shopName(withdrawn.page)).toHaveValue("Keep this unsaved draft");
    await expect.poll(metrics).toContain("voter_stream_subscribers 1\n");
    // Exactly one attachment released, not both: the surviving viewer stayed on
    // the shared watch throughout, which is what "warm cache" means here. The
    // live update at the end of this test is the proof that it kept working.
    await expect.poll(metrics).toContain("voter_stream_shared_subscriptions 1\n");
    const frames = await withdrawn.page.evaluate(async () => (await fetch("/public/stream?group=examples.configbutler.ai&version=v1alpha1&resource=coffeeconfigs&namespace=room-pass&name=demo-coffee")).text());
    expect(frames).toContain('"terminal":true');
    expect(frames).not.toContain('"object"');
    kube("-n", "room-pass", "patch", "coffeeconfig", "demo-coffee", "--type=merge", "-p", JSON.stringify({ spec: { shopName: "Still live after withdrawal" } }));
    await expect(shopName(retained.page)).toHaveValue("Still live after withdrawal");
  } finally {
    kube("-n", "room-pass", "patch", "rolebinding", "voter-audience", "--type=merge", "-p", JSON.stringify({ subjects: binding.subjects }));
    await withdrawn.context.close();
    await retained.context.close();
  }
});
