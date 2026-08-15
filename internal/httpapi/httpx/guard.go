package httpx

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
)

// networkReach is the one thing RefuseHostPath needs to know about a project
// manager. Taking the behaviour rather than the type is what keeps this
// package free of the domain: httpx is the transport toolkit every handler
// package leans on, so anything it imports is imported by all of them.
type networkReach interface {
	IsRemote() bool
}

// RefuseHostPath rejects requests that name a directory anywhere on the server
// machine while the server is reachable from the network.
func RefuseHostPath(c *gin.Context, reach networkReach) bool {
	if !reach.IsRemote() {
		return false
	}
	Err(c, http.StatusForbidden,
		errors.New("在網路模式下不接受伺服器本機路徑"))
	return true
}

// statusError is an error that already knows which status it deserves.
type statusError interface {
	error
	Status() int
}

// ErrWithStatus answers with the status an error carries, or 500 when it
// carries none.
func ErrWithStatus(c *gin.Context, err error) {
	var se statusError
	if errors.As(err, &se) {
		Err(c, se.Status(), se)
		return
	}
	Err(c, http.StatusInternalServerError, err)
}
