import { seedDemo } from "./demo";
import { expect, setTheme, test } from "./helpers";

// README and website screenshots with synthetic demo data (Alex, Sam, "Maple Street 12").
// Skipped in normal runs; `npm run screenshots` writes docs/assets/screens/*.png.
// Run it before every release; the website copies these files (`npm run sync` there).
test.skip(
  !process.env.GUNKLY_SCREENSHOTS,
  "set GUNKLY_SCREENSHOTS=1 (npm run screenshots)",
);
const OUT = "../docs/assets/screens/";
test("README and website screenshots", async ({ world, browser }) => {
  test.setTimeout(120_000);
  const { alex, gutters } = await seedDemo(world);

  const shot = async (
    path: string,
    file: string,
    theme: "light" | "dark",
    viewport: { width: number; height: number },
    scale: number,
    ready: string | RegExp,
  ) => {
    const context = await browser.newContext({
      storageState: await alex.api.storageState(),
      viewport,
      deviceScaleFactor: scale,
      colorScheme: theme,
      baseURL: world.baseURL,
    });
    const page = await context.newPage();
    await page.goto(path);
    await expect(page.getByText(ready).first()).toBeVisible();
    await setTheme(page, theme);
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
    { width: 1100, height: 760 },
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

  // The sign-in page with the passkey and single sign-on buttons (signed out, narrow screen).
  {
    const context = await world.newContext({
      viewport: { width: 420, height: 820 },
      deviceScaleFactor: 1.5,
      colorScheme: "light",
    });
    const page = await context.newPage();
    // The docs show the generic label (no provider name set) instead of the e2e fake provider's "Test IdP".
    await page.route("**/api/v1/instance", async (route) => {
      const res = await route.fetch();
      const body = await res.json();
      if (body.sso) body.sso.name = "";
      await route.fulfill({ response: res, json: body });
    });
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
    await confirm.getByLabel("Password").fill(alex.password);
    await confirm.getByRole("button", { name: "Confirm" }).click();
    await section.getByRole("button", { name: "I've saved them" }).click();
    await expect(section.getByText("Alex’s laptop")).toBeVisible();
    await setTheme(page, "dark");
    await expect(page.getByText("Passkey added.")).toHaveCount(0); // the toast is gone
    await page.waitForTimeout(400);
    // Just the "Ways to sign in" card (passkey list open), cropped from a window tall enough that the page never scrolls, so the
    // sticky header can't overlap it.
    await page.setViewportSize({ width: 1000, height: 2400 });
    await page.evaluate(() => window.scrollTo(0, 0));
    await page.waitForTimeout(300);
    const box = await page.getByRole("region", { name: "Ways to sign in" }).boundingBox();
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
    await context.close();
  }
});
