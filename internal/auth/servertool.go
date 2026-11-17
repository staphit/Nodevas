package auth

import "context"

// copilotServerToolKey is unexported: WithCopilotServerTool is the one door.
type copilotServerToolKey struct{}

// WithCopilotServerTool marks a request a copilot server tool replays
// in-process as the person whose turn it is (internal/copilot
// serverToolEnv.do), after they confirmed the tool call. It changes nothing
// about who the actor is; only POST /api/runs reads it, to let such a request
// ask for a draft plan run (P6). Like WithInProcessAgent it is trustworthy
// because a context value cannot arrive from the network. Nothing else should
// call it.
func WithCopilotServerTool(ctx context.Context) context.Context {
	return context.WithValue(ctx, copilotServerToolKey{}, true)
}

// CopilotServerTool reports whether WithCopilotServerTool marked ctx.
func CopilotServerTool(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	marked, _ := ctx.Value(copilotServerToolKey{}).(bool)
	return marked
}
