// Measures the web app in headless Chromium: what the login page and the file list load on a cold
// start, and how a folder with thousands of files behaves (loading all pages, scrolling, select all)
// in the list and in the grid view. Every run starts a new browser, so nothing is cached.
//
// Needs: a running server with the built web app, an account and a folder with many files
// (named like "file-1.txt"). Prints the median of all runs as JSON.
//
// Usage: node scripts/perf.mjs [--runs 5] [--files 2000]
//   --files: how many files of the folder are loaded (by scrolling) before scrolling and selecting.
// Settings: FYLVO_APP (default http://127.0.0.1:5292), FYLVO_USER, FYLVO_PASSWORD,
//           FYLVO_FOLDER (default cloudreve://my/bench), CHROME (default chromium on the PATH).
import puppeteer from "puppeteer-core";

const args = process.argv.slice(2);
const opt = (name, fallback) => (args.includes("--" + name) ? +args[args.indexOf("--" + name) + 1] : fallback);
const runs = opt("runs", 5);
const fileTarget = opt("files", 2000);
const APP = process.env.FYLVO_APP || "http://127.0.0.1:5292";
const USER = {
  email: process.env.FYLVO_USER || "bench@example.com",
  password: process.env.FYLVO_PASSWORD || "benchpass123",
};
const FOLDER = process.env.FYLVO_FOLDER || "cloudreve://my/bench";
const CHROME = process.env.CHROME || "/run/current-system/sw/bin/chromium";
const filesUrl = `${APP}/home?path=${encodeURIComponent(FOLDER)}`;

// Logs in and sets the layout of the folder on the server (it is stored per folder).
async function session(layout) {
  const res = await fetch(APP + "/api/v4/session/token", {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(USER),
  });
  const login = (await res.json()).data;
  const view = {
    page_size: 100,
    order: "created_at",
    order_direction: "asc",
    view: layout,
    thumbnail: true,
    gallery_width: 220,
  };
  await fetch(APP + "/api/v4/file/view", {
    method: "PATCH",
    headers: { "Content-Type": "application/json", Authorization: "Bearer " + login.token.access_token },
    body: JSON.stringify({ uri: FOLDER, view }),
  });
  const uid = login.user.id;
  return JSON.stringify({ current: uid, sessions: { [uid]: { ...login, settings: {} } }, anonymousSettings: {} });
}

// Counts requests and bytes of one page load (bytes as sent by the server, so compressed).
async function track(page) {
  const cdp = await page.createCDPSession();
  await cdp.send("Network.enable");
  const types = {};
  const stats = { requests: 0, jsBytes: 0, cssBytes: 0, fontBytes: 0, totalBytes: 0, jsFiles: 0 };
  cdp.on("Network.responseReceived", (e) => (types[e.requestId] = e.type));
  cdp.on("Network.loadingFinished", (e) => {
    stats.requests++;
    stats.totalBytes += e.encodedDataLength;
    const type = types[e.requestId];
    if (type == "Script") {
      stats.jsBytes += e.encodedDataLength;
      stats.jsFiles++;
    } else if (type == "Stylesheet") stats.cssBytes += e.encodedDataLength;
    else if (type == "Font") stats.fontBytes += e.encodedDataLength;
  });
  return stats;
}

const log = (m) => process.env.PERF_DEBUG && process.stderr.write(m + "\n");
const idle = (page) => page.waitForNetworkIdle({ idleTime: 1000, timeout: 60000 });
const sinceStart = (page) => page.evaluate(() => performance.now());

async function newPage(browser, layout) {
  const context = await browser.createBrowserContext();
  const page = await context.newPage();
  await page.setViewport({ width: 1440, height: 900, deviceScaleFactor: 1 });
  if (layout) {
    await page.evaluateOnNewDocument(
      (value) => {
        localStorage.setItem("cloudreve_session", value);
        localStorage.setItem("i18nextLng", "en-US");
      },
      await session(layout),
    );
  }
  return page;
}

async function heap(page) {
  const cdp = await page.createCDPSession();
  await cdp.send("HeapProfiler.collectGarbage");
  await cdp.send("Performance.enable");
  const { metrics } = await cdp.send("Performance.getMetrics");
  const m = Object.fromEntries(metrics.map((x) => [x.name, x.value]));
  return { heapMB: +(m.JSHeapUsedSize / 1048576).toFixed(1), domNodes: m.Nodes };
}

async function loginPage(browser) {
  const page = await newPage(browser);
  const stats = await track(page);
  await page.goto(APP + "/session");
  await page.waitForSelector("#email", { visible: true, timeout: 60000 });
  const readyMs = await sinceStart(page);
  await idle(page);
  return { readyMs, ...stats };
}

// The scrolling element of the file view, looked up once per page (the lookup itself is slow).
const scrollerOf = () => {
  if (!window.__scroller?.isConnected) {
    window.__scroller =
      document.querySelector("[data-virtuoso-scroller]") ||
      [...document.querySelectorAll("div")].find(
        (d) => d.scrollHeight > d.clientHeight + 50 && /auto|scroll/.test(getComputedStyle(d).overflowY),
      );
  }
  return window.__scroller;
};

// Watches the list requests: done when enough files came or there is no further page.
function watchList(page) {
  const state = { files: 0, done: false };
  page.on("response", async (res) => {
    if (!res.url().includes("/api/v4/file?")) return;
    const body = await res.json().catch(() => undefined);
    if (!body?.data?.files) return;
    state.files += body.data.files.length;
    if (!body.data.pagination?.next_token || state.files >= fileTarget) state.done = true;
  });
  return state;
}

// Scrolls the file view to the end until the files are loaded and shown.
async function loadAll(page, list) {
  const start = Date.now();
  while (!list.done && Date.now() - start < 600000) {
    await page.evaluate(`(${scrollerOf})().scrollTop = 1e9`);
    await new Promise((r) => setTimeout(r, 30));
  }
  // Two frames after the last page: it is drawn.
  await page.evaluate(() => new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r))));
  return Date.now() - start;
}

// Scrolls from top to bottom in steps of one screen and records the frame times.
async function scrollTest(page) {
  await page.evaluate(`window.scrollerOf = ${scrollerOf}`);
  return page.evaluate(async () => {
    const s = window.scrollerOf();
    s.scrollTop = 0;
    const frame = () => new Promise((r) => requestAnimationFrame((t) => r(t)));
    await frame();
    const steps = 60;
    const frames = [];
    let last = await frame();
    const start = last;
    for (let i = 1; i <= steps; i++) {
      s.scrollTop = ((s.scrollHeight - s.clientHeight) * i) / steps;
      const t = await frame();
      frames.push(t - last);
      last = t;
    }
    frames.sort((a, b) => a - b);
    return {
      scrollTotalMs: last - start,
      scrollWorstFrameMs: frames[frames.length - 1],
      scrollP90FrameMs: frames[Math.floor(frames.length * 0.9)],
    };
  });
}

async function selectAll(page) {
  await page.evaluate(() => {
    window.scrollerOf().scrollTop = 0;
    document.activeElement?.blur();
  });
  await page.evaluate(() => {
    window.__keyAt = 0;
    window.addEventListener("keydown", () => (window.__keyAt = performance.now()), { capture: true, once: true });
  });
  await page.keyboard.down("Control");
  await page.keyboard.press("a");
  await page.keyboard.up("Control");
  return page.evaluate(
    () =>
      new Promise((resolve) => {
        // Two frames after the key: the selection is drawn.
        requestAnimationFrame(() => requestAnimationFrame(() => resolve(performance.now() - window.__keyAt)));
      }),
  );
}

async function filesPage(browser, layout) {
  const page = await newPage(browser, layout);
  const stats = await track(page);
  const list = watchList(page);
  await page.goto(filesUrl);
  await page.waitForFunction(() => document.body.innerText.includes("file-"), { timeout: 60000 });
  const readyMs = await sinceStart(page);
  await idle(page);
  const first = { readyMs, ...stats };
  log(`${layout}: first page after ${readyMs} ms`);
  const loadAllMs = await loadAll(page, list);
  await new Promise((r) => setTimeout(r, 1500));
  log(`${layout}: ${list.files} files after ${loadAllMs} ms`);
  const mem = await heap(page);
  const scroll = await scrollTest(page);
  log(`${layout}: scrolled`);
  const selectMs = await selectAll(page);
  await page.close();
  return { first, files: list.files, loadAllMs, ...mem, ...scroll, selectMs };
}

const median = (values) => {
  const v = [...values].sort((a, b) => a - b);
  return v[Math.floor(v.length / 2)];
};

function collect(target, prefix, obj) {
  for (const [k, v] of Object.entries(obj)) {
    if (typeof v == "object") collect(target, `${prefix}${k}.`, v);
    else (target[prefix + k] ??= []).push(v);
  }
}

const all = {};
for (let i = 0; i < runs; i++) {
  const browser = await puppeteer.launch({
    executablePath: CHROME,
    headless: true,
    args: ["--no-first-run", "--disable-extensions", "--lang=en-US"],
    protocolTimeout: 600000,
  });
  try {
    collect(all, "login.", await loginPage(browser));
    log("login done");
    collect(all, "list.", await filesPage(browser, "list"));
    collect(all, "grid.", await filesPage(browser, "grid"));
  } finally {
    await browser.close();
  }
  process.stderr.write(`run ${i + 1}/${runs} done\n`);
}
log(JSON.stringify(all));
const result = Object.fromEntries(Object.entries(all).map(([k, v]) => [k, Math.round(median(v) * 10) / 10]));
console.log(JSON.stringify(result, null, 2));
