package fsbrowse

import (
	"nodevas/internal/auth"
)

// API serves this package's endpoints. It holds only what these
// handlers need, so the router wires each group explicitly.
type API struct {
	auth    auth.Authenticator
	openDir func(string) error
}

// New builds the handler set.
func New(auth auth.Authenticator, openDir func(string) error) *API {
	return &API{auth: auth, openDir: openDir}
}
