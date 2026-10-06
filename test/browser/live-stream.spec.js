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
// Dex, Room Pass enrolment, krm-foyer's session and its /k8s and /stream/v1,
// and a real CoffeeConfig in a real API server. The only thing the test asserts
// directly against Kubernetes is the setup and the restore.

const kubeconfig = resolve("../../.local/kubeconfig");
const kube = (...args) =>
  execFileSync("kubectl", ["--kubeconfig", kubeconfig, ...args], {
    encoding: "utf8",
  });
const room = () =>
  JSON.parse(kube("-n", "voter", "get", "room", "demo", "-o", "json"));
const participants = () =>
  JSON.parse(kube("-n", "voter", "get", "participants", "-o", "json"))
    .items;
const coffeeConfig = () =>
  JSON.parse(
    kube("-n", "voter", "get", "coffeeconfig", "demo-coffee", "-o", "json"),
  );

const APP = "https://app.voter.test:19443";

/** The one CoffeeConfig, as the editor reads and patches it through krm-foyer's
 *  /k8s. A predicate rather than a glob: the patch carries ?fieldManager=voter. */
const isCoffeeConfig = (url) =>
  new URL(url).pathname.endsWith("/namespaces/voter/coffeeconfigs/demo-coffee");

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
  // Room Pass STORES the folded name, so the cleanup below has to look for that
  // and not for what was typed -- otherwise every run leaks a Participant and
  // the room eventually hits maxParticipants.
  names.push(displayName.replaceAll(" ", "-"));

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
  // See docs/csp-form-action.md in sunib/room-pass.
  page.on("console", (msg) => {
    if (/Content Security Policy|violates/i.test(msg.text())) {
      throw new Error(`${label}: CSP blocked something -- ${msg.text()}`);
    }
  });

  // Straight at the application: the page sends the browser to krm-foyer's
  // login, Dex hands off to Room Pass, and the room form is what it is shown.
  await page.goto(`${APP}/admin`);
  await expect(page.getByLabel("Room code")).toBeVisible();
  await page.getByLabel("Room code").fill(room().status.joinCode.code);
  await page.getByLabel("Display name").fill(displayName);
  // The Room asks one question at the door; any answer will do here.
  await page.getByRole("radio", { name: "Vue" }).check();
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

// Editing the menu is the operator's live grant, as on the cluster: this file
// stands in for the operator and binds the room to the coffee-admin Role for
// its tests, then takes it back.
const grant = JSON.stringify({
  apiVersion: "rbac.authorization.k8s.io/v1",
  kind: "RoleBinding",
  metadata: { name: "voter-audience-coffee-admin", namespace: "voter" },
  subjects: [{ kind: "Group", apiGroup: "rbac.authorization.k8s.io", name: "demo:voter-audience" }],
  roleRef: { apiGroup: "rbac.authorization.k8s.io", kind: "Role", name: "voter-audience-coffee-admin" },
});
test.beforeAll(() => {
  execFileSync("kubectl", ["--kubeconfig", kubeconfig, "apply", "-f", "-"], { input: grant, encoding: "utf8" });
});
test.afterAll(() => {
  kube("-n", "voter", "delete", "rolebinding", "voter-audience-coffee-admin", "--ignore-not-found");
});

test.afterEach(async () => {
  // Restore the menu so the suite can run twice, and remove only the
  // Participants this test created.
  kube(
    "-n",
    "voter",
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
    kube("-n", "voter", "delete", "participant", p.metadata.name);
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
      "voter",
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
      "voter",
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


// Every PATCH is refused, so the editor's own retry cannot win either. Two things
// it must still do: keep the person's typing, and explain itself in OUR words.
// The API server's sentence about applying changes to the latest version is what
// the room read off its phones on 2026-09-17 and reported as "the CRD is not the
// newest version" -- docs/post-demo-2026-09-17.md.
test("a save that keeps losing says so and keeps unsaved input visible", async ({ browser }) => {
  const editor = await signIn(browser, "rejected-save");
  let patches = 0;
  try {
    await editor.page.route(isCoffeeConfig, async (route) => {
      if (route.request().method() !== "PATCH") return route.continue();
      patches++;
      await route.fulfill({
        status: 409,
        contentType: "application/json",
        body: JSON.stringify({ error: "Configuration changed; refresh and try again." }),
      });
    });
    await bannerText(editor.page).fill("Keep this unsaved edit");
    await editor.page.getByRole("button", { name: /^Save \d+ Change/ }).click();
    // Narrowed twice on purpose. getByText matches the <section> AND the <p>
    // inside it, and role="alert" alone matches two panels, because
    // PermissionRequirements is also an alert and this participant is missing a
    // permission, so it is on screen throughout. Neither is a real ambiguity --
    // both are strict-mode violations over an element that was there all along.
    // The heading belongs to the save-failure panel and to nothing else.
    const refusal = editor.page.getByRole("alert").filter({ hasText: "Admin request failed" });
    await expect(refusal).toContainText(/their save landed first each time/);
    // Bounded: one press of Save is three attempts, and then it stops trying.
    expect(patches).toBe(3);
    await expect(editor.page.getByText(/latest version/)).toHaveCount(0);
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
    kube("-n", "voter", "patch", "coffeeconfig", "demo-coffee", "--type=merge", "-p",
      JSON.stringify({ spec: { shopName: `Usage refresh ${Date.now()}` } }));
    await expect.poll(() => successfulReads).toBeGreaterThan(0);
    await expect(editor.page.getByText("Voucher usage unavailable", { exact: true })).toHaveCount(0);
    await expect(bannerText(editor.page)).toHaveValue("Preserve this while refreshing usage");
    await expect(editor.page.getByText(/Used 2/)).toBeVisible();
  } finally {
    await editor.context.close();
  }
});


// A genuine Kubernetes 409 against an edit that touches a DIFFERENT field. There
// is nothing for a person to review, so one press of Save takes the winner's
// version and re-sends the same edit on its own. Both writes survive.
test("a real Kubernetes 409 on a non-overlapping edit re-sends itself", async ({ browser }) => {
  const editor = await signIn(browser, "real-conflict");
  let patches = 0;
  try {
    await editor.page.route(isCoffeeConfig, async route => {
      if (route.request().method() !== "PATCH") return route.continue();
      patches++;
      if (patches === 1) {
        // The browser has already frozen its intent; a real persisted change
        // now invalidates that resourceVersion before the host submits it.
        kube("-n", "voter", "patch", "coffeeconfig", "demo-coffee", "--type=merge", "-p",
          JSON.stringify({ spec: { shopName: "Concurrent winner" } }));
      }
      await route.continue();
    });
    await bannerText(editor.page).fill("Reviewed local banner");
    const rejected = editor.page.waitForResponse(response => isCoffeeConfig(response.url()) && response.request().method() === "PATCH");
    const saved = editor.page.waitForResponse(response =>
      isCoffeeConfig(response.url()) && response.request().method() === "PATCH" && response.status() === 200);
    await editor.page.getByRole("button", { name: /^Save \d+ Change/ }).click();
    expect((await rejected).status()).toBe(409);
    // The API server's own answer to the patch: the object as stored.
    const stored = await (await saved).json();
    expect(stored.kind).toBe("CoffeeConfig");
    expect(stored.spec.bannerText).toBe("Reviewed local banner");
    // Nobody had to press anything a second time, and nobody was shown a refusal.
    expect(patches).toBe(2);
    await expect(editor.page.getByText(/Saved to Kubernetes/)).toBeVisible();
    await expect(editor.page.getByText(/review and save again/)).toHaveCount(0);
    await expect(bannerText(editor.page)).toHaveValue("Reviewed local banner");
    expect(coffeeConfig().spec.bannerText).toBe("Reviewed local banner");
    expect(coffeeConfig().spec.shopName).toBe("Concurrent winner");
  } finally { await editor.context.close(); }
});

test("overlapping edits require an explicit conflict choice", async ({ browser }) => {
  const editor = await signIn(browser, "overlap");
  try {
    await shopName(editor.page).fill("My chosen name");
    kube("-n", "voter", "patch", "coffeeconfig", "demo-coffee", "--type=merge", "-p",
      JSON.stringify({ spec: { shopName: "Their chosen name" } }));
    await expect(editor.page.getByRole("button", { name: "Keep Mine", exact: true })).toBeVisible();
    await expect(editor.page.getByRole("button", { name: /^Save \d+ Change/ })).toBeDisabled();
    await editor.page.getByRole("button", { name: "Keep Mine", exact: true }).click();
    await expect(shopName(editor.page)).toHaveValue("My chosen name");
    const saved = editor.page.waitForResponse(response => isCoffeeConfig(response.url()) && response.request().method() === "PATCH");
    await editor.page.getByRole("button", { name: /^Save \d+ Change/ }).click();
    expect((await saved).status()).toBe(200);
    expect(coffeeConfig().spec.shopName).toBe("My chosen name");
  } finally { await editor.context.close(); }
});

test("shared watches isolate RBAC withdrawal and deny access to a warm cache", async ({ browser }) => {
  test.setTimeout(75000);
  const retained = await signIn(browser, "retained-access");
  const withdrawn = await signIn(browser, "withdrawn-access");
  const binding = JSON.parse(kube("-n", "voter", "get", "rolebinding", "voter-audience", "-o", "json"));
  // krm-foyer's metrics, on a Service of their own and never on the origin.
  const metrics = () => kube("get", "--raw", "/api/v1/namespaces/voter/services/krm-foyer-metrics:9090/proxy/metrics");
  try {
    expect(await retained.page.evaluate(async () => (await fetch("/metrics")).status)).toBe(404);
    // Both viewers attached to krm-foyer's shared watch on the CoffeeConfig:
    // two subscriptions, one watch at the API server held by its shared-watch
    // identity. Balanced gauges, so they read the same whether this test runs
    // alone or after every other one in the file. This test's job is that
    // withdrawal isolates one viewer and leaves the other's cache warm.
    await expect.poll(metrics).toContain("krm_foyer_shared_subscriptions_open 2\n");
    await expect.poll(metrics).toMatch(/krm_foyer_upstream_watches_open\{identity="shared"\} [1-9]/);
    await shopName(withdrawn.page).fill("Keep this unsaved draft");
    // The name RBAC matches on is the API server's, from krm-foyer's whoami.
    const identity = (await retained.page.evaluate(async () => (await fetch("/auth/whoami")).json())).userInfo;
    expect(identity.username).toBeTruthy();
    const started = Date.now();
    kube("-n", "voter", "patch", "rolebinding", "voter-audience", "--type=merge", "-p", JSON.stringify({
      subjects: [{ kind: "User", name: identity.username, apiGroup: "rbac.authorization.k8s.io" }],
    }));
    await expect(withdrawn.page.getByRole("status").getByText(/^FORBIDDEN: Kubernetes refused/)).toBeVisible({ timeout: 60000 });
    expect(Date.now() - started).toBeLessThan(60000);
    await expect(shopName(withdrawn.page)).toHaveValue("Keep this unsaved draft");
    // Exactly one attachment released, not both: the surviving viewer stayed on
    // the shared watch throughout, which is what "warm cache" means here. The
    // live update at the end of this test is the proof that it kept working.
    await expect.poll(metrics).toContain("krm_foyer_shared_subscriptions_open 1\n");
    const frames = await withdrawn.page.evaluate(async () => (await fetch("/stream/v1?group=examples.configbutler.ai&version=v1alpha1&resource=coffeeconfigs&namespace=voter&name=demo-coffee")).text());
    expect(frames).toContain('"terminal":true');
    expect(frames).not.toContain('"object"');
    kube("-n", "voter", "patch", "coffeeconfig", "demo-coffee", "--type=merge", "-p", JSON.stringify({ spec: { shopName: "Still live after withdrawal" } }));
    await expect(shopName(retained.page)).toHaveValue("Still live after withdrawal");
  } finally {
    kube("-n", "voter", "patch", "rolebinding", "voter-audience", "--type=merge", "-p", JSON.stringify({ subjects: binding.subjects }));
    await withdrawn.context.close();
    await retained.context.close();
  }
});
