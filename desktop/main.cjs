"use strict";

const { app, BrowserWindow, dialog, Menu, session, shell } = require("electron");
const { autoUpdater } = require("electron-updater");
const log = require("electron-log");
const fs = require("node:fs");
const http = require("node:http");
const https = require("node:https");
const net = require("node:net");
const path = require("node:path");
const { spawn } = require("node:child_process");
const metadata = require("./package.json");

const UPDATE_INTERVAL_MS = 6 * 60 * 60 * 1000;
// The first start of a new install creates the embedded PostgreSQL (initdb
// takes several seconds on Windows with Defender scanning every file), so the
// backend gets two minutes to answer.
const STARTUP_TIMEOUT_MS = 120_000;
const WORKSPACE_PICKER_MARKER = "workspace-picker-seen";
const REMOTE_SERVER_ENV = "NODEVAS_SERVER_URL";
const DEFAULT_REMOTE_SERVER_URL = metadata.defaultServerURL || "";
const SIGNED_UPDATES = metadata.signedUpdates === true;
let backend = null;
let mainWindow = null;
let backendURL = "";
let remoteMode = false;
let quitting = false;
let workspacePickerBusy = false;
let backendStopPromise = null;
let updateTimer = null;
let manualUpdateCheck = false;
// The local backend's spawn arguments, kept so it can be started again on the
// same port (the page's origin, and with it its stored preferences, stays).
let backendLaunch = null;
let respawnTimer = null;
let respawnAttempts = 0;
let backendStartedAt = 0;
let unresponsivePrompt = false;
let rendererCrashes = [];

const RESPAWN_MIN_DELAY_MS = 1_000;
const RESPAWN_MAX_DELAY_MS = 30_000;
// A backend that stayed up this long counts as healthy again: its next exit
// starts the backoff over.
const RESPAWN_HEALTHY_MS = 60_000;
// Consecutive failed starts after which the app gives up and says so.
const RESPAWN_MAX_ATTEMPTS = 6;
// Renderer crashes within this window that are reloaded before giving up.
const RENDERER_CRASH_WINDOW_MS = 60_000;
const RENDERER_CRASH_LIMIT = 3;

log.initialize();
autoUpdater.logger = log;
autoUpdater.autoDownload = false;
autoUpdater.autoInstallOnAppQuit = true;
autoUpdater.channel = metadata.build?.publish?.[0]?.channel || "testflight";
// Only the testflight channel is allowed to pull prereleases; a stable build
// must never be silently moved onto a pre-release feed.
autoUpdater.allowPrerelease = autoUpdater.channel === "testflight";

function reservePort() {
  return new Promise((resolve, reject) => {
    const server = net.createServer();
    server.unref();
    server.once("error", reject);
    server.listen(0, "127.0.0.1", () => {
      const address = server.address();
      const port = typeof address === "object" && address ? address.port : 0;
      server.close((error) => (error ? reject(error) : resolve(port)));
    });
  });
}

function backendExecutable() {
  const binaryName = process.platform === "win32" ? "nodevas.exe" : "nodevas";
  if (app.isPackaged) return path.join(process.resourcesPath, "bin", binaryName);
  return path.resolve(__dirname, "..", binaryName);
}

// The pinned PostgreSQL binaries the release bundles for this platform
// (desktop/resources/pg/<platform>-<arch>, verified at release time by
// scripts/verify-pg-bundle.mjs and again by the backend against the
// manifest compiled into it). Unpackaged runs have none, and the backend
// falls back to its verified first-run download.
function bundledPostgres() {
  if (!app.isPackaged) return undefined;
  const dir = path.join(process.resourcesPath, "pg", `${process.platform}-${process.arch}`);
  return fs.existsSync(dir) ? dir : undefined;
}

// A small window saying what the wait is for, shown only if the backend is
// slow to answer (the first start creates the database).
function showPreparingWindow() {
  const window = new BrowserWindow({
    width: 360,
    height: 120,
    frame: false,
    resizable: false,
    show: false,
    webPreferences: { sandbox: true, contextIsolation: true, javascript: false },
  });
  const html =
    "<!doctype html><meta charset=utf-8><body style=\"margin:0;display:flex;align-items:center;" +
    "justify-content:center;height:100vh;font:15px system-ui,sans-serif;background:#1d1f24;color:#e6e6e6\">" +
    "正在準備資料庫…</body>";
  void window.loadURL(`data:text/html;charset=utf-8,${encodeURIComponent(html)}`);
  window.once("ready-to-show", () => {
    if (!window.isDestroyed()) window.show();
  });
  return window;
}

function configuredServerURL() {
  const args = process.argv;
  let value = process.env[REMOTE_SERVER_ENV] || "";
  for (let index = 0; index < args.length; index += 1) {
    const arg = args[index];
    if (arg === "--server-url") {
      value = args[index + 1] || "";
      index += 1;
    } else if (arg.startsWith("--server-url=")) {
      value = arg.slice("--server-url=".length);
    }
  }
  // Keep packaged and development builds local unless an explicit remote URL
  // is supplied. Deployment-specific hosts must never be committed here.
  if (!value && app.isPackaged) value = DEFAULT_REMOTE_SERVER_URL;
  value = value.trim();
  if (!value) return "";
  let parsed;
  try {
    parsed = new URL(value);
  } catch {
    throw new Error(`${REMOTE_SERVER_ENV} must be a valid http(s) URL`);
  }
  if (!new Set(["http:", "https:"]).has(parsed.protocol)) {
    throw new Error(`${REMOTE_SERVER_ENV} must use http or https`);
  }
  if (parsed.username || parsed.password || parsed.hash) {
    throw new Error(`${REMOTE_SERVER_ENV} must not contain credentials or a fragment`);
  }
  if (
    parsed.protocol === "http:" &&
    !new Set(["localhost", "127.0.0.1", "::1"]).has(parsed.hostname)
  ) {
    throw new Error("a remote Nodevas server must use HTTPS");
  }
  parsed.search = "";
  parsed.pathname = parsed.pathname.replace(/\/+$/, "");
  return parsed.toString().replace(/\/$/, "");
}

function requestModule(url) {
  return new URL(url).protocol === "https:" ? https : http;
}

// waitForBackend resolves once url answers. Given the child that is to
// answer, it rejects as soon as that child exits or fails to start: a wait
// must not outlive its process and be answered, or timed out, by the next one.
function waitForBackend(url, child = null) {
  const deadline = Date.now() + STARTUP_TIMEOUT_MS;
  return new Promise((resolve, reject) => {
    let settled = false;
    const settle = (error) => {
      if (settled) return;
      settled = true;
      if (child) {
        child.removeListener("exit", onExit);
        child.removeListener("error", onError);
      }
      if (error) reject(error);
      else resolve();
    };
    const onExit = (code, signal) =>
      settle(new Error(`後端服務在回應前結束 (exit=${code ?? "unknown"}, signal=${signal ?? "none"})`));
    const onError = (error) => settle(error);
    if (child) {
      if (child.exitCode !== null || child.signalCode !== null) {
        onExit(child.exitCode, child.signalCode);
        return;
      }
      child.once("exit", onExit);
      child.once("error", onError);
    }
    const probe = () => {
      if (settled) return;
      if (Date.now() >= deadline) {
        settle(new Error("後端服務啟動逾時"));
        return;
      }
      const request = requestModule(url).get(url, (response) => {
        response.resume();
        if (response.statusCode && response.statusCode < 500) {
          settle();
          return;
        }
        setTimeout(probe, 180);
      });
      request.setTimeout(800, () => request.destroy());
      request.on("error", () => setTimeout(probe, 180));
    };
    probe();
  });
}

async function startBackend() {
  const configured = configuredServerURL();
  if (configured) {
    remoteMode = true;
    backendURL = configured;
    await waitForBackend(backendURL);
    log.info("Connected to remote Nodevas server", { url: backendURL });
    return;
  }

  const executable = backendExecutable();
  const workspace = path.join(app.getPath("userData"), "workspace");
  await fs.promises.mkdir(workspace, { recursive: true });
  await fs.promises.access(executable, fs.constants.X_OK);

  const port = await reservePort();
  backendURL = `http://127.0.0.1:${port}`;
  backendLaunch = { executable, workspace, port };
  spawnBackend();
  let preparing = null;
  const preparingTimer = setTimeout(() => {
    preparing = showPreparingWindow();
  }, 1500);
  try {
    await waitForBackend(backendURL);
  } finally {
    clearTimeout(preparingTimer);
    if (preparing && !preparing.isDestroyed()) preparing.destroy();
  }
}

function spawnBackend() {
  const { executable, workspace, port } = backendLaunch;
  const env = { ...process.env };
  const pgBinaries = bundledPostgres();
  if (pgBinaries) env.NODEVAS_PG_BINARIES = pgBinaries;
  // stdin is a pipe the backend watches (--stop-on-stdin-close): closing it
  // is the graceful stop on every platform, Windows included, where kill()
  // is TerminateProcess and would skip the database's shutdown. If this
  // process dies, the pipe closes and the backend stops too.
  const child = spawn(
    executable,
    ["serve", "-project", workspace, "-port", String(port), "--stop-on-stdin-close"],
    { windowsHide: true, stdio: ["pipe", "pipe", "pipe"], env },
  );
  backend = child;
  backendStartedAt = Date.now();
  child.stdin.on("error", () => {});
  child.stdout.on("data", (chunk) => log.info(String(chunk).trim()));
  child.stderr.on("data", (chunk) => log.info(String(chunk).trim()));
  let ended = false;
  const onEnd = (code, signal) => {
    if (ended) return;
    ended = true;
    if (backend === child) backend = null;
    if (quitting) return;
    log.error("Nodevas backend stopped", { code, signal });
    scheduleBackendRespawn({ code, signal });
  };
  child.once("error", (error) => {
    log.error("Nodevas backend could not start", error);
    // A process that never started sends no exit; end it here instead.
    if (child.pid === undefined) onEnd(null, null);
  });
  child.once("exit", onEnd);
  return child;
}

// The local service ended on its own (a crash, a fatal error): start it again
// on the same port with a growing delay, and bring the page back once it
// answers. Only after several starts in a row fail does the app give up and
// say so, as it used to on the first exit.
function scheduleBackendRespawn(exit) {
  if (!backendLaunch || respawnTimer || quitting) return;
  if (Date.now() - backendStartedAt > RESPAWN_HEALTHY_MS) respawnAttempts = 0;
  respawnAttempts += 1;
  if (respawnAttempts > RESPAWN_MAX_ATTEMPTS) {
    void dialog.showMessageBox({
      type: "error",
      title: "Nodevas 已停止",
      message: "本機服務意外結束，重新啟動也未成功，請重新開啟應用程式。",
      detail: `exit=${exit.code ?? "unknown"}, signal=${exit.signal ?? "none"}`,
    });
    return;
  }
  const delay = Math.min(RESPAWN_MIN_DELAY_MS * 2 ** (respawnAttempts - 1), RESPAWN_MAX_DELAY_MS);
  log.info("Restarting the Nodevas backend", { attempt: respawnAttempts, delay });
  respawnTimer = setTimeout(async () => {
    respawnTimer = null;
    if (quitting) return;
    let child = null;
    try {
      child = spawnBackend();
      await waitForBackend(backendURL, child);
      log.info("Nodevas backend is back");
      if (mainWindow && !mainWindow.isDestroyed()) void mainWindow.loadURL(backendURL);
    } catch (error) {
      if (quitting) return;
      // A child that exited (or never started) has already scheduled the
      // next try from its exit handler; one still running that never
      // answered is stopped, and its exit does the same. Anything else is
      // not this attempt's to stop.
      log.error("Nodevas backend did not come back", error);
      if (child && backend === child) void stopBackend();
    }
  }, delay);
}

function backendRequest(method, pathname, body) {
  return new Promise((resolve, reject) => {
    const request = requestModule(backendURL).request(
      new URL(pathname, backendURL),
      {
        method,
        headers: body
          ? {
              "content-type": "application/json",
              "content-length": Buffer.byteLength(body),
            }
          : undefined,
        timeout: 5_000,
      },
      (response) => {
        let data = "";
        response.setEncoding("utf8");
        response.on("data", (chunk) => {
          data += chunk;
        });
        response.on("end", () => {
          const status = response.statusCode ?? 0;
          if (status < 200 || status >= 300) {
            reject(new Error(data || `backend returned HTTP ${status}`));
            return;
          }
          try {
            resolve(data ? JSON.parse(data) : null);
          } catch (error) {
            reject(new Error(`invalid backend response: ${error.message}`));
          }
        });
      },
    );
    request.on("timeout", () => request.destroy(new Error("backend request timed out")));
    request.on("error", reject);
    if (body) request.write(body);
    request.end();
  });
}

function workspacePickerMarker() {
  return path.join(app.getPath("userData"), WORKSPACE_PICKER_MARKER);
}

async function connectWorkspaceFromDialog() {
  if (remoteMode) {
    const owner = mainWindow && !mainWindow.isDestroyed() ? mainWindow : undefined;
    const options = {
      type: "info",
      title: "遠端工作區",
      message: "目前已連接遠端 Nodevas 伺服器。",
      detail: "請在伺服器的工作區管理介面操作；桌面 App 不會讀取或選取遠端主機的資料夾。",
    };
    if (owner) await dialog.showMessageBox(owner, options);
    else await dialog.showMessageBox(options);
    return false;
  }
  if (workspacePickerBusy) return false;
  workspacePickerBusy = true;
  const owner = mainWindow && !mainWindow.isDestroyed() ? mainWindow : undefined;
  try {
    const options = {
      title: "連接工作區",
      buttonLabel: "連接",
      properties: ["openDirectory", "createDirectory"],
    };
    const result = owner
      ? await dialog.showOpenDialog(owner, options)
      : await dialog.showOpenDialog(options);
    const selectedPath = result.filePaths[0];
    if (result.canceled || !selectedPath) return null;

    await backendRequest(
      "POST",
      "/api/workspaces/add",
      JSON.stringify({ path: selectedPath }),
    );
    if (mainWindow && !mainWindow.isDestroyed()) {
      await mainWindow.webContents.reload();
    }
    return true;
  } catch (error) {
    log.error("Connect workspace failed", error);
    const options = {
      type: "error",
      title: "無法連接工作區",
      message: "Nodevas 無法開啟選取的資料夾。",
      detail: error instanceof Error ? error.message : String(error),
    };
    if (owner) await dialog.showMessageBox(owner, options);
    else await dialog.showMessageBox(options);
    return false;
  } finally {
    workspacePickerBusy = false;
  }
}

async function promptForInitialWorkspace() {
  if (!app.isPackaged || remoteMode) return;
  const marker = workspacePickerMarker();
  try {
    await fs.promises.access(marker);
    return;
  } catch {
    // First launch (or an upgrade from a build without the picker).
  }

  try {
    const projects = await backendRequest("GET", "/api/projects");
    const defaultWorkspace = path.join(app.getPath("userData"), "workspace");
    const alreadyConfigured =
      projects?.workspaces?.length > 1 ||
      (projects?.workspace &&
        path.resolve(projects.workspace) !== path.resolve(defaultWorkspace));
    if (alreadyConfigured) {
      await fs.promises.writeFile(marker, "configured\n");
      return;
    }
  } catch (error) {
    log.warn("Could not inspect initial workspace", error);
  }

  const result = await connectWorkspaceFromDialog();
  if (result === null) {
    await fs.promises.writeFile(marker, "skipped\n");
  } else if (result) {
    await fs.promises.writeFile(marker, "configured\n");
  }
}

function stopBackend() {
  if (respawnTimer) {
    clearTimeout(respawnTimer);
    respawnTimer = null;
  }
  if (backendStopPromise) return backendStopPromise;
  if (!backend) return Promise.resolve();
  const processToStop = backend;
  backend = null;
  backendStopPromise = new Promise((resolve) => {
    let finished = false;
    const forceTimer = setTimeout(() => {
      if (processToStop.exitCode === null) processToStop.kill("SIGKILL");
    }, 35_000);
    forceTimer.unref();
    processToStop.once("exit", () => {
      if (finished) return;
      finished = true;
      clearTimeout(forceTimer);
      resolve();
    });
    // The backend shuts down when its stdin closes: the HTTP server, the
    // final backup, then its embedded PostgreSQL, cleanly. SIGKILL above is
    // the fallback for a backend that hangs.
    processToStop.stdin?.end();
  }).finally(() => {
    backendStopPromise = null;
  });
  return backendStopPromise;
}

// Packaged builds carry the icon in the executable itself; this covers the
// unpackaged run and Linux, where the window icon comes from the process.
function windowIcon() {
  const file = path.resolve(__dirname, "build", "icon.png");
  return fs.existsSync(file) ? file : undefined;
}

function createWindow() {
  mainWindow = new BrowserWindow({
    width: 1440,
    height: 920,
    minWidth: 960,
    minHeight: 640,
    title: "Nodevas",
    icon: windowIcon(),
    backgroundColor: "#10171c",
    show: false,
    webPreferences: {
      contextIsolation: true,
      nodeIntegration: false,
      sandbox: true,
    },
  });
  mainWindow.once("ready-to-show", () => mainWindow?.show());
  mainWindow.on("closed", () => {
    mainWindow = null;
  });
  watchRenderer(mainWindow);
  void mainWindow.loadURL(backendURL);
}

// The page itself can fail too: the renderer process crashing or being
// killed, a page stuck in a loop, or the backend not answering a load. Each
// gets a way back instead of a blank or frozen window.
function watchRenderer(window) {
  const contents = window.webContents;
  contents.on("render-process-gone", (_event, details) => {
    log.error("Renderer process gone", details);
    if (quitting || window.isDestroyed() || details.reason === "clean-exit") return;
    const now = Date.now();
    rendererCrashes = rendererCrashes.filter((at) => now - at < RENDERER_CRASH_WINDOW_MS);
    rendererCrashes.push(now);
    if (rendererCrashes.length > RENDERER_CRASH_LIMIT) {
      void dialog.showMessageBox(window, {
        type: "error",
        title: "Nodevas 畫面異常",
        message: "畫面短時間內多次結束，已停止自動重新載入。",
        detail: `reason=${details.reason}, exitCode=${details.exitCode}`,
      });
      return;
    }
    contents.reload();
  });
  contents.on("unresponsive", async () => {
    log.warn("Renderer unresponsive");
    if (unresponsivePrompt || window.isDestroyed()) return;
    unresponsivePrompt = true;
    try {
      const answer = await dialog.showMessageBox(window, {
        type: "warning",
        buttons: ["重新載入", "繼續等待"],
        defaultId: 1,
        cancelId: 1,
        title: "Nodevas 沒有回應",
        message: "畫面目前沒有回應。要重新載入嗎？",
      });
      if (answer.response === 0 && !window.isDestroyed()) contents.reload();
    } finally {
      unresponsivePrompt = false;
    }
  });
  contents.on("responsive", () => log.info("Renderer responsive again"));
  contents.on("did-fail-load", (_event, errorCode, errorDescription, validatedURL, isMainFrame) => {
    // -3 is ERR_ABORTED: a navigation replaced by another, not a failure.
    if (!isMainFrame || errorCode === -3 || window.isDestroyed()) return;
    log.warn("Page failed to load", { errorCode, errorDescription, url: validatedURL });
    void contents.loadURL(retryPageURL(errorDescription));
  });
}

function escapeHTML(text) {
  return String(text).replace(/[&<>"']/g, (c) => `&#${c.charCodeAt(0)};`);
}

// A minimal local page for a load that failed: it says so and tries the
// backend again every few seconds, or at once on the link.
function retryPageURL(reason) {
  const target = escapeHTML(backendURL);
  const html = `<!doctype html><html lang="zh-Hant"><head><meta charset="utf-8">
<meta http-equiv="refresh" content="5;url=${target}">
<title>Nodevas</title>
<style>body{margin:0;display:grid;place-items:center;height:100vh;background:#10171c;color:#d8e1e8;
font:14px system-ui,sans-serif}main{max-width:420px;padding:24px;text-align:center}
h1{font-size:18px}a{color:#7cc4d4}small{color:#8a97a3}</style></head><body><main>
<h1>正在連線到 Nodevas…</h1>
<p>本機服務暫時沒有回應，每 5 秒會自動重試。</p>
<p><a href="${target}">立即重試</a></p>
<small>${escapeHTML(reason || "")}</small>
</main></body></html>`;
  return `data:text/html;charset=utf-8,${encodeURIComponent(html)}`;
}

// The renderer only shows the configured Nodevas backend; it has no need for camera,
// microphone, geolocation, notifications, clipboard reads or anything else the
// permission system gates. Deny the lot, for every session and every frame.
function denyAllPermissions() {
  const target = session.defaultSession;
  target.setPermissionRequestHandler((_webContents, _permission, callback) => {
    callback(false);
  });
  target.setPermissionCheckHandler(() => false);
}

// Navigation policy lives on the app, not on one window, so any WebContents
// created later (a devtools-spawned view, a future window) inherits it instead
// of silently starting out unguarded.
function applyNavigationPolicy(contents) {
  contents.setWindowOpenHandler(({ url }) => {
    if (!isBackendURL(url)) void openExternalURL(url);
    return { action: "deny" };
  });
  const guardNavigation = (event, url) => {
    // will-frame-navigate fires first and will-navigate follows for the main
    // frame; if the first one already vetoed the navigation there is nothing
    // left to decide, and re-running the side effects would open the browser
    // twice for the same click.
    if (event.defaultPrevented) return;
    if (!isBackendURL(url)) {
      event.preventDefault();
      void openExternalURL(url);
    }
  };
  contents.on("will-navigate", guardNavigation);
  // Subframes navigate without ever firing will-navigate on some Electron
  // versions; will-frame-navigate covers the main frame and every child frame.
  contents.on("will-frame-navigate", (event) => {
    guardNavigation(event, event.url);
  });
}

// Only these schemes are ever handed to the OS. Anything else (file:, smb:, or
// a custom protocol registered by some other installed app) would let renderer
// content reach far outside the browser, so it is dropped.
const EXTERNAL_SCHEMES = new Set(["http:", "https:", "mailto:"]);

// Single exit point to the OS browser/handler for every renderer-supplied URL.
async function openExternalURL(url) {
  let parsed;
  try {
    parsed = new URL(url);
  } catch {
    return;
  }
  if (!EXTERNAL_SCHEMES.has(parsed.protocol)) {
    log.warn("Refused to open external URL", { url });
    return;
  }
  await shell.openExternal(parsed.toString());
}

// Membership must be decided on the parsed origin, never on a string prefix:
// "http://127.0.0.1:56660.evil.com" and "http://127.0.0.1:5666@evil.com" both
// start with the backend URL yet belong to an attacker.
function isBackendURL(url) {
  try {
    return new URL(url).origin === new URL(backendURL).origin;
  } catch {
    return false;
  }
}

async function checkForUpdates(manual = false) {
  if (!app.isPackaged) {
    if (manual) {
      await dialog.showMessageBox({
        type: "info",
        message: "開發模式不檢查更新。",
      });
    }
    return;
  }
  if (!SIGNED_UPDATES) {
    if (manual) {
      await dialog.showMessageBox({
        type: "info",
        message: "此開發版未啟用自動更新；請從可信來源安裝簽章版本。",
      });
    }
    return;
  }
  try {
    manualUpdateCheck = manual;
    await autoUpdater.checkForUpdates();
  } catch (error) {
    manualUpdateCheck = false;
    log.error("Update check failed", error);
    if (manual) {
      await dialog.showMessageBox({
        type: "error",
        message: "目前無法檢查更新。",
        detail: error instanceof Error ? error.message : String(error),
      });
    }
  }
}

function configureUpdater() {
  if (!SIGNED_UPDATES) {
    log.info("Automatic updates disabled for unsigned/development build");
    return;
  }
  autoUpdater.on("update-available", async (info) => {
    manualUpdateCheck = false;
    const answer = await dialog.showMessageBox({
      type: "info",
      buttons: ["下載更新", "稍後"],
      defaultId: 0,
      cancelId: 1,
      title: "Nodevas 有新版本",
      message: `可更新至 ${info.version}`,
      detail: "下載期間可繼續使用；完成後會詢問是否重新啟動安裝。",
    });
    if (answer.response === 0) await autoUpdater.downloadUpdate();
  });
  autoUpdater.on("update-not-available", async () => {
    log.info("Nodevas is up to date");
    if (manualUpdateCheck) {
      manualUpdateCheck = false;
      await dialog.showMessageBox({
        type: "info",
        message: "目前已是最新版本。",
      });
    }
  });
  autoUpdater.on("update-downloaded", async (info) => {
    const answer = await dialog.showMessageBox({
      type: "info",
      buttons: ["重新啟動並安裝", "結束時安裝"],
      defaultId: 0,
      cancelId: 1,
      title: "更新已下載",
      message: `Nodevas ${info.version} 已準備完成。`,
    });
    if (answer.response === 0) {
      quitting = true;
      void stopBackend().finally(() => autoUpdater.quitAndInstall());
    }
  });
  updateTimer = setInterval(() => void checkForUpdates(false), UPDATE_INTERVAL_MS);
  updateTimer.unref();
  setTimeout(() => void checkForUpdates(false), 8_000).unref();
}

function configureMenu() {
  const template = [
    {
      label: app.name,
      submenu: [
        { role: "about" },
        {
          label: remoteMode ? "遠端工作區（由伺服器管理）" : "連接工作區...",
          enabled: !remoteMode,
          click: () => void connectWorkspaceFromDialog(),
        },
        { type: "separator" },
        {
          label: "檢查更新…",
          click: () => void checkForUpdates(true),
        },
        { type: "separator" },
        { role: "services" },
        { type: "separator" },
        { role: "hide" },
        { role: "hideOthers" },
        { role: "unhide" },
        { type: "separator" },
        { role: "quit" },
      ],
    },
    {
      label: "編輯",
      submenu: [
        { role: "undo" },
        { role: "redo" },
        { type: "separator" },
        { role: "cut" },
        { role: "copy" },
        { role: "paste" },
        { role: "selectAll" },
      ],
    },
    {
      label: "顯示",
      submenu: [
        { role: "reload" },
        { role: "togglefullscreen" },
      ],
    },
    {
      label: "視窗",
      submenu: [
        { role: "minimize" },
        { role: "zoom" },
        { role: "front" },
      ],
    },
  ];
  Menu.setApplicationMenu(Menu.buildFromTemplate(template));
}

const hasSingleInstanceLock = app.requestSingleInstanceLock();
if (!hasSingleInstanceLock) {
  app.quit();
} else {
  app.on("second-instance", () => {
    if (!mainWindow) return;
    if (mainWindow.isMinimized()) mainWindow.restore();
    mainWindow.show();
    mainWindow.focus();
  });

  app.on("before-quit", (event) => {
    if (quitting) return;
    event.preventDefault();
    quitting = true;
    if (updateTimer) clearInterval(updateTimer);
    void stopBackend().finally(() => app.quit());
  });

  app.on("window-all-closed", () => {
    if (process.platform !== "darwin") app.quit();
  });

  app.on("activate", () => {
    if (!mainWindow && backendURL) createWindow();
  });

  app.on("web-contents-created", (_event, contents) => {
    applyNavigationPolicy(contents);
  });

  app.whenReady()
    .then(async () => {
      app.setAboutPanelOptions({
        applicationName: "Nodevas",
        applicationVersion: metadata.releaseLabel,
        version: app.getVersion(),
        copyright: `© ${new Date().getFullYear()} Nodevas`,
      });
      denyAllPermissions();
      await startBackend();
      await promptForInitialWorkspace();
      createWindow();
      configureMenu();
      configureUpdater();
    })
    .catch(async (error) => {
      log.error("Startup failed", error);
      await dialog.showMessageBox({
        type: "error",
        title: "無法啟動 Nodevas",
        message: remoteMode
          ? "應用程式無法連接 Nodevas 遠端服務。"
          : "應用程式無法啟動本機服務。",
        detail: error instanceof Error ? error.message : String(error),
      });
      app.quit();
    });
}
