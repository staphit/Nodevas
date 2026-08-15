// Claiming work: taking a node, giving it back, and reporting on it. Routes:
// routes.go.

package node

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"nodevas/internal/auth"
	"nodevas/internal/engine"
	"nodevas/internal/httpapi/httpx"
	"nodevas/internal/store"
)

// The codes a caller branches on. They are narrower than the HTTP statuses
// carrying them: ALREADY_CLAIMED and NOT_CLAIMABLE are both 409, and what to do
// next differs — wait for the other holder, or pick something else entirely.
const (
	codeAlreadyClaimed = "ALREADY_CLAIMED"
	codeNotClaimable   = "NOT_CLAIMABLE"
	codeNotOwner       = "NOT_OWNER"
	codeInvalid        = "INVALID_ARGUMENT"
)

// maxRequestID bounds the replay key. It is an opaque token from the caller,
// and the only thing this end needs of it is that it fit in a file.
const maxRequestID = 128

// postClaim takes a node for the caller and moves it to in_progress.
func (a *API) postClaim(c *gin.Context) {
	id := c.Param("id")
	var body struct {
		Owner        string `json:"owner"`
		LeaseSeconds int    `json:"leaseSeconds"`
		RequestID    string `json:"requestId"`
	}
	if !httpx.DecodeJSON(c, &body) {
		return
	}
	if body.Owner == "" {
		httpx.Coded(c, http.StatusBadRequest, codeInvalid,
			errors.New("body must be {owner, leaseSeconds?, requestId?}"), nil)
		return
	}
	if len(body.Owner) > 128 || len(body.RequestID) > maxRequestID {
		httpx.Coded(c, http.StatusBadRequest, codeInvalid,
			errors.New("owner or requestId is too long"), nil)
		return
	}

	st := httpx.StoreFor(c.Request, a.pm)
	result, err := st.ClaimNode(auth.ActorFrom(c.Request), id, body.Owner,
		time.Duration(body.LeaseSeconds)*time.Second, body.RequestID)
	if err != nil {
		if httpx.WriteDenied(c, err) {
			return
		}
		respondClaimError(c, err)
		return
	}
	g, _, err := st.LoadGraph()
	if err != nil {
		httpx.Err(c, 500, err)
		return
	}
	// The same event a person's status change sends: the point of routing an
	// agent through the server is that the browser sees it move.
	a.notifyRoom(c.Request, "state-changed", "")
	c.JSON(200, map[string]any{
		"ok":       true,
		"claim":    result.Claim,
		"task":     result.Node,
		"statuses": engine.ComputeStatuses(g, result.State),
	})
}

// postRelease gives a node back without finishing it.
func (a *API) postRelease(c *gin.Context) {
	id := c.Param("id")
	var body struct {
		Owner string `json:"owner"`
	}
	if !httpx.DecodeJSON(c, &body) {
		return
	}
	if body.Owner == "" {
		httpx.Coded(c, http.StatusBadRequest, codeInvalid,
			errors.New("body must be {owner}"), nil)
		return
	}
	st := httpx.StoreFor(c.Request, a.pm)
	if err := st.ReleaseClaim(id, body.Owner); err != nil {
		respondClaimError(c, err)
		return
	}
	a.notifyRoom(c.Request, "state-changed", "")
	c.JSON(200, map[string]any{"ok": true})
}

// getClaims lists what is currently held, so a person can see that an agent is
// working before they wonder why a node moved on its own.
func (a *API) getClaims(c *gin.Context) {
	c.JSON(200, map[string]any{
		"claims": httpx.StoreFor(c.Request, a.pm).LiveClaims(),
	})
}

// respondClaimError maps the store's refusals onto statuses and codes.
func respondClaimError(c *gin.Context, err error) {
	var taken *store.ErrAlreadyClaimed
	if errors.As(err, &taken) {
		httpx.Coded(c, http.StatusConflict, codeAlreadyClaimed, err, map[string]any{
			"owner":   taken.Owner,
			"expires": taken.Expires,
		})
		return
	}
	var notClaimable *store.ErrNotClaimable
	if errors.As(err, &notClaimable) {
		httpx.Coded(c, http.StatusConflict, codeNotClaimable, err, map[string]any{
			"reason": notClaimable.Reason,
		})
		return
	}
	var notOwner *store.ErrNotOwner
	if errors.As(err, &notOwner) {
		httpx.Coded(c, http.StatusConflict, codeNotOwner, err, map[string]any{
			"owner": notOwner.Owner,
		})
		return
	}
	// "node not found" comes back from the store as a plain error; everything
	// else at this point is a bad argument rather than a server fault.
	httpx.Coded(c, http.StatusBadRequest, codeInvalid, err, nil)
}
