import { test, expect } from "@playwright/test";
import { execFileSync } from "node:child_process";
import { resolve } from "node:path";

// The claim under test is the one the audience actually sees, and the one no
// unit test can make: somebody changes the menu, and a DIFFERENT browser that
// nobody touched shows the new value.
//
// Two independent browser contexts, so two independent cookie jars and two
// independent EventSource connections — a shared context would prove only that
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
async function signIn(browser, label) {
  const displayName = `Stream ${label} ${Date.now()}`;
  names.push(displayName);

  const context = await browser.newContext({
    ignoreHTTPSErrors: true,
    viewport: { width: 1280, height: 900 },
  });
  const page = await context.newPage();

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
    JSON.stringify({ spec: { shopName: "Fixture Coffee" } }),
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
