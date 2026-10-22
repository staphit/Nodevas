package auth

import (
	"context"

	"nodevas/internal/identity"
)

// inProcessAgentKey is unexported so that only this package can mint the
// marker; WithInProcessAgent is the one door.
type inProcessAgentKey struct{}

// WithInProcessAgent marks a request context as an agent running inside this
// server process — today only the copilot's tool bridge
// (internal/copilot/toolbridge), which serves its MCP client's HTTP calls with
// the server's own handler instead of a socket.
//
// The marker is trustworthy for a reason no header could be: a context value
// cannot arrive from the network. net/http builds every incoming request's
// context itself, so the only way a request carries this value is that code in
// this process put it there before calling the handler. withAuth relies on
// that to give the request an agent class on servers whose authenticator
// ignores the agent-role header, and to skip the CSRF double-submit check,
// which defends a browser against a forged cross-site request and has no
// browser to defend here.
//
// Nothing outside the bridge should call this. Marking a request served for
// a person would make that person's writes an agent's and drop their CSRF
// protection.
func WithInProcessAgent(ctx context.Context, class identity.AgentClass) context.Context {
	return context.WithValue(ctx, inProcessAgentKey{}, class)
}

// InProcessAgent reports the agent class WithInProcessAgent attached, if any.
// The class is returned as given; deciding which classes to accept is the
// caller's job, so an empty or unknown one is refused there rather than
// quietly mapped to something here.
func InProcessAgent(ctx context.Context) (identity.AgentClass, bool) {
	if ctx == nil {
		return "", false
	}
	class, ok := ctx.Value(inProcessAgentKey{}).(identity.AgentClass)
	return class, ok
}
