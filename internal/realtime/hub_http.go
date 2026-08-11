// The websocket endpoint. It lives with the Hub because it drives the hub's
// internals directly; the router only has to point /ws at Handle.

package realtime

import (
	"encoding/json"
	"log"
	"math"
	"net/http"
	"nodevas/internal/auth"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/gin-gonic/gin"
)

// Handle upgrades an HTTP request and serves one client until it disconnects.
func (h *Hub) Handle(c *gin.Context) {
	actor := auth.ActorFrom(c.Request)
	ip := auth.ClientIP(c.Request)
	if !h.reserveConnection(actor, ip) {
		c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": "websocket connection limit reached"})
		return
	}
	reserved := true
	defer func() {
		if reserved {
			h.releaseReservation(actor, ip)
		}
	}()

	conn, err := websocket.Accept(c.Writer, c.Request, nil)
	if err != nil {
		log.Printf("ws accept: %v", err)
		return
	}
	conn.SetReadLimit(wsReadLimit)
	id, err := auth.RandomToken()
	if err != nil {
		log.Printf("ws client id: %v", err)
		_ = conn.CloseNow()
		return
	}
	client := &wsClient{
		hub:           h,
		conn:          conn,
		outbound:      make(chan clientOutbound, 32),
		done:          make(chan struct{}),
		id:            id[:12],
		actor:         actor,
		ip:            ip,
		authorized:    func() bool { return auth.StillAuthorized(c.Request) },
		messageTokens: wsMessageBurst,
		rateAt:        time.Now(),
	}

	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		_ = conn.CloseNow()
		return
	}
	h.conns[client] = struct{}{}
	h.mu.Unlock()
	reserved = false // remove now owns and releases the reserved capacity.
	go h.writeLoop(client)

	h.sendTo(client, Event{Type: "hello", SelfID: client.id, Actor: &client.actor})

	defer h.remove(client)
	for {
		_, data, err := conn.Read(c.Request.Context())
		if err != nil {
			return
		}
		if client.authorized != nil && !client.authorized() {
			_ = conn.Close(websocket.StatusPolicyViolation, "authorization expired")
			return
		}
		var message clientMessage
		if err := json.Unmarshal(data, &message); err != nil {
			_ = conn.Close(websocket.StatusUnsupportedData, "invalid message")
			return
		}
		if !validClientMessage(message) {
			_ = conn.Close(websocket.StatusPolicyViolation, "invalid message")
			return
		}
		// Which budget this costs is decided by what the message is, after it
		// has been parsed: a client cannot get a cursor's allowance for a lock
		// by mislabelling it, because the label is what it is charged as.
		now := time.Now()
		// Charged to one budget or the other, never both: a cursor that also
		// spent a control token would still be capped at two a second.
		allowed := false
		// A chunk belongs to a start message that already reserved its complete
		// bounded transfer. Charging all 512 chunks to the ordinary 1MiB burst
		// would make a valid 24MiB snapshot impossible; docSnapshotChunk still
		// enforces token, sequence, exact total, timeout and Hub reservation.
		if message.Type == "doc-snapshot-chunk" {
			allowed = h.allowSnapshotChunk(client, docKey(message), message.Token, message.Seq)
		} else if isPayload(message.Type) {
			allowed = client.allowPayload(now, len(data))
		} else {
			allowed = client.allowMessage(now)
		}
		if !allowed {
			_ = conn.Close(websocket.StatusPolicyViolation, "message rate limit exceeded")
			return
		}
		// The HTTP gate cannot see this: the websocket is one upgraded GET, and
		// everything after it arrives as frames. Claiming a node lock is a
		// change to what other people can edit, so it is refused here on the
		// same terms. Subscribing and presence stay — watching a room and
		// showing a cursor in it is what a visitor is for.
		if !client.actor.CanWrite() && mutatesSharedState(message.Type) {
			_ = conn.Close(websocket.StatusPolicyViolation, "read-only session")
			return
		}
		h.handleMessage(client, message)
	}
}

// mutatesSharedState names the client messages that change something other
// clients observe. Written as a list of what is refused rather than what is
// allowed only because validClientMessage above already bounds the set.
func mutatesSharedState(messageType string) bool {
	switch messageType {
	case "lock", "unlock", "graph-drag":
		return true
	case "doc-update", "doc-snapshot", "doc-flushed", "doc-compact-request", "doc-snapshot-start", "doc-snapshot-chunk", "doc-snapshot-commit", "doc-snapshot-abort":
		// A visitor may open and close a live document — reading text that is
		// being typed is what a read-only session is for — but everything that
		// puts bytes into the shared log, or claims a file was written, is a
		// change other people take as theirs.
		return true
	default:
		return false
	}
}

func validClientMessage(message clientMessage) bool {
	if len(message.Project) > 256 || len(message.NodeID) > 256 || len(message.PageID) > 256 ||
		strings.ContainsAny(message.Project, "\x00\r\n") || strings.ContainsAny(message.NodeID, "\x00\r\n") ||
		strings.ContainsAny(message.PageID, "\x00\r\n") {
		return false
	}
	switch message.Type {
	case "subscribe":
		return message.Project != ""
	case "presence":
		return true
	case "lock", "unlock":
		return message.NodeID != ""
	case "awareness":
		// Opaque to this server, but still bounded: the frame limit is sized
		// for the largest of these, and a caller that wants more is not doing
		// this.
		return message.Payload != "" && len(message.Payload) <= maxAwarenessBytes
	case "graph-drag":
		if len(message.IDs) == 0 || len(message.IDs) > maxDragIDs {
			return false
		}
		for _, id := range message.IDs {
			if id == "" || len(id) > 256 || strings.ContainsAny(id, "\x00\r\n") {
				return false
			}
		}
		// A ghost that is not a finite offset would be drawn nowhere, or
		// everywhere.
		return isFinite(message.DX) && isFinite(message.DY)
	case "doc-open", "doc-close":
		return message.NodeID != ""
	case "doc-update":
		// Opaque here as well: the size is the only thing this server can
		// judge about a Yjs update, and it is the only thing that matters,
		// because the stored log is bounded in bytes.
		return message.NodeID != "" && message.Payload != "" && len(message.Payload) <= maxDocUpdateBytes
	case "doc-snapshot":
		return message.NodeID != "" && message.Payload != "" && len(message.Payload) <= maxDocSnapshotBytes
	case "doc-compact-request":
		return message.NodeID != ""
	case "doc-snapshot-start":
		return message.NodeID != "" && validDocToken(message.Token) && message.Size > 0 && message.Size <= maxDocSnapshotTotalBytes && message.Chunks > 0 && message.Chunks <= maxDocSnapshotChunks && message.Chunks == (message.Size+maxDocSnapshotBytes-1)/maxDocSnapshotBytes
	case "doc-snapshot-chunk":
		return message.NodeID != "" && validDocToken(message.Token) && message.Seq >= 0 && message.Seq < maxDocSnapshotChunks && message.Payload != "" && len(message.Payload) <= maxDocSnapshotBytes
	case "doc-snapshot-commit", "doc-snapshot-abort":
		return message.NodeID != "" && validDocToken(message.Token)
	case "doc-flushed":
		return message.NodeID != "" && validDocRev(message.Rev)
	default:
		return false
	}
}

func isFinite(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0)
}
