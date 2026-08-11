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
const STARTUP_TIMEOUT_MS = 20_000;
const WORKSPACE_PICKER_MARKER = "workspace-picker-seen";
const REMOTE_SERVER_ENV = "NODEVAS_SERVER_URL";
const DEFAULT_REMOTE_SERVER_URL = metadata.defaultServerURL || "";
const SIGNED_UPDATES = metadata.signedUpdates === true;
let backend = null;
let mainWindow = null;
let backendURL = "";
let remoteMode = false;
let remoteOAuthInProgress = false;
let quitting = false;
let workspacePickerBusy = false;
let driveAuthBusy = false;
let backendStopPromise = null;
let updateTimer = null;
let manualUpdateCheck = false;

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

function waitForBackend(url) {
  const deadline = Date.now() + STARTUP_TIMEOUT_MS;
  return new Promise((resolve, reject) => {
    const probe = () => {
      if (Date.now() >= deadline) {
        reject(new Error("後端服務啟動逾時"));
        return;
      }
      const request = requestModule(url).get(url, (response) => {
        response.resume();
        if (response.statusCode && response.statusCode < 500) {
          resolve();
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
  backend = spawn(
    executable,
    ["serve", "-project", workspace, "-port", String(port)],
    { windowsHide: true, stdio: ["ignore", "pipe", "pipe"] },
  );
  backend.stdout.on("data", (chunk) => log.info(String(chunk).trim()));
  backend.stderr.on("data", (chunk) => log.info(String(chunk).trim()));
  backend.once("exit", (code, signal) => {
    backend = null;
    if (!quitting) {
      log.error("Nodevas backend stopped", { code, signal });
      void dialog.showMessageBox({
        type: "error",
        title: "Nodevas 已停止",
        message: "本機服務意外結束，請重新開啟應用程式。",
        detail: `exit=${code ?? "unknown"}, signal=${signal ?? "none"}`,
      });
    }
  });
  await waitForBackend(backendURL);
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

async function startDriveAuth(authURL) {
  if (driveAuthBusy) return;
  driveAuthBusy = true;
  try {
    const consentURL = new URL(
      authURL || "/api/remote/drive/auth",
      backendURL,
    );
    const source = consentURL.searchParams.get("source") === "workspace"
      ? "workspace"
      : "";
    let initialConfig = null;
    try {
      initialConfig = await backendRequest("GET", "/api/remote/config");
    } catch (error) {
      log.warn("Could not inspect Drive OAuth status", error);
    }
    await openExternalURL(consentURL.toString());
    // A fully authorized account does not need another browser round trip.
    // For a token without folder-browsing scope, wait until the callback
    // replaces it with one that can browse existing projects.
    if (initialConfig?.driveConnected && !initialConfig?.driveNeedsReauth) return;
    const deadline = Date.now() + 5 * 60 * 1000;
    while (Date.now() < deadline && !quitting) {
      await new Promise((resolve) => setTimeout(resolve, 1_000));
      try {
        const config = await backendRequest("GET", "/api/remote/config");
        if (config?.driveConnected && !config?.driveNeedsReauth) {
          if (mainWindow && !mainWindow.isDestroyed()) {
            const destination = source
              ? `${backendURL}/?drive=connected&source=${source}`
              : `${backendURL}/?drive=connected`;
            await mainWindow.loadURL(destination);
          }
          return;
        }
      } catch (error) {
        log.warn("Drive OAuth status check failed", error);
      }
    }
  } catch (error) {
    log.error("Drive OAuth launch failed", error);
    const owner = mainWindow && !mainWindow.isDestroyed() ? mainWindow : undefined;
    const options = {
      type: "error",
      title: "無法連接 Google Drive",
      message: "Nodevas 無法開啟 Google 授權頁面。",
      detail: error instanceof Error ? error.message : String(error),
    };
    if (owner) await dialog.showMessageBox(owner, options);
    else await dialog.showMessageBox(options);
  } finally {
    driveAuthBusy = false;
  }
}

function stopBackend() {
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
    processToStop.kill("SIGTERM");
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
  void mainWindow.loadURL(backendURL);
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
    if (isDriveAuthURL(url) && !remoteMode) {
      void startDriveAuth(url);
      return { action: "deny" };
    }
    if (isDriveAuthURL(url) && remoteMode) {
      remoteOAuthInProgress = true;
      return { action: "allow" };
    }
    if (isRemoteOAuthURL(url)) return { action: "allow" };
    if (!isBackendURL(url)) void openExternalURL(url);
    return { action: "deny" };
  });
  const guardNavigation = (event, url) => {
    // will-frame-navigate fires first and will-navigate follows for the main
    // frame; if the first one already vetoed the navigation there is nothing
    // left to decide, and re-running the side effects would open the browser
    // twice for the same click.
    if (event.defaultPrevented) return;
    if (isDriveAuthURL(url) && !remoteMode) {
      event.preventDefault();
      void startDriveAuth(url);
      return;
    }
    if (isDriveAuthURL(url) && remoteMode) {
      remoteOAuthInProgress = true;
      return;
    }
    if (isRemoteOAuthURL(url)) return;
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

function isDriveAuthURL(url) {
  try {
    return isBackendURL(url) && new URL(url).pathname === "/api/remote/drive/auth";
  } catch {
    return false;
  }
}

function isRemoteOAuthURL(url) {
  if (!remoteMode || !remoteOAuthInProgress) return false;
  try {
    const parsed = new URL(url);
    if (isBackendURL(url)) {
      if (parsed.pathname === "/api/remote/drive/callback") {
        remoteOAuthInProgress = false;
      }
      return true;
    }
    return parsed.protocol === "https:" && parsed.hostname === "accounts.google.com";
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
