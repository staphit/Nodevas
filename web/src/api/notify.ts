import { req } from "./http";

export interface NotifySettings {
  enabled: boolean;
  leadMinutes: number;
  smtpHost: string;
  smtpPort: number;
  smtpUser: string;
  smtpPass: string;
  from: string;
  defaultTo: string;
}

export const notifyApi = {
  getNotifySettings: () =>
    req<{ settings: NotifySettings; hasPassword: boolean }>("/api/notify/settings"),
  putNotifySettings: (settings: NotifySettings) =>
    req<{ ok: boolean }>("/api/notify/settings", {
      method: "PUT",
      body: JSON.stringify(settings),
    }),
  testNotify: (to: string) =>
    req<{ ok: boolean }>("/api/notify/test", {
      method: "POST",
      body: JSON.stringify({ to }),
    }),
};
