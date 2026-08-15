// Package httpx is what every HTTP handler package shares: how a result, an
// error and a conflict are written, how a request body is read, and how a
// request names the project it targets.
//
// It is a leaf on purpose. The router imports the handler packages, so
// anything they have in common has to live below all of them.
//
// It may name a domain type only where the transport genuinely carries one:
// the 409 body is a store revision, and the store a request targets is a
// store. It must not learn a domain type merely to classify it — an error that
// knows its own status is answered through ErrWithStatus, so adding a new one
// never means editing this package.

package httpx

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"nodevas/internal/store"

	"github.com/gin-gonic/gin"
)

// MaxRequestBody caps any JSON request body.
const MaxRequestBody = 16 << 20

// writeErr is the one place an error becomes a response body, so the shape
// {"error": "..."} is the same everywhere. A 5xx is logged and its detail
// withheld: the caller gets the status text, the operator gets the cause.
func Err(c *gin.Context, status int, err error) {
	message := err.Error()
	if status >= 500 {
		log.Printf("request failed: %v", err)
		message = http.StatusText(status)
	}
	// AbortWithStatusJSON, not JSON: a handler that answers an error must not
	// have later middleware in the chain write a second body.
	c.AbortWithStatusJSON(status, map[string]string{"error": message})
}

// Coded is Err with a machine-readable reason alongside the prose.
//
// A status code is often not specific enough to act on: "somebody else already
// holds this node" and "this node is not claimable at all" are both 409, and a
// caller's next move differs completely between them. The prose is for a person
// or a model to read; the code is for a program to branch on.
func Coded(c *gin.Context, status int, code string, err error, extras map[string]any) {
	message := err.Error()
	if status >= 500 {
		log.Printf("request failed: %v", err)
		message = http.StatusText(status)
	}
	body := map[string]any{"error": message, "code": code}
	for key, value := range extras {
		// Never let a caller-supplied extra shadow the two fields every error
		// response is guaranteed to carry.
		if key != "error" && key != "code" {
			body[key] = value
		}
	}
	c.AbortWithStatusJSON(status, body)
}

// decodeJSONBody reads the one JSON value the request must carry.
//
// It does not use c.ShouldBindJSON: gin's binder accepts trailing content
// after the first value and, by default, silently ignores unknown fields.
// Both are mistakes this API reports instead of guessing at.
func DecodeJSON(c *gin.Context, dst any) bool {
	return DecodeJSONLimit(c, dst, MaxRequestBody)
}

// DecodeJSONLimit is DecodeJSON with a route-specific body cap. Authentication
// and settings requests should not inherit the much larger editor-content cap.
func DecodeJSONLimit(c *gin.Context, dst any, limit int64) bool {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, limit)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			Err(c, http.StatusRequestEntityTooLarge, errors.New("request body is too large"))
		} else {
			Err(c, http.StatusBadRequest, fmt.Errorf("invalid JSON body: %w", err))
		}
		return false
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		Err(c, http.StatusBadRequest, errors.New("request body must contain one JSON value"))
		return false
	}
	return true
}

// writeConflict emits the 409 payload for optimistic-lock failures: the
// caller gets the disk version so nothing is ever silently overwritten.
func Conflict(c *gin.Context, conflict *store.ErrConflict) {
	c.JSON(http.StatusConflict, map[string]any{
		"error":       "conflict",
		"diskRev":     conflict.DiskRev,
		"diskContent": conflict.DiskContent,
	})
}
