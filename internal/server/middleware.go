// The middleware chain every request passes through, in the order routes.go installs it: transport guards, then identity, then which project the request targets, then the audit trail. The origin and media-type checks the first one applies live here too.

package server

import (
	"context"
	"errors"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"nodevas/internal/audit"
	"nodevas/internal/auth"
	"nodevas/internal/httpapi/httpx"
)

// secureHTTP is the first middleware in the chain: it sets the headers every
// response carries and rejects unsafe requests that fail the origin or media
// type checks, before any handler sees them.
func (s *Server) secureHTTP(c *gin.Context) {
	r := c.Request
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Referrer-Policy", "no-referrer")
	c.Header("X-Frame-Options", "DENY")
	// style-src keeps 'unsafe-inline' because React writes component styles as
	// inline style attributes; there is no nonce to hang on them. Everything
	// else is as narrow as the app actually needs:
	//   connect-src 'self' — the client only ever talks to its own origin, and
	//     under CSP3 'self' already covers the same-origin ws:/wss: upgrade
	//     (web/src/api.ts derives the scheme and host from location), so the
	//     old "ws: wss:" wildcard was a free exfiltration channel and nothing else.
	//   img-src keeps https: because markdown previews render remote images
	//     (web/src/editor/livePreview.ts), but drops bare http: so a remote
	//     image cannot downgrade the page or beacon the reader's IP in the clear.
	c.Header(
		"Content-Security-Policy",
		"default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; "+
			"img-src 'self' data: blob: https:; connect-src 'self'; "+
			"font-src 'self' data:; object-src 'none'; base-uri 'none'; "+
			"form-action 'self'; frame-src 'none'; frame-ancestors 'none'",
	)

	// The Host check guards everything, not just unsafe /api/ calls: a rebound
	// name reaches the websocket and the reads too, and a GET that leaks the
	// whole graph is as bad as a write.
	if !s.allowsHost(r.Host) {
		httpx.Err(c, http.StatusForbidden,
			errors.New("this server does not answer on Host "+strconv.Quote(r.Host)))
		return
	}

	underAPI := strings.HasPrefix(r.URL.Path, "/api/")
	// A GET that changes state is a write wearing a safe method's clothes, so
	// it gets the same cross-origin treatment even though the method check
	// would wave it through.
	if underAPI && (isUnsafeMethod(r.Method) || stateChangingGET(r)) {
		if origin := r.Header.Get("Origin"); origin != "" && !s.isAllowedOrigin(origin, r.Host) {
			httpx.Err(c, http.StatusForbidden, errors.New("cross-origin API request rejected"))
			return
		}
		// Origin is absent on a top-level cross-site navigation, which is
		// exactly how a state-changing GET gets attacked. Sec-Fetch-Site is
		// set by the browser and unforgeable by page script, so it catches
		// what Origin cannot. It is missing entirely on non-browser clients
		// (agents, curl), which are not subject to CSRF in the first place.
		if !allowsFetchSite(r.Header.Get("Sec-Fetch-Site")) {
			httpx.Err(c, http.StatusForbidden, errors.New("cross-site API request rejected"))
			return
		}
	}

	if underAPI && isUnsafeMethod(r.Method) {
		// Empty POSTs are allowed through so route matching can return the
		// correct 404 (and body-requiring handlers can return 400). Any
		// request that actually carries a body must declare its media type.
		if r.Method != http.MethodDelete && r.ContentLength != 0 {
			mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
			multipartUpload := mediaType == "multipart/form-data" &&
				(r.URL.Path == "/api/projects/import" ||
					(strings.HasPrefix(r.URL.Path, "/api/nodes/") &&
						(strings.HasSuffix(r.URL.Path, "/files") ||
							strings.HasSuffix(r.URL.Path, "/pages/import"))))
			if err != nil || (mediaType != "application/json" && !multipartUpload) {
				httpx.Err(
					c,
					http.StatusUnsupportedMediaType,
					errors.New("Content-Type must be application/json (or multipart/form-data for uploads)"),
				)
				return
			}
		}
	}
	c.Next()
}

// withAuth resolves the actor once per request. Static assets stay public so
// the login screen can load; everything under /api/ and the websocket needs an
// identity.
func (s *Server) withAuth(c *gin.Context) {
	r := c.Request
	guarded := strings.HasPrefix(r.URL.Path, "/api/") || r.URL.Path == "/ws"
	if !guarded || auth.PublicPaths[r.URL.Path] {
		c.Next()
		return
	}
	actor, err := s.auth.Authenticate(r)
	if err != nil {
		httpx.Err(c, http.StatusUnauthorized, err)
		return
	}
	if s.auth.NeedsCSRF() && isUnsafeMethod(r.Method) {
		if err := auth.CheckCSRF(r); err != nil {
			httpx.Err(c, http.StatusForbidden, err)
			return
		}
	}
	// The actor rides on the request context, not gin's key/value st, so
	// ActorFrom keeps working anywhere a *http.Request is all that is at hand.
	requestContext := auth.WithActor(r.Context(), actor)
	if s.auth.Remote() {
		validationRequest := r.Clone(context.Background())
		requestContext = auth.WithRevalidator(requestContext, func() bool {
			current, authErr := s.auth.Authenticate(validationRequest)
			return authErr == nil && current.ID == actor.ID && current.Role == actor.Role
		})
	}
	c.Request = r.WithContext(requestContext)
	c.Next()
}

// withVisitorReadOnly is the whole of what a visitor session may not do.
//
// It sits here rather than in the handlers because the guarantee is negative:
// "a visitor changes nothing" cannot be kept by a set of checks that each route
// has to remember to add. So this decides by shape — safe method, not on the
// deny list — and every route added later is covered without being edited.
//
// It runs after withAuth, which is where the actor comes from, and before the
// rest of the chain, so a refusal costs nothing beyond the identity lookup that
// had already happened.
func (s *Server) withVisitorReadOnly(c *gin.Context) {
	r := c.Request
	if auth.ActorFrom(r).CanWrite() {
		c.Next()
		return
	}
	if !visitorMayCall(r) {
		httpx.Err(c, http.StatusForbidden,
			errors.New("this is a read-only visitor session"))
		return
	}
	c.Next()
}

// visitorMayCall decides one request. Deny by default: an unrecognised method
// or a path that has to be reasoned about is refused, because the cost of
// refusing a read a visitor should have had is a bug report, and the cost of
// allowing a write is the document.
func visitorMayCall(r *http.Request) bool {
	// Signing out is the one state change a visitor must keep. Signing in is
	// not listed because it happens before there is an actor to test.
	if r.URL.Path == "/api/auth/logout" {
		return true
	}
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
	default:
		return false
	}
	if visitorDeniedPaths[r.URL.Path] {
		return false
	}
	for _, prefix := range visitorDeniedPrefixes {
		if strings.HasPrefix(r.URL.Path, prefix) {
			return false
		}
	}
	return true
}

// visitorDeniedPaths are reads that are not viewing.
//
// An export is a copy of the whole project leaving the server in one request.
// It changes nothing, so nothing above stops it, and it is exactly what a
// credential handed out to be able to look must not also hand out. The audit
// trail and the notification settings are listed for the same reason they are
// admin-gated — they describe the deployment, not the document — and are
// repeated here so this file states the visitor's limits on its own.
var visitorDeniedPaths = map[string]bool{
	"/api/projects/export": true,
	"/api/audit":           true,
	"/api/notify/settings": true,
}

// visitorDeniedPrefixes are whole subtrees a visitor has no business in. The
// filesystem browser enumerates the host's directories, and the remote
// endpoints reach Google Drive with the operator's credentials; neither is
// part of the document a visitor came to read.
var visitorDeniedPrefixes = []string{
	"/api/fs/",
	"/api/remote/",
}

// withRequestProject resolves ?project= / X-Nodevas-Project once per request.
// An unknown project fails with 400 here, before any handler runs, instead of
// silently operating on the active project.
func (s *Server) withRequestProject(c *gin.Context) {
	r := c.Request
	name := httpx.RequestProjectName(r)
	if name == "" || !httpx.ProjectScopedPath(r.URL.Path) {
		c.Next()
		return
	}
	st, err := s.pm.StoreFor(name)
	if err != nil {
		httpx.Err(c, http.StatusBadRequest, err)
		return
	}
	c.Request = r.WithContext(httpx.WithStore(r.Context(), st))
	c.Next()
}

// withAuditTrail records who performed each successful write.
//
// It sits in one place on purpose: a per-handler call is a line that the next
// handler forgets to add, and an audit trail with holes is worse than none.
func (s *Server) withAuditTrail(c *gin.Context) {
	r := c.Request
	if !strings.HasPrefix(r.URL.Path, "/api/") ||
		!isUnsafeMethod(r.Method) ||
		strings.HasPrefix(r.URL.Path, "/api/auth/") {
		c.Next()
		return
	}
	c.Next()
	if c.Writer.Status() >= 400 {
		return
	}
	// Read the request back off the context: earlier middleware may have
	// replaced it, and the store the handler used hangs off that copy.
	r = c.Request
	actor := auth.ActorFrom(r)
	op := r.Method + " " + strings.TrimPrefix(r.URL.Path, "/api/")

	// The database trail is the one that is queried. It records server-wide
	// events too, and it survives a project being deleted.
	if s.audit != nil {
		project := ""
		if st := httpx.StoreFor(r, s.pm); st != nil {
			project = st.Root()
		}
		s.audit.RecordOrLog(r.Context(), audit.Event{
			At:        time.Now(),
			Project:   project,
			ActorID:   actor.ID,
			ActorName: actor.Name,
			Action:    op,
			ClientIP:  auth.ClientIP(r),
		})
	}
}

// stateChangingGETPaths are the GET routes that mutate server state. They are
// listed rather than inferred because the method is the only signal the
// transport layer has, and it lies about these.
//
// /api/remote/drive/auth mints a single-use OAuth state entry and redirects to
// Google's consent screen, so a cross-site page that can reach it can both
// exhaust that table and drive an administrator into an unexpected consent
// flow. It stays a GET because the browser has to follow the 302 as a
// navigation — a fetch cannot read a cross-origin redirect target, and a form
// POST cannot carry the CSRF header a networked deployment requires.
var stateChangingGETPaths = map[string]bool{
	"/api/remote/drive/auth": true,
}

func stateChangingGET(r *http.Request) bool {
	return (r.Method == http.MethodGet || r.Method == http.MethodHead) &&
		stateChangingGETPaths[r.URL.Path]
}

// allowsFetchSite reports whether a Sec-Fetch-Site value belongs to this
// application. "none" is a user-initiated navigation (address bar, bookmark,
// or the desktop shell handing the URL to the system browser), which is a
// deliberate act rather than something a page can cause. An empty value means
// the client did not send the header at all.
func allowsFetchSite(value string) bool {
	switch value {
	case "", "none", "same-origin":
		return true
	default:
		return false
	}
}

func isUnsafeMethod(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

// isAllowedOrigin accepts same-origin requests plus loopback. Comparing
// against the Host header is what keeps this working once the server answers
// on a real hostname: hard-coding localhost would reject the deployment it is
// meant to protect. The Host itself is only worth comparing against because
// allowsHost has already established that it is one of ours.
func (s *Server) isAllowedOrigin(value, host string) bool {
	if s.isLocalOrigin(value) {
		return true
	}
	origin, err := url.Parse(value)
	if err != nil || origin.Host == "" {
		return false
	}
	return strings.EqualFold(origin.Host, host)
}

// isLocalOrigin accepts a page served from loopback, but only on the port this
// server listens on: every other local port is a different application, and
// "runs on this machine" is not a reason to hand it the API.
func (s *Server) isLocalOrigin(value string) bool {
	origin, err := url.Parse(value)
	if err != nil || (origin.Scheme != "http" && origin.Scheme != "https") {
		return false
	}
	host := strings.ToLower(origin.Hostname())
	if host != "localhost" && host != "127.0.0.1" && host != "::1" {
		return false
	}
	if s.listenPort == "" {
		return true
	}
	return originPort(origin) == s.listenPort
}

// originPort fills in the port the browser left implicit in the Origin header.
func originPort(origin *url.URL) string {
	if port := origin.Port(); port != "" {
		return port
	}
	if origin.Scheme == "https" {
		return "443"
	}
	return "80"
}
