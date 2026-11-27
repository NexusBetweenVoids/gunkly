import type { Page } from "@playwright/test";
import { seedDemo } from "./demo";
import { expect, mailMark, nextMail, setTheme, test } from "./helpers";

// README and website screenshots with synthetic demo data (Alex, Sam, "Maple Street 12").
// Skipped in normal runs; `npm run screenshots` writes docs/assets/screens/*.png.
// Run it before every release; the website copies these files (`npm run sync` there).
// Phone shots are the viewport only (never full page), so the fixed tab bar sits at the bottom where it belongs.
test.skip(
  !process.env.GUNKLY_SCREENSHOTS,
  "set GUNKLY_SCREENSHOTS=1 (npm run screenshots)",
);
const OUT = "../docs/assets/screens/";

// The docs show the generic single sign-on label (no provider name set) instead of the e2e fake provider's "Test IdP".
async function genericSSO(page: Page) {
  await page.route("**/api/v1/instance", async (route) => {
    const res = await route.fetch();
    const body = await res.json();
    if (body.sso) body.sso.name = "";
    await route.fulfill({ response: res, json: body });
  });
}
test("README and website screenshots", async ({ world, browser }) => {
  test.setTimeout(180_000);
  // Alex is an admin, so the Administration shots have someone to show.
  const { alex, sam, h, gutters } = await seedDemo(world, {
    admin: true,
    names: true,
  });
  // A confirmed email address, so reminders by email are on and Notifications shows a working setup.
  const email = "alex@example.com";
  await alex.api.call(
    "POST",
    "/session/step-up",
    { password: alex.password },
    204,
  );
  const mark = mailMark();
  await alex.api.call("PATCH", "/account/email", { email });
  const confirm = await nextMail(email, /Confirm your email address/, mark);
  await alex.api.post("/account/email/verify", {
    token: new URL(confirm.link, world.baseURL).searchParams.get("token"),
  });

  type View = { width: number; height: number };
  const shot = async (
    path: string,
    file: string,
    theme: "light" | "dark",
    viewport: View,
    scale: number,
    ready: string | RegExp,
    opts: {
      as?: typeof alex | null; // null: signed out
      before?: (page: Page) => Promise<void>;
    } = {},
  ) => {
    const user = opts.as === undefined ? alex : opts.as;
    const phone = viewport.width < 500;
    const context = user
      ? await browser.newContext({
          storageState: await user.api.storageState(),
          viewport,
          deviceScaleFactor: scale,
          colorScheme: theme,
          baseURL: world.baseURL,
          isMobile: phone,
          hasTouch: phone,
        })
      : await world.newContext({
          viewport,
          deviceScaleFactor: scale,
          colorScheme: theme,
          isMobile: phone,
          hasTouch: phone,
        });
    const page = await context.newPage();
    await page.goto(path);
    await expect(page.getByText(ready).first()).toBeVisible();
    await setTheme(page, theme);
    if (opts.before) await opts.before(page);
    await page.evaluate(() => document.fonts.ready);
    await page.waitForTimeout(400);
    await page.screenshot({ path: OUT + file });
    await context.close();
  };
  const phone = { width: 360, height: 1015 };
  await shot(
    "/",
    "home-phone-light.png",
    "light",
    phone,
    1.5,
    "Descale the kettle",
  );
  await shot(
    "/",
    "home-phone-dark.png",
    "dark",
    phone,
    1.5,
    "Descale the kettle",
  );
  await shot(
    "/calendar",
    "calendar-desktop-dark.png",
    "dark",
    { width: 1100, height: 650 },
    1,
    "Subscribe",
  );
  await shot(
    `/chores/${gutters}/edit`,
    "chore-editor-desktop-light.png",
    "light",
    { width: 1100, height: 900 },
    1,
    "Take turns",
  );
  await shot(
    "/settings/household-people",
    "household-desktop-light.png",
    "light",
    { width: 1000, height: 830 },
    1,
    /^Invites$/,
  );

  await shot(
    "/calendar",
    "calendar-phone-light.png",
    "light",
    { width: 360, height: 860 },
    1.5,
    "Subscribe",
  );

  // Chore detail after Sam did one with a note, so its history isn't empty.
  const chores = await alex.api.get<{ id: string; name: string }[]>(
    `/households/${h.id}/chores`,
  );
  const bedding = chores.find((c) => c.name === "Wash the bedding")!.id;
  await sam.api.post(`/households/${h.id}/chores/${bedding}/complete`, {
    note: "Duvet covers too",
  });
  await shot(
    `/chores/${bedding}`,
    "chore-detail-phone-light.png",
    "light",
    phone,
    1.5,
    "Duvet covers too",
  );
  await alex.api.call("PATCH", "/me", { emailReminders: true });
  await shot(
    "/settings/notifications",
    "notifications-phone-dark.png",
    "dark",
    phone,
    1.5,
    "Ways to get reminders",
  );
  await shot(
    "/admin/users",
    "admin-users-desktop-light.png",
    "light",
    { width: 1100, height: 610 },
    1,
    "Sam",
  );

  // Someone new: the welcome flow's first step, and an invite link opened while signed out.
  const newcomer = await world.newUser({ username: "robin" });
  await newcomer.api.call("PATCH", "/me", { displayName: "Robin" });
  await shot(
    "/welcome",
    "welcome-phone-light.png",
    "light",
    { width: 360, height: 840 },
    1.5,
    "Name your household",
    { as: newcomer },
  );
  const invite = await alex.api.post<{ token: string }>(
    `/households/${h.id}/invites`,
    { role: "member", expiresInHours: 72 },
  );
  await shot(
    `/invite/${invite.token}`,
    "invite-phone-light.png",
    "light",
    { width: 360, height: 900 },
    1.5,
    "Maple Street 12",
    { as: null },
  );

  // The sign-in page with the passkey and single sign-on buttons (signed out, narrow screen).
  {
    const context = await world.newContext({
      viewport: { width: 420, height: 820 },
      deviceScaleFactor: 1.5,
      colorScheme: "light",
    });
    const page = await context.newPage();
    await genericSSO(page);
    await page.goto("/signin");
    await expect(
      page.getByRole("button", { name: "Sign in with a passkey" }),
    ).toBeVisible();
    await expect(
      page.getByRole("link", { name: "Sign in with SSO" }),
    ).toBeVisible();
    await setTheme(page, "light");
    await page.waitForTimeout(400);
    await page.screenshot({ path: OUT + "signin-passkey-sso-phone-light.png" });
    await context.close();
  }

  // Account, Sign-in & security with a registered passkey. Last: adding a passkey signs the other sessions out.
  {
    const context = await browser.newContext({
      storageState: await alex.api.storageState(),
      viewport: { width: 1000, height: 900 },
      colorScheme: "dark",
      baseURL: world.baseURL,
    });
    const page = await context.newPage();
    await genericSSO(page);
    const cdp = await context.newCDPSession(page);
    await cdp.send("WebAuthn.enable");
    await cdp.send("WebAuthn.addVirtualAuthenticator", {
      options: {
        protocol: "ctap2",
        transport: "internal",
        hasResidentKey: true,
        hasUserVerification: true,
        isUserVerified: true,
        automaticPresenceSimulation: true,
      },
    });
    await page.goto("/settings/security");
    const section = page.getByRole("region", { name: "Passkeys" });
    await section.getByRole("button", { name: "Add", exact: true }).click();
    await section.getByLabel("Name").fill("Alex’s laptop");
    await section.getByRole("button", { name: "Create passkey" }).click();
    const confirm = page.getByRole("dialog", { name: "Confirm it's you" });
    const saved = section.getByRole("button", { name: "I've saved them" });
    // The password check is skipped while the step-up from confirming the email address is still fresh.
    await expect(confirm.or(saved)).toBeVisible();
    if (await confirm.isVisible()) {
      await confirm.getByLabel("Password").fill(alex.password);
      await confirm.getByRole("button", { name: "Confirm" }).click();
    }
    await saved.click();
    await expect(section.getByText("Alex’s laptop")).toBeVisible();
    await setTheme(page, "dark");
    await expect(page.getByText("Passkey added.")).toHaveCount(0); // the toast is gone
    await page.waitForTimeout(400);
    // Just the "Ways to sign in" card (passkey list open), cropped from a window tall enough that the page never scrolls, so the
    // sticky header can't overlap it.
    await page.setViewportSize({ width: 1000, height: 2400 });
    await page.evaluate(() => window.scrollTo(0, 0));
    await page.waitForTimeout(300);
    const box = await page
      .getByRole("region", { name: "Ways to sign in" })
      .boundingBox();
    if (!box) throw new Error("Ways to sign in card not visible");
    await page.screenshot({
      path: OUT + "account-security-passkey-desktop-dark.png",
      clip: {
        x: box.x - 16,
        y: box.y - 16,
        width: box.width + 32,
        height: box.height + 32,
      },
    });
    // The whole page in light, freshly loaded: the verdict on top, then every way to sign in.
    await page.setViewportSize({ width: 1100, height: 900 });
    await page.reload();
    await expect(
      page.getByRole("region", { name: "Ways to sign in" }),
    ).toBeVisible();
    await setTheme(page, "light");
    await page.evaluate(() => document.fonts.ready);
    await page.waitForTimeout(400);
    await page.screenshot({ path: OUT + "account-security-desktop-light.png" });
    await context.close();
  }
});
