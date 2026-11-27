import { execFileSync } from "node:child_process";
import { mkdirSync, writeFileSync } from "node:fs";
import { homedir } from "node:os";
import { join } from "node:path";
import type { Browser, Page } from "@playwright/test";
import { seedDemo } from "./demo";
import { setTheme, test, type World } from "./helpers";

// Design review: a full-page screenshot of every screen, on desktop and phone, in light and dark,
// with the synthetic demo data, plus an index.html contact sheet. Skipped in normal runs.
//   npm run design-review                  -> ~/gunkly-design-review/<date>/
//   GUNKLY_DESIGN_OUT=/some/dir GUNKLY_DESIGN_LANG=nl npm run design-review
test.skip(
  !process.env.GUNKLY_DESIGN_REVIEW,
  "set GUNKLY_DESIGN_REVIEW=1 (npm run design-review)",
);

const LANG = process.env.GUNKLY_DESIGN_LANG || "en";
const OUT =
  process.env.GUNKLY_DESIGN_OUT ||
  join(
    homedir(),
    "gunkly-design-review",
    `${new Date().toISOString().slice(0, 10)}${LANG === "en" ? "" : `-${LANG}`}`,
  );
const VIEWS = [
  { name: "desktop", viewport: { width: 1280, height: 860 }, scale: 1 },
  { name: "phone", viewport: { width: 390, height: 844 }, scale: 2 },
] as const;
const THEMES = ["light", "dark"] as const;

type Screen = {
  id: string;
  title: string;
  path: string;
  note?: string;
  signedOut?: boolean;
  newcomer?: boolean;
};
type Shot = { screen: Screen; view: string; theme: string; file: string };

test("design review: every screen, desktop and phone, light and dark", async ({
  world,
  browser,
}) => {
  test.setTimeout(15 * 60_000);
  mkdirSync(OUT, { recursive: true });
  // Sign-up open (as on the hosted service), so the sign-up form and the sign-in page's "New to
  // Gunkly?" box show; restored at the end.
  const before = await world.admin.get<{
    openSignup: boolean;
    requirePow: boolean;
  }>("/admin/settings");
  await world.admin.call("PATCH", "/admin/settings", {
    openSignup: true,
    requirePow: false,
  });
  try {
    const { alex, h, gutters } = await seedDemo(world, { admin: true });
    if (LANG !== "en") await alex.api.call("PATCH", "/me", { locale: LANG });
    const newcomer = await world.newUser(); // signed in, no household yet
    if (LANG !== "en")
      await newcomer.api.call("PATCH", "/me", { locale: LANG });
    const invite = await alex.api.post<{ token: string }>(
      `/households/${h.id}/invites`,
      { role: "member", expiresInHours: 72 },
    );
    const chores = await alex.api.get<{ id: string; name: string }[]>(
      `/households/${h.id}/chores`,
    );
    const kettle = chores.find((c) => c.name === "Descale the kettle")!.id;

    const screens: Screen[] = [
      // Signed out
      { id: "signin", title: "Sign in", path: "/signin", signedOut: true },
      {
        id: "signup",
        title: "Create an account",
        path: "/signup",
        signedOut: true,
        note: "Only reachable when sign-up is open",
      },
      {
        id: "forgot-password",
        title: "Forgot password",
        path: "/forgot-password",
        signedOut: true,
      },
      {
        id: "reset-password",
        title: "Reset password (expired link)",
        path: "/reset-password?token=expired",
        signedOut: true,
      },
      {
        id: "verify-email",
        title: "Confirm email (expired link)",
        path: "/verify-email?token=expired",
        signedOut: true,
      },
      {
        id: "invite",
        title: "Invite link (signed out)",
        path: `/invite/${invite.token}`,
        signedOut: true,
      },
      // First run
      {
        id: "home-empty",
        title: "Home without a household",
        path: "/",
        newcomer: true,
      },
      {
        id: "welcome",
        title: "Welcome flow, first step",
        path: "/welcome",
        newcomer: true,
      },
      // Everyday screens
      { id: "home", title: "Home (chores)", path: "/" },
      { id: "calendar", title: "Calendar", path: "/calendar" },
      { id: "chore-detail", title: "Chore detail", path: `/chores/${kettle}` },
      { id: "chore-new", title: "Add a chore", path: "/chores/new" },
      {
        id: "chore-edit",
        title: "Edit a chore (taking turns)",
        path: `/chores/${gutters}/edit`,
      },
      { id: "household-new", title: "New household", path: "/households/new" },
      // Settings: you
      {
        id: "settings-profile",
        title: "Settings: Profile",
        path: "/settings/profile",
      },
      {
        id: "settings-security",
        title: "Settings: Sign-in & security",
        path: "/settings/security",
      },
      {
        id: "settings-notifications",
        title: "Settings: Notifications",
        path: "/settings/notifications",
      },
      { id: "settings-ai", title: "Settings: AI", path: "/settings/ai" },
      {
        id: "settings-integrations",
        title: "Settings: Integrations",
        path: "/settings/integrations",
      },
      {
        id: "settings-data",
        title: "Settings: Your data",
        path: "/settings/data",
      },
      // Settings: household
      {
        id: "household-general",
        title: "Household: General",
        path: "/settings/household-general",
      },
      {
        id: "household-people",
        title: "Household: People",
        path: "/settings/household-people",
      },
      {
        id: "household-notifications",
        title: "Household: Notifications",
        path: "/settings/household-notifications",
      },
      {
        id: "household-connections",
        title: "Household: Connections",
        path: "/settings/household-connections",
      },
      {
        id: "household-data",
        title: "Household: Data",
        path: "/settings/household-data",
      },
      // Administration
      ...[
        "users",
        "signin",
        "email",
        "connections",
        "ai",
        "data",
        "limits",
        "updates",
      ].map((g) => ({
        id: `admin-${g}`,
        title: `Administration: ${g[0]!.toUpperCase()}${g.slice(1)}`,
        path: `/admin/${g}`,
      })),
      {
        id: "setup-guide",
        title: "Administration: setup guide",
        path: "/setup/guide",
      },
      { id: "not-found", title: "Page not found", path: "/no-such-page" },
    ];

    const shots: Shot[] = [];
    for (const view of VIEWS) {
      for (const theme of THEMES) {
        const pages = {
          out: await open(browser, world, view, theme),
          alex: await open(
            browser,
            world,
            view,
            theme,
            await alex.api.storageState(),
          ),
          newcomer: await open(
            browser,
            world,
            view,
            theme,
            await newcomer.api.storageState(),
          ),
        };
        for (const [i, screen] of screens.entries()) {
          const page = screen.signedOut
            ? pages.out
            : screen.newcomer
              ? pages.newcomer
              : pages.alex;
          const file = `${String(i + 1).padStart(2, "0")}-${screen.id}__${view.name}-${theme}.png`;
          await page.goto(screen.path);
          await settle(page, theme);
          await page.screenshot({ path: join(OUT, file), fullPage: true });
          shots.push({ screen, view: view.name, theme, file });
        }
        for (const p of Object.values(pages)) await p.context().close();
      }
    }
    writeFileSync(join(OUT, "index.html"), contactSheet(screens, shots));
    // One file to send: <folder>.zip next to the folder.
    execFileSync("python3", [
      "-c",
      'import shutil,sys; shutil.make_archive(sys.argv[1], "zip", sys.argv[1])',
      OUT,
    ]);
    console.log(
      `design review: ${shots.length} screenshots of ${screens.length} screens in ${OUT} (and ${OUT}.zip)`,
    );
  } finally {
    await world.admin.call("PATCH", "/admin/settings", {
      openSignup: before.openSignup,
      requirePow: before.requirePow,
    });
  }
});

async function open(
  browser: Browser,
  world: World,
  view: (typeof VIEWS)[number],
  theme: string,
  storageState?: NonNullable<
    Parameters<Browser["newContext"]>[0]
  >["storageState"],
): Promise<Page> {
  const context = await browser.newContext({
    baseURL: world.baseURL,
    viewport: view.viewport,
    deviceScaleFactor: view.scale,
    isMobile: view.name === "phone",
    hasTouch: view.name === "phone",
    colorScheme: theme as "light" | "dark",
    locale: LANG,
    timezoneId: "Europe/Amsterdam",
    ...(storageState ? { storageState } : {}),
  });
  return context.newPage();
}

// Wait until the screen has loaded its data and lazy parts, and toasts are gone.
async function settle(page: Page, theme: "light" | "dark"): Promise<void> {
  await page.waitForLoadState("networkidle");
  await page
    .getByRole("status")
    .filter({ hasText: /^(Loading|Laden|Wird geladen|Chargement|Cargando)/ })
    .waitFor({ state: "detached", timeout: 10_000 })
    .catch(() => {});
  await setTheme(page, theme);
  await page.evaluate(() => document.fonts.ready);
  await page.waitForTimeout(500);
}

function contactSheet(screens: Screen[], shots: Shot[]): string {
  const esc = (s: string) =>
    s.replace(
      /[&<>"]/g,
      (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;" })[c]!,
    );
  const rows = screens
    .map((s) => {
      const imgs = shots
        .filter((x) => x.screen === s)
        .map(
          (x) =>
            `<figure class="${x.view}"><a href="${x.file}"><img src="${x.file}" loading="lazy" alt="${esc(`${s.title}, ${x.view}, ${x.theme}`)}"></a><figcaption>${x.view} · ${x.theme}</figcaption></figure>`,
        )
        .join("");
      return `<section id="${s.id}"><h2>${esc(s.title)} <code>${esc(s.path.replace(/\/invite\/.+/, "/invite/…").replace(/\/chores\/[^/]+/, "/chores/…"))}</code></h2>${s.note ? `<p>${esc(s.note)}</p>` : ""}<div class="row">${imgs}</div></section>`;
    })
    .join("\n");
  return `<!doctype html><html lang="en"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>Gunkly design review</title>
<style>
body{font:15px/1.5 system-ui,sans-serif;margin:0 auto;padding:24px 16px;max-width:1600px;background:#f4f4f5;color:#18181b}
h1{margin:0 0 4px}h2{font-size:17px;margin:32px 0 8px}code{font-size:13px;color:#52525b;font-weight:normal}
nav{columns:3 220px;margin:16px 0}nav a{display:block;color:#2563eb}
.row{display:flex;gap:16px;align-items:flex-start;overflow-x:auto}
figure{margin:0;flex:none}figure.desktop{width:420px}figure.phone{width:170px}
img{width:100%;border:1px solid #d4d4d8;border-radius:6px;background:#fff}figcaption{font-size:12px;color:#71717a}
</style>
<h1>Gunkly design review</h1>
<p>${screens.length} screens · desktop (1280 px) and phone (390 px) · light and dark · language: ${LANG} · demo data. Click a picture for the full size.</p>
<nav>${screens.map((s) => `<a href="#${s.id}">${esc(s.title)}</a>`).join("")}</nav>
${rows}
</html>
`;
}
