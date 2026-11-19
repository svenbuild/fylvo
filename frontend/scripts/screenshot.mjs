// Takes a screenshot of a running Fylvo dev setup, logged in as the local test user.
//
// Needs: the server on http://127.0.0.1:5212 with the test account below,
//        `yarn dev` on http://127.0.0.1:5173, and Google Chrome installed.
//
// Usage: node scripts/screenshot.mjs --out shot.png [--route home] [--width 1536] [--height 1024]
//          [--scheme dark|light] [--lang de-DE] [--anon] [--full] [--actions '<json>']
//
// Against another instance: set FYLVO_APP and FYLVO_API to its address, FYLVO_USER and
// FYLVO_PASSWORD to an account there.
//
// --actions is a JSON list of steps that run after the page has loaded, for example
//   '[{"rightClick":"::-p-text(Design.png)"},{"wait":400},{"click":"::-p-text(Teilen)"}]'
// Steps: click, rightClick, doubleClick, hover (selector), type (text), press (key),
//        combo (keys held together, e.g. ["Control","s"]), wait (ms),
//        upload (selector that opens a file chooser) together with file (local path).
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import puppeteer from "puppeteer-core";

const args = process.argv.slice(2);
const opt = (name, fallback) => {
  const i = args.indexOf("--" + name);
  return i === -1 ? fallback : args[i + 1];
};
const flag = (name) => args.includes("--" + name);

const APP = process.env.FYLVO_APP || "http://127.0.0.1:5173";
const API = process.env.FYLVO_API || "http://127.0.0.1:5212";
const USER = {
  email: process.env.FYLVO_USER || "test@fylvo.local",
  password: process.env.FYLVO_PASSWORD || "fylvo-local-test-1",
};
const CHROME = process.env.CHROME || "C:/Program Files/Google/Chrome/Application/chrome.exe";
const here = path.dirname(fileURLToPath(import.meta.url));
const themeFile = path.join(here, "..", "..", "inventory", "fylvo_theme.json");

const out = opt("out", "shot.png");
const route = opt("route", "home").replace(/^\/+/, "");
const lang = opt("lang", "de-DE");
const actions = JSON.parse(opt("actions", "[]"));

const browser = await puppeteer.launch({
  executablePath: CHROME,
  headless: true,
  args: ["--no-first-run", "--disable-extensions", `--lang=${lang}`],
});
try {
  const page = await browser.newPage();
  await page.setViewport({ width: +opt("width", 1536), height: +opt("height", 1024), deviceScaleFactor: 1 });
  await page.setExtraHTTPHeaders({ "Accept-Language": lang });
  await page.emulateMediaFeatures([{ name: "prefers-color-scheme", value: opt("scheme", "dark") }]);

  // On a local dev setup (any port), show the theme from the repo even when the local database
  // holds an older one. On a real instance, show what that instance really serves.
  const themes = JSON.stringify(JSON.parse(fs.readFileSync(themeFile, "utf8")));
  const useRepoTheme = ["127.0.0.1", "localhost"].includes(new URL(APP).hostname);
  await page.setRequestInterception(useRepoTheme);
  if (useRepoTheme) page.on("request", async (req) => {
    if (!req.url().includes("/api/v4/site/config/basic")) return req.continue();
    const real = await (await fetch(API + "/api/v4/site/config/basic")).json();
    real.data.themes = themes;
    real.data.default_theme = Object.keys(JSON.parse(themes))[0];
    req.respond({ status: 200, contentType: "application/json", body: JSON.stringify(real) });
  });
  page.on("pageerror", (e) => console.log("pageerror:", String(e).slice(0, 300)));

  if (!flag("anon")) {
    const login = await (
      await fetch(API + "/api/v4/session/token", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(USER),
      })
    ).json();
    const uid = login.data.user.id;
    const state = { current: uid, sessions: { [uid]: { ...login.data, settings: {} } }, anonymousSettings: {} };
    // Embedded pages from other servers (the office editor) must not receive the login.
    await page.evaluateOnNewDocument(
      (value, language, appOrigin) => {
        if (location.origin !== appOrigin) return;
        localStorage.setItem("cloudreve_session", value);
        localStorage.setItem("i18nextLng", language);
      },
      JSON.stringify(state),
      lang,
      new URL(APP).origin,
    );
  } else {
    await page.evaluateOnNewDocument((language) => localStorage.setItem("i18nextLng", language), lang);
  }

  await page.goto(`${APP}/${route}`, { waitUntil: "networkidle2", timeout: 120000 });
  await new Promise((r) => setTimeout(r, 1500));

  for (const step of actions) {
    if (step.click) await (await page.waitForSelector(step.click, { timeout: 8000 })).click();
    else if (step.rightClick) await (await page.waitForSelector(step.rightClick, { timeout: 8000 })).click({ button: "right" });
    else if (step.doubleClick) await (await page.waitForSelector(step.doubleClick, { timeout: 8000 })).click({ count: 2 });
    else if (step.hover) await (await page.waitForSelector(step.hover, { timeout: 8000 })).hover();
    else if (step.upload) {
      const target = await page.waitForSelector(step.upload, { timeout: 8000 });
      const [chooser] = await Promise.all([page.waitForFileChooser({ timeout: 8000 }), target.click()]);
      await chooser.accept([step.file]);
    }
    else if (step.type) await page.keyboard.type(step.type);
    else if (step.press) await page.keyboard.press(step.press);
    else if (step.combo) {
      for (const key of step.combo) await page.keyboard.down(key);
      for (const key of [...step.combo].reverse()) await page.keyboard.up(key);
    }
    await new Promise((r) => setTimeout(r, step.wait ?? 500));
  }

  await page.screenshot({ path: out, fullPage: flag("full") });
  console.log("saved", out, "at", page.url());
} finally {
  await browser.close();
}
