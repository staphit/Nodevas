/**
 * System notifications for agent messages sent with notify=true.
 *
 * Only the browser Notification API: the desktop shell (desktop/main.cjs) has
 * no preload bridge and denies every permission request, notifications
 * included, so inside it permission reads "denied" and the in-app toast is
 * all there is.
 *
 * Permission is asked only from a click (requestSystemNotifications), never
 * on load: browsers penalise sites that prompt unprompted, and a prompt nobody
 * asked for is how a permanent "denied" happens.
 */

export type SystemNotifyPermission = "unsupported" | NotificationPermission;

function notificationAPI(): typeof Notification | null {
  return typeof window !== "undefined" && "Notification" in window
    ? window.Notification
    : null;
}

export function systemNotifyPermission(): SystemNotifyPermission {
  const api = notificationAPI();
  return api ? api.permission : "unsupported";
}

/** Must be called from a user gesture. */
export async function requestSystemNotifications(): Promise<SystemNotifyPermission> {
  const api = notificationAPI();
  if (!api) return "unsupported";
  try {
    return await api.requestPermission();
  } catch {
    return api.permission;
  }
}

/**
 * A desktop browser gets a system notification even while the tab is in
 * front — the board may be on another monitor. A phone only gets one while
 * the page is hidden; on screen, the toast already says it.
 */
function wantsSystemNotification(): boolean {
  if (typeof document !== "undefined" && document.hidden) return true;
  try {
    return window.matchMedia?.("(hover: hover) and (pointer: fine)").matches ?? false;
  } catch {
    return false;
  }
}

/** Shows one system notification when allowed and wanted; returns whether it did. */
export function showSystemNotification(
  title: string,
  body: string,
  tag: string,
  onClick: () => void,
): boolean {
  const api = notificationAPI();
  if (!api || api.permission !== "granted" || !wantsSystemNotification()) {
    return false;
  }
  try {
    const notification = new api(title, { body, tag });
    notification.onclick = () => {
      window.focus();
      onClick();
      notification.close();
    };
    return true;
  } catch {
    // Chrome on Android throws for the constructor (it wants a service
    // worker); the toast still shows.
    return false;
  }
}
