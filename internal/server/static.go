// Serving of the embedded frontend, and what answers a request no route matched.

package server

import (
	"errors"
	"io/fs"
	"log"
	"net/http"
	"nodevas/internal/auth"
	"nodevas/internal/httpapi/httpx"
	pathpkg "path"
	"strings"

	"github.com/gin-gonic/gin"
)

// notFound answers unmatched routes: JSON under /api/, the SPA everywhere else
// so client-side routes still load index.html.
func (s *Server) notFound(c *gin.Context) {
	if strings.HasPrefix(c.Request.URL.Path, "/api/") {
		httpx.Err(c, http.StatusNotFound, errors.New("api endpoint not found"))
		return
	}
	s.serveStatic(c)
}

// serveStatic serves the embedded frontend build; unknown extension-less paths
// fall back to index.html (SPA routing).
//
// A missing file is a 404, never the app shell. A tab opened before an upgrade
// asks for the old build's hashed chunks; answered with index.html, the
// browser gets HTML where it expected JavaScript, and the cached 200 can stick
// for a year under the immutable header. A plain uncached 404 lets the page
// notice the chunk is gone and reload.
func (s *Server) serveStatic(c *gin.Context) {
	if s.webFS == nil {
		http.Error(c.Writer, "frontend not built; run `npm run build` in web/ or use the Vite dev server", 404)
		return
	}
	path := strings.TrimPrefix(c.Request.URL.Path, "/")
	if path == "" {
		path = "index.html"
	}
	if _, err := fs.Stat(s.webFS, path); err != nil {
		if strings.HasPrefix(path, "assets/") || pathpkg.Ext(path) != "" {
			c.Header("Cache-Control", "no-store")
			http.Error(c.Writer, "not found", http.StatusNotFound)
			return
		}
		path = "index.html"
	}
	if path == "index.html" {
		c.Header("Cache-Control", "no-cache")
	} else if strings.HasPrefix(path, "assets/") {
		c.Header("Cache-Control", "public, max-age=31536000, immutable")
	}
	http.ServeFileFS(c.Writer, c.Request, s.webFS, path)
}

// QuietRequest reports a request whose line the request log may leave out
// when it succeeds (logging.Options.Quiet): the build's hashed static assets,
// and the container's health probe, which asks for the app shell straight
// from this machine every half minute. A visitor loading the app is still
// recorded, by the shell and every API call it makes.
func QuietRequest(r *http.Request) bool {
	if strings.HasPrefix(r.URL.Path, "/assets/") {
		return true
	}
	return (r.URL.Path == "/" || r.URL.Path == healthzPath) && auth.DirectLoopback(r)
}

// LogRequests is simple request logging middleware.
func LogRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api") {
			log.Printf("%s %q", r.Method, r.URL.Path)
		}
		next.ServeHTTP(w, r)
	})
}
