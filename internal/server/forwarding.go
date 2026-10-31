package server

import (
	"log/slog"
	"net/http"
	"sync"

	"nodevas/internal/auth"
)

// maxForwardingNotes bounds how many peers forwardingNotes remembers. A
// misconfigured proxy is one or two peers; a client inventing the header
// can be any number, and must not grow the map without end.
const maxForwardingNotes = 256

// forwardingNotes warns, once per peer, about a request that carries
// forwarding headers from a peer that is not a trusted proxy. The usual cause
// is a reverse proxy (cloudflared, Caddy, ...) whose network is missing from
// --trusted-proxy: every user then has the proxy's address, and shares its
// rate-limit buckets and WebSocket allowance.
type forwardingNotes struct {
	note func(peer string)

	mu    sync.Mutex
	seen  map[string]bool
	full  bool
	limit int
}

func newForwardingNotes(note func(peer string)) *forwardingNotes {
	if note == nil {
		note = warnUntrustedForwarder
	}
	return &forwardingNotes{note: note, seen: map[string]bool{}, limit: maxForwardingNotes}
}

func warnUntrustedForwarder(peer string) {
	slog.Warn("X-Forwarded-For from a peer that is not a trusted proxy; the header is ignored and "+
		"every request through it counts as this one address. If this peer is your reverse proxy, "+
		"add its network to --trusted-proxy (NODEVAS_SERVE_TRUSTED_PROXY) with --behind-proxy",
		slog.String("event.code", "http.untrusted_forwarder"),
		slog.String("source.ip", peer))
}

// observe notes r's peer if r carries forwarding headers its peer is not
// trusted to set.
func (n *forwardingNotes) observe(r *http.Request) {
	if n == nil || r.Header.Get("X-Forwarded-For") == "" {
		return
	}
	peer, trusted := auth.PeerIsTrustedProxy(r)
	if trusted || peer == "" {
		return
	}
	n.mu.Lock()
	if n.seen[peer] || n.full {
		n.mu.Unlock()
		return
	}
	if len(n.seen) >= n.limit {
		n.full = true
		n.mu.Unlock()
		slog.Warn("more peers sent X-Forwarded-For without being trusted proxies; no longer reporting them",
			slog.String("event.code", "http.untrusted_forwarder"),
			slog.Int("nodevas.peers_reported", n.limit))
		return
	}
	n.seen[peer] = true
	n.mu.Unlock()
	n.note(peer)
}
