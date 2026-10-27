/**
 * Which model the copilot uses, in words: the panel header and the settings
 * tab both say it, so a free local model is never mistaken for a paid one, and
 * a model on the server is never mistaken for one on this PC.
 */

import type { CopilotStatus } from "../api";

type Translate = (key: string, values?: Record<string, string | number>) => string;

const LOCAL_SERVER_NAMES: Record<string, string> = { ollama: "Ollama", lmstudio: "LM Studio" };

/** Where the model runs, as a phrase ("on this PC", "on the server", "on desk PC"). */
export function modelPlace(status: CopilotStatus, t: Translate): string {
  if (status.modelHost === "runner") {
    return status.runner ? t("copilot.model.onRunner", { runner: status.runner }) : t("copilot.model.onThisPC");
  }
  if (status.modelHost === "server") {
    return status.serverIsLocal ? t("copilot.model.onThisPC") : t("copilot.model.onServer");
  }
  return "";
}

export function modelLabel(status: CopilotStatus | null, t: Translate): string {
  if (!status) return "";
  if (status.provider === "browser") return t("copilot.model.browser");
  if (status.localServer) {
    const server = LOCAL_SERVER_NAMES[status.localServer] ?? status.localServer;
    const name = status.model ? `${server} · ${status.model}` : server;
    const place = modelPlace(status, t);
    return place ? t("copilot.model.local", { name, place }) : name;
  }
  return status.model;
}
