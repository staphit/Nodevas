/**
 * Whether the server can be reached right now, as one fact the page can show.
 *
 * Set by the transport itself: a request that never got an answer (the
 * NetworkError in http.ts) or a dropped live socket marks the server out of
 * reach; any answered request, a reopened socket or the resync after a
 * reconnect marks it back. An answer that is an error (a 500, a 409) still
 * counts as reachable — the server is there, it said no.
 *
 * Its own tiny store, not a slice of the app store: http.ts and ws.ts sit
 * below the store, and importing it here would close a cycle.
 */

import { create } from "zustand";

interface ConnectivityState {
  /** True from a failed connection until the next one gets through. */
  offline: boolean;
}

export const useConnectivity = create<ConnectivityState>(() => ({ offline: false }));

export function markServerUnreachable(): void {
  if (!useConnectivity.getState().offline) useConnectivity.setState({ offline: true });
}

export function markServerReachable(): void {
  if (useConnectivity.getState().offline) useConnectivity.setState({ offline: false });
}
