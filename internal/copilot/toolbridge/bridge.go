package toolbridge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"nodevas/internal/auth"
	"nodevas/internal/copilot/provider"
	"nodevas/internal/identity"
	nodevasmcp "nodevas/internal/mcp"
)

// inProcessBase is the URL the MCP client is told to talk to. No request to it
// ever resolves a name or opens a socket — the transport below serves it — so
// it uses a reserved TLD that could not reach anything even if it tried. The
// Host the server checks is Options.Host, set per request.
const inProcessBase = "http://nodevas.in-process.invalid"

// requestTimeout matches the socket client `nodevas mcp` uses, so one tool
// call is bounded the same way whichever way it arrives.
const requestTimeout = 30 * time.Second

// defaultActor attributes a change the caller did not name. Like
// mcp.DefaultActor it says what made the change, so a reader of the timeline
// never mistakes the copilot for a person.
const defaultActor = "copilot"

// Open builds the MCP server in-process, connects a client to it, and lists
// its tools. The caller must Close the bridge.
func Open(ctx context.Context, opts Options) (Bridge, error) {
	if opts.Handler == nil {
		return nil, errors.New("toolbridge: Options.Handler is required")
	}
	role := opts.AgentRole
	if role == identity.AgentHuman {
		role = identity.AgentOrchestrator
	}
	if role != identity.AgentOrchestrator && role != identity.AgentWorker {
		return nil, fmt.Errorf("toolbridge: unknown agent role %q", role)
	}
	actor := strings.TrimSpace(opts.Actor)
	if actor == "" {
		actor = defaultActor
	}
	host := strings.TrimSpace(opts.Host)
	if host == "" {
		// Every Host allow-list admits loopback; an empty Host does not pass
		// all of them.
		host = "localhost"
	}
	remote := strings.TrimSpace(opts.RemoteAddr)
	if remote == "" {
		remote = "127.0.0.1:0"
	}
	transport := &inProcessTransport{
		handler:    opts.Handler,
		host:       host,
		cookie:     opts.Cookie,
		remoteAddr: remote,
		role:       role,
	}

	// The sessions and the RAG worker live until Close or until ctx ends,
	// whichever is first; cancel is what Close uses to stop them.
	runCtx, cancel := context.WithCancel(ctx)
	b := &bridge{cancel: cancel, closeRAG: func() {}, known: map[string]bool{}}
	fail := func(err error) (Bridge, error) {
		_ = b.Close()
		return nil, err
	}

	server, closeRAG, err := nodevasmcp.NewServerWithClose(runCtx, nodevasmcp.Options{
		Server:     inProcessBase,
		Project:    opts.Project,
		Actor:      actor,
		AgentRole:  string(role),
		ChromaURL:  opts.ChromaURL,
		RAGPython:  opts.RAGPython,
		HTTPClient: &http.Client{Transport: transport, Timeout: requestTimeout},
	})
	if err != nil {
		return fail(fmt.Errorf("toolbridge: build MCP server: %w", err))
	}
	if closeRAG != nil {
		b.closeRAG = closeRAG
	}

	// The server must be connected first: the client initializes the session
	// as part of connecting.
	serverSide, clientSide := sdk.NewInMemoryTransports()
	if b.server, err = server.Connect(runCtx, serverSide, nil); err != nil {
		return fail(fmt.Errorf("toolbridge: connect MCP server: %w", err))
	}
	client := sdk.NewClient(&sdk.Implementation{Name: "nodevas-copilot", Version: nodevasmcp.Version}, nil)
	if b.client, err = client.Connect(runCtx, clientSide, nil); err != nil {
		return fail(fmt.Errorf("toolbridge: connect MCP client: %w", err))
	}

	for tool, err := range b.client.Tools(runCtx, nil) {
		if err != nil {
			return fail(fmt.Errorf("toolbridge: list tools: %w", err))
		}
		mapped, err := mapTool(tool)
		if err != nil {
			return fail(err)
		}
		b.tools = append(b.tools, mapped)
		b.known[tool.Name] = true
	}
	return b, nil
}

// writeTools change content in a way history can undo. Everything else that
// is not read-only is risky, so a tool added to internal/mcp later is asked
// about until someone decides it deserves less.
var writeTools = map[string]bool{
	"create_node":      true,
	"link_nodes":       true,
	"update_node_meta": true,
	"set_node_status":  true,
	"claim_task":       true,
	"release_task":     true,
	"index_documents":  true,
}

// KindOf classifies a tool by name and its MCP read-only hint.
func KindOf(name string, readOnly bool) Kind {
	switch {
	case readOnly:
		return KindRead
	case writeTools[name]:
		return KindWrite
	default:
		// Includes update_node_body, which replaces a page wholesale.
		return KindRisky
	}
}

func mapTool(tool *sdk.Tool) (Tool, error) {
	schema := json.RawMessage(`{"type":"object"}`)
	if tool.InputSchema != nil {
		encoded, err := json.Marshal(tool.InputSchema)
		if err != nil {
			return Tool{}, fmt.Errorf("toolbridge: schema of %s: %w", tool.Name, err)
		}
		schema = encoded
	}
	readOnly := tool.Annotations != nil && tool.Annotations.ReadOnlyHint
	title := tool.Title
	if title == "" && tool.Annotations != nil {
		title = tool.Annotations.Title
	}
	return Tool{
		Tool:  provider.Tool{Name: tool.Name, Description: tool.Description, Schema: schema},
		Kind:  KindOf(tool.Name, readOnly),
		Title: title,
	}, nil
}

type bridge struct {
	tools  []Tool
	known  map[string]bool
	client *sdk.ClientSession
	server *sdk.ServerSession

	closeRAG  func()
	cancel    context.CancelFunc
	closeOnce sync.Once
	closeErr  error
}

func (b *bridge) Tools() []Tool {
	return append([]Tool(nil), b.tools...)
}

func (b *bridge) Call(ctx context.Context, name string, input json.RawMessage) (Result, error) {
	// A model that invents a tool name or garbles its arguments made a
	// mistake it can fix; that is a Result, not a broken bridge.
	if !b.known[name] {
		return Result{Output: fmt.Sprintf("unknown tool %q", name), IsError: true}, nil
	}
	var args any
	if trimmed := bytes.TrimSpace(input); len(trimmed) > 0 && !bytes.Equal(trimmed, []byte("null")) {
		if !json.Valid(trimmed) {
			return Result{Output: "tool input is not valid JSON", IsError: true}, nil
		}
		args = json.RawMessage(trimmed)
	}
	result, err := b.client.CallTool(ctx, &sdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		// A JSON-RPC error answered by the server (bad params, say) is about
		// this call; anything else is the session itself failing.
		var wire *jsonrpc.Error
		if errors.As(err, &wire) {
			return Result{Output: wire.Message, IsError: true}, nil
		}
		return Result{}, err
	}
	return Result{Output: resultText(result), IsError: result.IsError}, nil
}

// resultText is what the model reads back. The tools put their whole answer
// in text content (typed handlers serialize their output there), so joining
// it loses nothing; structured content is the fallback for a tool that only
// sets that.
func resultText(result *sdk.CallToolResult) string {
	var parts []string
	for _, content := range result.Content {
		if text, ok := content.(*sdk.TextContent); ok {
			parts = append(parts, text.Text)
		}
	}
	if len(parts) == 0 && result.StructuredContent != nil {
		if encoded, err := json.Marshal(result.StructuredContent); err == nil {
			return string(encoded)
		}
	}
	return strings.Join(parts, "\n")
}

func (b *bridge) Close() error {
	b.closeOnce.Do(func() {
		if b.client != nil {
			b.closeErr = b.client.Close()
		}
		if b.server != nil {
			// The client closing ends the in-memory pipe, so the server side
			// usually reports the connection already gone; that is the
			// expected way for it to end, not a failure.
			if err := b.server.Close(); err != nil && b.closeErr == nil &&
				!errors.Is(err, sdk.ErrConnectionClosed) {
				b.closeErr = err
			}
		}
		b.closeRAG()
		b.cancel()
	})
	return b.closeErr
}

// inProcessTransport answers HTTP requests by calling the server's handler
// directly. Each request passes the whole middleware chain as if it had come
// over a socket, carrying the person's session cookie and the in-process
// agent marker.
type inProcessTransport struct {
	handler    http.Handler
	host       string
	cookie     string
	remoteAddr string
	role       identity.AgentClass
}

func (t *inProcessTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	// A RoundTripper must close the body it was given, and must not change
	// the request it was handed, hence the clone.
	if request.Body != nil {
		defer request.Body.Close()
	}
	inner := request.Clone(auth.WithInProcessAgent(request.Context(), t.role))
	// Shape it like a request net/http's server would have parsed: path-only
	// URL, RequestURI set, a non-nil body, the peer address filled in.
	inner.URL.Scheme = ""
	inner.URL.Host = ""
	inner.RequestURI = request.URL.RequestURI()
	inner.Host = t.host
	inner.RemoteAddr = t.remoteAddr
	inner.Proto, inner.ProtoMajor, inner.ProtoMinor = "HTTP/1.1", 1, 1
	if inner.Body == nil {
		inner.Body = http.NoBody
	}
	// Only the person's cookie: nothing the MCP client set may stand in for
	// it.
	inner.Header.Del("Cookie")
	if t.cookie != "" {
		inner.Header.Set("Cookie", t.cookie)
	}

	// httptest's recorder is exactly the buffering ResponseWriter needed
	// here; the tool endpoints answer with bounded JSON, never a stream.
	recorder := httptest.NewRecorder()
	t.handler.ServeHTTP(recorder, inner)
	response := recorder.Result()
	response.Request = request
	return response, nil
}
