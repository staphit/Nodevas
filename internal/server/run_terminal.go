package server

import "strings"

// runTerminalPath reports whether path is a run's terminal socket,
// /api/runs/<id>/terminal (W2.5). It is a websocket upgrade, charged like
// /ws, and a socket whose frames type into a terminal on someone's machine,
// so it gets the cross-origin checks of a state-changing request.
func runTerminalPath(path string) bool {
	rest, ok := strings.CutPrefix(path, "/api/runs/")
	if !ok {
		return false
	}
	id, ok := strings.CutSuffix(rest, "/terminal")
	return ok && id != "" && !strings.Contains(id, "/")
}
