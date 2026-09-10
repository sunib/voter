import { test, expect } from "@playwright/test";
import { execFileSync } from "node:child_process";
import { resolve } from "node:path";
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
const testName = `Browser test ${Date.now()}`;

test.afterEach(() => {
  for (const p of participants().filter((p) => p.spec.displayName === testName))
    kube("-n", "room-pass", "delete", "participant", p.metadata.name);
});

test("room login and returning enrollment work in a phone-sized browser", async ({
  page,
  context,
}, testInfo) => {
  await page.goto("/app/login");
  await expect(page.getByLabel("Room code")).toBeVisible();
  await page.getByLabel("Room code").fill(room().status.joinCode.code);
  await page.getByLabel("Display name").fill(testName);
  await page.getByRole("button", { name: "Continue", exact: true }).click();
  await expect(
    page.getByText(`Welcome, ${testName}.`, { exact: true }),
  ).toBeVisible();
  const cookies = await context.cookies();
  const enrollment = cookies.find((c) => c.name === "__Host-rp-session");
  expect(enrollment).toMatchObject({
    secure: true,
    httpOnly: true,
    path: "/",
    sameSite: "Lax",
  });
  const before = participants().filter((p) => p.spec.displayName === testName);
  expect(before).toHaveLength(1);
  await page.screenshot({
    path: testInfo.outputPath("logged-in.png"),
    fullPage: true,
  });
  await page.getByRole("link", { name: "Sign in again" }).click();
  await expect(
    page.getByText(`You’re already enrolled as ${testName}.`, { exact: false }),
  ).toBeVisible();
  await expect(page.getByLabel("Room code")).toHaveCount(0);
  await page.getByRole("button", { name: "Continue", exact: true }).click();
  await expect(
    page.getByText(`Welcome, ${testName}.`, { exact: true }),
  ).toBeVisible();
  const after = participants().filter((p) => p.spec.displayName === testName);
  expect(after.map((p) => p.metadata.uid)).toEqual(
    before.map((p) => p.metadata.uid),
  );
});

test("invalid room code does not enroll", async ({ page }) => {
  await page.goto("/app/login");
  await page.getByLabel("Room code").fill("INVALID");
  await page.getByLabel("Display name").fill(testName);
  await page.getByRole("button", { name: "Continue", exact: true }).click();
  await expect(
    page.getByText("That code is invalid or joining has closed.", {
      exact: false,
    }),
  ).toBeVisible();
  expect(
    participants().filter((p) => p.spec.displayName === testName),
  ).toHaveLength(0);
});

test("a tampered form CSRF token does not enroll", async ({ page }) => {
  await page.goto("/app/login");
  await page.getByLabel("Room code").fill(room().status.joinCode.code);
  await page.getByLabel("Display name").fill(testName);
  await page
    .locator('input[name="csrf"]')
    .first()
    .evaluate((input) => {
      input.value = "forged";
    });
  await page.getByRole("button", { name: "Continue", exact: true }).click();
  await expect(
    page.getByText("Invalid form. Reload and try again.", { exact: true }),
  ).toBeVisible();
  expect(
    participants().filter((p) => p.spec.displayName === testName),
  ).toHaveLength(0);
});

test("closing enrollment removes the browser join form", async ({ page }) => {
  const enrollment = room().spec.enrollment;
  try {
    kube(
      "-n",
      "room-pass",
      "patch",
      "room",
      "demo",
      "--type=merge",
      "-p",
      JSON.stringify({ spec: { enrollment: "Closed" } }),
    );
    await page.goto("/app/login");
    await expect(
      page.getByText(
        "Joining is closed. If you already joined, return to the demo.",
        { exact: true },
      ),
    ).toBeVisible();
    await expect(page.getByLabel("Room code")).toHaveCount(0);
    await expect(
      page.getByRole("button", { name: "Continue", exact: true }),
    ).toHaveCount(0);
  } finally {
    kube(
      "-n",
      "room-pass",
      "patch",
      "room",
      "demo",
      "--type=merge",
      "-p",
      JSON.stringify({ spec: { enrollment } }),
    );
  }
});
